package files

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// hardRetentionDocumentCategories enumerates every documents.category bucket
// the per-category retention sweep loops over. MediaCategoryNone covers
// unclassified documents (stickers and anything else classifyDocumentCategory
// doesn't tag) and always uses the shared global age -- there is no
// per-category override for that bucket. Photo/Avatar are not in this list:
// photos have no category column and are instead split by a profile_photos
// join (see categoryRetentionAge/avatarRetentionAge below and the dedicated
// photo/avatar query variants).
var hardRetentionDocumentCategories = []domain.MediaCategory{
	domain.MediaCategoryNone,
	domain.MediaCategoryVideo,
	domain.MediaCategoryGif,
	domain.MediaCategoryFile,
	domain.MediaCategoryMusic,
	domain.MediaCategoryVoice,
	domain.MediaCategoryRoundVideo,
}

// categoryRetentionAge returns the effective retention age for a document
// media category: its configured override if positive, otherwise the shared
// global age.
func (s *Service) categoryRetentionAge(category domain.MediaCategory) time.Duration {
	if age, ok := s.storageRetentionCategoryAges[category]; ok && age > 0 {
		return age
	}
	return s.storageRetentionGlobalMaxAge
}

// avatarRetentionAge is the Photo category's counterpart for photos currently
// active as someone's avatar -- see categoryRetentionAge.
func (s *Service) avatarRetentionAge() time.Duration {
	if s.storageRetentionAvatarMaxAge > 0 {
		return s.storageRetentionAvatarMaxAge
	}
	return s.storageRetentionGlobalMaxAge
}

// mediaRetentionStore is implemented by store.MediaStore backends that
// support the storage retention sweep (currently only the Postgres store).
// A type assertion, not a MediaStore interface method, keeps these
// admin/maintenance-only queries out of the hot RPC-facing interface --
// same convention as photoBatchStore above.
type mediaRetentionStore interface {
	ListOrphanedDocumentIDsOlderThan(ctx context.Context, category domain.MediaCategory, cutoff time.Time, limit int) ([]int64, error)
	ListOrphanedPhotoIDsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]int64, error)
	ListAvatarOrphanedPhotoIDsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]int64, error)
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
	ListDocumentIDsForHardRetentionOlderThan(ctx context.Context, category domain.MediaCategory, cutoff time.Time, limit int) ([]int64, error)
	ListPhotoIDsForHardRetentionOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]int64, error)
	ListAvatarPhotoIDsForHardRetentionOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]int64, error)
	DeleteFileBlobsForDocument(ctx context.Context, id int64) ([]domain.FileBlob, error)
	DeleteFileBlobsForPhoto(ctx context.Context, id int64) ([]domain.FileBlob, error)

	// -- active eviction (TELESRV_STORAGE_EVICTION_ENABLE) --
	SumFileBlobBytes(ctx context.Context) (int64, error)
	ListOldestMediaForEviction(ctx context.Context, limit int) ([]domain.EvictionCandidate, error)

	// -- retention purge notice (see retention_purge.go) --
	ListMediaReferences(ctx context.Context, kind domain.MediaKind, mediaID int64) ([]domain.MediaReference, error)
}

// DeleteOrphanedOlderThan implements maintenance.OrphanedMediaRetentionStore:
// permanently deletes documents/photos that have had no live reference
// (message/profile-photo/sticker-set, see media_references) since at least
// the per-category cutoff derived from now, along with their blob(s) -- but
// only physically removes bytes from the backend once confirming no other
// file_blobs row still needs the object, since content-addressed storage
// means the same bytes can be shared across documents/photos. Loops one
// query per document category (each with its own effective age, see
// categoryRetentionAge) plus a regular/avatar split for photos; limit applies
// per category per tick, not as one shared budget across all of them --
// simplest correct behavior, revisit only if one category starves another in
// practice.
func (s *Service) DeleteOrphanedOlderThan(ctx context.Context, now time.Time, limit int) (int, error) {
	store, ok := s.media.(mediaRetentionStore)
	if !ok || limit <= 0 {
		return 0, nil
	}
	deleted := 0
	for _, cat := range hardRetentionDocumentCategories {
		age := s.categoryRetentionAge(cat)
		if age <= 0 {
			// Global age is 0 (retention only enabled for other, explicitly
			// overridden categories) and this category has no override of its
			// own -- skip it entirely rather than treating age<=0 as an
			// immediate "everything is older than now" cutoff.
			continue
		}
		cutoff := now.Add(-age)
		docIDs, err := store.ListOrphanedDocumentIDsOlderThan(ctx, cat, cutoff, limit)
		if err != nil {
			return deleted, fmt.Errorf("list orphaned documents (category %d): %w", cat, err)
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
	}
	if age := s.categoryRetentionAge(domain.MediaCategoryPhoto); age > 0 {
		photoCutoff := now.Add(-age)
		photoIDs, err := store.ListOrphanedPhotoIDsOlderThan(ctx, photoCutoff, limit)
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
	}
	if age := s.avatarRetentionAge(); age > 0 {
		avatarCutoff := now.Add(-age)
		avatarIDs, err := store.ListAvatarOrphanedPhotoIDsOlderThan(ctx, avatarCutoff, limit)
		if err != nil {
			return deleted, fmt.Errorf("list orphaned avatar photos: %w", err)
		}
		for _, id := range avatarIDs {
			blobs, err := store.DeletePhotoAndBlobs(ctx, id)
			if err != nil {
				s.log.Warn("delete orphaned avatar photo failed", zap.Int64("photo_id", id), zap.Error(err))
				continue
			}
			s.deleteOrphanedBlobs(ctx, store, blobs)
			deleted++
		}
	}
	return deleted, nil
}

// DeleteBlobBytesForMediaOlderThan implements
// maintenance.HardMediaRetentionStore ("hard" retention mode): for
// documents/photos whose upload/created_at is older than the per-category
// cutoff derived from now (see categoryRetentionAge/avatarRetentionAge),
// physically deletes their blob bytes (main body + thumbnail/rendition
// variants) from the backend and removes their file_blobs rows --
// REGARDLESS of whether a live message/profile-photo/sticker-set still
// references them. It deliberately never touches the documents/photos
// metadata row itself: a message must still be able to render "here was a
// photo/document" (dimensions, mime type, filename) after its bytes are
// gone, rather than the message breaking outright. A subsequent
// upload.getFile for the same location key finds no file_blobs row and
// returns LOCATION_INVALID, which stock clients already render as a "media
// unavailable" placeholder -- and every message that still embeds the purged
// media additionally gets turned into a visible retention-purge notice (see
// notifyRetentionPurge in retention_purge.go).
func (s *Service) DeleteBlobBytesForMediaOlderThan(ctx context.Context, now time.Time, limit int) (int, error) {
	store, ok := s.media.(mediaRetentionStore)
	if !ok || limit <= 0 {
		return 0, nil
	}
	purged := 0
	for _, cat := range hardRetentionDocumentCategories {
		age := s.categoryRetentionAge(cat)
		if age <= 0 {
			// See DeleteOrphanedOlderThan's identical guard: age<=0 means this
			// category has no override and the global age is 0 (retention
			// enabled only for other categories) -- skip, don't treat it as
			// "everything is older than now".
			continue
		}
		cutoff := now.Add(-age)
		docIDs, err := store.ListDocumentIDsForHardRetentionOlderThan(ctx, cat, cutoff, limit)
		if err != nil {
			return purged, fmt.Errorf("list documents for hard retention (category %d): %w", cat, err)
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
			s.notifyRetentionPurge(ctx, domain.MediaKindDocument, id)
			purged++
		}
	}
	if age := s.categoryRetentionAge(domain.MediaCategoryPhoto); age > 0 {
		photoCutoff := now.Add(-age)
		photoIDs, err := store.ListPhotoIDsForHardRetentionOlderThan(ctx, photoCutoff, limit)
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
			s.notifyRetentionPurge(ctx, domain.MediaKindPhoto, id)
			purged++
		}
	}
	if age := s.avatarRetentionAge(); age > 0 {
		avatarCutoff := now.Add(-age)
		avatarIDs, err := store.ListAvatarPhotoIDsForHardRetentionOlderThan(ctx, avatarCutoff, limit)
		if err != nil {
			return purged, fmt.Errorf("list avatar photos for hard retention: %w", err)
		}
		for _, id := range avatarIDs {
			blobs, err := store.DeleteFileBlobsForPhoto(ctx, id)
			if err != nil {
				s.log.Warn("hard-delete avatar photo blob bytes failed", zap.Int64("photo_id", id), zap.Error(err))
				continue
			}
			if len(blobs) == 0 {
				continue
			}
			s.deleteOrphanedBlobs(ctx, store, blobs)
			s.notifyRetentionPurge(ctx, domain.MediaKindPhoto, id)
			purged++
		}
	}
	return purged, nil
}

// EvictOldestMediaOverBudget implements maintenance.StorageEvictionStore
// (TELESRV_STORAGE_EVICTION_ENABLE): once total physical blob bytes
// (SumFileBlobBytes) exceed TELESRV_STORAGE_MAX_TOTAL_BYTES, purges the
// oldest documents/photos overall -- interleaved by created_at across both
// tables, regardless of category or age -- reusing the exact same blob-purge
// (DeleteFileBlobsForDocument/DeleteFileBlobsForPhoto) and retention-purge
// notice primitive as "hard" mode. Stops once the running total (tracked
// locally from each purge's returned blob sizes, avoiding a re-query per
// item) is back under budget, or limit purges have happened this tick,
// whichever comes first -- bounding how much one tick can reclaim at once.
func (s *Service) EvictOldestMediaOverBudget(ctx context.Context, limit int) (int, error) {
	store, ok := s.media.(mediaRetentionStore)
	if !ok || limit <= 0 || s.storageMaxTotalBytes <= 0 {
		return 0, nil
	}
	total, err := store.SumFileBlobBytes(ctx)
	if err != nil {
		return 0, fmt.Errorf("sum file blob bytes: %w", err)
	}
	if total <= s.storageMaxTotalBytes {
		return 0, nil
	}
	candidates, err := store.ListOldestMediaForEviction(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("list oldest media for eviction: %w", err)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CreatedAt.Before(candidates[j].CreatedAt) })
	evicted := 0
	for _, c := range candidates {
		if evicted >= limit || total <= s.storageMaxTotalBytes {
			break
		}
		var blobs []domain.FileBlob
		var delErr error
		switch c.Kind {
		case domain.MediaKindDocument:
			blobs, delErr = store.DeleteFileBlobsForDocument(ctx, c.MediaID)
		case domain.MediaKindPhoto:
			blobs, delErr = store.DeleteFileBlobsForPhoto(ctx, c.MediaID)
		default:
			continue
		}
		if delErr != nil {
			s.log.Warn("active storage eviction blob purge failed",
				zap.String("media_kind", string(c.Kind)), zap.Int64("media_id", c.MediaID), zap.Error(delErr))
			continue
		}
		if len(blobs) == 0 {
			continue
		}
		s.deleteOrphanedBlobs(ctx, store, blobs)
		for _, b := range blobs {
			total -= b.Size
		}
		s.notifyRetentionPurge(ctx, c.Kind, c.MediaID)
		evicted++
	}
	return evicted, nil
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
