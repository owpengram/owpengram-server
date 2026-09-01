package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"telesrv/internal/domain"
)

type countingReadModelVersionBase struct {
	mu    sync.Mutex
	calls int
	keys  int
}

func (b *countingReadModelVersionBase) ReadModelHash(
	ctx context.Context,
	model string,
	ownerUserID int64,
	peerType domain.PeerType,
	peerID int64,
) (int64, bool, error) {
	key := ReadModelKey{Model: model, OwnerUserID: ownerUserID, PeerType: peerType, PeerID: peerID}
	rows, err := b.ReadModelHashes(ctx, []ReadModelKey{key})
	return rows[key], rows[key] != 0, err
}

func (b *countingReadModelVersionBase) ReadModelHashes(
	_ context.Context,
	keys []ReadModelKey,
) (map[ReadModelKey]int64, error) {
	b.mu.Lock()
	b.calls++
	b.keys += len(keys)
	b.mu.Unlock()
	rows := make(map[ReadModelKey]int64, len(keys))
	for _, key := range keys {
		rows[key] = key.PeerID + 1000
	}
	return rows, nil
}

func (b *countingReadModelVersionBase) counts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls, b.keys
}

func TestBatchedReadModelVersionStoreCombinesConcurrentMisses(t *testing.T) {
	base := &countingReadModelVersionBase{}
	batcher, err := NewBatchedReadModelVersionStore(base, ReadModelVersionBatchConfig{
		MaxKeys: 128, MaxWait: 10 * time.Millisecond, QueueSize: 128, QueryTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(batcher.Close)

	const callers = 32
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for index := 0; index < callers; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			shared := ReadModelKey{Model: "channel_base", PeerType: domain.PeerTypeChannel, PeerID: 9}
			own := ReadModelKey{Model: "dialog_owner", OwnerUserID: int64(index + 1), PeerType: domain.PeerTypeUser, PeerID: int64(index + 1)}
			rows, readErr := batcher.ReadModelHashes(context.Background(), []ReadModelKey{shared, own})
			if readErr != nil {
				errs <- readErr
				return
			}
			if rows[shared] != 1009 || rows[own] != own.PeerID+1000 {
				errs <- context.Canceled
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	calls, keys := base.counts()
	if calls <= 0 || calls > 4 {
		t.Fatalf("base calls = %d, want 1..4", calls)
	}
	if keys < callers+1 || keys > callers+calls {
		t.Fatalf("base key inputs = %d, callers=%d calls=%d", keys, callers, calls)
	}
}

func TestBatchedReadModelVersionStoreSplitsOversizedRequest(t *testing.T) {
	base := &countingReadModelVersionBase{}
	batcher, err := NewBatchedReadModelVersionStore(base, ReadModelVersionBatchConfig{
		MaxKeys: 2, MaxWait: time.Microsecond, QueueSize: 4, QueryTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(batcher.Close)

	keys := []ReadModelKey{
		{Model: "m", PeerType: domain.PeerTypeUser, PeerID: 1},
		{Model: "m", PeerType: domain.PeerTypeUser, PeerID: 2},
		{Model: "m", PeerType: domain.PeerTypeUser, PeerID: 3},
		{Model: "m", PeerType: domain.PeerTypeUser, PeerID: 1},
	}
	rows, err := batcher.ReadModelHashes(context.Background(), keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[keys[2]] != 1003 {
		t.Fatalf("rows = %#v", rows)
	}
	calls, loaded := base.counts()
	if calls != 2 || loaded != 3 {
		t.Fatalf("base = %d calls / %d keys, want 2 / 3", calls, loaded)
	}
}

func TestBatchedReadModelVersionStoreCloseFailsNewReads(t *testing.T) {
	base := &countingReadModelVersionBase{}
	batcher, err := NewBatchedReadModelVersionStore(base, ReadModelVersionBatchConfig{
		MaxKeys: 2, MaxWait: time.Microsecond, QueueSize: 2, QueryTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	batcher.Close()
	if _, err := batcher.ReadModelHashes(context.Background(), []ReadModelKey{{Model: "m", PeerID: 1}}); err == nil {
		t.Fatal("read after close succeeded")
	}
}

func TestNewBatchedReadModelVersionStoreRejectsInvalidConfig(t *testing.T) {
	base := &countingReadModelVersionBase{}
	for _, cfg := range []ReadModelVersionBatchConfig{
		{},
		{MaxKeys: 1, MaxWait: 11 * time.Millisecond, QueueSize: 1, QueryTimeout: time.Second},
		{MaxKeys: 1, MaxWait: time.Microsecond, QueueSize: 0, QueryTimeout: time.Second},
		{MaxKeys: 1, MaxWait: time.Microsecond, QueueSize: 1, QueryTimeout: 31 * time.Second},
	} {
		if batcher, err := NewBatchedReadModelVersionStore(base, cfg); err == nil {
			batcher.Close()
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
}
