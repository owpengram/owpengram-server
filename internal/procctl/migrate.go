package procctl

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	filesapp "telesrv/internal/app/files"
	"telesrv/internal/domain"
	"telesrv/internal/editionmigrate"
	"telesrv/internal/embeddedpg"
	"telesrv/internal/store/postgres"
)

const (
	editionPortable = "portable"
	editionStandard = "standard"
)

// maintenancePG is one edition's PostgreSQL, made reachable while
// owpengram-server is stopped.
//
// The two editions differ in how the server gets there, not in how it is
// spoken to: the portable edition's instance is normally a child of
// owpengram-server (so with the server stopped, nothing is listening until
// this starts it), while the classic edition's lives in a container that
// may need `docker compose up` first. Once up, both are an ordinary DSN --
// see internal/editionmigrate, which does all the data movement over pgx.
type maintenancePG interface {
	// up brings the server online and blocks until it accepts connections.
	up(ctx context.Context) (string, error)
	// down releases it again. Best-effort: a migration that moved its data
	// must not report failure because a teardown did.
	down()
	// dsn addresses the database once up has returned.
	dsn() string
}

// BlobMode says what happens to stored media (photos, documents, stickers)
// when the edition changes. The database copy only carries the metadata; the
// bytes live in MinIO (classic) or under TELESRV_BLOB_DIR (portable).
type BlobMode int

const (
	// BlobsMove copies every object that lives on the old edition's storage
	// to the new edition's, so everything ends up in one place. The old
	// copies are left untouched.
	BlobsMove BlobMode = iota
	// BlobsKeep leaves the bytes where they are; the server still reads
	// each file from whichever backend wrote it. Not possible when the
	// target is portable, which has no MinIO to read them from.
	BlobsKeep
)

// EditionChange carries the optional parts of ChangeEdition.
type EditionChange struct {
	// Force lets a media file that cannot be moved (its bytes are missing
	// from the old storage) be skipped with a warning instead of aborting,
	// and allows leaving media behind when moving to portable.
	Force bool
	Blobs BlobMode
}

// ChangeEdition switches the install to another edition and carries the
// data over, which is the whole reason it exists rather than callers just
// calling SetEdition: the edition decides which PostgreSQL the server
// connects to, so flipping it alone silently swaps a populated database for
// an unrelated one. Nothing errors when that happens -- the server starts
// fine against the other database and every existing client is simply not
// known to it, which from the client side is indistinguishable from the
// server being offline.
//
// Stops both binaries, migrates, persists the choice, then restarts, so a
// caller gets one operation instead of a sequence it has to order
// correctly. opts.Blobs picks what happens to the media.
//
// The edition being left is the source of truth and the other edition's
// database is overwritten, whatever it holds: an install only ever runs one
// edition at a time, so the inactive one is either empty or a stale copy
// left by an earlier switch. Refusing because it "already has accounts"
// made switching back and forth impossible without a force flag nobody
// could reach from the launcher. The one exception is a source with no
// accounts at all, which has nothing worth carrying over and so never
// replaces a populated target (see migrateEditionData).
//
// A failed migration never leaves the install stopped: nothing has been
// persisted yet, so the edition being left is restarted as it was.
func (m *Manager) ChangeEdition(ctx context.Context, to string, opts EditionChange, progress func(string)) error {
	if to != editionPortable && to != editionStandard {
		return fmt.Errorf("unknown edition %q", to)
	}
	from, ok := m.Edition()
	if !ok || from == to {
		// Nothing to carry over: either no edition was ever chosen (a
		// first-time pick, where both sides are equally empty) or this is
		// a no-op re-selection of the current one.
		return m.SetEdition(to)
	}

	emit(progress, m.Stop())

	if err := m.migrateEditionData(ctx, from, to, opts, progress); err != nil {
		emit(progress, "Switching failed -- starting the "+DisplayEditionName(from)+" edition again.\n")
		log, restartErr := m.Restart(ctx)
		emit(progress, log)
		if restartErr != nil {
			return fmt.Errorf("%w (and restarting the %s edition failed: %v)", err, DisplayEditionName(from), restartErr)
		}
		return err
	}

	if err := m.SetEdition(to); err != nil {
		return fmt.Errorf("save edition: %w", err)
	}
	emit(progress, "Edition set to "+DisplayEditionName(to)+".\n")

	log, err := m.Restart(ctx)
	emit(progress, log)
	return err
}

// migrateEditionData moves the data from one edition's PostgreSQL to the
// other's. Both binaries must already be stopped.
func (m *Manager) migrateEditionData(ctx context.Context, from, to string, opts EditionChange, progress func(string)) error {
	force := opts.Force
	if err := m.checkBlobsSurviveEdition(to, opts); err != nil {
		return err
	}

	src, err := m.maintenancePGFor(from)
	if err != nil {
		return err
	}
	dst, err := m.maintenancePGFor(to)
	if err != nil {
		return err
	}

	emit(progress, "Opening the "+DisplayEditionName(from)+" database to copy from...\n")
	log, err := src.up(ctx)
	emit(progress, log)
	if err != nil {
		return err
	}
	defer src.down()

	source, err := editionmigrate.Probe(ctx, src.dsn())
	if err != nil {
		return fmt.Errorf("inspect %s database: %w", DisplayEditionName(from), err)
	}
	switch {
	case !source.HasSchema:
		emit(progress, "The "+DisplayEditionName(from)+" edition has no database yet -- nothing to carry over.\n")
		return nil
	case source.Accounts == 0:
		// Without this, switching away from an edition that was only ever
		// started and never signed into would copy its empty database over
		// a populated one on the other side -- the migration destroying
		// the data it exists to preserve.
		emit(progress, "The "+DisplayEditionName(from)+" edition has no accounts -- treating it as a fresh install and leaving the "+DisplayEditionName(to)+" data as it is.\n")
		return nil
	}

	emit(progress, "Opening the "+DisplayEditionName(to)+" database to copy into...\n")
	log, err = dst.up(ctx)
	emit(progress, log)
	if err != nil {
		return err
	}
	defer dst.down()

	target, err := editionmigrate.Probe(ctx, dst.dsn())
	if err != nil {
		return fmt.Errorf("inspect %s database: %w", DisplayEditionName(to), err)
	}
	if target.Accounts > 0 {
		emit(progress, fmt.Sprintf("The %s edition's old data (%d account(s)) will be replaced by the %s data.\n",
			DisplayEditionName(to), target.Accounts, DisplayEditionName(from)))
	}

	emit(progress, fmt.Sprintf("Copying %d account(s) and all their data to the %s database...\n",
		source.Accounts, DisplayEditionName(to)))
	if err := editionmigrate.Copy(ctx, src.dsn(), dst.dsn(), progress); err != nil {
		return err
	}
	if opts.Blobs == BlobsKeep {
		emit(progress, "Media files stay where they are; the server reads each one from the storage that holds it.\n")
		return nil
	}
	return m.moveBlobs(ctx, to, dst.dsn(), force, progress)
}

// moveBlobs copies the media the freshly migrated database lists under the
// old edition's storage over to the new edition's, relabelling rows as it
// goes. dsn is the *target* database: the source one is left exactly as it
// was, so switching back still finds everything where it expects it.
func (m *Manager) moveBlobs(ctx context.Context, to, dsn string, force bool, progress func(string)) error {
	toBackend, fromBackend := domain.MediaBackendS3, domain.MediaBackendLocalFS
	if to == editionPortable {
		toBackend, fromBackend = domain.MediaBackendLocalFS, domain.MediaBackendS3
	}

	// An exclusive lock proves no server is attached to this database; a
	// relabelled row under a running server would point it at storage it is
	// not reading from.
	lock, err := postgres.AcquireBlobMigrationLock(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	pool, err := postgres.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open the target database for media: %w", err)
	}
	defer pool.Close()
	media := postgres.NewMediaStore(pool)

	counts, err := media.FileBlobBackendCounts(ctx)
	if err != nil {
		return err
	}
	if counts[fromBackend] == 0 {
		emit(progress, "No media files stored on "+string(fromBackend)+" -- nothing to move.\n")
		return nil
	}

	emit(progress, fmt.Sprintf("Moving media files from %s to %s...\n", fromBackend, toBackend))
	src, err := m.blobStoreFor(ctx, fromBackend)
	if err != nil {
		return err
	}
	dst, err := m.blobStoreFor(ctx, toBackend)
	if err != nil {
		return err
	}
	result, err := editionmigrate.CopyBlobs(ctx, media, fromBackend, toBackend, src, dst, force, progress)
	if err != nil {
		return err
	}
	emit(progress, fmt.Sprintf("Moved %d media files (%s). The originals were left in place.\n", result.Moved, formatBytes(result.Bytes)))
	if len(result.Skipped) > 0 {
		emit(progress, fmt.Sprintf("%d files could not be moved and stay on %s: %s\n",
			len(result.Skipped), fromBackend, strings.Join(result.Skipped, ", ")))
	}
	return nil
}

// blobStoreFor builds one storage backend from .env, resolving the same
// settings and defaults as internal/config.
func (m *Manager) blobStoreFor(ctx context.Context, backend domain.MediaBackend) (editionmigrate.BlobStore, error) {
	values, err := m.readEnvFile()
	if err != nil {
		return nil, err
	}
	get := func(key, def string) string {
		if v := strings.TrimSpace(values[key]); v != "" {
			return v
		}
		return def
	}
	if backend == domain.MediaBackendLocalFS {
		dir := get("TELESRV_BLOB_DIR", "data/blobs")
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(m.Root, dir)
		}
		return filesapp.NewLocalFS(dir)
	}

	endpoint := get("TELESRV_S3_ENDPOINT", "127.0.0.1:9000")
	useSSL := strings.EqualFold(get("TELESRV_S3_USE_SSL", "false"), "true")
	pathStyle := !strings.EqualFold(get("TELESRV_S3_PATH_STYLE", "true"), "false")
	// The container was started moments ago on the classic side and is only
	// waited on for PostgreSQL, so MinIO may still be coming up.
	var lastErr error
	for attempt := 0; attempt < 15; attempt++ {
		store, err := filesapp.NewS3FS(ctx, endpoint,
			get("TELESRV_S3_ACCESS_KEY_ID", "owpengram"),
			get("TELESRV_S3_SECRET_ACCESS_KEY", "owpengram123"),
			get("TELESRV_S3_BUCKET", "owpengram-media"),
			get("TELESRV_S3_REGION", "us-east-1"),
			useSSL, pathStyle)
		if err == nil {
			return store, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("reach the S3 storage at %s: %w", endpoint, lastErr)
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// checkBlobsSurviveEdition refuses a switch that would leave already-stored
// media unreachable.
//
// Only one case can: switching to portable without moving the media. The
// portable edition has no MinIO (no Docker at all), so internal/config.Load
// drops a loopback S3 endpoint entirely -- blobs written to the
// compose-managed MinIO would still be recorded in file_blobs with
// backend='s3' and no backend left able to serve them. Moving them (the
// default) fixes that; keeping them is only allowed with Force.
//
// An external S3 endpoint is deliberately fine: config keeps that one, and
// portable PostgreSQL with cloud object storage is a coherent setup.
func (m *Manager) checkBlobsSurviveEdition(to string, opts EditionChange) error {
	if to != editionPortable || opts.Blobs != BlobsKeep || opts.Force {
		return nil
	}
	values, err := m.readEnvFile()
	if err != nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(values["TELESRV_BLOB_BACKEND"]), "s3") {
		return nil
	}
	if !isLoopbackEndpoint(values["TELESRV_S3_ENDPOINT"]) {
		return nil
	}
	return fmt.Errorf("this install stores media in the local MinIO container (TELESRV_BLOB_BACKEND=s3), which the portable edition does not run -- that media would become unreachable. Let it move the media to TELESRV_BLOB_DIR (the default), or re-run with --force to keep it where it is and lose access to it")
}

// maintenancePGFor builds the maintenance handle for one edition's
// PostgreSQL.
func (m *Manager) maintenancePGFor(edition string) (maintenancePG, error) {
	if edition == editionPortable {
		return &embeddedMaintenancePG{
			dir:     m.embeddedPostgresDataDir(),
			port:    m.embeddedPostgresPort(),
			logPath: m.embeddedPostgresLog(),
		}, nil
	}

	dsn := ""
	if values, err := m.readEnvFile(); err == nil {
		dsn = strings.TrimSpace(values["TELESRV_POSTGRES_DSN"])
	}
	if !isLoopbackDSN(dsn) {
		// Bringing this one up means `docker compose up` against this
		// checkout's own compose file, so a DSN pointing at some other
		// PostgreSQL would have its data copied over by a container that
		// has nothing to do with it.
		return nil, fmt.Errorf("TELESRV_POSTGRES_DSN does not point at this checkout's own PostgreSQL container (%s), so the data cannot be moved automatically -- migrate that database yourself", dsn)
	}
	return &dockerMaintenancePG{m: m, connDSN: dsn}, nil
}

// embeddedPostgresPort resolves TELESRV_EMBEDDED_POSTGRES_PORT the way
// internal/config does, falling back to the same default.
func (m *Manager) embeddedPostgresPort() int {
	if values, err := m.readEnvFile(); err == nil {
		if n, convErr := strconv.Atoi(strings.TrimSpace(values["TELESRV_EMBEDDED_POSTGRES_PORT"])); convErr == nil && n > 0 {
			return n
		}
	}
	return embeddedpg.DefaultPort
}

// --- portable edition ------------------------------------------------------

// embeddedMaintenancePG drives the portable edition's embedded PostgreSQL.
type embeddedMaintenancePG struct {
	dir  string
	port int
	// logPath receives the server's own startup/shutdown output. Left to
	// itself the library writes that to os.Stdout, which lands in the middle
	// of whatever terminal UI is driving the migration.
	logPath string
	logf    *os.File
	srv     *embeddedpg.Server
}

// embeddedPostgresLog is where the maintenance-mode embedded PostgreSQL's
// output goes, next to the other binaries' logs.
func (m *Manager) embeddedPostgresLog() string {
	return filepath.Join(m.Root, "logs", "embedded-postgres.log")
}

// up starts the embedded server through the same package
// owpengram-server itself uses, so a cluster that doesn't exist yet gets
// initialised (and its binaries downloaded) here rather than failing --
// which is what switching *to* portable on a Docker-only install needs.
func (e *embeddedMaintenancePG) up(ctx context.Context) (string, error) {
	var logger io.Writer = io.Discard
	if err := os.MkdirAll(filepath.Dir(e.logPath), 0o755); err == nil {
		if f, err := os.OpenFile(e.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			e.logf = f
			logger = f
		}
	}
	srv, err := embeddedpg.Start(e.dir, e.port, logger)
	if err != nil {
		e.closeLog()
		return "", fmt.Errorf("start embedded PostgreSQL (see %s): %w", e.logPath, err)
	}
	e.srv = srv
	return "", nil
}

func (e *embeddedMaintenancePG) down() {
	if e.srv != nil {
		_ = e.srv.Stop()
		e.srv = nil
	}
	// After Stop: pg_ctl's shutdown output is still written through it.
	e.closeLog()
}

func (e *embeddedMaintenancePG) closeLog() {
	if e.logf != nil {
		_ = e.logf.Close()
		e.logf = nil
	}
}

func (e *embeddedMaintenancePG) dsn() string { return embeddedpg.DSN(e.port) }

// --- classic edition -------------------------------------------------------

// dockerMaintenancePG drives the classic edition's PostgreSQL container.
// Only the bring-up is Docker-specific; the container publishes the port on
// loopback, so the connection itself is the DSN from .env.
type dockerMaintenancePG struct {
	m       *Manager
	connDSN string
}

func (d *dockerMaintenancePG) up(ctx context.Context) (string, error) {
	// composeUpWaitPostgres rather than ensureDocker: .env still names the
	// edition being migrated *away from*, and ensureDocker deliberately
	// skips the bring-up when that says portable -- precisely the case
	// where this needs the container running.
	return d.m.composeUpWaitPostgres(ctx, d.m.loadState())
}

// down leaves the container running: it is a long-lived service either way
// (a successful migration restarts into it, and an operator who had it up
// before a failed one expects it still up afterwards).
func (d *dockerMaintenancePG) down() {}

func (d *dockerMaintenancePG) dsn() string { return d.connDSN }

// --- shared ----------------------------------------------------------------

// isLoopbackDSN reports whether a PostgreSQL URL addresses this machine's
// loopback interface, which is where deploy/docker-compose.yml publishes
// the classic edition's container.
func isLoopbackDSN(dsn string) bool {
	if dsn == "" {
		return false
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		return false
	}
	return isLoopbackEndpoint(parsed.Host)
}

// isLoopbackEndpoint reports whether a host, host:port or URL-ish endpoint
// resolves to this machine's loopback interface.
func isLoopbackEndpoint(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	if idx := strings.Index(endpoint, "://"); idx >= 0 {
		endpoint = endpoint[idx+3:]
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	host := endpoint
	if h, _, err := net.SplitHostPort(endpoint); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func emit(progress func(string), text string) {
	if progress == nil || strings.TrimSpace(text) == "" {
		return
	}
	progress(text)
}
