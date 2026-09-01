package postgres

import (
	"context"
	"fmt"

	"telesrv/internal/domain"
	"telesrv/internal/store/postgres/sqlcgen"
)

type GifCatalogStore struct {
	db sqlcgen.DBTX
}

func NewGifCatalogStore(db sqlcgen.DBTX) *GifCatalogStore {
	return &GifCatalogStore{db: db}
}

func (s *GifCatalogStore) CreateGifCatalogEntry(ctx context.Context, entry domain.GifCatalogEntry) (domain.GifCatalogEntry, error) {
	if entry.ID == 0 || entry.DocumentID == 0 {
		return domain.GifCatalogEntry{}, fmt.Errorf("create gif catalog entry: id and document_id are required")
	}
	var count int64
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM gif_catalog`).Scan(&count); err != nil {
		return domain.GifCatalogEntry{}, fmt.Errorf("count gif catalog entries: %w", err)
	}
	if count >= domain.MaxGifCatalogEntries {
		return domain.GifCatalogEntry{}, domain.ErrGifCatalogFull
	}
	row := s.db.QueryRow(ctx, `
INSERT INTO gif_catalog (id, title, document_id, enabled, sort_order, created_by, source_filename, category)
VALUES ($1, $2, $3, true, $4, $5, $6, $7)
RETURNING id, title, document_id, enabled, sort_order, created_by, created_at, updated_at, source_filename, category`,
		entry.ID, entry.Title, entry.DocumentID, entry.SortOrder, entry.CreatedBy, entry.SourceFilename, entry.Category)
	out, err := scanGifCatalogEntry(row.Scan)
	if err != nil {
		return domain.GifCatalogEntry{}, fmt.Errorf("create gif catalog entry: %w", err)
	}
	return out, nil
}

// HasGifCatalogSourceFilename reports whether a seed-imported entry for this
// filename already exists. Deliberately not folded into CreateGifCatalogEntry
// as an ON CONFLICT DO NOTHING: SeedGifs needs to know *before* transcoding
// whether a file is new, not just fail silently after paying for ffmpeg work
// that turns out to be wasted.
func (s *GifCatalogStore) HasGifCatalogSourceFilename(ctx context.Context, filename string) (bool, error) {
	if filename == "" {
		return false, nil
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gif_catalog WHERE source_filename = $1)`, filename).Scan(&exists); err != nil {
		return false, fmt.Errorf("check gif catalog source filename: %w", err)
	}
	return exists, nil
}

func (s *GifCatalogStore) ListGifCatalog(ctx context.Context, onlyEnabled bool, limit int) ([]domain.GifCatalogEntry, error) {
	// Postgres treats LIMIT NULL as "no limit" -- a nil *int parameter gets
	// there without a second query string for the unbounded (admin) case.
	var limitParam *int
	if limit > 0 {
		limitParam = &limit
	}
	rows, err := s.db.Query(ctx, `
SELECT id, title, document_id, enabled, sort_order, created_by, created_at, updated_at, source_filename, category
FROM gif_catalog
WHERE NOT $1 OR enabled
ORDER BY sort_order, id
LIMIT $2`, onlyEnabled, limitParam)
	if err != nil {
		return nil, fmt.Errorf("list gif catalog: %w", err)
	}
	defer rows.Close()
	out := make([]domain.GifCatalogEntry, 0)
	for rows.Next() {
		item, err := scanGifCatalogEntry(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan gif catalog entry: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *GifCatalogStore) SetGifCatalogEnabled(ctx context.Context, id int64, enabled bool) (bool, error) {
	tag, err := s.db.Exec(ctx, `UPDATE gif_catalog SET enabled = $2, updated_at = now() WHERE id = $1`, id, enabled)
	if err != nil {
		return false, fmt.Errorf("set gif catalog entry enabled: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *GifCatalogStore) SetGifCatalogSortOrder(ctx context.Context, id int64, order int) (bool, error) {
	tag, err := s.db.Exec(ctx, `UPDATE gif_catalog SET sort_order = $2, updated_at = now() WHERE id = $1`, id, order)
	if err != nil {
		return false, fmt.Errorf("set gif catalog entry sort order: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *GifCatalogStore) SetGifCatalogCategory(ctx context.Context, id int64, category string) (bool, error) {
	tag, err := s.db.Exec(ctx, `UPDATE gif_catalog SET category = $2, updated_at = now() WHERE id = $1`, id, category)
	if err != nil {
		return false, fmt.Errorf("set gif catalog entry category: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *GifCatalogStore) DeleteGifCatalogEntry(ctx context.Context, id int64) (bool, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM gif_catalog WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete gif catalog entry: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func scanGifCatalogEntry(scan func(dest ...any) error) (domain.GifCatalogEntry, error) {
	var e domain.GifCatalogEntry
	if err := scan(&e.ID, &e.Title, &e.DocumentID, &e.Enabled, &e.SortOrder, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt, &e.SourceFilename, &e.Category); err != nil {
		return domain.GifCatalogEntry{}, err
	}
	return e, nil
}
