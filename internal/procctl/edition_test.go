package procctl

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const editionTestTemplate = `## Edition
TELESRV_EDITION=
TELESRV_EMBEDDED_POSTGRES_DIR=data/postgres
TELESRV_BLOB_BACKEND=s3
`

func newEditionTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte(editionTestTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewManager(dir)
}

func TestEditionUnsetByDefault(t *testing.T) {
	m := newEditionTestManager(t)
	if edition, ok := m.Edition(); ok {
		t.Fatalf("Edition() = %q, true before anything was persisted", edition)
	}
}

func TestSetEditionRoundTrips(t *testing.T) {
	m := newEditionTestManager(t)
	for _, want := range []string{"portable", "standard"} {
		if err := m.SetEdition(want); err != nil {
			t.Fatalf("SetEdition(%q): %v", want, err)
		}
		got, ok := m.Edition()
		if !ok || got != want {
			t.Fatalf("Edition() = %q, %v, want %q, true", got, ok, want)
		}
	}
}

func TestEditionIgnoresGarbageValue(t *testing.T) {
	m := newEditionTestManager(t)
	if err := os.WriteFile(filepath.Join(m.Root, ".env"), []byte("TELESRV_EDITION=nonsense\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Edition(); ok {
		t.Fatal("Edition() accepted a value that is neither standard nor portable")
	}
}

// Portable has no MinIO to talk to and internal/config.Load force-sets
// localfs regardless, so leaving "s3" in .env only produces a misleading
// "s3 blob backend configured but failed to initialize" warning on every
// start -- see SetEdition's doc comment.
func TestSetEditionPortablePinsBlobBackendToLocalfs(t *testing.T) {
	m := newEditionTestManager(t)
	if err := m.SetEdition("portable"); err != nil {
		t.Fatal(err)
	}
	values, err := m.readEnvFile()
	if err != nil {
		t.Fatal(err)
	}
	if values["TELESRV_BLOB_BACKEND"] != "localfs" {
		t.Fatalf("TELESRV_BLOB_BACKEND = %q, want localfs", values["TELESRV_BLOB_BACKEND"])
	}
}

// Switching back must not silently re-point the server at an object store
// that doesn't have the blobs written while it was portable.
func TestSetEditionStandardLeavesBlobBackendAlone(t *testing.T) {
	m := newEditionTestManager(t)
	if err := m.SetEdition("portable"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEdition("standard"); err != nil {
		t.Fatal(err)
	}
	values, err := m.readEnvFile()
	if err != nil {
		t.Fatal(err)
	}
	if values["TELESRV_BLOB_BACKEND"] != "localfs" {
		t.Fatalf("TELESRV_BLOB_BACKEND = %q after portable->standard, want it left at localfs", values["TELESRV_BLOB_BACKEND"])
	}
}

func TestStopEmbeddedPostgresIsNoopOutsidePortable(t *testing.T) {
	m := newEditionTestManager(t)
	if err := m.SetEdition("standard"); err != nil {
		t.Fatal(err)
	}
	// Must not panic or touch anything; there is no runtime/data dir here.
	m.StopEmbeddedPostgres()
}

// Portable never runs `docker compose up` (see ensureDocker), so
// DockerStatus must not even try `docker compose ps` for it -- Docker
// merely being installed but not running (a real setup: Docker Desktop
// present for something else, portable edition not using it) would
// otherwise surface as a spurious error in the admin panel's Services tab.
func TestDockerStatusSkipsDockerEntirelyForPortableEdition(t *testing.T) {
	m := newEditionTestManager(t)
	if err := m.SetEdition("portable"); err != nil {
		t.Fatal(err)
	}
	services, err := m.DockerStatus(context.Background())
	if err != nil {
		t.Fatalf("DockerStatus() error = %v, want nil", err)
	}
	if services != nil {
		t.Fatalf("DockerStatus() = %v, want nil", services)
	}
}

func TestEmbeddedPostgresDataDirDefaultsUnderRoot(t *testing.T) {
	m := newEditionTestManager(t)
	want := filepath.Join(m.Root, "data", "postgres")
	if got := m.embeddedPostgresDataDir(); got != want {
		t.Fatalf("embeddedPostgresDataDir() = %q, want %q", got, want)
	}
}
