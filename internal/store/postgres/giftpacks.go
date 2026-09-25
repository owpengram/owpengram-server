package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
	"telesrv/internal/store/postgres/sqlcgen"
)

// GiftPackStore is the operator's uploaded gift-pack shelf.
type GiftPackStore struct {
	db sqlcgen.DBTX
}

func NewGiftPackStore(db sqlcgen.DBTX) *GiftPackStore {
	return &GiftPackStore{db: db}
}

// SaveGiftPack stores an uploaded pack, replacing any pack already stored
// under the same slug -- re-uploading a corrected build of a pack is the
// normal case, and a second copy of it would just be confusing.
func (s *GiftPackStore) SaveGiftPack(ctx context.Context, pack domain.GiftPack) (domain.GiftPack, error) {
	row := s.db.QueryRow(ctx, `
                INSERT INTO gift_packs (pack_id, name, author, description, file_name, gift_count, size_bytes, manifest, archive, uploaded_by, uploaded_at)
                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
                ON CONFLICT (pack_id) DO UPDATE SET
                        name = EXCLUDED.name,
                        author = EXCLUDED.author,
                        description = EXCLUDED.description,
                        file_name = EXCLUDED.file_name,
                        gift_count = EXCLUDED.gift_count,
                        size_bytes = EXCLUDED.size_bytes,
                        manifest = EXCLUDED.manifest,
                        archive = EXCLUDED.archive,
                        uploaded_by = EXCLUDED.uploaded_by,
                        uploaded_at = EXCLUDED.uploaded_at
                RETURNING pack_id, name, author, description, file_name, gift_count, size_bytes, uploaded_by, uploaded_at`,
		pack.PackID, pack.Name, pack.Author, pack.Description, pack.FileName,
		pack.GiftCount, pack.SizeBytes, pack.Manifest, pack.Archive, pack.UploadedBy, pack.UploadedAt)
	var out domain.GiftPack
	if err := row.Scan(&out.PackID, &out.Name, &out.Author, &out.Description, &out.FileName,
		&out.GiftCount, &out.SizeBytes, &out.UploadedBy, &out.UploadedAt); err != nil {
		return domain.GiftPack{}, fmt.Errorf("save gift pack: %w", err)
	}
	out.Manifest = pack.Manifest
	return out, nil
}

// GiftPacks lists the shelf newest first, without the archives.
func (s *GiftPackStore) GiftPacks(ctx context.Context) ([]domain.GiftPack, error) {
	rows, err := s.db.Query(ctx, `
                SELECT pack_id, name, author, description, file_name, gift_count, size_bytes, manifest, uploaded_by, uploaded_at
                FROM gift_packs ORDER BY uploaded_at DESC, pack_id`)
	if err != nil {
		return nil, fmt.Errorf("list gift packs: %w", err)
	}
	defer rows.Close()
	var out []domain.GiftPack
	for rows.Next() {
		var pack domain.GiftPack
		if err := rows.Scan(&pack.PackID, &pack.Name, &pack.Author, &pack.Description, &pack.FileName,
			&pack.GiftCount, &pack.SizeBytes, &pack.Manifest, &pack.UploadedBy, &pack.UploadedAt); err != nil {
			return nil, fmt.Errorf("scan gift pack: %w", err)
		}
		out = append(out, pack)
	}
	return out, rows.Err()
}

// GiftPack loads one pack including its archive.
func (s *GiftPackStore) GiftPack(ctx context.Context, packID string) (domain.GiftPack, bool, error) {
	row := s.db.QueryRow(ctx, `
                SELECT pack_id, name, author, description, file_name, gift_count, size_bytes, manifest, archive, uploaded_by, uploaded_at
                FROM gift_packs WHERE pack_id = $1`, packID)
	var pack domain.GiftPack
	err := row.Scan(&pack.PackID, &pack.Name, &pack.Author, &pack.Description, &pack.FileName,
		&pack.GiftCount, &pack.SizeBytes, &pack.Manifest, &pack.Archive, &pack.UploadedBy, &pack.UploadedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.GiftPack{}, false, nil
	}
	if err != nil {
		return domain.GiftPack{}, false, fmt.Errorf("load gift pack: %w", err)
	}
	return pack, true, nil
}

func (s *GiftPackStore) DeleteGiftPack(ctx context.Context, packID string) (bool, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM gift_packs WHERE pack_id = $1`, packID)
	if err != nil {
		return false, fmt.Errorf("delete gift pack: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
