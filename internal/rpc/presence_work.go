package rpc

// PresenceWorkSnapshot measures pending grace callbacks and their last-seen
// writers, including online last-seen work in the shared batch. It is not a
// durability receipt or an inventory of all router background work.
type PresenceWorkSnapshot struct {
	WaitingTimers    int64
	RunningCallbacks int64
	DirectWrites     int64
	PendingLastSeen  int64
}

// PresenceWorkSnapshot is bounded and identity-free. Holding the tracker lock
// across the batch read prevents a false zero at callback -> batch handoff:
// submission precedes the callback's locked running-count decrement.
func (r *Router) PresenceWorkSnapshot() PresenceWorkSnapshot {
	if r == nil || r.presence == nil {
		return PresenceWorkSnapshot{}
	}
	p := r.presence
	p.mu.RLock()
	defer p.mu.RUnlock()
	s := PresenceWorkSnapshot{
		WaitingTimers:    int64(len(p.offlineTimers)),
		RunningCallbacks: p.offlineRunning,
		DirectWrites:     p.lastSeenDirectRunning,
	}
	if r.lastSeenBatch != nil {
		s.PendingLastSeen = r.lastSeenBatch.pending.Load()
	}
	return s
}

func (p *presenceTracker) changeDirectLastSeenRunning(delta int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.lastSeenDirectRunning += delta
	p.mu.Unlock()
}
