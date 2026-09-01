package postgres

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSelectDistinctActiveChannelIDsBatchDefersDuplicate(t *testing.T) {
	selector := activeChannelIDsSelector{userID: 1, limit: 1000}
	first := activeChannelIDsBatchRequest{selector: selector}
	duplicate := activeChannelIDsBatchRequest{selector: selector}
	other := activeChannelIDsBatchRequest{selector: activeChannelIDsSelector{userID: 2, limit: 1000}}
	batch, remaining := selectDistinctActiveChannelIDsBatch([]activeChannelIDsBatchRequest{first, duplicate, other}, 3)
	if len(batch) != 2 || batch[0].selector.userID != 1 || batch[1].selector.userID != 2 {
		t.Fatalf("batch = %#v", batch)
	}
	if len(remaining) != 1 || remaining[0].selector != selector {
		t.Fatalf("remaining = %#v", remaining)
	}
}

func TestActiveChannelIDsPageBatcherCoalescesSelectors(t *testing.T) {
	const count = 32
	backend := &fakeActiveChannelIDsBatchBackend{}
	batcher, err := newActiveChannelIDsPageBatcher(backend, ActiveChannelIDsBatchConfig{
		MaxSize: count, MaxWait: 100 * time.Millisecond, QueueSize: count * 2, QueryTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(batcher.Close)
	start := make(chan struct{})
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for index := range count {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := batcher.ListActiveChannelIDsForUser(context.Background(), int64(index+1), 0, 1000)
			if err != nil {
				errs <- err
			} else if !slices.Equal(got, []int64{int64(index + 1)}) {
				errs <- errors.New("unexpected active channel IDs page")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if backend.calls.Load() != 1 || backend.inputs.Load() != count {
		t.Fatalf("backend calls=%d inputs=%d", backend.calls.Load(), backend.inputs.Load())
	}
}

func TestActiveChannelIDsPageBatcherCapacityAndShutdownAreExplicit(t *testing.T) {
	started := make(chan struct{})
	backend := &fakeActiveChannelIDsBatchBackend{started: started, block: true}
	metrics := &fakeActiveChannelIDsBatchMetrics{}
	batcher, err := newActiveChannelIDsPageBatcher(backend, ActiveChannelIDsBatchConfig{
		MaxSize: 1, MaxWait: time.Millisecond, QueueSize: 1, QueryTimeout: time.Second, Metrics: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() {
		_, err := batcher.ListActiveChannelIDsForUser(context.Background(), 1, 0, 1000)
		results <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first batch did not start")
	}
	go func() {
		_, err := batcher.ListActiveChannelIDsForUser(context.Background(), 2, 0, 1000)
		results <- err
	}()
	deadline := time.Now().Add(time.Second)
	for metrics.pending.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := batcher.ListActiveChannelIDsForUser(ctx, 3, 0, 1000); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capacity wait err = %v", err)
	}
	batcher.Close()
	for range 2 {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown result = %v", err)
		}
	}
	if metrics.pending.Load() != 0 {
		t.Fatalf("pending = %d", metrics.pending.Load())
	}
	if _, err := batcher.ListActiveChannelIDsForUser(context.Background(), 4, 0, 1000); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-close err = %v", err)
	}
}

type fakeActiveChannelIDsBatchBackend struct {
	calls   atomic.Int64
	inputs  atomic.Int64
	started chan struct{}
	block   bool
	once    sync.Once
}

func (f *fakeActiveChannelIDsBatchBackend) listActiveChannelIDPages(
	ctx context.Context,
	selectors []activeChannelIDsSelector,
) ([][]int64, error) {
	f.calls.Add(1)
	f.inputs.Add(int64(len(selectors)))
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	pages := make([][]int64, len(selectors))
	for index, selector := range selectors {
		pages[index] = []int64{selector.userID}
	}
	return pages, nil
}

type fakeActiveChannelIDsBatchMetrics struct {
	pending atomic.Int64
}

func (*fakeActiveChannelIDsBatchMetrics) ActiveChannelIDsBatch(int, int, time.Duration, error) {}

func (m *fakeActiveChannelIDsBatchMetrics) ActiveChannelIDsPending(delta int) {
	m.pending.Add(int64(delta))
}
