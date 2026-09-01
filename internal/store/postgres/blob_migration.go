package postgres

import (
	"context"
	"encoding/hex"
	"fmt"

	"telesrv/internal/domain"
)

// BlobMigrationObject is one immutable content-addressed object referenced by
// one or more logical file locations on the same permanent backend.
type BlobMigrationObject struct {
	ObjectKey    string
	Size         int64
	SHA256       []byte
	LocationRows int64
}

// ListBlobMigrationObjects keyset-pages distinct objects. Inconsistent size or
// digest metadata for one content key is returned as an error, never guessed.
func (s *MediaStore) ListBlobMigrationObjects(
	ctx context.Context,
	backend domain.MediaBackend,
	afterObjectKey string,
	limit int,
) ([]BlobMigrationObject, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
SELECT
    object_key,
    min(size)::bigint,
    count(DISTINCT size)::bigint,
    min(encode(sha256, 'hex')),
    count(DISTINCT encode(sha256, 'hex'))::bigint,
    count(*)::bigint
FROM file_blobs
WHERE backend = $1 AND object_key > $2
GROUP BY object_key
ORDER BY object_key
LIMIT $3`, string(backend), afterObjectKey, limit)
	if err != nil {
		return nil, fmt.Errorf("list %s blob migration objects: %w", backend, err)
	}
	defer rows.Close()
	objects := make([]BlobMigrationObject, 0, limit)
	for rows.Next() {
		var (
			object         BlobMigrationObject
			sizeVariants   int64
			digestHex      string
			digestVariants int64
		)
		if err := rows.Scan(
			&object.ObjectKey,
			&object.Size,
			&sizeVariants,
			&digestHex,
			&digestVariants,
			&object.LocationRows,
		); err != nil {
			return nil, fmt.Errorf("scan blob migration object: %w", err)
		}
		if sizeVariants != 1 || digestVariants != 1 {
			return nil, fmt.Errorf("blob %q has inconsistent persisted size or SHA-256 metadata", object.ObjectKey)
		}
		digest, err := hex.DecodeString(digestHex)
		if err != nil || len(digest) != 32 {
			return nil, fmt.Errorf("blob %q has invalid persisted SHA-256 metadata", object.ObjectKey)
		}
		if object.ObjectKey != digestHex {
			return nil, fmt.Errorf("blob %q object key does not match persisted SHA-256 %q", object.ObjectKey, digestHex)
		}
		object.SHA256 = digest
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate blob migration objects: %w", err)
	}
	return objects, nil
}

// MoveFileBlobBackendForObject atomically relabels every logical location for a
// verified immutable object. It refuses partial/racing changes.
func (s *MediaStore) MoveFileBlobBackendForObject(
	ctx context.Context,
	from domain.MediaBackend,
	to domain.MediaBackend,
	objectKey string,
	expectedRows int64,
) error {
	if expectedRows <= 0 {
		return fmt.Errorf("blob %q expected row count must be positive", objectKey)
	}
	result, err := s.db.Exec(ctx, `
UPDATE file_blobs
SET backend = $1
WHERE backend = $2 AND object_key = $3`, string(to), string(from), objectKey)
	if err != nil {
		return fmt.Errorf("move blob %q metadata from %s to %s: %w", objectKey, from, to, err)
	}
	if result.RowsAffected() != expectedRows {
		return fmt.Errorf(
			"move blob %q metadata changed %d rows, want %d",
			objectKey, result.RowsAffected(), expectedRows,
		)
	}
	return nil
}
