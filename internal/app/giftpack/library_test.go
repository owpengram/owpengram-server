package giftpack

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"telesrv/internal/domain"
)

// memLibraryStore is an in-memory LibraryStore.
type memLibraryStore struct {
	mu    sync.Mutex
	packs map[string]domain.GiftPack
	order []string
}

func newMemLibraryStore() *memLibraryStore {
	return &memLibraryStore{packs: map[string]domain.GiftPack{}}
}

func (m *memLibraryStore) SaveGiftPack(_ context.Context, pack domain.GiftPack) (domain.GiftPack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, existed := m.packs[pack.PackID]; !existed {
		m.order = append(m.order, pack.PackID)
	}
	m.packs[pack.PackID] = pack
	return pack, nil
}

func (m *memLibraryStore) GiftPacks(context.Context) ([]domain.GiftPack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.GiftPack, 0, len(m.order))
	for _, id := range m.order {
		pack := m.packs[id]
		pack.Archive = nil // the real store never loads archives for a listing
		out = append(out, pack)
	}
	return out, nil
}

func (m *memLibraryStore) GiftPack(_ context.Context, packID string) (domain.GiftPack, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pack, ok := m.packs[packID]
	return pack, ok, nil
}

func (m *memLibraryStore) DeleteGiftPack(_ context.Context, packID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.packs[packID]; !ok {
		return false, nil
	}
	delete(m.packs, packID)
	for i, id := range m.order {
		if id == packID {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	return true, nil
}

// fixtureArchive zips the shared test manifest and its assets into the .zip
// an operator would upload.
func fixtureArchive(t *testing.T, mutate func(*Manifest)) []byte {
	t.Helper()
	manifest := fixtureManifest()
	manifest.Description = "A pack for tests."
	if mutate != nil {
		mutate(&manifest)
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("pack.json", manifestJSON)
	for name, data := range fixtureAssets() {
		write(name, data)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func newTestLibrary(t *testing.T) (*Library, *memLibraryStore) {
	t.Helper()
	store := newMemLibraryStore()
	return NewLibrary(store, newTestService(t)), store
}

// TestLibraryStoresAndPreviewsAnUploadedPack is the whole shelf loop: an
// archive goes in, comes back as a previewable summary, and every animation
// the preview would show resolves by slug.
func TestLibraryStoresAndPreviewsAnUploadedPack(t *testing.T) {
	lib, _ := newTestLibrary(t)
	ctx := context.Background()

	pack, err := lib.Save(ctx, "test-pack.zip", fixtureArchive(t, nil), "operator")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if pack.PackID != "test-pack" {
		t.Fatalf("PackID = %q, want test-pack (slugified from the pack name)", pack.PackID)
	}
	if pack.GiftCount != 3 || pack.UploadedBy != "operator" {
		t.Fatalf("stored %+v, want 3 gifts uploaded by operator", pack)
	}

	packs, err := lib.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(packs) != 1 || packs[0].ID != "test-pack" || len(packs[0].Gifts) != 3 {
		t.Fatalf("List = %+v, want one pack with three gifts", packs)
	}
	if packs[0].Description != "A pack for tests." {
		t.Errorf("Description = %q, want the manifest's", packs[0].Description)
	}
	if packs[0].Icon != packs[0].Gifts[0].Slug {
		t.Errorf("Icon = %q, want the first gift's slug %q", packs[0].Icon, packs[0].Gifts[0].Slug)
	}

	// Every slug the panel would ask for must resolve -- including the
	// upgradeable gift's models and patterns, which have no slug of their
	// own in the manifest.
	upgradeable := packs[0].Gifts[1]
	slugs := []string{packs[0].Icon, upgradeable.Slug}
	for _, a := range append(append([]AttrSummary{}, upgradeable.Upgrade.Models...), upgradeable.Upgrade.Patterns...) {
		slugs = append(slugs, a.ID)
	}
	for _, slug := range slugs {
		data, found, err := lib.Animation(ctx, "test-pack", slug)
		if err != nil || !found {
			t.Fatalf("Animation(%q): err=%v found=%v", slug, err, found)
		}
		if !bytes.Contains(data, []byte(`"layers"`)) {
			t.Errorf("Animation(%q) is not Lottie JSON", slug)
		}
	}
}

// TestLibraryAnimationRefusesUnknownSlugs pins that the preview endpoint
// cannot be steered at an arbitrary archive entry: it resolves through the
// manifest's own slug map, so a path is never taken from the caller.
func TestLibraryAnimationRefusesUnknownSlugs(t *testing.T) {
	lib, _ := newTestLibrary(t)
	ctx := context.Background()
	if _, err := lib.Save(ctx, "test-pack.zip", fixtureArchive(t, nil), "operator"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, slug := range []string{"pack.json", "basic.json", "../../etc/passwd", "nope"} {
		if _, found, err := lib.Animation(ctx, "test-pack", slug); found || err != nil {
			t.Errorf("Animation(%q) = found %v err %v, want not found", slug, found, err)
		}
	}
}

// TestLibraryRejectsAPackMissingAnAsset proves validation happens on the way
// in. Without it a pack sits on the shelf looking importable and fails
// halfway through the import, leaving half its gifts published.
func TestLibraryRejectsAPackMissingAnAsset(t *testing.T) {
	lib, store := newTestLibrary(t)
	broken := fixtureArchive(t, func(m *Manifest) {
		m.Gifts[0].BaseAnimation = "not-in-the-archive.json"
	})
	if _, err := lib.Save(context.Background(), "test-pack.zip", broken, "operator"); err == nil {
		t.Fatal("Save accepted a pack whose animation is missing from the archive")
	}
	if len(store.packs) != 0 {
		t.Fatalf("a rejected pack was stored anyway: %+v", store.packs)
	}
}

// TestLibraryReuploadReplacesTheSamePack: shipping a corrected build of a
// pack is the normal case, and it must not leave two near-identical packs on
// the shelf.
func TestLibraryReuploadReplacesTheSamePack(t *testing.T) {
	lib, _ := newTestLibrary(t)
	ctx := context.Background()
	if _, err := lib.Save(ctx, "v1.zip", fixtureArchive(t, nil), "operator"); err != nil {
		t.Fatalf("Save v1: %v", err)
	}
	updated := fixtureArchive(t, func(m *Manifest) {
		m.Gifts = append(m.Gifts, GiftSpec{Title: "Extra", Stars: 5, BaseAnimation: "basic.json"})
	})
	if _, err := lib.Save(ctx, "v2.zip", updated, "operator"); err != nil {
		t.Fatalf("Save v2: %v", err)
	}
	packs, err := lib.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("shelf holds %d packs, want 1", len(packs))
	}
	if len(packs[0].Gifts) != 4 {
		t.Fatalf("pack has %d gifts, want the re-uploaded 4", len(packs[0].Gifts))
	}
}

func TestLibraryDelete(t *testing.T) {
	lib, _ := newTestLibrary(t)
	ctx := context.Background()
	if _, err := lib.Save(ctx, "test-pack.zip", fixtureArchive(t, nil), "operator"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	removed, err := lib.Delete(ctx, "test-pack")
	if err != nil || !removed {
		t.Fatalf("Delete = %v, %v; want true, nil", removed, err)
	}
	if removed, _ := lib.Delete(ctx, "test-pack"); removed {
		t.Error("Delete reported a second removal of the same pack")
	}
	packs, _ := lib.List(ctx)
	if len(packs) != 0 {
		t.Fatalf("shelf still holds %d packs", len(packs))
	}
}

// TestSlugCollisionIsRejected: two gift titles that reduce to one URL slug
// would share a preview handle, showing one gift's art for both.
func TestSlugCollisionIsRejected(t *testing.T) {
	manifest := fixtureManifest()
	manifest.Gifts[1].Title = "Basic!"
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	_, err = ParseManifest(data)
	if err == nil || !strings.Contains(err.Error(), "id slug") {
		t.Fatalf("ParseManifest err = %v, want a slug collision refusal", err)
	}
}
