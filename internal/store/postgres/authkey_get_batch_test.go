package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"telesrv/internal/store"
	"telesrv/internal/store/postgres/sqlcgen"
)

func TestBatchedAuthKeyStorePostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	const keyCount = 32
	keys := NewAuthKeyStore(pool)
	ids := make([][8]byte, 0, keyCount)
	old := time.Now().Add(-time.Hour)
	for index := 0; index < keyCount; index++ {
		id := randomLayerTestAuthKeyID(t)
		data := store.AuthKeyData{ID: id, ServerSalt: int64(index + 1)}
		data.Value[0] = byte(index + 1)
		if err := keys.Save(ctx, data); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE auth_keys SET last_used_at = $2 WHERE auth_key_id = $1`, authKeyIDToInt64(id), old); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() {
		for _, id := range ids {
			_ = keys.Delete(ctx, id)
		}
	})

	counted := &authKeyGetCountingDB{db: pool}
	batcher, err := NewBatchedAuthKeyStore(NewAuthKeyStore(counted), AuthKeyGetBatchConfig{
		MaxSize: keyCount, MaxWait: 10 * time.Millisecond,
		QueueSize: keyCount * 2, QueryTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(batcher.Close)

	start := make(chan struct{})
	errs := make(chan error, keyCount)
	var wg sync.WaitGroup
	for index, id := range ids {
		index, id := index, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			data, found, getErr := batcher.Get(ctx, id)
			if getErr != nil {
				errs <- getErr
				return
			}
			if !found || data.ID != id || data.ServerSalt != int64(index+1) || data.Value[0] != byte(index+1) {
				errs <- errors.New("batched auth-key result mismatch")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if calls := counted.batchQueries.Load(); calls <= 0 || calls > 4 {
		t.Fatalf("batch SQL calls = %d, want 1..4 for %d concurrent keys", calls, keyCount)
	}
	for _, id := range ids {
		var touched time.Time
		if err := pool.QueryRow(ctx, `SELECT last_used_at FROM auth_keys WHERE auth_key_id = $1`, authKeyIDToInt64(id)).Scan(&touched); err != nil {
			t.Fatal(err)
		}
		if !touched.After(old) {
			t.Fatalf("auth key %x was not touched: %v", id, touched)
		}
	}

	readMarker := time.Now().Add(-2 * time.Hour).Truncate(time.Microsecond)
	for _, id := range ids {
		if _, err := pool.Exec(ctx, `UPDATE auth_keys SET last_used_at = $2 WHERE auth_key_id = $1`, authKeyIDToInt64(id), readMarker); err != nil {
			t.Fatal(err)
		}
	}
	errs = make(chan error, keyCount)
	start = make(chan struct{})
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			data, found, getErr := batcher.Revalidate(ctx, id)
			if getErr != nil || !found || data.ID != id {
				errs <- errors.New("batched auth-key revalidate mismatch")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if calls := counted.revalidateQueries.Load(); calls <= 0 || calls > 4 {
		t.Fatalf("revalidate SQL calls = %d, want 1..4 for %d concurrent keys", calls, keyCount)
	}
	for _, id := range ids {
		var lastUsed time.Time
		if err := pool.QueryRow(ctx, `SELECT last_used_at FROM auth_keys WHERE auth_key_id = $1`, authKeyIDToInt64(id)).Scan(&lastUsed); err != nil {
			t.Fatal(err)
		}
		if !lastUsed.Equal(readMarker) {
			t.Fatalf("revalidate touched auth key %x: got %v want %v", id, lastUsed, readMarker)
		}
	}

	missing := randomLayerTestAuthKeyID(t)
	if _, found, err := batcher.Get(ctx, missing); err != nil || found {
		t.Fatalf("missing Get = found %v err %v", found, err)
	}
	batcher.Close()
	if _, _, err := batcher.Get(ctx, ids[0]); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get after close err = %v", err)
	}
}

func TestNewBatchedAuthKeyStoreRejectsInvalidConfig(t *testing.T) {
	pool := testPool(t)
	base := NewAuthKeyStore(pool)
	for _, cfg := range []AuthKeyGetBatchConfig{
		{},
		{MaxSize: 1, MaxWait: 11 * time.Millisecond, QueueSize: 1, QueryTimeout: time.Second},
		{MaxSize: 2, MaxWait: time.Microsecond, QueueSize: 1, QueryTimeout: time.Second},
		{MaxSize: 1, MaxWait: time.Microsecond, QueueSize: 1, QueryTimeout: 31 * time.Second},
	} {
		if batcher, err := NewBatchedAuthKeyStore(base, cfg); err == nil {
			batcher.Close()
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
}

type authKeyGetCountingDB struct {
	db                sqlcgen.DBTX
	batchQueries      atomic.Int64
	revalidateQueries atomic.Int64
}

func (db *authKeyGetCountingDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return db.db.Exec(ctx, sql, args...)
}

func (db *authKeyGetCountingDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "auth_key_get_batch") {
		db.batchQueries.Add(1)
	}
	if strings.Contains(sql, "auth_key_revalidate_batch") {
		db.revalidateQueries.Add(1)
	}
	return db.db.Query(ctx, sql, args...)
}

func (db *authKeyGetCountingDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return db.db.QueryRow(ctx, sql, args...)
}

func (db *authKeyGetCountingDB) Begin(ctx context.Context) (pgx.Tx, error) {
	beginner, ok := db.db.(txBeginner)
	if !ok {
		return nil, errors.New("counted database does not support transactions")
	}
	return beginner.Begin(ctx)
}

var _ sqlcgen.DBTX = (*authKeyGetCountingDB)(nil)
