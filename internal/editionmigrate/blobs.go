package editionmigrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/minio/minio-go/v7"

	"telesrv/internal/domain"
	"telesrv/internal/store/postgres"
)

// BlobStore is the slice of internal/app/files.BlobBackend the copy needs.
// Declared here so this package does not import the whole files service.
type BlobStore interface {
	PutReader(ctx context.Context, r io.Reader) (objectKey string, size int64, sha256 []byte, err error)
	GetRange(ctx context.Context, objectKey string, offset, limit int64) (data []byte, total int64, err error)
}

// BlobLedger is the file_blobs bookkeeping side of the copy, satisfied by
// *postgres.MediaStore.
type BlobLedger interface {
	ListBlobMigrationObjects(ctx context.Context, backend domain.MediaBackend, afterObjectKey string, limit int) ([]postgres.BlobMigrationObject, error)
	MoveFileBlobBackendForObject(ctx context.Context, from, to domain.MediaBackend, objectKey string, expectedRows int64) error
}

// BlobCopyResult reports what one CopyBlobs pass did.
type BlobCopyResult struct {
	Moved   int
	Bytes   int64
	Skipped []string
}

const (
	blobListPage  = 200
	blobChunkSize = 8 << 20
)

// CopyBlobs carries every object that file_blobs lists under backend `from`
// over to `dst`, then relabels its rows as backend `to`.
//
// Order per object is copy, verify, relabel -- the rows only change once the
// bytes are known to be at the destination under the right content hash, so
// an interrupted run leaves every object readable from one place or the
// other and a re-run simply picks up what is still labelled `from`. Sources
// are never deleted: the previous edition keeps working as a fallback until
// the operator removes it.
//
// An object whose bytes are simply gone from the source (deleted by hand,
// never written) cannot be moved by anyone, so it is skipped with a warning
// and stays labelled `from` -- refusing the whole switch over a file that is
// already unreadable would only trap the install on its current edition.
// Any other failure (storage unreachable, a read error, a damaged object)
// aborts, because skipping those would quietly leave readable media behind;
// tolerateErrors skips those too.
func CopyBlobs(
	ctx context.Context,
	ledger BlobLedger,
	from, to domain.MediaBackend,
	src, dst BlobStore,
	tolerateErrors bool,
	progress func(string),
) (BlobCopyResult, error) {
	var result BlobCopyResult
	// Skipped objects stay labelled `from`, so a keyset page that restarted
	// from the top would hand them back forever; the cursor only moves on.
	after := ""
	for {
		objects, err := ledger.ListBlobMigrationObjects(ctx, from, after, blobListPage)
		if err != nil {
			return result, err
		}
		if len(objects) == 0 {
			return result, nil
		}
		for _, object := range objects {
			after = object.ObjectKey
			if err := copyBlobObject(ctx, src, dst, object); err != nil {
				if ctx.Err() != nil || (!tolerateErrors && !isMissingObject(err)) {
					return result, fmt.Errorf("copy blob %s: %w", object.ObjectKey, err)
				}
				result.Skipped = append(result.Skipped, object.ObjectKey)
				if isMissingObject(err) {
					emit(progress, fmt.Sprintf("  skipped %s: its file is missing from the old storage\n", object.ObjectKey))
				} else {
					emit(progress, fmt.Sprintf("  skipped %s: %v\n", object.ObjectKey, err))
				}
				continue
			}
			if err := ledger.MoveFileBlobBackendForObject(ctx, from, to, object.ObjectKey, object.LocationRows); err != nil {
				return result, err
			}
			result.Moved++
			result.Bytes += object.Size
			if result.Moved%100 == 0 {
				emit(progress, fmt.Sprintf("  %d files moved...\n", result.Moved))
			}
		}
	}
}

// isMissingObject reports whether err means the source has no such object,
// for either backend: a local file that does not exist, or S3's NoSuchKey.
func isMissingObject(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	var resp minio.ErrorResponse
	return errors.As(err, &resp) && (resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket")
}

func copyBlobObject(ctx context.Context, src, dst BlobStore, object postgres.BlobMigrationObject) error {
	key, size, sum, err := dst.PutReader(ctx, &rangeReader{ctx: ctx, store: src, key: object.ObjectKey})
	if err != nil {
		return err
	}
	// The key is the SHA-256 of the content, so this also catches a source
	// object that is corrupt or truncated -- it would have been stored
	// under a different name.
	if key != object.ObjectKey || size != object.Size || string(sum) != string(object.SHA256) {
		return fmt.Errorf("destination stored %d bytes as %s, want %d bytes as %s (source object is damaged)",
			size, key, object.Size, object.ObjectKey)
	}
	return nil
}

// rangeReader streams one object out of a BlobStore in fixed-size ranges,
// because the interface only offers whole-object reads (which would hold a
// multi-gigabyte video in memory) or GetRange.
type rangeReader struct {
	ctx    context.Context
	store  BlobStore
	key    string
	offset int64
	buf    []byte
	done   bool
}

func (r *rangeReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.done {
			return 0, io.EOF
		}
		data, total, err := r.store.GetRange(r.ctx, r.key, r.offset, blobChunkSize)
		if err != nil {
			return 0, err
		}
		r.offset += int64(len(data))
		r.buf = data
		if r.offset >= total || len(data) == 0 {
			r.done = true
		}
		if len(data) == 0 && r.offset < total {
			return 0, errors.New("source returned no data before the end of the object")
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}
