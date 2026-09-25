package postgres

import (
	"bytes"
	"context"
	"testing"
	"time"

	"telesrv/internal/domain"
)

// TestGiftPackStoreRoundTrip exercises the shelf against real Postgres: an
// archive survives the round trip byte for byte, a listing never drags the
// archives along with it, and re-uploading a pack replaces it instead of
// leaving two copies behind.
func TestGiftPackStoreRoundTrip(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewGiftPackStore(pool)

	packID := "testpack" + randomSuffix(t)[:6]
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM gift_packs WHERE pack_id = $1", packID) })

	archive := bytes.Repeat([]byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0xff}, 4096)
	uploaded := time.Now().UTC().Truncate(time.Millisecond)
	saved, err := store.SaveGiftPack(ctx, domain.GiftPack{
		PackID: packID, Name: "Test Pack", Author: "OwpenGram", Description: "d",
		FileName: "test.zip", GiftCount: 3, SizeBytes: len(archive),
		Manifest: []byte(`{"pack_name":"Test Pack"}`), Archive: archive,
		UploadedBy: "operator", UploadedAt: uploaded,
	})
	if err != nil {
		t.Fatalf("SaveGiftPack: %v", err)
	}
	if saved.PackID != packID || saved.GiftCount != 3 || saved.UploadedBy != "operator" {
		t.Fatalf("SaveGiftPack returned %+v", saved)
	}

	loaded, found, err := store.GiftPack(ctx, packID)
	if err != nil || !found {
		t.Fatalf("GiftPack: %v (found=%v)", err, found)
	}
	if !bytes.Equal(loaded.Archive, archive) {
		t.Fatalf("archive round-tripped as %d bytes, want %d", len(loaded.Archive), len(archive))
	}
	if !loaded.UploadedAt.Equal(uploaded) {
		t.Errorf("UploadedAt = %s, want %s", loaded.UploadedAt, uploaded)
	}

	listed, err := store.GiftPacks(ctx)
	if err != nil {
		t.Fatalf("GiftPacks: %v", err)
	}
	var row *domain.GiftPack
	for i := range listed {
		if listed[i].PackID == packID {
			row = &listed[i]
		}
	}
	if row == nil {
		t.Fatal("the stored pack is missing from the listing")
	}
	if len(row.Archive) != 0 {
		// A listing that carried archives would pull megabytes per pack into
		// memory just to draw the shelf.
		t.Errorf("listing carried %d archive bytes, want none", len(row.Archive))
	}
	if len(row.Manifest) == 0 {
		t.Error("listing has no manifest, so the panel cannot render the pack")
	}

	// Re-upload: same slug, different contents.
	second := bytes.Repeat([]byte{0x42}, 128)
	if _, err := store.SaveGiftPack(ctx, domain.GiftPack{
		PackID: packID, Name: "Test Pack", GiftCount: 4, SizeBytes: len(second),
		Manifest: []byte(`{"pack_name":"Test Pack"}`), Archive: second,
		UploadedBy: "operator", UploadedAt: uploaded,
	}); err != nil {
		t.Fatalf("SaveGiftPack again: %v", err)
	}
	again, found, err := store.GiftPack(ctx, packID)
	if err != nil || !found {
		t.Fatalf("GiftPack after re-upload: %v (found=%v)", err, found)
	}
	if !bytes.Equal(again.Archive, second) || again.GiftCount != 4 {
		t.Fatalf("re-upload left %d bytes / %d gifts, want %d / 4", len(again.Archive), again.GiftCount, len(second))
	}

	removed, err := store.DeleteGiftPack(ctx, packID)
	if err != nil || !removed {
		t.Fatalf("DeleteGiftPack = %v, %v", removed, err)
	}
	if _, found, _ := store.GiftPack(ctx, packID); found {
		t.Error("the pack survived its own deletion")
	}
	if removed, _ := store.DeleteGiftPack(ctx, packID); removed {
		t.Error("DeleteGiftPack reported a second removal")
	}
}
