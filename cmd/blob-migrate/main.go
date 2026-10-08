// Command blob-migrate copies every file_blobs row on one permanent backend
// to another -- in practice s3 (a Docker MinIO an install is retiring) to
// localfs (the disk-only setup a native, no-Docker install uses instead).
// It never touches the source: S3 objects are only ever read, never deleted,
// so a bad run leaves the old backend fully intact to retry from.
//
// It is deliberately NOT a general-purpose sync tool: it moves exactly the
// rows matching -from, writes them through the same BlobBackend
// implementations the server itself uses (internal/app/files), and flips
// each row's file_blobs.backend only once the write is verified -- so
// re-running it after a partial run (crash, Ctrl+C, a transient S3 error)
// only ever processes what -from still selects, which is exactly the rows
// the previous run did not finish.
//
// Usage (flags, not .env -- this needs only a handful of settings, not a
// full server config):
//
//	blob-migrate \
//	  -postgres-dsn "postgres://owpengram:owpengram@127.0.0.1:5432/owpengram?sslmode=disable" \
//	  -s3-endpoint 127.0.0.1:9000 -s3-bucket owpengram-media \
//	  -s3-access-key-id owpengram -s3-secret-access-key owpengram123 \
//	  -local-dir data/blobs \
//	  -dry-run
//
// Drop -dry-run to actually write files and flip rows. -workers controls
// how many objects are copied at once (default 8); -limit caps how many
// rows a single run processes, for a small first pass before the real one.
package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"telesrv/internal/app/files"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var (
		postgresDSN = flag.String("postgres-dsn", "", "PostgreSQL connection string (required)")

		from = flag.String("from", "s3", "file_blobs.backend value to migrate rows away from")
		to   = flag.String("to", "localfs", "file_blobs.backend value to migrate rows to (must be localfs for now)")

		s3Endpoint  = flag.String("s3-endpoint", "", "source S3 endpoint, host[:port] without a scheme (required)")
		s3Bucket    = flag.String("s3-bucket", "", "source S3 bucket (required)")
		s3Region    = flag.String("s3-region", "us-east-1", "source S3 region")
		s3AccessKey = flag.String("s3-access-key-id", "", "source S3 access key (required)")
		s3SecretKey = flag.String("s3-secret-access-key", "", "source S3 secret key (required)")
		s3UseSSL    = flag.Bool("s3-use-ssl", false, "use HTTPS for the source S3 endpoint")
		s3PathStyle = flag.Bool("s3-path-style", true, "path-style S3 addressing (MinIO needs this on, AWS S3 does not)")

		localDir = flag.String("local-dir", "", "destination localfs root, i.e. the new install's TELESRV_BLOB_DIR (required)")

		workers = flag.Int("workers", 8, "objects copied at once")
		limit   = flag.Int("limit", 0, "stop after this many rows (0 = no limit); run a small one first")
		dryRun  = flag.Bool("dry-run", false, "download and verify every selected row, but write nothing and flip no rows")
	)
	flag.Parse()

	if *postgresDSN == "" {
		return fmt.Errorf("-postgres-dsn is required")
	}
	if *to != "localfs" {
		// Nothing here actually writes anything but localfs -- Put/pathFor
		// below are LocalFS's, not shared with S3FS -- so this is the one
		// direction this tool implements. Flag kept explicit (not
		// hardcoded) so a run's intent always shows up in its own command
		// line, not just in this file's doc comment.
		return fmt.Errorf("-to must be \"localfs\" (the only direction this tool writes)")
	}
	if *s3Endpoint == "" || *s3Bucket == "" || *s3AccessKey == "" || *s3SecretKey == "" {
		return fmt.Errorf("-s3-endpoint, -s3-bucket, -s3-access-key-id and -s3-secret-access-key are all required")
	}
	if *localDir == "" {
		return fmt.Errorf("-local-dir is required")
	}
	if *workers < 1 {
		*workers = 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, *postgresDSN)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	src, err := files.NewS3FS(ctx, *s3Endpoint, *s3AccessKey, *s3SecretKey, *s3Bucket, *s3Region, *s3UseSSL, *s3PathStyle)
	if err != nil {
		return fmt.Errorf("connect source s3: %w", err)
	}
	dst, err := files.NewLocalFS(*localDir)
	if err != nil {
		return fmt.Errorf("open destination localfs: %w", err)
	}

	total, err := countRows(ctx, pool, *from)
	if err != nil {
		return fmt.Errorf("count rows: %w", err)
	}
	if total == 0 {
		fmt.Printf("no file_blobs rows with backend=%q -- nothing to do\n", *from)
		return nil
	}
	selected := total
	if *limit > 0 && int64(*limit) < selected {
		selected = int64(*limit)
	}
	mode := "LIVE"
	if *dryRun {
		mode = "DRY RUN (nothing written, no rows flipped)"
	}
	fmt.Printf("%s: %d row(s) with backend=%q (%d total), %d worker(s), %q -> %q\n",
		mode, selected, *from, total, *workers, *from, *to)

	rows := make(chan blobRow, *workers*2)
	var (
		wg                                   sync.WaitGroup
		copied, skippedExisting, failed, bts atomic.Int64
	)
	start := time.Now()

	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for row := range rows {
				n, existed, err := migrateOne(ctx, pool, src, dst, row, *from, *to, *dryRun)
				switch {
				case err != nil:
					failed.Add(1)
					log.Printf("FAILED location_key=%s object_key=%s: %v", row.locationKey, row.objectKey, err)
				case existed:
					skippedExisting.Add(1)
				default:
					copied.Add(1)
					bts.Add(n)
				}
			}
		}()
	}

	feedErr := feedRows(ctx, pool, *from, selected, rows)
	close(rows)
	wg.Wait()

	elapsed := time.Since(start).Round(time.Second)
	fmt.Printf("done in %s: migrated=%d already-present=%d failed=%d bytes=%s\n",
		elapsed, copied.Load(), skippedExisting.Load(), failed.Load(), humanBytes(bts.Load()))
	if feedErr != nil && !errors.Is(feedErr, context.Canceled) {
		return fmt.Errorf("listing rows: %w", feedErr)
	}
	if failed.Load() > 0 {
		return fmt.Errorf("%d row(s) failed -- re-run the same command to retry just those (already-migrated rows are skipped automatically)", failed.Load())
	}
	return nil
}

type blobRow struct {
	locationKey string
	objectKey   string
	size        int64
	sha256      []byte
}

func countRows(ctx context.Context, pool *pgxpool.Pool, backend string) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, "SELECT count(*) FROM file_blobs WHERE backend = $1", backend).Scan(&n)
	return n, err
}

// feedRows streams rows into out in location_key order, oldest first, so a
// run that stops partway (Ctrl+C, -limit) always covers a stable, repeatable
// prefix rather than whatever order the database happened to return.
func feedRows(ctx context.Context, pool *pgxpool.Pool, backend string, limit int64, out chan<- blobRow) error {
	query := "SELECT location_key, object_key, size, sha256 FROM file_blobs WHERE backend = $1 ORDER BY location_key"
	args := []any{backend}
	if limit > 0 {
		query += " LIMIT $2"
		args = append(args, limit)
	}
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r blobRow
		if err := rows.Scan(&r.locationKey, &r.objectKey, &r.size, &r.sha256); err != nil {
			return err
		}
		select {
		case out <- r:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return rows.Err()
}

// migrateOne copies one object end to end: read from src, verify it against
// what file_blobs already recorded, write to dst, confirm dst wrote it back
// under the same content-addressed key, then (outside -dry-run) flip the
// row. existed reports a row whose bytes were already correctly in place in
// dst -- LocalFS.Put itself dedupes identical content, so this mainly shows
// up on a second run over rows a first run already wrote but had not
// flipped yet (e.g. it was killed between the write and the UPDATE).
func migrateOne(ctx context.Context, pool *pgxpool.Pool, src *files.S3FS, dst *files.LocalFS, row blobRow, from, to string, dryRun bool) (bytesWritten int64, existed bool, err error) {
	data, err := src.Get(ctx, row.objectKey)
	if err != nil {
		return 0, false, fmt.Errorf("read from source: %w", err)
	}
	if row.size > 0 && int64(len(data)) != row.size {
		return 0, false, fmt.Errorf("size mismatch: file_blobs says %d bytes, source has %d", row.size, len(data))
	}
	if len(row.sha256) == sha256.Size {
		sum := sha256.Sum256(data)
		if string(sum[:]) != string(row.sha256) {
			return 0, false, fmt.Errorf("sha256 mismatch against file_blobs -- source object is not what this row expects, not migrating it")
		}
	}

	if dryRun {
		return int64(len(data)), false, nil
	}

	before, statErr := dst.Get(ctx, row.objectKey)
	alreadyThere := statErr == nil && len(before) == len(data)

	// LocalFS.Put is itself content-addressed: it hashes data and derives the
	// path from that hash, same as every other writer. The returned key
	// matching row.objectKey is therefore not just "it wrote something" --
	// it is confirmation the bytes just read from the source really do hash
	// to the key this row already claims.
	localKey, err := dst.Put(ctx, data)
	if err != nil {
		return 0, false, fmt.Errorf("write to destination: %w", err)
	}
	if localKey != row.objectKey {
		return 0, false, fmt.Errorf("content-address mismatch: wrote under %q, file_blobs expects %q -- backend not flipped for this row", localKey, row.objectKey)
	}

	tag, err := pool.Exec(ctx,
		"UPDATE file_blobs SET backend = $1 WHERE location_key = $2 AND backend = $3",
		to, row.locationKey, from)
	if err != nil {
		return 0, false, fmt.Errorf("flip backend column: %w", err)
	}
	if tag.RowsAffected() == 0 || alreadyThere {
		// Either another run already moved this row between the SELECT and
		// this UPDATE, or the file was already correctly in place from an
		// earlier, interrupted run. The bytes are right either way.
		return int64(len(data)), true, nil
	}
	return int64(len(data)), false, nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
