package procctl

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A release archive install (see scripts/build-release.sh) has bin/
// binaries and no go.mod/cmd sources at all -- goBuild must treat the
// existing binary as already built rather than trying (and failing) to
// "go build" a package that isn't there.
func TestGoBuildUsesExistingBinaryWithNoSourceTree(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	exe := filepath.Join(dir, "bin", "owpengram-server")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("fake binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	log, err := m.goBuild(context.Background(), exe, "./cmd/telesrv")
	if err != nil {
		t.Fatalf("goBuild with no source tree but an existing binary: %v", err)
	}
	if log == "" {
		t.Fatal("expected a non-empty log explaining the binary was kept as-is")
	}

	// The file must be untouched -- no rebuild attempted.
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake binary" {
		t.Fatalf("existing binary was modified: %q", data)
	}
}

func TestGoBuildErrorsWithNoSourceTreeAndNoBinary(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	exe := filepath.Join(dir, "bin", "owpengram-server")
	if _, err := m.goBuild(context.Background(), exe, "./cmd/telesrv"); err == nil {
		t.Fatal("expected an error when there is neither a source tree nor an existing binary")
	}
}

func TestHasSourceTreeDetectsGoMod(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	if m.hasSourceTree() {
		t.Fatal("hasSourceTree() true in a directory with no go.mod")
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !m.hasSourceTree() {
		t.Fatal("hasSourceTree() false in a directory with a go.mod")
	}
}
