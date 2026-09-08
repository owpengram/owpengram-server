package rpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

func waitPresenceWork(t *testing.T, r *Router, want PresenceWorkSnapshot) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		got := r.PresenceWorkSnapshot()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("presence work = %+v, want %+v", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPresenceWorkRunningCallbackSurvivesReplacementAndCancel(t *testing.T) {
	r := &Router{presence: newPresenceTracker()}
	started, release := make(chan struct{}), make(chan struct{})
	r.presence.armOfflineTimer(7, 0, func() { close(started); <-release })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("callback did not start")
	}
	defer func() {
		close(release)
		waitPresenceWork(t, r, PresenceWorkSnapshot{})
	}()
	waitPresenceWork(t, r, PresenceWorkSnapshot{RunningCallbacks: 1})
	r.presence.armOfflineTimer(7, time.Hour, func() { t.Error("canceled replacement fired") })
	waitPresenceWork(t, r, PresenceWorkSnapshot{WaitingTimers: 1, RunningCallbacks: 1})
	r.presence.cancelOfflineTimer(7)
	waitPresenceWork(t, r, PresenceWorkSnapshot{RunningCallbacks: 1})
}

func TestPresenceWorkWaitingCancellationAndOtherDevice(t *testing.T) {
	r := &Router{presence: newPresenceTracker()}
	key := presenceSessionKey{sessionID: 2}
	r.presence.setSessionStatus(key, 7, domain.UserStatus{Kind: domain.UserStatusOnline, Expires: 100})
	r.SessionOfflineAt([8]byte{}, 1, 7, false, 50)
	waitPresenceWork(t, r, PresenceWorkSnapshot{})
	if _, ok := r.presence.statusFor(7, 60); !ok {
		t.Fatal("departure erased the other device")
	}
	r.presence.armOfflineTimer(7, time.Hour, func() { t.Error("canceled timer fired") })
	waitPresenceWork(t, r, PresenceWorkSnapshot{WaitingTimers: 1})
	r.presence.cancelOfflineTimer(7)
	waitPresenceWork(t, r, PresenceWorkSnapshot{})
}

type heldPresenceBatch struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *heldPresenceBatch) UpdateLastSeenBatch(ctx context.Context, _ []store.UserLastSeenUpdate) error {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestPresenceWorkCallbackToBatchHasNoUnobservedHandoff(t *testing.T) {
	u := &heldPresenceBatch{started: make(chan struct{}), release: make(chan struct{})}
	metrics := &capturePresenceLastSeenMetrics{}
	d := newPresenceLastSeenBatchDispatcher(u, presenceLastSeenBatchConfig{MaxSize: 1}, zap.NewNop(), metrics)
	r := &Router{presence: newPresenceTracker(), lastSeenBatch: d}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	defer func() { close(u.release); cancel(); <-done }()
	callbackStarted, submit := make(chan struct{}), make(chan struct{})
	r.presence.armOfflineTimer(7, 0, func() {
		close(callbackStarted)
		<-submit
		if err := d.submit(store.UserLastSeenUpdate{UserID: 7, LastSeenAt: 99}); err != nil {
			t.Error(err)
		}
	})
	<-callbackStarted
	var observing atomic.Bool
	observing.Store(true)
	snapshotsDone := make(chan struct{})
	go func() {
		defer close(snapshotsDone)
		for observing.Load() {
			s := r.PresenceWorkSnapshot()
			if s.RunningCallbacks+s.PendingLastSeen != 1 && s.RunningCallbacks+s.PendingLastSeen != 2 {
				t.Errorf("unobserved callback handoff: %+v", s)
				return
			}
		}
	}()
	close(submit)
	select {
	case <-u.started:
	case <-time.After(time.Second):
		t.Fatal("batch did not begin")
	}
	waitPresenceWork(t, r, PresenceWorkSnapshot{PendingLastSeen: 1})
	observing.Store(false)
	<-snapshotsDone
	if d.pending.Load() != metrics.pending.Load() || metrics.pending.Load() != 1 {
		t.Fatal("snapshot and exported batch pending disagree")
	}
}

type heldDirectLastSeen struct{ started, release chan struct{} }

func (u heldDirectLastSeen) UpdateLastSeen(ctx context.Context, _ int64, _ int) error {
	close(u.started)
	select {
	case <-u.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestPresenceWorkDirectWriteRemainsVisibleAfterParentReturns(t *testing.T) {
	u := heldDirectLastSeen{make(chan struct{}), make(chan struct{})}
	r := &Router{presence: newPresenceTracker(), log: zap.NewNop()}
	r.persistReservedLastSeenAsync(context.Background(), u, 7, 99)
	<-u.started
	waitPresenceWork(t, r, PresenceWorkSnapshot{DirectWrites: 1})
	close(u.release)
	waitPresenceWork(t, r, PresenceWorkSnapshot{})
}

func TestPresenceWorkConcurrentArmCancel(t *testing.T) {
	p := newPresenceTracker()
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Go(func() {
			for j := 0; j < 100; j++ {
				p.armOfflineTimer(int64(j%4+1), time.Hour, func() { t.Error("canceled timer fired") })
				p.cancelOfflineTimer(int64(j%4 + 1))
			}
		})
	}
	workers.Wait()
	for user := int64(1); user <= 4; user++ {
		p.cancelOfflineTimer(user)
	}
	waitPresenceWork(t, &Router{presence: p}, PresenceWorkSnapshot{})
}

func TestPresenceWorkBatchRetryDrainAndOverflowAccounting(t *testing.T) {
	u := &capturePresenceLastSeenUpdater{failFirst: 1, called: make(chan struct{}, 8)}
	m := &capturePresenceLastSeenMetrics{}
	d := newPresenceLastSeenBatchDispatcher(u, presenceLastSeenBatchConfig{MaxSize: 1, QueueSize: 1, DrainTimeout: time.Second}, zap.NewNop(), m)
	if err := d.submit(store.UserLastSeenUpdate{UserID: 7, LastSeenAt: 99}); err != nil {
		t.Fatal(err)
	}
	if err := d.submit(store.UserLastSeenUpdate{UserID: 8, LastSeenAt: 100}); !errors.Is(err, errPresenceLastSeenBatchFull) {
		t.Fatalf("overflow = %v", err)
	}
	if d.pending.Load() != 1 || m.pending.Load() != 1 {
		t.Fatal("rejected work changed pending")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.Run(ctx)
	if d.pending.Load() != 0 || m.pending.Load() != 0 || m.failures.Load() != 1 || m.dropped.Load() != 0 {
		t.Fatalf("drain accounting: pending=%d metric=%d failures=%d dropped=%d", d.pending.Load(), m.pending.Load(), m.failures.Load(), m.dropped.Load())
	}
}

func TestPresenceWorkFailedDrainIsCountedNotSuccessful(t *testing.T) {
	u := &capturePresenceLastSeenUpdater{failFirst: 1000, called: make(chan struct{}, 8)}
	m := &capturePresenceLastSeenMetrics{}
	d := newPresenceLastSeenBatchDispatcher(u, presenceLastSeenBatchConfig{MaxSize: 1, DrainTimeout: 10 * time.Millisecond}, zap.NewNop(), m)
	if err := d.submit(store.UserLastSeenUpdate{UserID: 7, LastSeenAt: 99}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.Run(ctx)
	if d.pending.Load() != 0 || m.pending.Load() != 0 || m.dropped.Load() != 1 || m.failures.Load() < 1 {
		t.Fatalf("failed drain accounting: pending=%d metric=%d failures=%d dropped=%d", d.pending.Load(), m.pending.Load(), m.failures.Load(), m.dropped.Load())
	}
}
