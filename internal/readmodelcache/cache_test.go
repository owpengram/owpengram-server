package readmodelcache

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock 让 TTL 行为可确定地推进,无需 sleep。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}

func TestGetOrLoadSingleflightsConcurrentMiss(t *testing.T) {
	ctx := context.Background()
	c := New[int, string](Config[int, string]{MaxEntries: 16})

	var calls atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	load := func() (string, error) {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-release
		return "v", nil
	}

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			v, err := c.GetOrLoad(ctx, 42, load)
			if err != nil {
				errs <- err
				return
			}
			if v != "v" {
				errs <- fmt.Errorf("value = %q, want v", v)
			}
		}()
	}
	<-started
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("load called %d times, want 1 (singleflight)", got)
	}
	// 命中:不再 load。
	if v, err := c.GetOrLoad(ctx, 42, func() (string, error) { return "miss", nil }); err != nil || v != "v" {
		t.Fatalf("cached GetOrLoad = %q,%v want v,<nil>", v, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cache hit re-loaded: calls=%d", got)
	}
}

func TestInvalidateWhereValueUsesImmutableDependency(t *testing.T) {
	type value struct{ channels []int64 }
	c := New[int, value](Config[int, value]{MaxEntries: 4})
	c.Store(1, value{channels: []int64{7, 8}})
	c.Store(2, value{channels: []int64{9}})

	c.InvalidateWhereValue(func(_ int, v value) bool {
		for _, id := range v.channels {
			if id == 8 {
				return true
			}
		}
		return false
	})
	if _, ok := c.Peek(1); ok {
		t.Fatal("dependency match remained cached")
	}
	if got, ok := c.Peek(2); !ok || len(got.channels) != 1 || got.channels[0] != 9 {
		t.Fatalf("unrelated value = %+v,%v, want cached channel 9", got, ok)
	}
}

// TestEpochGuardRejectsStaleWriteback 证明 epoch 守卫堵住 lost-update:一次锁外 load
// 期间到达的 Invalidate 不得被这次 load 的(已陈旧)结果覆盖;在飞读者最终拿到的是
// 失效后重载的新值,且缓存未被陈旧值污染。
func TestEpochGuardRejectsStaleWriteback(t *testing.T) {
	ctx := context.Background()
	c := New[int, string](Config[int, string]{MaxEntries: 16})

	var seq atomic.Int32 // 第 1 次 load 返回 stale,失效后第 2 次返回 fresh
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	load := func() (string, error) {
		n := seq.Add(1)
		if n == 1 {
			once.Do(func() { close(started) })
			<-release // 第一次 load 阻塞,模拟跨越一次失效
			return "stale", nil
		}
		return "fresh", nil
	}

	resCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		v, err := c.GetOrLoad(ctx, 7, load)
		if err != nil {
			errCh <- err
			return
		}
		resCh <- v
	}()

	<-started
	// 失效在 load 期间到达(此刻缓存里还没有 key 7,delete 是 no-op,但 epoch++)。
	c.Invalidate(7)
	close(release) // 放行第一次 load:它返回 "stale",epoch 守卫必须拒绝写回。

	select {
	case err := <-errCh:
		t.Fatalf("GetOrLoad: %v", err)
	case v := <-resCh:
		if v != "fresh" {
			t.Fatalf("in-flight reader got %q, want fresh (stale must not win)", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for in-flight reader")
	}

	// 缓存现在应持有 fresh,且后续读不再 load。
	callsBefore := seq.Load()
	if v, err := c.GetOrLoad(ctx, 7, load); err != nil || v != "fresh" {
		t.Fatalf("post-race GetOrLoad = %q,%v want fresh,<nil>", v, err)
	}
	if seq.Load() != callsBefore {
		t.Fatalf("cache poisoned/evicted: reloaded (seq %d -> %d)", callsBefore, seq.Load())
	}
}

func TestLRUEvictsOldestNotWholeMap(t *testing.T) {
	c := New[int, int](Config[int, int]{MaxEntries: 3})
	for i := 1; i <= 3; i++ {
		mustLoad(t, c, i, i*10)
	}
	// 插入第 4 个:应只驱逐最旧的(key 1),而不是整表清空。
	mustLoad(t, c, 4, 40)
	if c.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (single-entry eviction, not whole-map flush)", c.Len())
	}
	if _, ok := c.Peek(1); ok {
		t.Fatal("key 1 should have been evicted as oldest")
	}
	for _, k := range []int{2, 3, 4} {
		if v, ok := c.Peek(k); !ok || v != k*10 {
			t.Fatalf("key %d evicted/lost (whole-map flush?): v=%d ok=%v", k, v, ok)
		}
	}
}

func TestLRUTouchOnGet(t *testing.T) {
	ctx := context.Background()
	c := New[int, int](Config[int, int]{MaxEntries: 2})
	mustLoad(t, c, 1, 10)
	mustLoad(t, c, 2, 20)
	// 触碰 key 1 使其变最近使用。
	if v, err := c.GetOrLoad(ctx, 1, failLoad[int](t)); err != nil || v != 10 {
		t.Fatalf("touch GetOrLoad(1) = %d,%v", v, err)
	}
	// 插入 key 3:应驱逐 key 2(最久未用),保留 key 1。
	mustLoad(t, c, 3, 30)
	if _, ok := c.Peek(2); ok {
		t.Fatal("key 2 should have been evicted (LRU), key 1 was touched")
	}
	if _, ok := c.Peek(1); !ok {
		t.Fatal("key 1 was touched and must survive")
	}
}

func TestWeightedLRUEvictsByTotalWeightAndSkipsOversize(t *testing.T) {
	ctx := context.Background()
	c := New[int, int](Config[int, int]{
		MaxEntries: 10,
		MaxWeight:  5,
		Weight:     func(v int) int64 { return int64(v) },
	})
	mustLoad(t, c, 1, 2)
	mustLoad(t, c, 2, 2)
	mustLoad(t, c, 3, 3)
	if _, ok := c.Peek(1); ok {
		t.Fatal("oldest entry should be evicted when aggregate weight exceeds five")
	}
	for _, key := range []int{2, 3} {
		if _, ok := c.Peek(key); !ok {
			t.Fatalf("weighted LRU lost retained key %d", key)
		}
	}
	loads := 0
	loadOversize := func() (int, error) { loads++; return 6, nil }
	if v, err := c.GetOrLoad(ctx, 4, loadOversize); err != nil || v != 6 {
		t.Fatalf("oversize first load = %d,%v", v, err)
	}
	if v, err := c.GetOrLoad(ctx, 4, loadOversize); err != nil || v != 6 {
		t.Fatalf("oversize second load = %d,%v", v, err)
	}
	if loads != 2 {
		t.Fatalf("oversize value unexpectedly retained: loads=%d, want 2", loads)
	}
	if _, ok := c.Peek(4); ok {
		t.Fatal("single value above MaxWeight must not remain cached")
	}
}

func TestLifecycleCallbacksCoverReplaceEvictAndFlush(t *testing.T) {
	type event struct {
		op    string
		key   int
		value string
	}
	var events []event
	c := New[int, string](Config[int, string]{
		MaxEntries: 2,
		OnStore: func(key int, value string) {
			events = append(events, event{op: "store", key: key, value: value})
		},
		OnRemove: func(key int, value string) {
			events = append(events, event{op: "remove", key: key, value: value})
		},
	})
	c.Store(1, "a")
	c.Store(1, "b")
	c.Store(2, "c")
	c.Store(3, "d")
	c.Flush()
	want := []event{
		{op: "store", key: 1, value: "a"},
		{op: "remove", key: 1, value: "a"},
		{op: "store", key: 1, value: "b"},
		{op: "store", key: 2, value: "c"},
		{op: "store", key: 3, value: "d"},
		{op: "remove", key: 1, value: "b"},
		{op: "remove", key: 3, value: "d"},
		{op: "remove", key: 2, value: "c"},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle events = %#v, want %#v", events, want)
	}
}

func TestVersionGateReloadsOnHashChange(t *testing.T) {
	ctx := context.Background()
	c := New[int, string](Config[int, string]{MaxEntries: 16})

	var calls atomic.Int32
	load := func(tag string) func() (string, error) {
		return func() (string, error) { calls.Add(1); return tag, nil }
	}
	// hash 100 装入。
	if v, _ := c.GetOrLoadVersioned(ctx, 1, 100, load("h100")); v != "h100" {
		t.Fatalf("first load = %q", v)
	}
	// 同 hash:命中,不 load。
	if v, _ := c.GetOrLoadVersioned(ctx, 1, 100, failLoad[string](t)); v != "h100" {
		t.Fatalf("same-hash hit = %q", v)
	}
	if calls.Load() != 1 {
		t.Fatalf("same-hash re-loaded: calls=%d", calls.Load())
	}
	// hash 改变:miss,重载。
	if v, _ := c.GetOrLoadVersioned(ctx, 1, 200, load("h200")); v != "h200" {
		t.Fatalf("hash-change reload = %q", v)
	}
	if calls.Load() != 2 {
		t.Fatalf("hash change should reload once more: calls=%d", calls.Load())
	}
}

func TestTTLExpiryIsPerKeyAndDoesNotBumpEpoch(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()
	c := New[int, int](Config[int, int]{MaxEntries: 16, TTL: time.Minute})
	c.now = clock.now

	mustLoad(t, c, 1, 10)
	epochBefore := c.cacheEpoch()

	clock.advance(2 * time.Minute) // 越过 TTL

	var reloaded atomic.Int32
	v, err := c.GetOrLoad(ctx, 1, func() (int, error) { reloaded.Add(1); return 11, nil })
	if err != nil || v != 11 {
		t.Fatalf("expired GetOrLoad = %d,%v want 11", v, err)
	}
	if reloaded.Load() != 1 {
		t.Fatal("expired entry should have reloaded")
	}
	if c.cacheEpoch() != epochBefore {
		t.Fatalf("TTL expiry bumped epoch %d -> %d (would thrash in-flight loads)", epochBefore, c.cacheEpoch())
	}
}

func TestInvalidateWhereFanout(t *testing.T) {
	ctx := context.Background()
	type key struct{ channel, user int64 }
	c := New[key, int](Config[key, int]{MaxEntries: 64})
	put := func(ch, u int64) {
		if _, err := c.GetOrLoad(ctx, key{ch, u}, func() (int, error) { return 1, nil }); err != nil {
			t.Fatal(err)
		}
	}
	put(10, 1)
	put(10, 2)
	put(20, 1)
	epochBefore := c.cacheEpoch()

	c.InvalidateWhere(func(k key) bool { return k.channel == 10 })

	if _, ok := c.Peek(key{10, 1}); ok {
		t.Fatal("channel 10 user 1 should be invalidated")
	}
	if _, ok := c.Peek(key{10, 2}); ok {
		t.Fatal("channel 10 user 2 should be invalidated")
	}
	if _, ok := c.Peek(key{20, 1}); !ok {
		t.Fatal("channel 20 must survive a channel-10 fanout invalidate")
	}
	if c.cacheEpoch() == epochBefore {
		t.Fatal("InvalidateWhere must bump epoch")
	}
}

func TestCloneIsolatesCallerFromCache(t *testing.T) {
	ctx := context.Background()
	c := New[int, []int](Config[int, []int]{
		MaxEntries: 16,
		Clone:      func(v []int) []int { return append([]int(nil), v...) },
	})
	orig := []int{1, 2, 3}
	got, err := c.GetOrLoad(ctx, 1, func() ([]int, error) { return orig, nil })
	if err != nil {
		t.Fatal(err)
	}
	got[0] = 999  // 突变返回值
	orig[1] = 888 // 突变 load 来源
	again, _ := c.Peek(1)
	if again[0] != 1 || again[1] != 2 || again[2] != 3 {
		t.Fatalf("cache entry mutated through aliasing: %v", again)
	}
}

func TestFlushClearsAndBumpsEpoch(t *testing.T) {
	ctx := context.Background()
	c := New[int, int](Config[int, int]{MaxEntries: 16})
	for i := 0; i < 5; i++ {
		mustLoad(t, c, i, i)
	}
	epochBefore := c.cacheEpoch()
	c.Flush()
	if c.Len() != 0 {
		t.Fatalf("Len after flush = %d, want 0", c.Len())
	}
	if c.cacheEpoch() == epochBefore {
		t.Fatal("Flush must bump epoch")
	}
	_ = ctx
}

func TestNilCacheBypassesToLoad(t *testing.T) {
	ctx := context.Background()
	var c *Cache[int, string] // New 在 MaxEntries<=0 时返回 nil
	if got := New[int, string](Config[int, string]{MaxEntries: 0}); got != nil {
		t.Fatalf("New(MaxEntries:0) = %v, want nil", got)
	}
	var calls atomic.Int32
	v, err := c.GetOrLoad(ctx, 1, func() (string, error) { calls.Add(1); return "x", nil })
	if err != nil || v != "x" {
		t.Fatalf("nil cache GetOrLoad = %q,%v", v, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("nil cache should call load exactly once: %d", calls.Load())
	}
	// 其它方法对 nil 安全。
	c.Invalidate(1)
	c.InvalidateWhere(func(int) bool { return true })
	c.Flush()
	c.Store(1, "y")
	if _, ok := c.Peek(1); ok {
		t.Fatal("nil cache Peek must miss")
	}
	if c.Len() != 0 {
		t.Fatal("nil cache Len must be 0")
	}
}

func TestStoreIfEpochRejectsStaleExternalBuild(t *testing.T) {
	// 外部构建模式:LoadEpoch 快照 → 构建期间发生失效 → StoreIfEpoch 必须拒绝写回。
	c := New[int, string](Config[int, string]{MaxEntries: 16})
	loadEpoch := c.LoadEpoch()
	c.Invalidate(7) // 构建期间的失效(epoch++)
	c.StoreIfEpoch(7, "stale", loadEpoch)
	if _, ok := c.Peek(7); ok {
		t.Fatal("StoreIfEpoch 在 epoch 变更后必须拒绝写回")
	}
	// 无并发失效时,StoreIfEpoch 正常写入。
	fresh := c.LoadEpoch()
	c.StoreIfEpoch(7, "fresh", fresh)
	if v, ok := c.Peek(7); !ok || v != "fresh" {
		t.Fatalf("StoreIfEpoch 在 epoch 未变时应写入: %q ok=%v", v, ok)
	}
}

// batchVal 模拟带负缓存的值(found=false 表示「查过但不存在」)。
type batchVal struct {
	n     int
	found bool
}

func TestGetOrLoadBatchCachesHitsMissesAndNegatives(t *testing.T) {
	ctx := context.Background()
	c := New[int, batchVal](Config[int, batchVal]{MaxEntries: 64})
	noVersion := func(int) (int64, bool) { return 0, true }

	var loadCalls atomic.Int32
	var lastMissing atomic.Int32
	load := func(_ context.Context, missing []int) (map[int]batchVal, error) {
		loadCalls.Add(1)
		lastMissing.Store(int32(len(missing)))
		out := make(map[int]batchVal, len(missing))
		for _, k := range missing {
			// 偶数存在,奇数为负结果(仍须返回,以便缓存负结果)。
			out[k] = batchVal{n: k * 10, found: k%2 == 0}
		}
		return out, nil
	}

	// 首次:全 miss,一次批量 load。
	got, err := c.GetOrLoadBatch(ctx, []int{1, 2, 3, 4}, noVersion, load)
	if err != nil {
		t.Fatal(err)
	}
	if got[2].n != 20 || !got[2].found || got[3].found {
		t.Fatalf("batch result wrong: %+v", got)
	}
	if loadCalls.Load() != 1 || lastMissing.Load() != 4 {
		t.Fatalf("first batch: calls=%d missing=%d, want 1/4", loadCalls.Load(), lastMissing.Load())
	}

	// 再查(含一个新键 6):仅 6 是 miss,负结果(1/3)也已缓存不再 load。
	got2, err := c.GetOrLoadBatch(ctx, []int{1, 2, 3, 4, 6}, noVersion, load)
	if err != nil {
		t.Fatal(err)
	}
	if loadCalls.Load() != 2 || lastMissing.Load() != 1 {
		t.Fatalf("second batch should load only key 6: calls=%d missing=%d", loadCalls.Load(), lastMissing.Load())
	}
	if got2[6].n != 60 || !got2[6].found {
		t.Fatalf("key 6 = %+v, want 60/found", got2[6])
	}
}

func TestGetOrLoadBatchCoalescesOverlappingConcurrentMissesPerKey(t *testing.T) {
	ctx := context.Background()
	c := New[int, batchVal](Config[int, batchVal]{MaxEntries: 64})
	noVersion := func(int) (int64, bool) { return 0, true }

	firstStarted := make(chan struct{})
	secondLoaded := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	var mu sync.Mutex
	loadedKeys := make(map[int]int)
	load := func(_ context.Context, missing []int) (map[int]batchVal, error) {
		call := calls.Add(1)
		mu.Lock()
		for _, key := range missing {
			loadedKeys[key]++
		}
		mu.Unlock()
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
		} else {
			close(secondLoaded)
		}
		out := make(map[int]batchVal, len(missing))
		for _, key := range missing {
			out[key] = batchVal{n: key * 10, found: true}
		}
		return out, nil
	}

	firstResult := make(chan map[int]batchVal, 1)
	firstErr := make(chan error, 1)
	go func() {
		got, err := c.GetOrLoadBatch(ctx, []int{1, 2, 3}, noVersion, load)
		firstResult <- got
		firstErr <- err
	}()
	<-firstStarted

	secondResult := make(chan map[int]batchVal, 1)
	secondErr := make(chan error, 1)
	go func() {
		got, err := c.GetOrLoadBatch(ctx, []int{2, 3, 4}, noVersion, load)
		secondResult <- got
		secondErr <- err
	}()
	<-secondLoaded
	close(releaseFirst)

	first, second := <-firstResult, <-secondResult
	if err := <-firstErr; err != nil {
		t.Fatal(err)
	}
	if err := <-secondErr; err != nil {
		t.Fatal(err)
	}
	if first[1].n != 10 || first[2].n != 20 || first[3].n != 30 ||
		second[2].n != 20 || second[3].n != 30 || second[4].n != 40 {
		t.Fatalf("overlapping results first=%+v second=%+v", first, second)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls.Load() != 2 || loadedKeys[1] != 1 || loadedKeys[2] != 1 || loadedKeys[3] != 1 || loadedKeys[4] != 1 {
		t.Fatalf("backend calls=%d loaded=%v, want two batches and every key exactly once", calls.Load(), loadedKeys)
	}
}

func TestGetOrLoadBatchVersionGateReloadsOnHashChange(t *testing.T) {
	ctx := context.Background()
	c := New[int, batchVal](Config[int, batchVal]{MaxEntries: 64})
	var hash atomic.Int64
	hash.Store(100)
	versionOf := func(int) (int64, bool) { return hash.Load(), true }

	var loadCalls atomic.Int32
	load := func(_ context.Context, missing []int) (map[int]batchVal, error) {
		loadCalls.Add(1)
		out := make(map[int]batchVal, len(missing))
		for _, k := range missing {
			out[k] = batchVal{n: int(hash.Load()), found: true}
		}
		return out, nil
	}
	if _, err := c.GetOrLoadBatch(ctx, []int{1}, versionOf, load); err != nil {
		t.Fatal(err)
	}
	// 同 hash:命中,不 load。
	if _, err := c.GetOrLoadBatch(ctx, []int{1}, versionOf, load); err != nil {
		t.Fatal(err)
	}
	if loadCalls.Load() != 1 {
		t.Fatalf("same-hash batch re-loaded: calls=%d", loadCalls.Load())
	}
	// hash 改变:版本闸门 miss,重载。
	hash.Store(200)
	got, err := c.GetOrLoadBatch(ctx, []int{1}, versionOf, load)
	if err != nil {
		t.Fatal(err)
	}
	if loadCalls.Load() != 2 || got[1].n != 200 {
		t.Fatalf("hash change should reload: calls=%d got=%+v", loadCalls.Load(), got[1])
	}
}

// TestGetOrLoadBatchStoresUnderLookupHashNotStoreHash 钉住版本闸门修复:写回必须用查阶段
// 快照的 hash,而非写回时重算的 hash。这里在 loadMissing(恰好在查与写之间执行)里把版本
// 从 100 改成 200——若写回误用新 hash 200,则按版本 100 加载的数据会被当作 200 的新数据命中。
func TestGetOrLoadBatchStoresUnderLookupHashNotStoreHash(t *testing.T) {
	ctx := context.Background()
	c := New[int, batchVal](Config[int, batchVal]{MaxEntries: 64})
	var gen atomic.Int64
	gen.Store(100)
	versionOf := func(int) (int64, bool) { return gen.Load(), true }

	var loadCalls atomic.Int32
	load := func(_ context.Context, missing []int) (map[int]batchVal, error) {
		loadCalls.Add(1)
		gen.Store(200) // 版本在「查(@100,miss)」与「写回」之间变更
		out := make(map[int]batchVal, len(missing))
		for _, k := range missing {
			out[k] = batchVal{n: k, found: true}
		}
		return out, nil
	}

	// 首批:查@100 miss → load(把 gen 改到 200)→ 写回须用快照的 100,而非 200。
	if _, err := c.GetOrLoadBatch(ctx, []int{1}, versionOf, load); err != nil {
		t.Fatal(err)
	}
	if loadCalls.Load() != 1 {
		t.Fatalf("first batch load calls = %d, want 1", loadCalls.Load())
	}
	// 现在 currentHash=200。若 entry 误以 200 戳入(bug),这次会命中陈旧数据、不重载;
	// 修复后 entry 是 100 != 200 → miss → 重载。
	if _, err := c.GetOrLoadBatch(ctx, []int{1}, versionOf, load); err != nil {
		t.Fatal(err)
	}
	if loadCalls.Load() != 2 {
		t.Fatalf("version changed during first load; second read must reload (loadCalls=%d, want 2) — stale-as-fresh version-gate bypass", loadCalls.Load())
	}
}

func TestGetOrLoadBatchBypassesNonCacheable(t *testing.T) {
	ctx := context.Background()
	c := New[int, batchVal](Config[int, batchVal]{MaxEntries: 64})
	// key 7 不可缓存(version 缺失),其余纯 TTL 缓存。
	versionOf := func(k int) (int64, bool) { return 0, k != 7 }
	var loadCalls atomic.Int32
	load := func(_ context.Context, missing []int) (map[int]batchVal, error) {
		loadCalls.Add(1)
		out := make(map[int]batchVal, len(missing))
		for _, k := range missing {
			out[k] = batchVal{n: k, found: true}
		}
		return out, nil
	}
	if _, err := c.GetOrLoadBatch(ctx, []int{7, 8}, versionOf, load); err != nil {
		t.Fatal(err)
	}
	// 7 不可缓存故未写回;8 已缓存。再查应只为 7 load。
	got, err := c.GetOrLoadBatch(ctx, []int{7, 8}, versionOf, load)
	if err != nil {
		t.Fatal(err)
	}
	if loadCalls.Load() != 2 {
		t.Fatalf("non-cacheable key 7 should reload every time: calls=%d", loadCalls.Load())
	}
	if _, ok := c.Peek(7); ok {
		t.Fatal("non-cacheable key 7 must not be stored")
	}
	if got[8].n != 8 {
		t.Fatalf("key 8 = %+v", got[8])
	}
}

func TestGetOrLoadBatchRetriesOnEpochChangeDuringLoad(t *testing.T) {
	ctx := context.Background()
	c := New[int, batchVal](Config[int, batchVal]{MaxEntries: 64})
	noVersion := func(int) (int64, bool) { return 0, true }

	var seq atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	load := func(_ context.Context, missing []int) (map[int]batchVal, error) {
		n := seq.Add(1)
		out := make(map[int]batchVal, len(missing))
		if n == 1 {
			once.Do(func() { close(started) })
			<-release // 第一趟批量 load 阻塞,期间发生失效
			for _, k := range missing {
				out[k] = batchVal{n: k, found: true} // 旧批数据
			}
			return out, nil
		}
		for _, k := range missing {
			out[k] = batchVal{n: k + 1000, found: true} // 失效后重载的新数据
		}
		return out, nil
	}

	resCh := make(chan map[int]batchVal, 1)
	go func() {
		got, _ := c.GetOrLoadBatch(ctx, []int{1}, noVersion, load)
		resCh <- got
	}()
	<-started
	c.Invalidate(1) // 批量 load 期间失效 → epoch++
	close(release)

	got := <-resCh
	if got[1].n != 1001 {
		t.Fatalf("epoch retry should return reloaded data: got %+v, want 1001", got[1])
	}
	// 缓存里应是重载后的新值,且不再 load。
	callsBefore := seq.Load()
	if v, ok := c.Peek(1); !ok || v.n != 1001 {
		t.Fatalf("cache should hold reloaded value: %+v ok=%v", v, ok)
	}
	if seq.Load() != callsBefore {
		t.Fatalf("post-retry read hit loader: seq %d", seq.Load())
	}
}

func TestStoreWarmFromListDoesNotBumpEpoch(t *testing.T) {
	c := New[int, int](Config[int, int]{MaxEntries: 16})
	epochBefore := c.cacheEpoch()
	c.Store(1, 10)
	if c.cacheEpoch() != epochBefore {
		t.Fatal("Store (warm) must not bump epoch")
	}
	if v, ok := c.Peek(1); !ok || v != 10 {
		t.Fatalf("Store then Peek = %d,%v", v, ok)
	}
}

func TestLoadErrorNotCached(t *testing.T) {
	ctx := context.Background()
	c := New[int, int](Config[int, int]{MaxEntries: 16})
	wantErr := fmt.Errorf("boom")
	if _, err := c.GetOrLoad(ctx, 1, func() (int, error) { return 0, wantErr }); err != wantErr {
		t.Fatalf("err = %v, want boom", err)
	}
	if _, ok := c.Peek(1); ok {
		t.Fatal("failed load must not be cached")
	}
	// 之后成功 load 应生效。
	if v, err := c.GetOrLoad(ctx, 1, func() (int, error) { return 5, nil }); err != nil || v != 5 {
		t.Fatalf("retry GetOrLoad = %d,%v", v, err)
	}
}

// TestConcurrentAccessNoPanic 在 -race 下能抓数据竞争;无 -race 时验证无 panic/死锁。
func TestConcurrentAccessNoPanic(t *testing.T) {
	ctx := context.Background()
	c := New[int, int](Config[int, int]{MaxEntries: 64, TTL: time.Millisecond})
	var wg sync.WaitGroup
	for g := 0; g < 24; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				k := (g + i) % 128
				switch i % 5 {
				case 0:
					_, _ = c.GetOrLoad(ctx, k, func() (int, error) { return k, nil })
				case 1:
					_, _ = c.GetOrLoadVersioned(ctx, k, int64(i%7+1), func() (int, error) { return k, nil })
				case 2:
					c.Invalidate(k)
				case 3:
					c.InvalidateWhere(func(x int) bool { return x%3 == 0 })
				case 4:
					c.Store(k, k)
				}
			}
		}(g)
	}
	wg.Wait()
}

// --- helpers ---

func mustLoad[K comparable, V comparable](t *testing.T, c *Cache[K, V], key K, val V) {
	t.Helper()
	v, err := c.GetOrLoad(context.Background(), key, func() (V, error) { return val, nil })
	if err != nil {
		t.Fatalf("GetOrLoad(%v): %v", key, err)
	}
	if v != val {
		t.Fatalf("GetOrLoad(%v) = %v, want %v", key, v, val)
	}
}

func failLoad[V any](t *testing.T) func() (V, error) {
	return func() (V, error) {
		t.Helper()
		t.Fatal("load should not have been called (expected cache hit)")
		var zero V
		return zero, nil
	}
}
