package postgres

import (
	"context"
	"sync"
	"telesrv/internal/domain"
	"telesrv/internal/store/postgres/sqlcgen"
)

type testAllocatorBundle struct {
	boxIDs            *testBoxIDAllocator
	channelIDs        *testChannelIDAllocator
	channelMessageIDs *testChannelMessageIDAllocator
}

var testAllocatorBundles sync.Map

func testAllocatorsFor(db sqlcgen.DBTX) *testAllocatorBundle {
	if existing, ok := testAllocatorBundles.Load(db); ok {
		return existing.(*testAllocatorBundle)
	}
	bundle := &testAllocatorBundle{
		boxIDs:            &testBoxIDAllocator{source: NewMessageBoxCounterSource(db)},
		channelIDs:        &testChannelIDAllocator{source: NewChannelIDCounterSource(db)},
		channelMessageIDs: &testChannelMessageIDAllocator{source: NewChannelMessageIDCounterSource(db)},
	}
	actual, _ := testAllocatorBundles.LoadOrStore(db, bundle)
	return actual.(*testAllocatorBundle)
}

func newTestMessageStore(db sqlcgen.DBTX, opts ...MessageStoreOption) *MessageStore {
	bundle := testAllocatorsFor(db)
	all := append([]MessageStoreOption{WithMessageAllocators(bundle.boxIDs)}, opts...)
	return NewMessageStore(db, all...)
}

func newTestChannelStore(db sqlcgen.DBTX, opts ...ChannelStoreOption) *ChannelStore {
	bundle := testAllocatorsFor(db)
	all := append([]ChannelStoreOption{WithChannelAllocators(bundle.channelIDs, bundle.channelMessageIDs)}, opts...)
	return NewChannelStore(db, all...)
}

type testBoxIDAllocator struct {
	mu     sync.Mutex
	source *MessageBoxCounterSource
	values map[int64]int
}

func (a *testBoxIDAllocator) NextBoxID(ctx context.Context, userID int64) (int, error) {
	values, err := a.NextBoxIDs(ctx, []int64{userID})
	return values[userID], err
}

func (a *testBoxIDAllocator) NextBoxIDs(ctx context.Context, userIDs []int64) (map[int64]int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.source.CurrentBatch(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	if a.values == nil {
		a.values = make(map[int64]int, len(userIDs))
	}
	out := make(map[int64]int, len(userIDs))
	for _, userID := range userIDs {
		if current[userID] > a.values[userID] {
			a.values[userID] = current[userID]
		}
		a.values[userID]++
		out[userID] = a.values[userID]
	}
	return out, nil
}

func (a *testBoxIDAllocator) CurrentBoxID(ctx context.Context, userID int64) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.source.Current(ctx, userID)
	if err != nil {
		return 0, err
	}
	if a.values[userID] > current {
		return a.values[userID], nil
	}
	return current, nil
}

type testChannelIDAllocator struct {
	mu      sync.Mutex
	source  *ChannelIDCounterSource
	current int64
}

func (a *testChannelIDAllocator) NextChannelID(ctx context.Context) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.source.Current(ctx, 0)
	if err != nil {
		return 0, err
	}
	if int64(current) > a.current {
		a.current = int64(current)
	}
	a.current++
	return a.current, nil
}

func (a *testChannelIDAllocator) CurrentChannelID(ctx context.Context) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.source.Current(ctx, 0)
	if err != nil {
		return 0, err
	}
	if int64(current) > a.current {
		a.current = int64(current)
	}
	return a.current, nil
}

type testChannelMessageIDAllocator struct {
	mu     sync.Mutex
	source *ChannelMessageIDCounterSource
	values map[int64]int
}

func (a *testChannelMessageIDAllocator) NextChannelMessageID(ctx context.Context, channelID int64) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.source.Current(ctx, channelID)
	if err != nil {
		return 0, err
	}
	if a.values == nil {
		a.values = make(map[int64]int)
	}
	if current > a.values[channelID] {
		a.values[channelID] = current
	}
	a.values[channelID]++
	return a.values[channelID], nil
}

func (a *testChannelMessageIDAllocator) CurrentChannelMessageID(ctx context.Context, channelID int64) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.source.Current(ctx, channelID)
	if err != nil {
		return 0, err
	}
	if a.values[channelID] > current {
		return a.values[channelID], nil
	}
	return current, nil
}

type fixedBoxIDAllocator struct {
	next int
}

func (a fixedBoxIDAllocator) NextBoxID(context.Context, int64) (int, error) {
	return a.next, nil
}

func (a fixedBoxIDAllocator) NextBoxIDs(_ context.Context, userIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(userIDs))
	for _, userID := range userIDs {
		out[userID] = a.next
	}
	return out, nil
}

func (a fixedBoxIDAllocator) CurrentBoxID(context.Context, int64) (int, error) {
	return a.next, nil
}

type perUserCounterAllocator struct {
	mu     sync.Mutex
	values map[int64]int
}

func (a *perUserCounterAllocator) NextBoxID(_ context.Context, userID int64) (int, error) {
	return a.next(userID), nil
}

func (a *perUserCounterAllocator) NextBoxIDs(_ context.Context, userIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(userIDs))
	for _, userID := range userIDs {
		if _, ok := out[userID]; !ok {
			out[userID] = a.next(userID)
		}
	}
	return out, nil
}

func (a *perUserCounterAllocator) CurrentBoxID(_ context.Context, userID int64) (int, error) {
	return a.current(userID), nil
}

func (a *perUserCounterAllocator) next(userID int64) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.values == nil {
		a.values = map[int64]int{}
	}
	a.values[userID]++
	return a.values[userID]
}

func (a *perUserCounterAllocator) current(userID int64) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.values[userID]
}

func messageIDs(messages []domain.Message) []int {
	out := make([]int, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.ID)
	}
	return out
}

func sameInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
