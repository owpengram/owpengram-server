package memory

import (
	"context"
	"sync"
	"testing"

	"telesrv/internal/store"
)

// fakeCounterSource is a minimal store.CounterSource double, for pinning the
// cold-start recovery behavior redisstore's Lua scripts enforced.
type fakeCounterSource struct {
	mu      sync.Mutex
	current map[int64]int
	calls   int
}

func newFakeCounterSource(seed map[int64]int) *fakeCounterSource {
	return &fakeCounterSource{current: seed}
}

func (f *fakeCounterSource) Current(_ context.Context, userID int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.current[userID], nil
}

func (f *fakeCounterSource) CurrentBatch(_ context.Context, userIDs []int64) (map[int64]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	out := make(map[int64]int, len(userIDs))
	for _, id := range userIDs {
		out[id] = f.current[id]
	}
	return out, nil
}

// TestBoxIDAllocatorRecoversFromColdStart pins the exact behavior the Redis
// Lua scripts enforced: a key that has never been allocated in this process
// recovers from the durable source and the first NextBoxID after that is
// recovered+1, not recovered or recovered+2.
func TestBoxIDAllocatorRecoversFromColdStart(t *testing.T) {
	ctx := context.Background()
	source := newFakeCounterSource(map[int64]int{7: 41})
	alloc := NewBoxIDAllocator(source)

	next, err := alloc.NextBoxID(ctx, 7)
	if err != nil {
		t.Fatalf("NextBoxID: %v", err)
	}
	if next != 42 {
		t.Fatalf("NextBoxID after cold-start recovery = %d, want 42 (recovered 41 + 1)", next)
	}
	if source.calls != 1 {
		t.Fatalf("CounterSource called %d times, want exactly 1 (only on the cold key)", source.calls)
	}

	// A second call must not recover again -- the counter is now warm.
	next2, err := alloc.NextBoxID(ctx, 7)
	if err != nil {
		t.Fatalf("NextBoxID (warm): %v", err)
	}
	if next2 != 43 {
		t.Fatalf("second NextBoxID = %d, want 43", next2)
	}
	if source.calls != 1 {
		t.Fatalf("CounterSource called again on a warm key: %d calls", source.calls)
	}
}

// TestBoxIDAllocatorCurrentDoesNotAllocate pins that CurrentBoxID recovers
// (and caches) a cold key without incrementing it -- unlike NextBoxID.
func TestBoxIDAllocatorCurrentDoesNotAllocate(t *testing.T) {
	ctx := context.Background()
	source := newFakeCounterSource(map[int64]int{7: 41})
	alloc := NewBoxIDAllocator(source)

	current, err := alloc.CurrentBoxID(ctx, 7)
	if err != nil {
		t.Fatalf("CurrentBoxID: %v", err)
	}
	if current != 41 {
		t.Fatalf("CurrentBoxID = %d, want the recovered value 41 unchanged", current)
	}
	next, err := alloc.NextBoxID(ctx, 7)
	if err != nil {
		t.Fatalf("NextBoxID: %v", err)
	}
	if next != 42 {
		t.Fatalf("NextBoxID after a prior Current = %d, want 42 -- Current must not have consumed an id", next)
	}
}

// TestBoxIDAllocatorConcurrentNextIsUniqueAndMonotonic is the concurrency
// stress test the plan called for: N goroutines racing NextBoxID for the
// same user must each get a distinct, and collectively contiguous, value.
// This is the property the Redis Lua script's atomicity gave; a mutex must
// give the identical property in one process.
func TestBoxIDAllocatorConcurrentNextIsUniqueAndMonotonic(t *testing.T) {
	ctx := context.Background()
	alloc := NewBoxIDAllocator(nil)
	const n = 500
	results := make(chan int, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			v, err := alloc.NextBoxID(ctx, 99)
			if err != nil {
				t.Errorf("NextBoxID: %v", err)
				return
			}
			results <- v
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[int]bool, n)
	for v := range results {
		if seen[v] {
			t.Fatalf("value %d allocated more than once -- allocator is not safe under concurrency", v)
		}
		seen[v] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d unique values, want %d", len(seen), n)
	}
	for v := 1; v <= n; v++ {
		if !seen[v] {
			t.Fatalf("value %d never allocated -- gap in an allocator that must never regress or skip under normal operation", v)
		}
	}
}

// TestBoxIDAllocatorNextBoxIDsBatchRecoversOnce pins that the batch path
// resolves every cold key in the batch through one CurrentBatch call, not
// one Current call per key (the property the Redis version's doc comment
// on NextBoxIDs calls out explicitly: "never degrades into per-user network
// calls").
func TestBoxIDAllocatorNextBoxIDsBatchRecoversOnce(t *testing.T) {
	ctx := context.Background()
	source := newFakeCounterSource(map[int64]int{1: 10, 2: 20, 3: 30})
	alloc := NewBoxIDAllocator(source)

	out, err := alloc.NextBoxIDs(ctx, []int64{1, 2, 3, 1}) // duplicate 1 on purpose
	if err != nil {
		t.Fatalf("NextBoxIDs: %v", err)
	}
	want := map[int64]int{1: 11, 2: 21, 3: 31}
	if len(out) != len(want) {
		t.Fatalf("NextBoxIDs returned %d entries, want %d: %+v", len(out), len(want), out)
	}
	for id, w := range want {
		if out[id] != w {
			t.Errorf("NextBoxIDs[%d] = %d, want %d", id, out[id], w)
		}
	}
	if source.calls != 1 {
		t.Fatalf("CounterSource called %d times for a 3-key cold batch, want exactly 1", source.calls)
	}
}

func TestBoxIDAllocatorRejectsInvalidUserID(t *testing.T) {
	ctx := context.Background()
	alloc := NewBoxIDAllocator(nil)
	if _, err := alloc.NextBoxID(ctx, 0); err == nil {
		t.Fatal("NextBoxID(0) succeeded, want an error")
	}
	if _, err := alloc.NextBoxIDs(ctx, []int64{1, -1}); err == nil {
		t.Fatal("NextBoxIDs with a negative id succeeded, want an error")
	}
}

func TestBoxIDAllocatorBumpAtLeastSelfHeals(t *testing.T) {
	ctx := context.Background()
	alloc := NewBoxIDAllocator(nil)
	if _, err := alloc.NextBoxID(ctx, 5); err != nil { // warms the counter to 1
		t.Fatalf("NextBoxID: %v", err)
	}
	if err := alloc.BumpBoxIDAtLeast(ctx, 5, 100); err != nil {
		t.Fatalf("BumpBoxIDAtLeast: %v", err)
	}
	next, err := alloc.NextBoxID(ctx, 5)
	if err != nil {
		t.Fatalf("NextBoxID after bump: %v", err)
	}
	if next != 101 {
		t.Fatalf("NextBoxID after BumpBoxIDAtLeast(100) = %d, want 101", next)
	}
	// A bump below the current value must be a no-op.
	if err := alloc.BumpBoxIDAtLeast(ctx, 5, 50); err != nil {
		t.Fatalf("BumpBoxIDAtLeast (below current): %v", err)
	}
	next2, err := alloc.NextBoxID(ctx, 5)
	if err != nil {
		t.Fatalf("NextBoxID: %v", err)
	}
	if next2 != 102 {
		t.Fatalf("a bump below the current value regressed the counter: got %d, want 102", next2)
	}
}

// TestBoxIDAllocatorSatisfiesDistributedCapability pins that the type
// assertion internal/store/postgres/message_send.go relies on (routing
// private sends onto the microbatch path only for a "distributed" allocator)
// still succeeds against the in-process type.
func TestBoxIDAllocatorSatisfiesDistributedCapability(t *testing.T) {
	var alloc store.BoxIDAllocator = NewBoxIDAllocator(nil)
	if _, ok := alloc.(store.DistributedBoxIDAllocator); !ok {
		t.Fatal("*BoxIDAllocator no longer satisfies store.DistributedBoxIDAllocator")
	}
}

func TestChannelIDAllocatorNextAndCurrent(t *testing.T) {
	ctx := context.Background()
	source := newFakeCounterSource(map[int64]int{1: 5})
	alloc := NewChannelIDAllocator(source)

	first, err := alloc.NextChannelID(ctx)
	if err != nil {
		t.Fatalf("NextChannelID: %v", err)
	}
	if first != 6 {
		t.Fatalf("NextChannelID = %d, want 6 (recovered 5 + 1)", first)
	}
	current, err := alloc.CurrentChannelID(ctx)
	if err != nil {
		t.Fatalf("CurrentChannelID: %v", err)
	}
	if current != 6 {
		t.Fatalf("CurrentChannelID = %d, want 6", current)
	}
}

func TestChannelIDAllocatorNextAtLeastBumpsThenAllocates(t *testing.T) {
	ctx := context.Background()
	alloc := NewChannelIDAllocator(nil)
	if _, err := alloc.NextChannelID(ctx); err != nil { // warms to 1
		t.Fatalf("NextChannelID: %v", err)
	}
	v, err := alloc.NextChannelIDAtLeast(ctx, 1000)
	if err != nil {
		t.Fatalf("NextChannelIDAtLeast: %v", err)
	}
	if v != 1001 {
		t.Fatalf("NextChannelIDAtLeast(1000) = %d, want 1001", v)
	}
}

func TestChannelMessageIDAllocatorIsPerChannel(t *testing.T) {
	ctx := context.Background()
	alloc := NewChannelMessageIDAllocator(nil)
	a1, err := alloc.NextChannelMessageID(ctx, 111)
	if err != nil {
		t.Fatalf("NextChannelMessageID: %v", err)
	}
	b1, err := alloc.NextChannelMessageID(ctx, 222)
	if err != nil {
		t.Fatalf("NextChannelMessageID: %v", err)
	}
	if a1 != 1 || b1 != 1 {
		t.Fatalf("first message id per channel = %d, %d, want 1, 1 (independent counters)", a1, b1)
	}
	a2, err := alloc.NextChannelMessageID(ctx, 111)
	if err != nil {
		t.Fatalf("NextChannelMessageID: %v", err)
	}
	if a2 != 2 {
		t.Fatalf("second message id for channel 111 = %d, want 2", a2)
	}
}
