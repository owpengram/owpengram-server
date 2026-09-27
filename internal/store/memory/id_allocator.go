package memory

import (
	"context"
	"fmt"
	"sync"

	"telesrv/internal/store"
)

// counterAllocator is the shared engine behind BoxIDAllocator,
// ChannelIDAllocator and ChannelMessageIDAllocator: a mutex-guarded
// map[int64]int64 that lazily recovers its starting value from a
// store.CounterSource (the durable PostgreSQL log) the first time a given
// key is touched.
//
// This is the direct in-process translation of redisstore's Lua scripts
// (counterNextScript / counterRecoverNextScript / counterRecoverCurrentScript
// / counterNextAtLeastScript / counterSetAtLeastScript): those existed only
// because multiple Redis clients could race on the same counter key over the
// network. In one process, holding the mutex for the whole
// read-recover-if-missing-then-increment sequence gives the identical
// atomicity guarantee with no scripting needed -- and, unlike a round trip to
// Redis, a mutex plus a map read costs nothing worth measuring.
type counterAllocator struct {
	mu     sync.Mutex
	values map[int64]int64
	source store.CounterSource
	name   string
}

func newCounterAllocator(name string, source store.CounterSource) *counterAllocator {
	return &counterAllocator{values: make(map[int64]int64), source: source, name: name}
}

// next returns the next value for key, recovering from source on a cold key.
func (a *counterAllocator) next(ctx context.Context, key int64) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, ok := a.values[key]
	if !ok {
		recovered, err := a.recoverLocked(ctx, key)
		if err != nil {
			return 0, err
		}
		current = recovered
	}
	current++
	a.values[key] = current
	return current, nil
}

// nextBatch is next for many keys in one critical section -- the in-process
// equivalent of the Redis version's one-pipeline-per-cold-batch behavior:
// every key here is resolved under a single lock acquisition rather than
// looping next() once per key, so the batch API never turns into N separate
// round trips (there are none to begin with, but the property -- one atomic
// pass over the whole batch -- is worth preserving for anyone reasoning about
// concurrent batches interleaving).
func (a *counterAllocator) nextBatch(ctx context.Context, keys []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(keys))
	a.mu.Lock()
	defer a.mu.Unlock()
	var missing []int64
	for _, key := range keys {
		if _, ok := out[key]; ok {
			continue // duplicate in the input slice
		}
		if current, ok := a.values[key]; ok {
			current++
			a.values[key] = current
			out[key] = current
			continue
		}
		missing = append(missing, key)
	}
	if len(missing) == 0 {
		return out, nil
	}
	if a.source == nil {
		return nil, fmt.Errorf("recover %s counters: missing durable source", a.name)
	}
	recovered, err := a.source.CurrentBatch(ctx, missing)
	if err != nil {
		return nil, fmt.Errorf("recover %s counters: %w", a.name, err)
	}
	for _, key := range missing {
		floor, ok := recovered[key]
		if !ok {
			return nil, fmt.Errorf("durable source omitted %s counter for %d", a.name, key)
		}
		next := int64(floor) + 1
		a.values[key] = next
		out[key] = next
	}
	return out, nil
}

// current returns key's present value without incrementing, recovering (and
// caching, without incrementing) from source on a cold key.
func (a *counterAllocator) current(ctx context.Context, key int64) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if current, ok := a.values[key]; ok {
		return current, nil
	}
	return a.recoverLocked(ctx, key)
}

// bumpAtLeast raises key's value to floor if it is missing or lower, without
// allocating a new id -- the self-heal path BumpBoxIDAtLeast/
// NextChannelIDAtLeast need when a durable write outran this allocator's
// in-memory counter (e.g. an id created directly against Postgres, bypassing
// this process entirely).
func (a *counterAllocator) bumpAtLeast(key, floor int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if current, ok := a.values[key]; !ok || current < floor {
		a.values[key] = floor
	}
}

// nextAtLeast bumps key to at least floor, then allocates the next value --
// ChannelIDAllocator.NextChannelIDAtLeast's primitive.
func (a *counterAllocator) nextAtLeast(key, floor int64) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if current, ok := a.values[key]; !ok || current < floor {
		a.values[key] = floor
	}
	a.values[key]++
	return a.values[key]
}

func (a *counterAllocator) recoverLocked(ctx context.Context, key int64) (int64, error) {
	if a.source == nil {
		a.values[key] = 0
		return 0, nil
	}
	recovered, err := a.source.Current(ctx, key)
	if err != nil {
		return 0, fmt.Errorf("recover %s counter: %w", a.name, err)
	}
	a.values[key] = int64(recovered)
	return int64(recovered), nil
}

// --- BoxIDAllocator ---------------------------------------------------

// BoxIDAllocator is the in-process replacement for
// redisstore.BoxIDAllocator.
type BoxIDAllocator struct {
	counter *counterAllocator
}

var (
	_ store.BoxIDAllocator            = (*BoxIDAllocator)(nil)
	_ store.DistributedBoxIDAllocator = (*BoxIDAllocator)(nil)
)

// NewBoxIDAllocator builds a box-id allocator recovering from source (may be
// nil, in which case cold keys start at 0).
func NewBoxIDAllocator(source store.CounterSource) *BoxIDAllocator {
	return &BoxIDAllocator{counter: newCounterAllocator("box_id", source)}
}

// DistributedBoxIDAllocation marks this allocator's per-owner reservations
// as safe for the private-send microbatch path -- true here for the same
// reason it is true for the Redis version: the whole read-recover-increment
// sequence for one key happens under one mutex critical section, so it is
// atomic with respect to every other concurrent call in this process (and
// this process is the only place that will ever call it -- see
// internal/store/allocator.go's doc comment on why no multi-process
// coordination exists in this codebase).
func (*BoxIDAllocator) DistributedBoxIDAllocation() {}

func (a *BoxIDAllocator) NextBoxID(ctx context.Context, userID int64) (int, error) {
	if userID == 0 {
		return 0, fmt.Errorf("box_id counter: missing user id")
	}
	v, err := a.counter.next(ctx, userID)
	return int(v), err
}

func (a *BoxIDAllocator) NextBoxIDs(ctx context.Context, userIDs []int64) (map[int64]int, error) {
	unique := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			return nil, fmt.Errorf("box_id counter: invalid user id %d", id)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return map[int64]int{}, nil
	}
	values, err := a.counter.nextBatch(ctx, unique)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int, len(values))
	for id, v := range values {
		out[id] = int(v)
	}
	return out, nil
}

func (a *BoxIDAllocator) CurrentBoxID(ctx context.Context, userID int64) (int, error) {
	if userID == 0 {
		return 0, fmt.Errorf("box_id counter: missing user id")
	}
	v, err := a.counter.current(ctx, userID)
	return int(v), err
}

// BumpBoxIDAtLeast mirrors redisstore.BoxIDAllocator.BumpBoxIDAtLeast --
// picked up automatically by internal/store/postgres/message_send.go's
// optional-capability type assertion, no call-site change needed.
func (a *BoxIDAllocator) BumpBoxIDAtLeast(_ context.Context, userID int64, floor int) error {
	a.counter.bumpAtLeast(userID, int64(floor))
	return nil
}

// --- ChannelIDAllocator -------------------------------------------------

// channelIDCounterKey is the single key ChannelIDAllocator stores its one
// global counter under (mirrors redisstore's fixed "counter:channel_id"
// key -- there is only ever one channel-id sequence, so any constant works).
const channelIDCounterKey = 1

// ChannelIDAllocator is the in-process replacement for
// redisstore.ChannelIDAllocator.
type ChannelIDAllocator struct {
	counter *counterAllocator
}

var _ store.ChannelIDAllocator = (*ChannelIDAllocator)(nil)

func NewChannelIDAllocator(source store.CounterSource) *ChannelIDAllocator {
	return &ChannelIDAllocator{counter: newCounterAllocator("channel_id", source)}
}

func (a *ChannelIDAllocator) NextChannelID(ctx context.Context) (int64, error) {
	return a.counter.next(ctx, channelIDCounterKey)
}

func (a *ChannelIDAllocator) CurrentChannelID(ctx context.Context) (int64, error) {
	return a.counter.current(ctx, channelIDCounterKey)
}

// NextChannelIDAtLeast mirrors redisstore.ChannelIDAllocator.NextChannelIDAtLeast
// -- picked up automatically by internal/store/postgres/channel_core.go's
// optional-capability type assertion.
func (a *ChannelIDAllocator) NextChannelIDAtLeast(_ context.Context, floor int64) (int64, error) {
	return a.counter.nextAtLeast(channelIDCounterKey, floor), nil
}

// --- ChannelMessageIDAllocator -------------------------------------------

// ChannelMessageIDAllocator is the in-process replacement for
// redisstore.ChannelMessageIDAllocator.
type ChannelMessageIDAllocator struct {
	counter *counterAllocator
}

var _ store.ChannelMessageIDAllocator = (*ChannelMessageIDAllocator)(nil)

func NewChannelMessageIDAllocator(source store.CounterSource) *ChannelMessageIDAllocator {
	return &ChannelMessageIDAllocator{counter: newCounterAllocator("channel_msg_id", source)}
}

func (a *ChannelMessageIDAllocator) NextChannelMessageID(ctx context.Context, channelID int64) (int, error) {
	if channelID == 0 {
		return 0, fmt.Errorf("channel_msg_id counter: missing channel id")
	}
	v, err := a.counter.next(ctx, channelID)
	return int(v), err
}

func (a *ChannelMessageIDAllocator) CurrentChannelMessageID(ctx context.Context, channelID int64) (int, error) {
	if channelID == 0 {
		return 0, fmt.Errorf("channel_msg_id counter: missing channel id")
	}
	v, err := a.counter.current(ctx, channelID)
	return int(v), err
}
