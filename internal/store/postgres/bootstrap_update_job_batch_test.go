package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func TestSelectDistinctBootstrapReadyBatchDefersSameFence(t *testing.T) {
	first := bootstrapReadyBatchRequest{userID: 1, authKeyID: [8]byte{1}, sessionID: 10}
	duplicate := bootstrapReadyBatchRequest{userID: 1, authKeyID: [8]byte{1}, sessionID: 11}
	other := bootstrapReadyBatchRequest{userID: 1, authKeyID: [8]byte{2}, sessionID: 12}
	batch, remaining := selectDistinctBootstrapReadyBatch(
		[]bootstrapReadyBatchRequest{first, duplicate, other},
		3,
	)
	if len(batch) != 2 || batch[0].sessionID != 10 || batch[1].sessionID != 12 {
		t.Fatalf("batch = %#v", batch)
	}
	if len(remaining) != 1 || remaining[0].sessionID != 11 {
		t.Fatalf("remaining = %#v", remaining)
	}
}

func TestBatchedBootstrapUpdateJobStoreCoalescesSynchronousSelectors(t *testing.T) {
	const count = 16
	backend := &fakeBootstrapReadyBackend{}
	batcher, err := newBatchedBootstrapUpdateJobStore(backend, BootstrapReadyBatchConfig{
		MaxSize: count, MaxWait: 100 * time.Millisecond,
		QueueSize: count * 2, QueryTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(batcher.Close)

	start := make(chan struct{})
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for index := 0; index < count; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			matched, err := batcher.MarkReadyForSession(
				context.Background(), int64(index+1), [8]byte{byte(index + 1)}, int64(index+100),
			)
			if err != nil {
				errs <- err
			} else if matched != 0 {
				errs <- errors.New("unexpected bootstrap readiness match")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if calls := backend.calls.Load(); calls != 1 {
		t.Fatalf("batch calls = %d, want 1", calls)
	}
	if inputs := backend.inputs.Load(); inputs != count {
		t.Fatalf("batch inputs = %d, want %d", inputs, count)
	}
}

func TestBatchedBootstrapUpdateJobStoreCapacityAndShutdownAreExplicit(t *testing.T) {
	started := make(chan struct{})
	backend := &fakeBootstrapReadyBackend{started: started, block: true}
	metrics := &fakeBootstrapReadyMetrics{}
	batcher, err := newBatchedBootstrapUpdateJobStore(backend, BootstrapReadyBatchConfig{
		MaxSize: 1, MaxWait: time.Millisecond, QueueSize: 1, QueryTimeout: time.Second, Metrics: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}

	results := make(chan error, 2)
	go func() {
		_, err := batcher.MarkReadyForSession(context.Background(), 1, [8]byte{1}, 1)
		results <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first batch did not start")
	}
	go func() {
		_, err := batcher.MarkReadyForSession(context.Background(), 2, [8]byte{2}, 2)
		results <- err
	}()
	deadline := time.Now().Add(time.Second)
	for metrics.pending.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if metrics.pending.Load() != 2 {
		t.Fatalf("pending = %d, want 2", metrics.pending.Load())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := batcher.MarkReadyForSession(ctx, 3, [8]byte{3}, 3); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capacity wait err = %v, want deadline exceeded", err)
	}
	batcher.Close()
	for range 2 {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown result = %v, want canceled", err)
		}
	}
	if pending := metrics.pending.Load(); pending != 0 {
		t.Fatalf("pending after shutdown = %d", pending)
	}
	if _, err := batcher.MarkReadyForSession(context.Background(), 4, [8]byte{4}, 4); !errors.Is(err, context.Canceled) {
		t.Fatalf("mark after close err = %v, want canceled", err)
	}
}

type fakeBootstrapReadyBackend struct {
	calls   atomic.Int64
	inputs  atomic.Int64
	started chan struct{}
	block   bool
	once    sync.Once
}

func (s *fakeBootstrapReadyBackend) markReadyForSessions(ctx context.Context, requests []bootstrapReadyBatchRequest) ([]int, error) {
	s.calls.Add(1)
	s.inputs.Add(int64(len(requests)))
	if s.started != nil {
		s.once.Do(func() { close(s.started) })
	}
	if s.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return make([]int, len(requests)), nil
}

func (*fakeBootstrapReadyBackend) EnqueueLoginMessage(context.Context, domain.BootstrapUpdateJob) (domain.BootstrapUpdateJob, error) {
	return domain.BootstrapUpdateJob{}, nil
}

func (*fakeBootstrapReadyBackend) MarkReadyForSession(context.Context, int64, [8]byte, int64) (int, error) {
	return 0, nil
}

func (*fakeBootstrapReadyBackend) ClaimReady(context.Context, int, time.Duration) ([]domain.BootstrapUpdateJob, error) {
	return nil, nil
}

func (*fakeBootstrapReadyBackend) MarkPublished(context.Context, int64) error { return nil }

func (*fakeBootstrapReadyBackend) MarkFailed(context.Context, int64, string) error { return nil }

type fakeBootstrapReadyMetrics struct {
	pending atomic.Int64
}

func (*fakeBootstrapReadyMetrics) BootstrapReadyBatch(int, int, time.Duration, error) {}

func (m *fakeBootstrapReadyMetrics) BootstrapReadyPending(delta int) {
	m.pending.Add(int64(delta))
}
