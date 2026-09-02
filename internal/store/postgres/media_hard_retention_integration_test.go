package postgres

import (
	"context"
	"strconv"
	"testing"
	"time"

	"telesrv/internal/domain"
)

// TestHardRetentionPurgesBlobBytesButKeepsMetadataRow exercises the "hard"
// storage retention mode's store methods end-to-end against a real
// Postgres: a document old enough (by created_at, not orphaned_at) is a
// candidate for ListDocumentIDsForHardRetentionOlderThan REGARDLESS of
// still having a live media_references row, DeleteFileBlobsForDocument
// removes only its file_blobs row (never the documents row itself), and a
// second sweep pass no longer finds it a candidate since it no longer owns
// any file_blobs row. This is the correctness property the whole "hard"
// mode design hinges on: a message must still be able to render its media
// placeholder after the bytes are gone.
func TestHardRetentionPurgesBlobBytesButKeepsMetadataRow(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	media := NewMediaStore(pool)

	docID := time.Now().UnixNano()
	locationKey := "doc:" + strconv.FormatInt(docID, 10)

	if err := media.PutDocument(ctx, domain.Document{
		ID:       docID,
		MimeType: "application/octet-stream",
		Size:     1024,
	}); err != nil {
		t.Fatalf("PutDocument: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM documents WHERE id = $1", docID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM media_references WHERE media_kind = 'document' AND media_id = $1", docID)
	})

	// Backdate created_at well past any plausible test cutoff -- PutDocument
	// always stamps now() and has no created_at parameter.
	if _, err := pool.Exec(ctx, "UPDATE documents SET created_at = now() - interval '100 days' WHERE id = $1", docID); err != nil {
		t.Fatalf("backdate document: %v", err)
	}

	blob := postgresTestBlob(locationKey, "hard-retention-doc", 1024, "application/octet-stream")
	if err := media.PutFileBlob(ctx, blob); err != nil {
		t.Fatalf("PutFileBlob: %v", err)
	}

	// A LIVE reference (as if a message still embeds this document) must not
	// exempt it from "hard" mode -- that's the entire point of the mode,
	// unlike the orphan-only sweep.
	if err := addMediaReferencesTx(ctx, pool, &domain.MessageMedia{
		Kind:     domain.MessageMediaKindDocument,
		Document: &domain.Document{ID: docID},
	}, domain.MediaRefKindMessageBox, "hard-retention-test:1"); err != nil {
		t.Fatalf("add media reference: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM media_references WHERE ref_key = 'hard-retention-test:1'")
	})

	cutoff := time.Now().Add(-24 * time.Hour)
	ids, err := media.ListDocumentIDsForHardRetentionOlderThan(ctx, cutoff, 1000)
	if err != nil {
		t.Fatalf("ListDocumentIDsForHardRetentionOlderThan: %v", err)
	}
	if !containsInt64(ids, docID) {
		t.Fatalf("hard retention candidates = %v, want to include still-referenced but old document %d", ids, docID)
	}

	blobs, err := media.DeleteFileBlobsForDocument(ctx, docID)
	if err != nil {
		t.Fatalf("DeleteFileBlobsForDocument: %v", err)
	}
	if len(blobs) != 1 || blobs[0].LocationKey != locationKey {
		t.Fatalf("deleted blobs = %+v, want exactly one for %q", blobs, locationKey)
	}

	// The documents row itself must survive -- a message referencing it
	// still needs to render its placeholder (mime type, size, filename).
	if _, found, err := media.GetDocument(ctx, docID); err != nil || !found {
		t.Fatalf("document metadata row missing after hard blob purge: found=%v err=%v", found, err)
	}

	// The file_blobs row is gone: a subsequent download attempt resolves via
	// GetFileBlob (files.Service.GetFile's path) to not-found, which the rpc
	// layer already maps to LOCATION_INVALID.
	if _, found, err := media.GetFileBlob(ctx, locationKey); err != nil || found {
		t.Fatalf("file_blobs row still present after hard purge: found=%v err=%v", found, err)
	}

	// Idempotent / self-terminating: a second sweep pass no longer selects
	// this document, since it no longer owns any file_blobs row.
	ids, err = media.ListDocumentIDsForHardRetentionOlderThan(ctx, cutoff, 1000)
	if err != nil {
		t.Fatalf("ListDocumentIDsForHardRetentionOlderThan (2nd pass): %v", err)
	}
	if containsInt64(ids, docID) {
		t.Fatalf("hard retention re-selected already-purged document %d", docID)
	}
}
