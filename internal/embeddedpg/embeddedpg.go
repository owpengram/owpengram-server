// Package embeddedpg wraps github.com/fergusstrange/embedded-postgres so a
// server can run with a natively compiled PostgreSQL and nothing to install:
// owpengram-ctl starts it before owpengram-server and owpengram-admin-panel
// and stops it on exit (TELESRV_POSTGRES_MODE=embedded, see internal/config).
//
// owpengram-ctl OWNS the server's lifecycle. owpengram-server and
// owpengram-admin-panel are ordinary clients: internal/config.Load computes
// the same deterministic DSN (this package's DSN function) for both, so
// neither needs to be told where it is, and a restart of either leaves the
// database running.
package embeddedpg

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

const (
	// DefaultPort avoids colliding with a system PostgreSQL that might also
	// be listening on 5432 on the same machine.
	DefaultPort = 15433
	// Username/Password/Database match the optional deploy/docker-compose.yml
	// postgres service (POSTGRES_USER/PASSWORD/DB all "owpengram") -- not a
	// security boundary (127.0.0.1-only), just consistent naming.
	username = "owpengram"
	password = "owpengram"
	database = "owpengram"
	// pgVersion is pinned to match the postgres:17-alpine of the optional
	// compose file, so a dump moves between the two without surprises.
	pgVersion = embeddedpostgres.V17
)

// DSN returns the connection string for an embedded server listening on
// port -- pure and side-effect-free, so internal/config can compute it for
// both cmd/telesrv (which also starts the server) and cmd/telesrv-admin
// (which only ever connects).
func DSN(port int) string {
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", username, password, port, database)
}

// Server is a running embedded PostgreSQL instance.
type Server struct {
	db  *embeddedpostgres.EmbeddedPostgres
	dsn string
}

// Start launches an embedded PostgreSQL server rooted at dataDir
// (typically Config.EmbeddedPostgresDataDir), listening on port
// (Config.EmbeddedPostgresPort), and blocks until it's accepting
// connections. logger receives the server's own startup/shutdown output
// (verbose and, on Windows, in the OS's configured locale -- worth routing
// to a debug-level sink rather than stdout).
func Start(dataDir string, port int, logger io.Writer) (*Server, error) {
	if port <= 0 {
		port = DefaultPort
	}
	// A previous run's embedded server can still be alive and holding this
	// exact port -- not from a clean Stop() (that always precedes releasing
	// it), but from owpengram-ctl itself being killed or crashing: Postgres is a
	// real child process of telesrv on every platform, and nothing here
	// sets up the OS-level linkage (a Windows Job Object, a process group
	// on Unix) that would make it die automatically alongside its parent --
	// see internal/procctl.killPID's own "/T" doc comment for the Windows
	// half of that story. The library's own Start() only ever *detects*
	// "something is already listening on this port" and refuses outright;
	// it never asks whether that something is a stale instance of itself
	// it could just clean up and take over from -- so every start
	// pre-empts that by trying to gracefully stop whatever's already
	// running against this exact data directory first. A clean prior
	// shutdown leaves no postmaster.pid, so this is a fast no-op in the
	// overwhelmingly common case.
	stopStaleInstance(dataDir)

	cfg := embeddedpostgres.DefaultConfig().
		Version(pgVersion).
		Port(uint32(port)).
		Username(username).
		Password(password).
		Database(database).
		// "C" rather than the en_US.utf8 of the compose file's image:
		// avoids depending on the host OS having that locale installed
		// (Windows Postgres builds use a different locale-name scheme
		// entirely) and only affects text sort order, not the data itself
		// -- pg_dump/pg_restore between the two do not depend on this
		// matching.
		Locale("C").
		Encoding("UTF8").
		// A release archive (see scripts/build-release.sh) pre-places
		// the exact cache file the library would otherwise fetch from
		// Maven Central on first run -- same filename scheme
		// (embedded-postgres-binaries-<os>-<arch>-<version>.txz) computed
		// from CachePath, so Start() below finds it already "downloaded"
		// and never touches the network. A git-clone install has no such
		// file here; the library just downloads it once as it always did,
		// straight into this same directory.
		CachePath(filepath.Join(filepath.Dir(dataDir), "pgcache")).
		DataPath(filepath.Join(dataDir, "data")).
		RuntimePath(filepath.Join(dataDir, "runtime")).
		StartTimeout(60 * time.Second)
	if logger != nil {
		cfg = cfg.Logger(logger)
	}

	db := embeddedpostgres.NewDatabase(cfg)
	if err := db.Start(); err != nil {
		return nil, fmt.Errorf("start embedded postgres: %w", annotateStartError(err))
	}
	return &Server{db: db, dsn: DSN(port)}, nil
}

// annotateStartError appends an actionable hint when the built-in PostgreSQL
// fails to start on Windows with STATUS_DLL_NOT_FOUND (0xc0000135): the zonky
// PostgreSQL binaries are MSVC builds, so they need the Microsoft Visual C++
// Redistributable that a bare Windows install often lacks. Other errors (and
// every error on non-Windows) pass through unchanged.
func annotateStartError(err error) error {
	if runtime.GOOS != "windows" || err == nil || !strings.Contains(err.Error(), "0xc0000135") {
		return err
	}
	return fmt.Errorf("%w\n\n    the built-in PostgreSQL needs the Microsoft Visual C++ Redistributable\n    (2015-2022, x64) -- install vc_redist.x64.exe from\n    https://aka.ms/vs/17/release/vc_redist.x64.exe and start again", err)
}

// DSN is this running server's connection string.
func (s *Server) DSN() string { return s.dsn }

// stopStaleInstance best-effort-stops whatever embedded PostgreSQL is
// already running against dataDir, via a graceful `pg_ctl stop -m fast` --
// never a hard kill, so a shutdown mid-checkpoint can't corrupt data on the
// way to (usually) being reused by the fresh Start() that follows. Silent
// and harmless when there's nothing to stop: no postmaster.pid at all (the
// overwhelmingly common case -- a clean prior Stop() always removes it),
// no runtime downloaded yet (the very first run ever), or a postmaster.pid
// left over from a process that's already gone (pg_ctl's own stale-lock
// handling reports that as an error, which is discarded here exactly like
// every other outcome -- Start() right after this call is what actually
// surfaces a real, still-unavailable port as an error worth the caller
// seeing).
func stopStaleInstance(dataDir string) {
	if _, err := os.Stat(filepath.Join(dataDir, "data", "postmaster.pid")); err != nil {
		return
	}
	pgCtl := pgCtlPath(dataDir)
	if _, err := os.Stat(pgCtl); err != nil {
		return
	}
	cmd := exec.Command(pgCtl, "stop", "-D", filepath.Join(dataDir, "data"), "-m", "fast")
	hideWindow(cmd)
	_ = cmd.Run()
}

func pgCtlPath(dataDir string) string {
	name := "pg_ctl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dataDir, "runtime", "bin", name)
}

// Stop shuts the server down. Must be called after every user of DSN() has
// finished with it (e.g. after a pgx pool's own Close()) -- embedded
// Postgres has no other client to hand off to once this returns. A nil
// receiver is a no-op, so a deferred Stop() is safe even when Start never
// ran (an external PostgreSQL).
func (s *Server) Stop() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Stop()
}
