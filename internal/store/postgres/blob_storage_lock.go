package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// blobStorageAdvisoryLockKey is the signed int64 encoding of "telesrvb". Every
// running server holds a shared session lock; the offline migration tool needs
// the exclusive form, which proves no server using this database is active.
const blobStorageAdvisoryLockKey int64 = 0x74656c6573727662

type BlobStorageLock struct {
	conn   *pgx.Conn
	shared bool
}

func AcquireBlobRuntimeLock(ctx context.Context, dsn string) (*BlobStorageLock, error) {
	return acquireBlobStorageLock(ctx, dsn, true)
}

func AcquireBlobMigrationLock(ctx context.Context, dsn string) (*BlobStorageLock, error) {
	return acquireBlobStorageLock(ctx, dsn, false)
}

func acquireBlobStorageLock(ctx context.Context, dsn string, shared bool) (*BlobStorageLock, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect for blob storage lock: %w", err)
	}
	query := "SELECT pg_try_advisory_lock($1)"
	kind := "exclusive migration"
	if shared {
		query = "SELECT pg_try_advisory_lock_shared($1)"
		kind = "shared runtime"
	}
	var acquired bool
	if err := conn.QueryRow(ctx, query, blobStorageAdvisoryLockKey).Scan(&acquired); err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("acquire %s blob storage lock: %w", kind, err)
	}
	if !acquired {
		_ = conn.Close(context.Background())
		if shared {
			return nil, fmt.Errorf("blob migration lock is active; wait for the offline migration to finish before starting telesrv")
		}
		return nil, fmt.Errorf("one or more telesrv processes are active; stop every process using this PostgreSQL database before migrating blobs")
	}
	return &BlobStorageLock{conn: conn, shared: shared}, nil
}

func (l *BlobStorageLock) Close() error {
	if l == nil || l.conn == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	query := "SELECT pg_advisory_unlock($1)"
	if l.shared {
		query = "SELECT pg_advisory_unlock_shared($1)"
	}
	var unlocked bool
	err := l.conn.QueryRow(ctx, query, blobStorageAdvisoryLockKey).Scan(&unlocked)
	closeErr := l.conn.Close(ctx)
	l.conn = nil
	if err != nil {
		return fmt.Errorf("release blob storage lock: %w", err)
	}
	if !unlocked {
		return fmt.Errorf("blob storage advisory lock was not held by its session")
	}
	if closeErr != nil {
		return fmt.Errorf("close blob storage lock connection: %w", closeErr)
	}
	return nil
}
