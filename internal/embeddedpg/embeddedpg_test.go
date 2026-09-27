package embeddedpg

import (
	"context"
	"database/sql"
	"io"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestStartStopReal downloads and runs a real embedded PostgreSQL server --
// slow (first run fetches ~30MB) and needs outbound network access, so it's
// gated behind an env var rather than running in the default `go test
// ./...`, the same convention internal/loadtest uses for its own
// real-Postgres cases.
//
//	TELESRV_TEST_EMBEDDED_POSTGRES=1 go test ./internal/embeddedpg/ -run TestStartStopReal -v -count=1
//
// Manually verified beyond what this covers, on the actual target platform
// (Windows): a fresh start (schema migration + full app seed against it)
// and a simulated crash (hard-killed mid-run) followed by a clean restart
// on the same data directory, recovering without data loss or re-seeding.
func TestStartStopReal(t *testing.T) {
	if os.Getenv("TELESRV_TEST_EMBEDDED_POSTGRES") == "" {
		t.Skip("set TELESRV_TEST_EMBEDDED_POSTGRES=1 to run (downloads a real Postgres binary)")
	}

	dataDir := t.TempDir()
	const port = 25599

	srv, err := Start(dataDir, port, io.Discard)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := srv.Stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()

	if got, want := srv.DSN(), DSN(port); got != want {
		t.Fatalf("DSN() = %q, want %q", got, want)
	}

	db, err := sql.Open("pgx", srv.DSN())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var one int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("query: %v", err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", one)
	}
}

func TestStopOnNilServerIsNoop(t *testing.T) {
	var srv *Server
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop() on nil *Server = %v, want nil", err)
	}
}
