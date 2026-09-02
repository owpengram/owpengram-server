package files

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// mediaRetentionStore is implemented by store.MediaStore backends that
// support the storage retention sweep (currently only the Postgres store).
// A type assertion, not a MediaStore interface method, keeps these
// admin/maintenance-only queries out of the hot RPC-facing interface --
// same convention as photoBatchStore above.
type mediaRetentionStore interface {
	ListOrphanedDocumentIDsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]int64, error)
	ListOrphanedPhotoIDsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]int64, error)
	CountFileBlobRefs(ctx context.Context, backend, objectKey string) (int, error)
	DeleteDocumentAndBlobs(ctx context.Context, id int64) ([]domain.FileBlob, error)
	DeletePhotoAndBlobs(ctx context.Context, id int64) ([]domain.FileBlob, error)
	// OrphanDocumentIfUnreferenced is the immediate (no grace period)
	// counterpart to the age-based sweep above -- see its doc comment.
	OrphanDocumentIfUnreferenced(ctx context.Context, id int64) (bool, error)

	// -- "hard" retention mode (TELESRV_STORAGE_RETENTION_MODE=hard) --
	// Candidate selection ignores media_references entirely: a document/
	// photo still referenced by a live message is exactly as eligible as an
	// orphaned one once it's old enough. The delete methods physically
	// remove only the file_blobs row(s)/bytes, never the document/photo
	// metadata row -- see DeleteFileBlobsForDocument's doc comment.
	ListDocumentIDsForHardRetentionOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]int64, error)
	ListPhotoIDsForHardRetentionOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]int64, error)
	DeleteFileBlobsForDocument(ctx context.Context, id int64) ([]domain.FileBlob, error)
	DeleteFileBlobsForPhoto(ctx context.Context, id int64) ([]domain.FileBlob, error)
}

// DeleteOrphanedOlderThan implements maintenance.OrphanedMediaRetentionStore:
// permanently deletes documents/photos that have had no live reference
// (message/profile-photo/sticker-set, see media_references) since at least
// cutoff, along with their blob(s) -- but only physically removes bytes
// from the backend once confirming no other file_blobs row still needs the
// object, since content-addressed storage means the same bytes can be
// shared across documents/photos.
func (s *Service) DeleteOrphanedOlderThan(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	store, ok := s.media.(mediaRetentionStore)
	if !ok || limit <= 0 {
		return 0, nil
	}
	deleted := 0
	docIDs, err := store.ListOrphanedDocumentIDsOlderThan(ctx, cutoff, limit)
	if err != nil {
		return deleted, fmt.Errorf("list orphaned documents: %w", err)
	}
	for _, id := range docIDs {
		blobs, err := store.DeleteDocumentAndBlobs(ctx, id)
		if err != nil {
			s.log.Warn("delete orphaned document failed", zap.Int64("document_id", id), zap.Error(err))
			continue
		}
		s.deleteOrphanedBlobs(ctx, store, blobs)
		deleted++
	}
	photoIDs, err := store.ListOrphanedPhotoIDsOlderThan(ctx, cutoff, limit)
	if err != nil {
		return deleted, fmt.Errorf("list orphaned photos: %w", err)
	}
	for _, id := range photoIDs {
		blobs, err := store.DeletePhotoAndBlobs(ctx, id)
		if err != nil {
			s.log.Warn("delete orphaned photo failed", zap.Int64("photo_id", id), zap.Error(err))
			continue
		}
		s.deleteOrphanedBlobs(ctx, store, blobs)
		deleted++
	}
	return deleted, nil
}

// DeleteBlobBytesForMediaOlderThan implements
// maintenance.HardMediaRetentionStore ("hard" retention mode): for
// documents/photos whose upload/created_at is older than cutoff, physically
// deletes their blob bytes (main body + thumbnail/rendition variants) from
// the backend and removes their file_blobs rows -- REGARDLESS of whether a
// live message/profile-photo/sticker-set still references them. It
// deliberately never touches the documents/photos metadata row itself: a
// message must still be able to render "here was a photo/document"
// (dimensions, mime type, filename) after its bytes are gone, rather than
// the message breaking outright. A subsequent upload.getFile for the same
// location key finds no file_blobs row and returns LOCATION_INVALID, which
// stock clients already render as a "media unavailable" placeholder.
func (s *Service) DeleteBlobBytesForMediaOlderThan(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	store, ok := s.media.(mediaRetentionStore)
	if !ok || limit <= 0 {
		return 0, nil
	}
	purged := 0
	docIDs, err := store.ListDocumentIDsForHardRetentionOlderThan(ctx, cutoff, limit)
	if err != nil {
		return purged, fmt.Errorf("list documents for hard retention: %w", err)
	}
	for _, id := range docIDs {
		blobs, err := store.DeleteFileBlobsForDocument(ctx, id)
		if err != nil {
			s.log.Warn("hard-delete document blob bytes failed", zap.Int64("document_id", id), zap.Error(err))
			continue
		}
		if len(blobs) == 0 {
			continue
		}
		s.deleteOrphanedBlobs(ctx, store, blobs)
		purged++
	}
	remaining := limit - len(docIDs)
	if remaining <= 0 {
		return purged, nil
	}
	photoIDs, err := store.ListPhotoIDsForHardRetentionOlderThan(ctx, cutoff, remaining)
	if err != nil {
		return purged, fmt.Errorf("list photos for hard retention: %w", err)
	}
	for _, id := range photoIDs {
		blobs, err := store.DeleteFileBlobsForPhoto(ctx, id)
		if err != nil {
			s.log.Warn("hard-delete photo blob bytes failed", zap.Int64("photo_id", id), zap.Error(err))
			continue
		}
		if len(blobs) == 0 {
			continue
		}
		s.deleteOrphanedBlobs(ctx, store, blobs)
		purged++
	}
	return purged, nil
}

// deleteDocumentNowIfUnreferenced is the immediate counterpart to the
// age-based sweep DeleteOrphanedOlderThan runs in the background: orphans
// id right now (skipping the grace period) and, only if that succeeds --
// i.e. nothing else currently references it -- physically deletes it and
// its blobs immediately. Returns whether it was actually deleted; false
// (with no error) means something still references the document, so it and
// its blob(s) were deliberately left alone.
func (s *Service) deleteDocumentNowIfUnreferenced(ctx context.Context, id int64) (bool, error) {
	store, ok := s.media.(mediaRetentionStore)
	if !ok {
		return false, nil
	}
	orphaned, err := store.OrphanDocumentIfUnreferenced(ctx, id)
	if err != nil {
		return false, fmt.Errorf("orphan document: %w", err)
	}
	if !orphaned {
		return false, nil
	}
	blobs, err := store.DeleteDocumentAndBlobs(ctx, id)
	if err != nil {
		return false, fmt.Errorf("delete document: %w", err)
	}
	s.deleteOrphanedBlobs(ctx, store, blobs)
	return true, nil
}

// deleteOrphanedBlobs removes each blob from its backend once confirming
// (via CountFileBlobRefs) no other file_blobs row still references
// (backend, object_key). Resolves the correct backend per blob via
// backendFor (not just the currently active one) -- a blob written before a
// TELESRV_BLOB_BACKEND switch still needs deleting from wherever it
// actually lives. If that backend is no longer configured (its credentials
// were removed after switching away from it), the blob is logged and
// skipped rather than silently dropped, since there's nothing reachable to
// delete it from.
func (s *Service) deleteOrphanedBlobs(ctx context.Context, store mediaRetentionStore, blobs []domain.FileBlob) {
	for _, b := range blobs {
		// The file_blobs row for this exact location_key is already gone
		// (the caller deleted it in the same transaction that produced this
		// blob list) -- so any cached "found" metadata for it is now wrong
		// regardless of whether the underlying bytes turn out to still be
		// shared by another row below. Without this, a hot GetFile path that
		// had this location_key's metadata cached would keep trying to read
		// bytes that may no longer exist (hard retention mode purges blobs
		// for actively-referenced, potentially still-hot media), producing
		// an internal error instead of the graceful LOCATION_INVALID a stock
		// client knows how to render.
		s.blobCache.delete(b.LocationKey)
		refs, err := store.CountFileBlobRefs(ctx, string(b.Backend), b.ObjectKey)
		if err != nil {
			s.log.Warn("count file blob refs failed", zap.String("object_key", b.ObjectKey), zap.Error(err))
			continue
		}
		if refs > 0 {
			continue
		}
		backend, err := s.backendFor(b.Backend)
		if err != nil {
			s.log.Warn("orphaned blob's backend is not configured, skipping physical delete",
				zap.String("backend", string(b.Backend)), zap.String("object_key", b.ObjectKey), zap.Error(err))
			continue
		}
		if err := backend.Delete(ctx, b.ObjectKey); err != nil {
			s.log.Warn("delete orphaned blob failed", zap.String("object_key", b.ObjectKey), zap.Error(err))
		}
		s.byteCache.delete(b.ObjectKey)
	}
}
