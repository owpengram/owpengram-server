package procctl

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"telesrv/internal/editionmigrate"
	"telesrv/internal/embeddedpg"
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
// correctly. force overrides the refusal to overwrite a target that holds
// accounts of its own.
func (m *Manager) ChangeEdition(ctx context.Context, to string, force bool, progress func(string)) error {
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

	if err := m.migrateEditionData(ctx, from, to, force, progress); err != nil {
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
func (m *Manager) migrateEditionData(ctx context.Context, from, to string, force bool, progress func(string)) error {
	if err := m.checkBlobsSurviveEdition(to, force); err != nil {
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
	if target.Accounts > 0 && !force {
		return fmt.Errorf("the %s edition already holds %d account(s) of its own -- refusing to replace them with the %s data; re-run `telesrv-ctl set-edition %s --force` to overwrite them",
			DisplayEditionName(to), target.Accounts, DisplayEditionName(from), DisplayEditionName(to))
	}

	emit(progress, fmt.Sprintf("Copying %d account(s) and all their data to the %s database...\n",
		source.Accounts, DisplayEditionName(to)))
	if err := editionmigrate.Copy(ctx, src.dsn(), dst.dsn(), progress); err != nil {
		return err
	}
	return nil
}

// checkBlobsSurviveEdition refuses a switch that would leave already-stored
// media unreachable.
//
// Only one direction can: the portable edition has no MinIO (no Docker at
// all), so internal/config.Load drops a loopback S3 endpoint entirely --
// blobs written to the compose-managed MinIO would still be recorded in
// file_blobs with backend='s3' and no backend left able to serve them.
// Blobs are content-addressed and file_blobs.backend is per row, so copying
// them across is a straightforward pass that simply isn't written yet;
// until it is, refusing beats silently serving broken media.
//
// An external S3 endpoint is deliberately fine: config keeps that one, and
// portable PostgreSQL with cloud object storage is a coherent setup.
func (m *Manager) checkBlobsSurviveEdition(to string, force bool) error {
	if to != editionPortable || force {
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
	return fmt.Errorf("this install stores media in the local MinIO container (TELESRV_BLOB_BACKEND=s3), which the portable edition does not run -- that media would become unreachable. Move it to TELESRV_BLOB_DIR first, or re-run with --force to switch anyway and lose access to it")
}

// maintenancePGFor builds the maintenance handle for one edition's
// PostgreSQL.
func (m *Manager) maintenancePGFor(edition string) (maintenancePG, error) {
	if edition == editionPortable {
		return &embeddedMaintenancePG{
			dir:  m.embeddedPostgresDataDir(),
			port: m.embeddedPostgresPort(),
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
	srv  *embeddedpg.Server
}

// up starts the embedded server through the same package
// owpengram-server itself uses, so a cluster that doesn't exist yet gets
// initialised (and its binaries downloaded) here rather than failing --
// which is what switching *to* portable on a Docker-only install needs.
func (e *embeddedMaintenancePG) up(ctx context.Context) (string, error) {
	srv, err := embeddedpg.Start(e.dir, e.port, nil)
	if err != nil {
		return "", fmt.Errorf("start embedded PostgreSQL: %w", err)
	}
	e.srv = srv
	return "", nil
}

func (e *embeddedMaintenancePG) down() {
	if e.srv != nil {
		_ = e.srv.Stop()
		e.srv = nil
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
