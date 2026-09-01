package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func TestBlobStorageAdvisoryLock(t *testing.T) {
	dsn := os.Getenv("TELESRV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TELESRV_TEST_POSTGRES_DSN to run postgres integration test")
	}
	ctx := context.Background()
	runtimeLock, err := AcquireBlobRuntimeLock(ctx, dsn)
	if err != nil {
		t.Fatalf("acquire runtime lock: %v", err)
	}
	if _, err := AcquireBlobMigrationLock(ctx, dsn); err == nil {
		t.Fatal("exclusive migration lock acquired while runtime shared lock was held")
	}
	if err := runtimeLock.Close(); err != nil {
		t.Fatalf("close runtime lock: %v", err)
	}
	migrationLock, err := AcquireBlobMigrationLock(ctx, dsn)
	if err != nil {
		t.Fatalf("acquire migration lock after runtime stopped: %v", err)
	}
	if err := migrationLock.Close(); err != nil {
		t.Fatalf("close migration lock: %v", err)
	}
}

func TestBlobMigrationMetadataRoundTrip(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	media := NewMediaStore(pool)
	uniqueBefore, err := media.UniqueFileBlobBytes(ctx, domain.MediaBackendLocalFS)
	if err != nil {
		t.Fatalf("unique blob bytes before insert: %v", err)
	}
	suffix := time.Now().UnixNano()
	first := postgresTestBlob("blob-migration:first:"+time.Unix(0, suffix).Format("150405.000000000"), "shared-migration", 4096, "application/octet-stream")
	second := first
	second.LocationKey = "blob-migration:second:" + time.Unix(0, suffix).Format("150405.000000000")
	for _, blob := range []domain.FileBlob{first, second} {
		if err := media.PutFileBlob(ctx, blob); err != nil {
			t.Fatalf("put %s: %v", blob.LocationKey, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM file_blobs WHERE location_key = ANY($1::text[])", []string{first.LocationKey, second.LocationKey})
	})

	counts, err := media.FileBlobBackendCounts(ctx)
	if err != nil || counts[domain.MediaBackendLocalFS] < 2 {
		t.Fatalf("backend counts=%v err=%v", counts, err)
	}
	uniqueBytes, err := media.UniqueFileBlobBytes(ctx, domain.MediaBackendLocalFS)
	if err != nil {
		t.Fatalf("unique blob bytes: %v", err)
	}
	if uniqueBytes-uniqueBefore != first.Size {
		t.Fatalf("unique blob byte delta=%d, want shared object counted once as %d", uniqueBytes-uniqueBefore, first.Size)
	}
	objects, err := media.ListBlobMigrationObjects(ctx, domain.MediaBackendLocalFS, first.ObjectKey[:len(first.ObjectKey)-1], 10)
	if err != nil {
		t.Fatalf("list migration objects: %v", err)
	}
	var found *BlobMigrationObject
	for i := range objects {
		if objects[i].ObjectKey == first.ObjectKey {
			found = &objects[i]
			break
		}
	}
	if found == nil || found.LocationRows != 2 || found.Size != first.Size {
		t.Fatalf("migration object=%+v", found)
	}
	if err := media.MoveFileBlobBackendForObject(
		ctx, domain.MediaBackendLocalFS, domain.MediaBackendS3,
		found.ObjectKey, found.LocationRows,
	); err != nil {
		t.Fatalf("move backend: %v", err)
	}
	for _, key := range []string{first.LocationKey, second.LocationKey} {
		blob, ok, err := media.GetFileBlob(ctx, key)
		if err != nil || !ok || blob.Backend != domain.MediaBackendS3 {
			t.Fatalf("get %s backend=%q ok=%v err=%v", key, blob.Backend, ok, err)
		}
	}
}
