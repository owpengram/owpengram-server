package mtprotoedge

import "sync"

// bulkRPCScheduler limits runnable file handlers before they enter the shared
// inbound worker pool. Waiters remain behind inboundRPCGate, so bulk backend
// latency cannot occupy every worker needed by bootstrap and control RPCs.
type bulkRPCScheduler struct {
	mu      sync.Mutex
	max     int
	inUse   int
	waiters []*bulkRPCLease
	closed  bool
}

type bulkRPCLease struct {
	scheduler *bulkRPCScheduler
	granted   bool
	released  bool
	notified  bool
	notify    func(bool)
}

// bulkRPCAdmission serializes the two bulk prerequisites: a request may enter
// the process-wide handler scheduler only after its logical session owns an ACK
// window credit. This prevents credit-blocked requests from parking all global
// handler slots.
type bulkRPCAdmission struct {
	mu        sync.Mutex
	scheduler *bulkRPCScheduler
	lease     *bulkRPCLease
	released  bool
}

func newBulkRPCScheduler(max int) *bulkRPCScheduler {
	if max <= 0 {
		max = 1
	}
	return &bulkRPCScheduler{max: max}
}

func (s *bulkRPCScheduler) reserve() *bulkRPCLease {
	if s == nil {
		return nil
	}
	lease := &bulkRPCLease{scheduler: s}
	s.mu.Lock()
	if s.closed {
		lease.released = true
	} else if s.inUse < s.max {
		s.inUse++
		lease.granted = true
	} else {
		s.waiters = append(s.waiters, lease)
	}
	s.mu.Unlock()
	return lease
}

func (l *bulkRPCLease) subscribe(notify func(bool)) {
	if l == nil || l.scheduler == nil || notify == nil {
		return
	}
	s := l.scheduler
	s.mu.Lock()
	l.notify = notify
	granted, released := l.granted, l.released
	shouldNotify := (granted || released) && !l.notified
	if shouldNotify {
		l.notified = true
	}
	s.mu.Unlock()
	if !shouldNotify {
		return
	}
	if granted {
		notify(true)
	} else {
		notify(false)
	}
}

func (l *bulkRPCLease) release() {
	if l == nil || l.scheduler == nil {
		return
	}
	s := l.scheduler
	var notifications []func(bool)
	s.mu.Lock()
	if l.released {
		s.mu.Unlock()
		return
	}
	l.released = true
	if l.granted {
		l.granted = false
		s.inUse--
	}
	for !s.closed && s.inUse < s.max && len(s.waiters) > 0 {
		next := s.waiters[0]
		s.waiters[0] = nil
		s.waiters = s.waiters[1:]
		if next == nil || next.released {
			continue
		}
		next.granted = true
		s.inUse++
		if next.notify != nil && !next.notified {
			next.notified = true
			notifications = append(notifications, next.notify)
		}
	}
	s.mu.Unlock()
	for _, notify := range notifications {
		notify(true)
	}
}

func newBulkRPCAdmission(scheduler *bulkRPCScheduler) *bulkRPCAdmission {
	if scheduler == nil {
		return nil
	}
	return &bulkRPCAdmission{scheduler: scheduler}
}

func (a *bulkRPCAdmission) subscribeAfter(credit *outboundBulkCredit, notify func(bool)) {
	if a == nil || a.scheduler == nil || credit == nil || notify == nil {
		if notify != nil {
			notify(false)
		}
		return
	}
	credit.subscribe(func(success bool) {
		if !success {
			notify(false)
			return
		}
		lease := a.scheduler.reserve()
		a.mu.Lock()
		if a.released {
			a.mu.Unlock()
			lease.release()
			notify(false)
			return
		}
		a.lease = lease
		a.mu.Unlock()
		lease.subscribe(notify)
	})
}

func (a *bulkRPCAdmission) release() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.released {
		a.mu.Unlock()
		return
	}
	a.released = true
	lease := a.lease
	a.lease = nil
	a.mu.Unlock()
	lease.release()
}

func (s *bulkRPCScheduler) close() {
	if s == nil {
		return
	}
	var notifications []func(bool)
	s.mu.Lock()
	s.closed = true
	for _, lease := range s.waiters {
		if lease == nil || lease.released {
			continue
		}
		lease.released = true
		if lease.notify != nil && !lease.notified {
			lease.notified = true
			notifications = append(notifications, lease.notify)
		}
	}
	s.waiters = nil
	s.mu.Unlock()
	for _, notify := range notifications {
		notify(false)
	}
}
