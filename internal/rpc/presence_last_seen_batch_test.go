package rpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"telesrv/internal/store"
)

type capturePresenceLastSeenUpdater struct {
	mu        sync.Mutex
	calls     [][]store.UserLastSeenUpdate
	failFirst int
	called    chan struct{}
}

func (u *capturePresenceLastSeenUpdater) UpdateLastSeenBatch(_ context.Context, updates []store.UserLastSeenUpdate) error {
	u.mu.Lock()
	u.calls = append(u.calls, append([]store.UserLastSeenUpdate(nil), updates...))
	fail := u.failFirst > 0
	if fail {
		u.failFirst--
	}
	u.mu.Unlock()
	select {
	case u.called <- struct{}{}:
	default:
	}
	if fail {
		return errors.New("temporary batch failure")
	}
	return nil
}

func (u *capturePresenceLastSeenUpdater) snapshot() [][]store.UserLastSeenUpdate {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([][]store.UserLastSeenUpdate, len(u.calls))
	for index := range u.calls {
		out[index] = append([]store.UserLastSeenUpdate(nil), u.calls[index]...)
	}
	return out
}

type capturePresenceLastSeenMetrics struct {
	NopMetrics
	batches   atomic.Int64
	failures  atomic.Int64
	submitted atomic.Int64
	pending   atomic.Int64
	overflow  atomic.Int64
	dropped   atomic.Int64
}

func (m *capturePresenceLastSeenMetrics) PresenceLastSeenBatch(_ int, _ time.Duration, err error) {
	m.batches.Add(1)
	if err != nil {
		m.failures.Add(1)
	}
}

func (m *capturePresenceLastSeenMetrics) PresenceLastSeenOverflow() {
	m.overflow.Add(1)
}

func (m *capturePresenceLastSeenMetrics) PresenceLastSeenSubmitted() {
	m.submitted.Add(1)
}

func (m *capturePresenceLastSeenMetrics) PresenceLastSeenPending(delta int) {
	m.pending.Add(int64(delta))
}

func (m *capturePresenceLastSeenMetrics) PresenceLastSeenDrainDropped(count int) {
	m.dropped.Add(int64(count))
}

func TestPresenceLastSeenBatchCoalescesMaximumTimestamp(t *testing.T) {
	updater := &capturePresenceLastSeenUpdater{called: make(chan struct{}, 4)}
	d := newPresenceLastSeenBatchDispatcher(updater, presenceLastSeenBatchConfig{
		MaxSize: 16, MaxWait: 10 * time.Millisecond, QueueSize: 32,
		QueryTimeout: time.Second, DrainTimeout: time.Second,
	}, zaptest.NewLogger(t), NopMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()

	for _, update := range []store.UserLastSeenUpdate{
		{UserID: 2, LastSeenAt: 10},
		{UserID: 1, LastSeenAt: 5},
		{UserID: 1, LastSeenAt: 12},
	} {
		if err := d.submit(update); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	select {
	case <-updater.called:
	case <-time.After(time.Second):
		t.Fatal("batch was not executed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not stop")
	}

	calls := updater.snapshot()
	if len(calls) != 1 {
		t.Fatalf("batch calls = %d, want 1", len(calls))
	}
	want := []store.UserLastSeenUpdate{{UserID: 1, LastSeenAt: 12}, {UserID: 2, LastSeenAt: 10}}
	if len(calls[0]) != len(want) {
		t.Fatalf("batch = %#v, want %#v", calls[0], want)
	}
	for index := range want {
		if calls[0][index] != want[index] {
			t.Fatalf("batch[%d] = %#v, want %#v", index, calls[0][index], want[index])
		}
	}
}

func TestPresenceLastSeenBatchRetriesAcceptedWork(t *testing.T) {
	updater := &capturePresenceLastSeenUpdater{failFirst: 1, called: make(chan struct{}, 4)}
	metrics := &capturePresenceLastSeenMetrics{}
	d := newPresenceLastSeenBatchDispatcher(updater, presenceLastSeenBatchConfig{
		MaxSize: 8, MaxWait: time.Millisecond, QueueSize: 8,
		QueryTimeout: time.Second, DrainTimeout: time.Second,
	}, zaptest.NewLogger(t), metrics)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	if err := d.submit(store.UserLastSeenUpdate{UserID: 7, LastSeenAt: 19}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	for calls := 0; calls < 2; calls++ {
		select {
		case <-updater.called:
		case <-time.After(time.Second):
			t.Fatal("retry was not executed")
		}
	}
	cancel()
	<-done
	if got := len(updater.snapshot()); got != 2 {
		t.Fatalf("batch calls = %d, want failed attempt + retry", got)
	}
	if metrics.batches.Load() != 2 || metrics.failures.Load() != 1 {
		t.Fatalf("metrics batches/failures = %d/%d, want 2/1", metrics.batches.Load(), metrics.failures.Load())
	}
	if metrics.pending.Load() != 0 {
		t.Fatalf("pending metric = %d, want drained", metrics.pending.Load())
	}
	if metrics.submitted.Load() != 1 {
		t.Fatalf("submitted metric = %d, want 1", metrics.submitted.Load())
	}
}

func TestPresenceLastSeenBatchCapacityIsExplicit(t *testing.T) {
	updater := &capturePresenceLastSeenUpdater{called: make(chan struct{}, 1)}
	metrics := &capturePresenceLastSeenMetrics{}
	d := newPresenceLastSeenBatchDispatcher(updater, presenceLastSeenBatchConfig{
		MaxSize: 1, MaxWait: time.Second, QueueSize: 1,
		QueryTimeout: time.Second, DrainTimeout: time.Second,
	}, zaptest.NewLogger(t), metrics)
	if err := d.submit(store.UserLastSeenUpdate{UserID: 1, LastSeenAt: 1}); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if err := d.submit(store.UserLastSeenUpdate{UserID: 2, LastSeenAt: 2}); !errors.Is(err, errPresenceLastSeenBatchFull) {
		t.Fatalf("second submit error = %v, want capacity error", err)
	}
	if metrics.overflow.Load() != 1 {
		t.Fatalf("overflow metric = %d, want 1", metrics.overflow.Load())
	}
	if metrics.pending.Load() != 1 {
		t.Fatalf("pending metric = %d, want only accepted first update", metrics.pending.Load())
	}
	if metrics.submitted.Load() != 1 {
		t.Fatalf("submitted metric = %d, want only accepted first update", metrics.submitted.Load())
	}
}

func TestPresenceLastSeenBatchShutdownDrainsAcceptedWork(t *testing.T) {
	updater := &capturePresenceLastSeenUpdater{called: make(chan struct{}, 4)}
	d := newPresenceLastSeenBatchDispatcher(updater, presenceLastSeenBatchConfig{
		MaxSize: 16, MaxWait: time.Second, QueueSize: 16,
		QueryTimeout: time.Second, DrainTimeout: time.Second,
	}, zaptest.NewLogger(t), NopMetrics{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	if err := d.submit(store.UserLastSeenUpdate{UserID: 11, LastSeenAt: 21}); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	if err := d.submit(store.UserLastSeenUpdate{UserID: 12, LastSeenAt: 22}); err != nil {
		t.Fatalf("submit second: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher did not finish shutdown drain")
	}
	seen := map[int64]int{}
	for _, call := range updater.snapshot() {
		for _, update := range call {
			seen[update.UserID] = update.LastSeenAt
		}
	}
	if seen[11] != 21 || seen[12] != 22 {
		t.Fatalf("drained updates = %v, want both accepted updates", seen)
	}
	if err := d.submit(store.UserLastSeenUpdate{UserID: 13, LastSeenAt: 23}); !errors.Is(err, errPresenceLastSeenBatchStopped) {
		t.Fatalf("post-stop submit error = %v, want stopped", err)
	}
}
