package rpc

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestPresenceOfflineExpiredCallbackCannotEraseReplacement(t *testing.T) {
	p := newPresenceTracker()
	var fired atomic.Int64
	p.armOfflineTimer(7, 10*time.Millisecond, func() { fired.Add(1) })
	p.mu.Lock()
	old := p.offlineTimers[7]
	// The real AfterFunc has expired and is waiting for the tracker lock.
	time.Sleep(100 * time.Millisecond)
	if old.Stop() {
		p.mu.Unlock()
		t.Fatal("old callback has not started")
	}
	// Reproduce the replacement critical section before the old callback
	// reacquires the lock. No scheduler timing or production test hook.
	replacement := time.AfterFunc(time.Hour, func() { fired.Add(100) })
	defer replacement.Stop()
	p.offlineTimers[7] = replacement
	p.mu.Unlock()
	// Synchronize on the callback lock acquisition without holding it.
	time.Sleep(20 * time.Millisecond)
	p.mu.RLock()
	current := p.offlineTimers[7]
	p.mu.RUnlock()
	if current != replacement || fired.Load() != 0 {
		t.Fatalf("stale callback: replacement retained=%v, business callbacks=%d", current == replacement, fired.Load())
	}
	p.cancelOfflineTimer(7)
	if replacement.Stop() {
		t.Fatal("replacement was no longer cancelable through the tracker")
	}
}
