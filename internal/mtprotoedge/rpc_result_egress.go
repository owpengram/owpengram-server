package mtprotoedge

import (
	"context"
	"errors"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iamxvbaba/td/bin"
	"github.com/iamxvbaba/td/proto"
)

const (
	rpcResultGZIPMinBytes            = 4 << 10
	rpcResultGZIPMaxInputBytes       = (10 << 20) - 1 // gotd client decompression hard limit.
	rpcResultGZIPMinSavedBytes       = 1 << 10
	rpcResultGZIPMinSavedDivisor     = 12 // Require roughly 8.3% reduction.
	rpcResultGZIPConcurrency         = 8
	defaultRPCDeliveryHookWorkers    = 32
	defaultRPCDeliveryHookMaxPending = 16_384
)

var rpcResultGZIPSlots = make(chan struct{}, rpcResultGZIPConcurrency)

// This executor is only a compatibility boundary for directly constructed
// Conn/encoded-message tests. Production Conns always use their owning Server's
// isolated executor.
var defaultRPCDeliveryHookExecutor = newRPCDeliveryHookExecutor(
	defaultRPCDeliveryHookWorkers,
	defaultRPCDeliveryHookMaxPending,
)

// ErrRPCDeliveryHookCapacity means an RPC result with a delivery-dependent
// transition cannot reserve reliable executor capacity. The result must not be
// written: its connection is fenced and the immutable result stays replayable.
var ErrRPCDeliveryHookCapacity = errors.New("mtproto rpc delivery hook capacity exhausted")

type rpcDeliveryHookTicketState uint32

const (
	rpcDeliveryHookTicketReserved rpcDeliveryHookTicketState = iota + 1
	rpcDeliveryHookTicketQueued
	rpcDeliveryHookTicketReleased
	rpcDeliveryHookTicketDone
)

type rpcDeliveryHookTicket struct {
	executor *rpcDeliveryHookExecutor
	state    atomic.Uint32
	job      rpcDeliveryHookJob
}

type rpcDeliveryHookJob struct {
	next   *rpcDeliveryHookJob
	ticket *rpcDeliveryHookTicket
	fn     func()
}

// rpcDeliveryHookExecutor is owned by one Server. Capacity bounds reserved plus
// queued plus running hooks; every physical write reserves a ticket before
// admission. A successful writer therefore performs only one short O(1) queue
// append and never waits for capacity or hook work. Failed writes release their
// ticket, while the shared logical coordinator remains eligible for a later
// replay.
type rpcDeliveryHookExecutor struct {
	workers  int
	capacity int
	slots    chan struct{}
	start    sync.Once
	wg       sync.WaitGroup

	mu       sync.Mutex
	cond     *sync.Cond
	head     *rpcDeliveryHookJob
	tail     *rpcDeliveryHookJob
	stopping bool

	queued        atomic.Int64
	running       atomic.Int64
	completed     atomic.Uint64
	rejected      atomic.Uint64
	panics        atomic.Uint64
	durationNanos atomic.Uint64
}

type rpcDeliveryHookRuntimeSnapshot struct {
	workers         int64
	capacity        int64
	reserved        int64
	queued          int64
	running         int64
	completed       uint64
	rejected        uint64
	panics          uint64
	durationSeconds float64
}

func newRPCDeliveryHookExecutor(workers, capacity int) *rpcDeliveryHookExecutor {
	if workers <= 0 {
		workers = 1
	}
	if capacity < workers {
		capacity = workers
	}
	e := &rpcDeliveryHookExecutor{
		workers:  workers,
		capacity: capacity,
		slots:    make(chan struct{}, capacity),
	}
	e.cond = sync.NewCond(&e.mu)
	return e
}

func (e *rpcDeliveryHookExecutor) startWorkers() {
	if e == nil {
		return
	}
	e.start.Do(func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.stopping {
			return
		}
		e.wg.Add(e.workers)
		for range e.workers {
			go e.run()
		}
	})
}

func (e *rpcDeliveryHookExecutor) reserve() (*rpcDeliveryHookTicket, bool) {
	if e == nil {
		return nil, false
	}
	e.startWorkers()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping {
		e.rejected.Add(1)
		return nil, false
	}
	select {
	case e.slots <- struct{}{}:
		ticket := &rpcDeliveryHookTicket{executor: e}
		ticket.state.Store(uint32(rpcDeliveryHookTicketReserved))
		return ticket, true
	default:
		e.rejected.Add(1)
		return nil, false
	}
}

func (t *rpcDeliveryHookTicket) release() {
	if t == nil || t.executor == nil || !t.state.CompareAndSwap(
		uint32(rpcDeliveryHookTicketReserved), uint32(rpcDeliveryHookTicketReleased),
	) {
		return
	}
	<-t.executor.slots
	t.executor.signalStateChange()
}

func (t *rpcDeliveryHookTicket) submit(fn func()) bool {
	if t == nil || t.executor == nil || fn == nil || !t.state.CompareAndSwap(
		uint32(rpcDeliveryHookTicketReserved), uint32(rpcDeliveryHookTicketQueued),
	) {
		return false
	}
	t.job.ticket = t
	t.job.fn = fn
	t.executor.enqueue(&t.job)
	return true
}

func (e *rpcDeliveryHookExecutor) enqueue(job *rpcDeliveryHookJob) {
	e.mu.Lock()
	if e.tail == nil {
		e.head = job
	} else {
		e.tail.next = job
	}
	e.tail = job
	e.queued.Add(1)
	e.cond.Signal()
	e.mu.Unlock()
}

func (e *rpcDeliveryHookExecutor) run() {
	defer e.wg.Done()
	for {
		e.mu.Lock()
		for e.head == nil {
			if e.stopping && len(e.slots) == 0 {
				e.mu.Unlock()
				return
			}
			e.cond.Wait()
		}
		job := e.head
		e.head = job.next
		if e.head == nil {
			e.tail = nil
		}
		job.next = nil
		e.queued.Add(-1)
		e.running.Add(1)
		e.mu.Unlock()
		e.runOne(job)
	}
}

func (e *rpcDeliveryHookExecutor) runOne(job *rpcDeliveryHookJob) {
	started := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			e.panics.Add(1)
			log.Printf("mtprotoedge: rpc delivery hook panic: %v\n%s", recovered, debug.Stack())
		}
		e.durationNanos.Add(uint64(time.Since(started)))
		e.completed.Add(1)
		e.running.Add(-1)
		if job != nil && job.ticket != nil {
			job.ticket.state.Store(uint32(rpcDeliveryHookTicketDone))
			<-e.slots
		}
		e.signalStateChange()
	}()
	if job != nil && job.fn != nil {
		job.fn()
	}
}

func (e *rpcDeliveryHookExecutor) signalStateChange() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.cond.Broadcast()
	e.mu.Unlock()
}

// stop rejects new reservations and lets every already-reserved ticket either
// be released or submitted and executed. Timing out never abandons jobs: the
// existing workers continue draining under their Server-owned executor.
func (e *rpcDeliveryHookExecutor) stop(timeout time.Duration) bool {
	if e == nil {
		return true
	}
	e.mu.Lock()
	e.stopping = true
	e.cond.Broadcast()
	e.mu.Unlock()
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	if timeout <= 0 {
		<-done
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (e *rpcDeliveryHookExecutor) runtimeSnapshot() rpcDeliveryHookRuntimeSnapshot {
	if e == nil {
		return rpcDeliveryHookRuntimeSnapshot{}
	}
	return rpcDeliveryHookRuntimeSnapshot{
		workers:         int64(e.workers),
		capacity:        int64(e.capacity),
		reserved:        int64(len(e.slots)),
		queued:          e.queued.Load(),
		running:         e.running.Load(),
		completed:       e.completed.Load(),
		rejected:        e.rejected.Load(),
		panics:          e.panics.Load(),
		durationSeconds: float64(e.durationNanos.Load()) / float64(time.Second),
	}
}

// encodeAdaptiveRPCResultInner returns either the original layer-specific TL
// object or one complete gzip_packed object. Compression is CPU bounded and is
// retained only when it materially reduces the non-preemptible transport frame.
func encodeAdaptiveRPCResultInner(ctx context.Context, stop <-chan struct{}, inner []byte) ([]byte, bool, error) {
	if len(inner) < rpcResultGZIPMinBytes || len(inner) > rpcResultGZIPMaxInputBytes {
		return inner, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case rpcResultGZIPSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-stop:
		return nil, false, ErrConnClosed
	}
	defer func() { <-rpcResultGZIPSlots }()

	var packed bin.Buffer
	if err := (proto.GZIP{Data: inner}).Encode(&packed); err != nil {
		return nil, false, err
	}
	saved := len(inner) - packed.Len()
	required := max(rpcResultGZIPMinSavedBytes, len(inner)/rpcResultGZIPMinSavedDivisor)
	if saved < required {
		return inner, false, nil
	}
	return packed.Raw(), true, nil
}

// rpcResultPriority is protocol scheduling metadata, not handler business
// behavior. Difference/state responses converge the update state, while the
// dialogs+pinned pair converges the initial chat list in both TDesktop and
// Android. These bootstrap barriers must pass background prefetch regardless of
// platform or their own encoded size.
func rpcResultPriority(method string, encoded *encodedOutboundMessage) outboundPriority {
	base := method
	if i := strings.IndexByte(base, '#'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "updates.getDifference", "updates.getChannelDifference", "updates.getState",
		"messages.getDialogs", "messages.getPinnedDialogs":
		return outboundPriorityCritical
	}
	return classifyOutboundPriority(encoded, false)
}

func (p outboundPriority) String() string {
	switch p {
	case outboundPriorityCritical:
		return "convergence"
	case outboundPriorityBulk:
		return "bulk"
	case outboundPriorityControl:
		return "control"
	default:
		return "normal"
	}
}
