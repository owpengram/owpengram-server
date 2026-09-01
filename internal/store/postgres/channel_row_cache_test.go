package postgres

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func TestChannelRowCacheNilDisabled(t *testing.T) {
	if c := NewChannelRowCache(0); c != nil {
		t.Fatalf("max<=0 应返回 nil(禁用)，got %v", c)
	}
	if c := NewChannelRowCache(-5); c != nil {
		t.Fatalf("负容量应返回 nil(禁用)，got %v", c)
	}
	// nil 缓存上的所有操作都必须安全(no-op)。
	var nilCache *ChannelRowCache
	nilCache.put(domain.Channel{ID: 1})
	nilCache.delete(1)
	nilCache.flush()
	if _, ok := nilCache.get(1); ok {
		t.Fatalf("nil 缓存 get 必须返回 ok=false")
	}
}

func TestChannelRowCachePutGetDeleteFlush(t *testing.T) {
	c := NewChannelRowCache(16)
	ch := domain.Channel{ID: 42, Title: "hello", AccessHash: 7}

	if _, ok := c.get(42); ok {
		t.Fatalf("空缓存不应命中")
	}
	c.put(ch)
	got, ok := c.get(42)
	if !ok || got.ID != 42 || got.Title != "hello" || got.AccessHash != 7 {
		t.Fatalf("put/get 往返失败: %+v ok=%v", got, ok)
	}

	c.delete(42)
	if _, ok := c.get(42); ok {
		t.Fatalf("delete 后不应命中")
	}

	c.put(ch)
	c.flush()
	if _, ok := c.get(42); ok {
		t.Fatalf("flush 后不应命中")
	}

	// id==0 既不存也不查。
	c.put(domain.Channel{ID: 0, Title: "zero"})
	if _, ok := c.get(0); ok {
		t.Fatalf("id==0 不应缓存/命中")
	}
}

func TestChannelRowCacheReturnsIsolatedClone(t *testing.T) {
	c := NewChannelRowCache(8)
	orig := domain.Channel{
		ID:            9,
		PhotoStripped: []byte{1, 2, 3},
	}
	orig.ReactionPolicy.Emoticons = []string{"👍"}
	orig.ReactionPolicy.CustomEmojiIDs = []int64{100}
	c.put(orig)

	// 改写 put 入参的底层切片不得污染缓存(put 须自持副本)。
	orig.PhotoStripped[0] = 99
	orig.ReactionPolicy.Emoticons[0] = "💩"
	orig.ReactionPolicy.CustomEmojiIDs[0] = 999

	got, ok := c.get(9)
	if !ok {
		t.Fatalf("应命中")
	}
	if got.PhotoStripped[0] != 1 || got.ReactionPolicy.Emoticons[0] != "👍" || got.ReactionPolicy.CustomEmojiIDs[0] != 100 {
		t.Fatalf("缓存被 put 入参后续改写污染: %+v", got)
	}

	// 改写 get 返回值的切片也不得污染缓存(get 须返回副本)。
	got.PhotoStripped[0] = 77
	got.ReactionPolicy.Emoticons[0] = "🔥"
	got.ReactionPolicy.CustomEmojiIDs[0] = 777
	again, _ := c.get(9)
	if again.PhotoStripped[0] != 1 || again.ReactionPolicy.Emoticons[0] != "👍" || again.ReactionPolicy.CustomEmojiIDs[0] != 100 {
		t.Fatalf("缓存被 get 返回值改写污染: %+v", again)
	}
}

func TestChannelRowCacheCapEviction(t *testing.T) {
	c := NewChannelRowCache(2)
	c.put(domain.Channel{ID: 1})
	c.put(domain.Channel{ID: 2})
	// 第三个新 id 超 cap=2:LRU 单条驱逐最旧的 id1,而非整表清空。
	c.put(domain.Channel{ID: 3})
	if _, ok := c.get(1); ok {
		t.Fatalf("超限后最旧条目 id1 应被驱逐")
	}
	if _, ok := c.get(2); !ok {
		t.Fatalf("LRU 单条驱逐应保留次新条目 id2,证明非整表 flush")
	}
	if _, ok := c.get(3); !ok {
		t.Fatalf("最新写入的条目 id3 应在")
	}
	// 已存在 id 的更新原地生效,不触发驱逐。
	c.put(domain.Channel{ID: 3, Title: "v2"})
	if got, ok := c.get(3); !ok || got.Title != "v2" {
		t.Fatalf("已存在 id 的更新应原地生效: %+v ok=%v", got, ok)
	}
}

func TestChannelRowCacheSingleflightsColdLoad(t *testing.T) {
	c := NewChannelRowCache(16)
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	load := func() (domain.Channel, error) {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-release
		return domain.Channel{ID: 42, Title: "loaded"}, nil
	}

	const goroutines = 16
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			ch, err := c.getOrLoad(context.Background(), 42, load)
			if err != nil {
				errs <- err
				return
			}
			if ch.ID != 42 || ch.Title != "loaded" {
				errs <- fmt.Errorf("channel = %+v, want loaded 42", ch)
			}
		}()
	}
	<-started
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("load calls = %d, want 1", got)
	}
	if _, err := c.getOrLoad(context.Background(), 42, load); err != nil {
		t.Fatalf("cached getOrLoad: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cache hit called load again: calls=%d", got)
	}
}

func TestChannelRowCacheBatchesMissesAndNegativeRows(t *testing.T) {
	c := NewChannelRowCache(8)
	loads := 0
	load := func(_ context.Context, ids []int64) (map[int64]domain.Channel, error) {
		loads++
		out := make(map[int64]domain.Channel, len(ids))
		for _, id := range ids {
			if id != 2 {
				out[id] = domain.Channel{ID: id, Title: "channel"}
			}
		}
		return out, nil
	}
	for i := 0; i < 2; i++ {
		got, err := c.getOrLoadBatch(context.Background(), []int64{1, 2, 3}, load)
		if err != nil {
			t.Fatal(err)
		}
		if got[1].ID != 1 || got[2].ID != 0 || got[3].ID != 3 {
			t.Fatalf("batch result = %+v", got)
		}
	}
	if loads != 1 {
		t.Fatalf("batch loads = %d, want 1", loads)
	}
}
