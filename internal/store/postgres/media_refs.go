package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"telesrv/internal/domain"
	"telesrv/internal/store/postgres/sqlcgen"
)

// messageBoxRefKey/channelMessageRefKey are the ref_key encodings used by
// media_references rows registered from the private-mailbox and channel
// message write paths, respectively. Kept as named helpers so the add and
// remove sides can never drift apart on format.
func messageBoxRefKey(ownerUserID int64, boxID int) string {
	return fmt.Sprintf("user:%d:box:%d", ownerUserID, boxID)
}

func channelMessageRefKey(channelID int64, messageID int) string {
	return fmt.Sprintf("channel:%d:msg:%d", channelID, messageID)
}

// addMediaReferencesTx registers every document/photo embedded in media as
// referenced by refKind/refKey, clearing orphaned_at on each if it had been
// set by an earlier removal. Must run in the same transaction as the write
// that creates the reference (message send/edit).
func addMediaReferencesTx(ctx context.Context, tx sqlcgen.DBTX, media *domain.MessageMedia, refKind domain.MediaRefKind, refKey string) error {
	targets := domain.ExtractMediaRefTargets(media)
	if len(targets) == 0 {
		return nil
	}
	q := sqlcgen.New(tx)
	for _, t := range targets {
		if err := q.InsertMediaReference(ctx, sqlcgen.InsertMediaReferenceParams{
			MediaKind: string(t.Kind),
			MediaID:   t.ID,
			RefKind:   string(refKind),
			RefKey:    refKey,
		}); err != nil {
			return fmt.Errorf("insert media reference: %w", err)
		}
		var clearErr error
		switch t.Kind {
		case domain.MediaKindDocument:
			clearErr = q.ClearDocumentOrphan(ctx, t.ID)
		case domain.MediaKindPhoto:
			clearErr = q.ClearPhotoOrphan(ctx, t.ID)
		}
		if clearErr != nil {
			return fmt.Errorf("clear media orphan: %w", clearErr)
		}
	}
	return nil
}

// removeMediaReferencesByKeyTx drops every media_references row registered
// under refKind/refKey (no need to know which document/photo ids those were
// -- the delete finds them) and, for each one that becomes fully
// unreferenced as a result, marks it orphaned so the storage retention
// sweep can consider it once old enough. Must run in the same transaction
// as the write that removes the reference (a message being soft-deleted).
func removeMediaReferencesByKeyTx(ctx context.Context, tx sqlcgen.DBTX, refKind domain.MediaRefKind, refKey string) error {
	rows, err := tx.Query(ctx, `
DELETE FROM media_references
WHERE ref_kind = $1 AND ref_key = $2
RETURNING media_kind, media_id`, string(refKind), refKey)
	if err != nil {
		return fmt.Errorf("remove media references: %w", err)
	}
	type removedRef struct {
		kind string
		id   int64
	}
	var removed []removedRef
	for rows.Next() {
		var r removedRef
		if err := rows.Scan(&r.kind, &r.id); err != nil {
			rows.Close()
			return fmt.Errorf("scan removed media reference: %w", err)
		}
		removed = append(removed, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("remove media references: %w", err)
	}
	rows.Close()

	q := sqlcgen.New(tx)
	for _, r := range removed {
		var orphanErr error
		switch domain.MediaKind(r.kind) {
		case domain.MediaKindDocument:
			orphanErr = q.OrphanDocumentIfUnreferenced(ctx, r.id)
		case domain.MediaKindPhoto:
			orphanErr = q.OrphanPhotoIfUnreferenced(ctx, r.id)
		}
		if orphanErr != nil {
			return fmt.Errorf("orphan check media: %w", orphanErr)
		}
	}
	return nil
}

// ListMediaReferences returns every live reference row for (kind, mediaID) --
// used by the storage retention sweep to turn a hard-retention/eviction blob
// purge into a visible notice on every message that still embeds the purged
// media. See internal/app/files.notifyRetentionPurge.
func (s *MediaStore) ListMediaReferences(ctx context.Context, kind domain.MediaKind, mediaID int64) ([]domain.MediaReference, error) {
	rows, err := s.q.ListMediaReferences(ctx, sqlcgen.ListMediaReferencesParams{
		MediaKind: string(kind),
		MediaID:   mediaID,
	})
	if err != nil {
		return nil, fmt.Errorf("list media references: %w", err)
	}
	out := make([]domain.MediaReference, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.MediaReference{
			Kind:    domain.MediaKind(r.MediaKind),
			MediaID: r.MediaID,
			RefKind: domain.MediaRefKind(r.RefKind),
			RefKey:  r.RefKey,
		})
	}
	return out, nil
}

// ---- storage retention sweep ----

// OrphanDocumentIfUnreferenced marks a document orphaned right now if
// nothing currently references it (media_references), for a caller that
// wants an immediate answer instead of waiting for the age-based
// ListOrphanedDocumentIDsOlderThan sweep -- e.g. a human deliberately
// pruning catalog entries, not an accidental delete the sweep's grace period
// exists to protect against. Returns whether it just became orphaned; false
// if something still references it (safe: the document is left alone) or it
// was already orphaned.
func (s *MediaStore) OrphanDocumentIfUnreferenced(ctx context.Context, id int64) (bool, error) {
	tag, err := s.db.Exec(ctx, `
UPDATE documents SET orphaned_at = now()
WHERE id = $1
  AND orphaned_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM media_references WHERE media_kind = 'document' AND media_id = $1)`,
		id)
	if err != nil {
		return false, fmt.Errorf("orphan document if unreferenced: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// OrphanPhotoIfUnreferenced is OrphanDocumentIfUnreferenced's photo
// counterpart -- see its doc comment.
func (s *MediaStore) OrphanPhotoIfUnreferenced(ctx context.Context, id int64) (bool, error) {
	tag, err := s.db.Exec(ctx, `
UPDATE photos SET orphaned_at = now()
WHERE id = $1
  AND orphaned_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM media_references WHERE media_kind = 'photo' AND media_id = $1)`,
		id)
	if err != nil {
		return false, fmt.Errorf("orphan photo if unreferenced: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListOrphanedDocumentIDsOlderThan returns document ids in the given category
// whose orphaned_at is set and older than cutoff, oldest first, up to limit.
func (s *MediaStore) ListOrphanedDocumentIDsOlderThan(ctx context.Context, category domain.MediaCategory, cutoff time.Time, limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, nil
	}
	return s.q.ListOrphanedDocumentIDsOlderThan(ctx, sqlcgen.ListOrphanedDocumentIDsOlderThanParams{
		Cutoff:     pgtype.Timestamptz{Time: cutoff, Valid: true},
		Category:   int16(category),
		BatchLimit: int32(limit),
	})
}

// ListOrphanedPhotoIDsOlderThan returns photo ids (excluding a live avatar --
// see ListAvatarOrphanedPhotoIDsOlderThan) whose orphaned_at is set and older
// than cutoff, oldest first, up to limit.
func (s *MediaStore) ListOrphanedPhotoIDsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, nil
	}
	return s.q.ListOrphanedPhotoIDsOlderThan(ctx, sqlcgen.ListOrphanedPhotoIDsOlderThanParams{
		Cutoff:     pgtype.Timestamptz{Time: cutoff, Valid: true},
		BatchLimit: int32(limit),
	})
}

// ListAvatarOrphanedPhotoIDsOlderThan is the "Avatar" category counterpart of
// ListOrphanedPhotoIDsOlderThan -- see the query's doc comment for why this
// is expected to stay empty in practice under "orphan" mode.
func (s *MediaStore) ListAvatarOrphanedPhotoIDsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, nil
	}
	return s.q.ListAvatarOrphanedPhotoIDsOlderThan(ctx, sqlcgen.ListAvatarOrphanedPhotoIDsOlderThanParams{
		Cutoff:     pgtype.Timestamptz{Time: cutoff, Valid: true},
		BatchLimit: int32(limit),
	})
}

// CountFileBlobRefs reports how many file_blobs rows still point at
// (backend, objectKey) -- the caller must not physically delete the object
// from that backend while this is > 0 (content-addressed storage: the same
// object can be shared by multiple documents/photos).
func (s *MediaStore) CountFileBlobRefs(ctx context.Context, backend, objectKey string) (int, error) {
	n, err := s.q.CountFileBlobRefs(ctx, sqlcgen.CountFileBlobRefsParams{Backend: backend, ObjectKey: objectKey})
	return int(n), err
}

// DeleteDocumentAndBlobs deletes a document row and every file_blobs row it
// owns (main body + thumbnail variants), returning what was deleted so the
// caller can physically remove each object from its backend once confirming
// (via CountFileBlobRefs, after this call) no other row still needs it.
// Assumes the document is already orphaned -- does not check references.
func (s *MediaStore) DeleteDocumentAndBlobs(ctx context.Context, id int64) ([]domain.FileBlob, error) {
	var blobs []domain.FileBlob
	err := withTx(ctx, s.db, "delete document and blobs", func(tx pgx.Tx) error {
		qtx := s.q.WithTx(tx)
		rows, err := qtx.ListFileBlobsByLocationPrefix(ctx, sqlcgen.ListFileBlobsByLocationPrefixParams{
			ExactKey:      fmt.Sprintf("doc:%d", id),
			PrefixPattern: fmt.Sprintf("doc:%d:%%", id),
		})
		if err != nil {
			return fmt.Errorf("list document blobs: %w", err)
		}
		for _, r := range rows {
			blobs = append(blobs, domain.FileBlob{
				LocationKey: r.LocationKey, Backend: domain.MediaBackend(r.Backend), ObjectKey: r.ObjectKey, Size: r.Size,
			})
			if err := qtx.DeleteFileBlobRow(ctx, r.LocationKey); err != nil {
				return fmt.Errorf("delete file blob row: %w", err)
			}
		}
		if err := qtx.DeleteDocumentRow(ctx, id); err != nil {
			return fmt.Errorf("delete document row: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.documents.remove(id)
	return blobs, nil
}

// nullableTimestamptz converts an optional cutoff into the nullable
// pgtype.Timestamptz the hard-retention List*/Count* queries expect: nil
// means "no age filter at all" (sqlc.narg(cutoff) IS NULL in the query),
// matching the manual-purge admin action's "no date = everything" contract.
// The automatic sweep (files.Service.DeleteBlobBytesForMediaOlderThan)
// always passes a non-nil cutoff.
func nullableTimestamptz(cutoff *time.Time) pgtype.Timestamptz {
	if cutoff == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *cutoff, Valid: true}
}

// ListDocumentIDsForHardRetentionOlderThan returns document ids in the given
// category older than cutoff (by upload/created_at) that still own at least
// one file_blobs row, oldest first, up to limit -- the "hard" retention
// sweep's candidate list. Unlike ListOrphanedDocumentIDsOlderThan, this
// ignores media_references entirely: a document still referenced by a live
// message is exactly as eligible as an orphaned one. cutoff may be nil,
// meaning no age filter at all (every document in the category is a
// candidate) -- used by the manual purge admin action.
func (s *MediaStore) ListDocumentIDsForHardRetentionOlderThan(ctx context.Context, category domain.MediaCategory, cutoff *time.Time, limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, nil
	}
	return s.q.ListDocumentIDsForHardRetentionOlderThan(ctx, sqlcgen.ListDocumentIDsForHardRetentionOlderThanParams{
		Cutoff:     nullableTimestamptz(cutoff),
		Category:   int16(category),
		BatchLimit: int32(limit),
	})
}

// CountDocumentsForHardRetention is the exact-count counterpart of
// ListDocumentIDsForHardRetentionOlderThan, for the manual purge admin
// action's dry-run preview.
func (s *MediaStore) CountDocumentsForHardRetention(ctx context.Context, category domain.MediaCategory, cutoff *time.Time) (int, error) {
	n, err := s.q.CountDocumentsForHardRetention(ctx, sqlcgen.CountDocumentsForHardRetentionParams{
		Cutoff:   nullableTimestamptz(cutoff),
		Category: int16(category),
	})
	return int(n), err
}

// ListPhotoIDsForHardRetentionOlderThan is the photo counterpart of
// ListDocumentIDsForHardRetentionOlderThan (excluding a live avatar -- see
// ListAvatarPhotoIDsForHardRetentionOlderThan). cutoff may be nil -- see
// ListDocumentIDsForHardRetentionOlderThan.
func (s *MediaStore) ListPhotoIDsForHardRetentionOlderThan(ctx context.Context, cutoff *time.Time, limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, nil
	}
	return s.q.ListPhotoIDsForHardRetentionOlderThan(ctx, sqlcgen.ListPhotoIDsForHardRetentionOlderThanParams{
		Cutoff:     nullableTimestamptz(cutoff),
		BatchLimit: int32(limit),
	})
}

// CountPhotosForHardRetention is the exact-count counterpart of
// ListPhotoIDsForHardRetentionOlderThan.
func (s *MediaStore) CountPhotosForHardRetention(ctx context.Context, cutoff *time.Time) (int, error) {
	n, err := s.q.CountPhotosForHardRetention(ctx, nullableTimestamptz(cutoff))
	return int(n), err
}

// ListAvatarPhotoIDsForHardRetentionOlderThan is the "Avatar" category
// counterpart of ListPhotoIDsForHardRetentionOlderThan. cutoff may be nil --
// see ListDocumentIDsForHardRetentionOlderThan.
func (s *MediaStore) ListAvatarPhotoIDsForHardRetentionOlderThan(ctx context.Context, cutoff *time.Time, limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, nil
	}
	return s.q.ListAvatarPhotoIDsForHardRetentionOlderThan(ctx, sqlcgen.ListAvatarPhotoIDsForHardRetentionOlderThanParams{
		Cutoff:     nullableTimestamptz(cutoff),
		BatchLimit: int32(limit),
	})
}

// CountAvatarPhotosForHardRetention is the exact-count counterpart of
// ListAvatarPhotoIDsForHardRetentionOlderThan.
func (s *MediaStore) CountAvatarPhotosForHardRetention(ctx context.Context, cutoff *time.Time) (int, error) {
	n, err := s.q.CountAvatarPhotosForHardRetention(ctx, nullableTimestamptz(cutoff))
	return int(n), err
}

// DeleteFileBlobsForDocument deletes every file_blobs row a document owns
// (main body + thumbnail variants), returning what was deleted so the caller
// can physically remove each object from its backend once confirming (via
// CountFileBlobRefs, after this call) no other row still needs it. Unlike
// DeleteDocumentAndBlobs, this deliberately does NOT delete the documents
// row itself -- "hard" retention mode keeps the metadata (dimensions, mime
// type, filename) so a message can still render "this document is no longer
// available" instead of disappearing outright. A subsequent
// upload.getFile/GetFileBlob lookup for a location key this call removed
// correctly finds nothing and reports not-found.
func (s *MediaStore) DeleteFileBlobsForDocument(ctx context.Context, id int64) ([]domain.FileBlob, error) {
	var blobs []domain.FileBlob
	err := withTx(ctx, s.db, "delete document blob bytes (hard retention)", func(tx pgx.Tx) error {
		qtx := s.q.WithTx(tx)
		rows, err := qtx.ListFileBlobsByLocationPrefix(ctx, sqlcgen.ListFileBlobsByLocationPrefixParams{
			ExactKey:      fmt.Sprintf("doc:%d", id),
			PrefixPattern: fmt.Sprintf("doc:%d:%%", id),
		})
		if err != nil {
			return fmt.Errorf("list document blobs: %w", err)
		}
		for _, r := range rows {
			blobs = append(blobs, domain.FileBlob{
				LocationKey: r.LocationKey, Backend: domain.MediaBackend(r.Backend), ObjectKey: r.ObjectKey, Size: r.Size,
			})
			if err := qtx.DeleteFileBlobRow(ctx, r.LocationKey); err != nil {
				return fmt.Errorf("delete file blob row: %w", err)
			}
		}
		// gif_catalog and user_sticker_collections entries are pure picker
		// metadata -- unlike a chat message (which keeps rendering "media
		// unavailable" off the documents row after its bytes are gone), a
		// picker entry has no placeholder concept: once the underlying gif's
		// bytes are purged it's just a dead thumbnail nobody can open, so it
		// gets removed here too, in the same transaction as the actual blob
		// purge, instead of drifting out of sync until some separate sweep
		// happens to notice. No-op (0 rows) for the overwhelming majority of
		// documents, which are never in either table.
		if _, err := tx.Exec(ctx, `DELETE FROM gif_catalog WHERE document_id = $1`, id); err != nil {
			return fmt.Errorf("delete gif catalog entry for purged document: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_sticker_collections WHERE document_id = $1`, id); err != nil {
			return fmt.Errorf("delete sticker collection entries for purged document: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return blobs, nil
}

// DeleteFileBlobsForPhoto is the photo counterpart of
// DeleteFileBlobsForDocument -- see its doc comment. Deliberately does not
// delete the photos row.
func (s *MediaStore) DeleteFileBlobsForPhoto(ctx context.Context, id int64) ([]domain.FileBlob, error) {
	var blobs []domain.FileBlob
	err := withTx(ctx, s.db, "delete photo blob bytes (hard retention)", func(tx pgx.Tx) error {
		qtx := s.q.WithTx(tx)
		rows, err := qtx.ListFileBlobsByLocationPrefix(ctx, sqlcgen.ListFileBlobsByLocationPrefixParams{
			ExactKey:      fmt.Sprintf("photo:%d", id),
			PrefixPattern: fmt.Sprintf("photo:%d:%%", id),
		})
		if err != nil {
			return fmt.Errorf("list photo blobs: %w", err)
		}
		for _, r := range rows {
			blobs = append(blobs, domain.FileBlob{
				LocationKey: r.LocationKey, Backend: domain.MediaBackend(r.Backend), ObjectKey: r.ObjectKey, Size: r.Size,
			})
			if err := qtx.DeleteFileBlobRow(ctx, r.LocationKey); err != nil {
				return fmt.Errorf("delete file blob row: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return blobs, nil
}

// ListOldestMediaForEviction returns up to limit documents and limit photos
// still owning file_blobs bytes, oldest-uploaded first, for the active
// eviction sweep to interleave by actual created_at (not drain one table
// before the other) -- see domain.EvictionCandidate.
func (s *MediaStore) ListOldestMediaForEviction(ctx context.Context, limit int) ([]domain.EvictionCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	docs, err := s.q.ListOldestDocumentsForEviction(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list oldest documents for eviction: %w", err)
	}
	photos, err := s.q.ListOldestPhotosForEviction(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list oldest photos for eviction: %w", err)
	}
	out := make([]domain.EvictionCandidate, 0, len(docs)+len(photos))
	for _, d := range docs {
		out = append(out, domain.EvictionCandidate{Kind: domain.MediaKindDocument, MediaID: d.ID, CreatedAt: d.CreatedAt.Time})
	}
	for _, p := range photos {
		out = append(out, domain.EvictionCandidate{Kind: domain.MediaKindPhoto, MediaID: p.ID, CreatedAt: p.CreatedAt.Time})
	}
	return out, nil
}

// DeletePhotoAndBlobs deletes a photo row and every file_blobs row it owns
// (one per rendition size), returning what was deleted so the caller can
// physically remove each object from its backend once confirming (via
// CountFileBlobRefs, after this call) no other row still needs it. Assumes
// the photo is already orphaned -- does not check references.
func (s *MediaStore) DeletePhotoAndBlobs(ctx context.Context, id int64) ([]domain.FileBlob, error) {
	var blobs []domain.FileBlob
	err := withTx(ctx, s.db, "delete photo and blobs", func(tx pgx.Tx) error {
		qtx := s.q.WithTx(tx)
		rows, err := qtx.ListFileBlobsByLocationPrefix(ctx, sqlcgen.ListFileBlobsByLocationPrefixParams{
			ExactKey:      fmt.Sprintf("photo:%d", id),
			PrefixPattern: fmt.Sprintf("photo:%d:%%", id),
		})
		if err != nil {
			return fmt.Errorf("list photo blobs: %w", err)
		}
		for _, r := range rows {
			blobs = append(blobs, domain.FileBlob{
				LocationKey: r.LocationKey, Backend: domain.MediaBackend(r.Backend), ObjectKey: r.ObjectKey, Size: r.Size,
			})
			if err := qtx.DeleteFileBlobRow(ctx, r.LocationKey); err != nil {
				return fmt.Errorf("delete file blob row: %w", err)
			}
		}
		if err := qtx.DeletePhotoRow(ctx, id); err != nil {
			return fmt.Errorf("delete photo row: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return blobs, nil
}
