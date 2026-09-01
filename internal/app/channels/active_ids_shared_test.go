package channels

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/memory"
)

type fakeActiveChannelIDsPageCache struct {
	mu     sync.Mutex
	values map[store.ActiveChannelIDsPageKey][]int64
	getErr error
	putErr error
	gets   int
	puts   int
}

func (f *fakeActiveChannelIDsPageCache) GetActiveChannelIDsPage(
	_ context.Context,
	key store.ActiveChannelIDsPageKey,
) ([]int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.getErr != nil {
		return nil, false, f.getErr
	}
	value, found := f.values[key]
	return append([]int64(nil), value...), found, nil
}

func (f *fakeActiveChannelIDsPageCache) PutActiveChannelIDsPage(
	_ context.Context,
	key store.ActiveChannelIDsPageKey,
	value []int64,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	if f.putErr != nil {
		return f.putErr
	}
	if f.values == nil {
		f.values = make(map[store.ActiveChannelIDsPageKey][]int64)
	}
	f.values[key] = append([]int64(nil), value...)
	return nil
}

type fakeActiveChannelIDsLoader struct {
	mu     sync.Mutex
	values []int64
	err    error
	calls  int
	onLoad func()
}

func (f *fakeActiveChannelIDsLoader) ListActiveChannelIDsForUser(
	_ context.Context,
	_, _ int64,
	_ int,
) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.onLoad != nil {
		f.onLoad()
	}
	return append([]int64(nil), f.values...), f.err
}

type fakeActiveChannelIDsMetrics struct {
	mu       sync.Mutex
	outcomes map[string]int
}

type mutableReadModelVersions struct {
	mu     sync.Mutex
	hashes map[store.ReadModelKey]int64
}

func (m *mutableReadModelVersions) ReadModelHash(
	_ context.Context,
	model string,
	ownerUserID int64,
	peerType domain.PeerType,
	peerID int64,
) (int64, bool, error) {
	key := store.ReadModelKey{Model: model, OwnerUserID: ownerUserID, PeerType: peerType, PeerID: peerID}
	m.mu.Lock()
	defer m.mu.Unlock()
	hash := m.hashes[key]
	return hash, hash != 0, nil
}

func (m *mutableReadModelVersions) ReadModelHashes(
	_ context.Context,
	keys []store.ReadModelKey,
) (map[store.ReadModelKey]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[store.ReadModelKey]int64, len(keys))
	for _, key := range keys {
		out[key] = m.hashes[key]
	}
	return out, nil
}

func (m *mutableReadModelVersions) set(key store.ReadModelKey, hash int64) {
	m.mu.Lock()
	m.hashes[key] = hash
	m.mu.Unlock()
}

func (f *fakeActiveChannelIDsMetrics) ActiveChannelIDsCache(outcome string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.outcomes == nil {
		f.outcomes = make(map[string]int)
	}
	f.outcomes[outcome]++
}

func TestActiveChannelIDsSharedPageSurvivesServiceRestart(t *testing.T) {
	ctx := context.Background()
	const ownerID int64 = 1001
	key := store.ReadModelKey{Model: "channel_active_memberships", OwnerUserID: ownerID, PeerType: domain.PeerTypeUser, PeerID: ownerID}
	versions := &fakeReadModelVersions{hashes: map[store.ReadModelKey]int64{key: 501}}
	shared := &fakeActiveChannelIDsPageCache{}
	firstLoader := &fakeActiveChannelIDsLoader{values: []int64{11, 12}}
	firstMetrics := &fakeActiveChannelIDsMetrics{}
	first := NewService(memory.NewChannelStore(),
		WithReadModelVersions(versions),
		WithActiveChannelIDsReadModel(shared, firstLoader, 32, 0, firstMetrics),
	)
	got, err := first.ActiveChannelIDsForUser(ctx, ownerID, 0, 1000)
	if err != nil || !slices.Equal(got, []int64{11, 12}) {
		t.Fatalf("first page = %v err=%v", got, err)
	}
	if firstLoader.calls != 1 || shared.puts != 1 || firstMetrics.outcomes["miss"] != 1 || firstMetrics.outcomes["fill"] != 1 {
		t.Fatalf("first load calls=%d puts=%d metrics=%v", firstLoader.calls, shared.puts, firstMetrics.outcomes)
	}

	secondLoader := &fakeActiveChannelIDsLoader{err: errors.New("cold loader must not run")}
	secondMetrics := &fakeActiveChannelIDsMetrics{}
	second := NewService(memory.NewChannelStore(),
		WithReadModelVersions(versions),
		WithActiveChannelIDsReadModel(shared, secondLoader, 32, 0, secondMetrics),
	)
	got, err = second.ActiveChannelIDsForUser(ctx, ownerID, 0, 1000)
	if err != nil || !slices.Equal(got, []int64{11, 12}) {
		t.Fatalf("restart page = %v err=%v", got, err)
	}
	if secondLoader.calls != 0 || secondMetrics.outcomes["hit"] != 1 || secondMetrics.outcomes["served"] != 1 {
		t.Fatalf("restart loader=%d metrics=%v", secondLoader.calls, secondMetrics.outcomes)
	}
}

func TestActiveChannelIDsSharedPageFailsClosedOnRedisError(t *testing.T) {
	const ownerID int64 = 1001
	key := store.ReadModelKey{Model: "channel_active_memberships", OwnerUserID: ownerID, PeerType: domain.PeerTypeUser, PeerID: ownerID}
	versions := &fakeReadModelVersions{hashes: map[store.ReadModelKey]int64{key: 601}}
	shared := &fakeActiveChannelIDsPageCache{getErr: errors.New("redis down")}
	loader := &fakeActiveChannelIDsLoader{values: []int64{11}}
	service := NewService(memory.NewChannelStore(),
		WithReadModelVersions(versions),
		WithActiveChannelIDsReadModel(shared, loader, 32, 0, nil),
	)
	if _, err := service.ActiveChannelIDsForUser(context.Background(), ownerID, 0, 1000); err == nil {
		t.Fatal("Redis error was silently bypassed")
	}
	if loader.calls != 0 {
		t.Fatalf("cold loader calls = %d, want 0", loader.calls)
	}
}

func TestActiveChannelIDsSharedPageRetriesGenerationChange(t *testing.T) {
	ctx := context.Background()
	const ownerID int64 = 1001
	key := store.ReadModelKey{Model: "channel_active_memberships", OwnerUserID: ownerID, PeerType: domain.PeerTypeUser, PeerID: ownerID}
	versions := &fakeReadModelVersions{hashes: map[store.ReadModelKey]int64{key: 701}}
	shared := &fakeActiveChannelIDsPageCache{}
	loader := &fakeActiveChannelIDsLoader{values: []int64{11}}
	loader.onLoad = func() {
		if loader.calls == 1 {
			versions.hashes[key] = 702
			loader.values = []int64{11, 12}
		}
	}
	metrics := &fakeActiveChannelIDsMetrics{}
	service := NewService(memory.NewChannelStore(),
		WithReadModelVersions(versions),
		WithActiveChannelIDsReadModel(shared, loader, 32, 0, metrics),
	)
	got, err := service.ActiveChannelIDsForUser(ctx, ownerID, 0, 1000)
	if err != nil || !slices.Equal(got, []int64{11, 12}) {
		t.Fatalf("page = %v err=%v", got, err)
	}
	if loader.calls != 2 || shared.puts != 1 || metrics.outcomes["generation_retry"] != 1 {
		t.Fatalf("loader=%d puts=%d metrics=%v", loader.calls, shared.puts, metrics.outcomes)
	}
	oldKey := store.ActiveChannelIDsPageKey{UserID: ownerID, Generation: 701, AfterChannelID: 0, Limit: 1000}
	if _, found := shared.values[oldKey]; found {
		t.Fatal("generation-raced page was stored under old key")
	}
}

func TestActiveChannelIDsSharedMissingGenerationOnlyCachesEmpty(t *testing.T) {
	shared := &fakeActiveChannelIDsPageCache{}
	loader := &fakeActiveChannelIDsLoader{values: []int64{11}}
	service := NewService(memory.NewChannelStore(),
		WithReadModelVersions(&fakeReadModelVersions{}),
		WithActiveChannelIDsReadModel(shared, loader, 32, 0, nil),
	)
	if _, err := service.ActiveChannelIDsForUser(context.Background(), 1001, 0, 1000); err == nil {
		t.Fatal("non-empty page without durable generation accepted")
	}
	if shared.puts != 0 {
		t.Fatalf("shared puts = %d, want 0", shared.puts)
	}
}

func TestActiveChannelIDsLocalWriteInvalidatesCachedGenerationBeforeNotify(t *testing.T) {
	ctx := context.Background()
	const ownerID int64 = 1001
	versionKey := store.ReadModelKey{Model: "channel_active_memberships", OwnerUserID: ownerID, PeerType: domain.PeerTypeUser, PeerID: ownerID}
	baseVersions := &mutableReadModelVersions{hashes: map[store.ReadModelKey]int64{versionKey: 801}}
	cachedVersions := store.NewCachedReadModelVersionStore(baseVersions, 0, 32)
	oldPageKey := store.ActiveChannelIDsPageKey{UserID: ownerID, Generation: 801, AfterChannelID: 0, Limit: 1000}
	shared := &fakeActiveChannelIDsPageCache{values: map[store.ActiveChannelIDsPageKey][]int64{oldPageKey: {11}}}
	loader := &fakeActiveChannelIDsLoader{values: []int64{11, 12}}
	service := NewService(memory.NewChannelStore(),
		WithReadModelVersions(cachedVersions),
		WithActiveChannelIDsReadModel(shared, loader, 32, 0, nil),
	)
	first, err := service.ActiveChannelIDsForUser(ctx, ownerID, 0, 1000)
	if err != nil || !slices.Equal(first, []int64{11}) {
		t.Fatalf("first = %v err=%v", first, err)
	}
	baseVersions.set(versionKey, 802)
	// Simulate the synchronous post-commit app hook before PostgreSQL NOTIFY is
	// delivered to this process.
	service.invalidateActiveChannelIDs(ownerID)
	second, err := service.ActiveChannelIDsForUser(ctx, ownerID, 0, 1000)
	if err != nil || !slices.Equal(second, []int64{11, 12}) {
		t.Fatalf("after local invalidation = %v err=%v", second, err)
	}
	if loader.calls != 1 {
		t.Fatalf("cold loader calls = %d, want 1 for new generation", loader.calls)
	}
	newPageKey := store.ActiveChannelIDsPageKey{UserID: ownerID, Generation: 802, AfterChannelID: 0, Limit: 1000}
	if !slices.Equal(shared.values[newPageKey], []int64{11, 12}) {
		t.Fatalf("new generation page = %v", shared.values[newPageKey])
	}
}
