package loadharness

import (
	"path/filepath"
	"testing"

	"github.com/owpengram/owpengram-td/tg"
)

func TestPersistedFileFixtureRoundTripAndIdentityChecks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.json")
	endpoint := Endpoint{Address: "127.0.0.1:2398", DC: 2}
	want := &downloadFixture{
		location: &tg.InputDocumentFileLocation{ID: 42, AccessHash: 99, FileReference: []byte{1, 2, 3}},
		size:     4 << 20, chunk: 1 << 20,
	}
	if err := writePersistedFileFixture(path, endpoint, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadPersistedFileFixture(path, endpoint, want.size, want.chunk)
	if err != nil {
		t.Fatal(err)
	}
	if got.size != want.size || got.chunk != want.chunk || got.location.ID != want.location.ID || got.location.AccessHash != want.location.AccessHash || string(got.location.FileReference) != string(want.location.FileReference) {
		t.Fatalf("fixture = %#v, want %#v", got, want)
	}
	if _, err := loadPersistedFileFixture(path, Endpoint{Address: "other:2398", DC: 2}, want.size, want.chunk); err == nil {
		t.Fatal("expected endpoint mismatch")
	}
	if _, err := loadPersistedFileFixture(path, endpoint, want.size/2, want.chunk); err == nil {
		t.Fatal("expected size mismatch")
	}
}

func TestResolveFileFixturePathDefaultsBesideManifest(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "bundle", "manifest.json")
	if got, want := resolveFileFixturePath(manifest, ""), filepath.Join(filepath.Dir(manifest), "file-fixture.json"); got != want {
		t.Fatalf("default path = %q, want %q", got, want)
	}
	if got, want := resolveFileFixturePath(manifest, "custom.json"), filepath.Join(filepath.Dir(manifest), "custom.json"); got != want {
		t.Fatalf("relative path = %q, want %q", got, want)
	}
}
