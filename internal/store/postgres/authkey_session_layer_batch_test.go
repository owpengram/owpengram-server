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

func TestSelectDistinctLayerAdvanceBatchDefersSameSession(t *testing.T) {
	first := authKeySessionLayerBatchRequest{rawAuthKeyID: [8]byte{1}, sessionID: 7}
	duplicate := authKeySessionLayerBatchRequest{rawAuthKeyID: [8]byte{1}, sessionID: 7}
	other := authKeySessionLayerBatchRequest{rawAuthKeyID: [8]byte{1}, sessionID: 8}
	batch, remaining := selectDistinctLayerAdvanceBatch(
		[]authKeySessionLayerBatchRequest{first, duplicate, other},
		3,
	)
	if len(batch) != 2 || batch[0].sessionID != 7 || batch[1].sessionID != 8 {
		t.Fatalf("batch = %#v", batch)
	}
	if len(remaining) != 1 || remaining[0].sessionID != 7 {
		t.Fatalf("remaining = %#v", remaining)
	}
}

func TestBatchedAuthKeySessionLayerStorePostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	const accountCount = 32
	now := time.Now().UTC()
	keys := NewAuthKeyStore(pool)
	type seeded struct {
		id            [8]byte
		sessionID     int64
		observationID int64
		msgID         int64
	}
	seededKeys := make([]seeded, 0, accountCount)
	for index := 0; index < accountCount; index++ {
		id := randomLayerTestAuthKeyID(t)
		sessionID := int64(91000 + index)
		if err := keys.Save(ctx, store.AuthKeyData{ID: id}); err != nil {
			t.Fatal(err)
		}
		firstMsgID := authKeySessionLayerTestMsgID(now, uint32(index+1))
		first, applied, err := keys.AdvanceSessionLayer(ctx, id, sessionID, 227, firstMsgID)
		if err != nil || !applied || first.ObservationID <= 0 {
			t.Fatalf("seed %d = (%+v,%v,%v)", index, first, applied, err)
		}
		seededKeys = append(seededKeys, seeded{
			id: id, sessionID: sessionID, observationID: first.ObservationID,
			msgID: authKeySessionLayerTestMsgID(now, uint32(accountCount+index+1)),
		})
	}
	t.Cleanup(func() {
		for _, item := range seededKeys {
			_ = keys.Delete(ctx, item.id)
		}
	})

	counted := &layerBatchCountingDB{db: pool}
	batchedBase := NewAuthKeyStore(counted)
	batcher, err := NewBatchedAuthKeySessionLayerStore(batchedBase, AuthKeySessionLayerBatchConfig{
		MaxSize: accountCount, MaxWait: 10 * time.Millisecond,
		QueueSize: accountCount * 2, QueryTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(batcher.Close)

	start := make(chan struct{})
	errs := make(chan error, accountCount)
	var wg sync.WaitGroup
	for _, item := range seededKeys {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			current, applied, err := batcher.AdvanceSessionLayer(ctx, item.id, item.sessionID, 227, item.msgID)
			if err != nil {
				errs <- err
				return
			}
			if !applied || current.MessageID != item.msgID || current.ObservationID != item.observationID {
				errs <- errors.New("same-Layer batch changed durable generation or failed to advance")
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
		t.Fatalf("batch SQL calls = %d, want 1..4 for %d concurrent sessions", calls, accountCount)
	}
	for _, item := range seededKeys {
		current, found, err := keys.GetSessionLayer(ctx, item.id, item.sessionID)
		if err != nil || !found || current.MessageID != item.msgID || current.ObservationID != item.observationID {
			t.Fatalf("durable result %x/%d = (%+v,%v,%v)", item.id, item.sessionID, current, found, err)
		}
	}

	// A fast miss must synchronously execute the original full state machine,
	// rather than treating a successful batch statement as success for every row.
	missingSession := int64(99001)
	missingMsgID := authKeySessionLayerTestMsgID(now, 1000)
	created, applied, err := batcher.AdvanceSessionLayer(ctx, seededKeys[0].id, missingSession, 225, missingMsgID)
	if err != nil || !applied || created.Layer != 225 || created.MessageID != missingMsgID || created.ObservationID <= 0 {
		t.Fatalf("batch miss full fallback = (%+v,%v,%v)", created, applied, err)
	}

	batcher.Close()
	if _, _, err := batcher.AdvanceSessionLayer(ctx, seededKeys[0].id, missingSession, 225, missingMsgID); !errors.Is(err, context.Canceled) {
		t.Fatalf("advance after close err = %v", err)
	}
}

type layerBatchCountingDB struct {
	db           sqlcgen.DBTX
	batchQueries atomic.Int64
}

func (db *layerBatchCountingDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return db.db.Exec(ctx, sql, args...)
}

func (db *layerBatchCountingDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "WITH input AS") && strings.Contains(sql, "candidates AS MATERIALIZED") {
		db.batchQueries.Add(1)
	}
	return db.db.Query(ctx, sql, args...)
}

func (db *layerBatchCountingDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return db.db.QueryRow(ctx, sql, args...)
}

func (db *layerBatchCountingDB) Begin(ctx context.Context) (pgx.Tx, error) {
	beginner, ok := db.db.(txBeginner)
	if !ok {
		return nil, errors.New("counted database does not support transactions")
	}
	return beginner.Begin(ctx)
}

var _ sqlcgen.DBTX = (*layerBatchCountingDB)(nil)
