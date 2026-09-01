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

func TestChannelDialogCacheNilDisabled(t *testing.T) {
	if c := NewChannelDialogCache(0); c != nil {
		t.Fatalf("max<=0 应返回 nil(禁用), got %v", c)
	}
	var nilCache *ChannelDialogCache
	nilCache.put(domain.ChannelDialog{UserID: 1, ChannelID: 2})
	nilCache.delete(1, 2)
	nilCache.deleteChannel(2)
	nilCache.flush()
	if _, ok := nilCache.get(1, 2); ok {
		t.Fatalf("nil 缓存 get 必须返回 ok=false")
	}
}

func TestChannelDialogCachePutGetDeleteFlushAndClone(t *testing.T) {
	c := NewChannelDialogCache(16)
	dialog := domain.ChannelDialog{
		UserID:              10,
		ChannelID:           20,
		FolderID:            1,
		TopMessageID:        9,
		TopMessageDate:      100,
		ReadInboxMaxID:      7,
		ReadOutboxMaxID:     8,
		UnreadCount:         2,
		Pinned:              true,
		ViewForumAsMessages: true,
		DefaultSendAs:       &domain.Peer{Type: domain.PeerTypeUser, ID: 10},
	}

	if _, ok := c.get(10, 20); ok {
		t.Fatalf("空缓存不应命中")
	}
	c.put(dialog)
	dialog.DefaultSendAs.ID = 99
	got, ok := c.get(10, 20)
	if !ok || got.UserID != 10 || got.ChannelID != 20 || got.DefaultSendAs == nil || got.DefaultSendAs.ID != 10 {
		t.Fatalf("put/get 往返或写入 clone 失败: %+v ok=%v", got, ok)
	}
	got.DefaultSendAs.ID = 77
	again, ok := c.get(10, 20)
	if !ok || again.DefaultSendAs == nil || again.DefaultSendAs.ID != 10 {
		t.Fatalf("读取结果必须 clone，避免调用方污染缓存: %+v ok=%v", again, ok)
	}

	c.delete(10, 20)
	if _, ok := c.get(10, 20); ok {
		t.Fatalf("delete 后不应命中")
	}
	c.put(dialog)
	c.flush()
	if _, ok := c.get(10, 20); ok {
		t.Fatalf("flush 后不应命中")
	}
}

func TestChannelDialogCacheSeparatesAuthoritativeListProjection(t *testing.T) {
	c := NewChannelDialogCache(16)
	dialog := domain.ChannelDialog{UserID: 10, ChannelID: 20, TopMessageID: 9}
	c.put(dialog)
	if _, ok := c.getListProjection(10, 20); ok {
		t.Fatal("single-channel cache entry without active-list proof must not enter getDialogs")
	}
	epoch := c.cacheEpoch()
	c.putListProjectionIfEpoch(dialog, true, true, true, epoch)
	entry, ok := c.getListProjection(10, 20)
	if !ok || !entry.listVisible || !entry.topMentioned || !entry.topMediaUnread || !entry.topUnreadProjected {
		t.Fatalf("list projection = %+v,%v", entry, ok)
	}
	entry.dialog.TopMessageID = 99
	again, ok := c.getListProjection(10, 20)
	if !ok || again.dialog.TopMessageID != 9 {
		t.Fatalf("list projection clone isolation = %+v,%v", again, ok)
	}
}

func TestChannelDialogCacheDeleteChannelAndCap(t *testing.T) {
	c := NewChannelDialogCache(2)
	c.put(domain.ChannelDialog{UserID: 1, ChannelID: 10, TopMessageID: 1})
	c.put(domain.ChannelDialog{UserID: 2, ChannelID: 10, TopMessageID: 2})
	c.put(domain.ChannelDialog{UserID: 1, ChannelID: 11, TopMessageID: 3})
	c.deleteChannel(10)
	if _, ok := c.get(1, 10); ok {
		t.Fatalf("deleteChannel 应失效同频道 user 1")
	}
	if _, ok := c.get(2, 10); ok {
		t.Fatalf("deleteChannel 应失效同频道 user 2")
	}
	if got, ok := c.get(1, 11); !ok || got.TopMessageID != 3 {
		t.Fatalf("其它频道不应失效: %+v ok=%v", got, ok)
	}

	c.put(domain.ChannelDialog{UserID: 1, ChannelID: 12, TopMessageID: 4})
	c.put(domain.ChannelDialog{UserID: 1, ChannelID: 13, TopMessageID: 5}) // 超 cap=2:LRU 驱逐最旧的 (1,11)
	if _, ok := c.get(1, 11); ok {
		t.Fatalf("超限后最旧条目 (1,11) 应被驱逐")
	}
	if got, ok := c.get(1, 12); !ok || got.TopMessageID != 4 {
		t.Fatalf("LRU 单条驱逐应保留次新条目 (1,12),证明非整表 flush: %+v ok=%v", got, ok)
	}
	if got, ok := c.get(1, 13); !ok || got.TopMessageID != 5 {
		t.Fatalf("最新条目 (1,13) 应在: %+v ok=%v", got, ok)
	}
}

func TestChannelDialogCacheSingleflightsColdLoad(t *testing.T) {
	c := NewChannelDialogCache(16)
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	load := func() (domain.ChannelDialog, error) {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-release
		return domain.ChannelDialog{UserID: 7, ChannelID: 100, TopMessageID: 11}, nil
	}

	const goroutines = 16
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			dialog, err := c.getOrLoad(context.Background(), 7, 100, load)
			if err != nil {
				errs <- err
				return
			}
			if dialog.UserID != 7 || dialog.ChannelID != 100 || dialog.TopMessageID != 11 {
				errs <- fmt.Errorf("dialog = %+v, want 7/100/top11", dialog)
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
	if _, err := c.getOrLoad(context.Background(), 7, 100, load); err != nil {
		t.Fatalf("cached getOrLoad: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cache hit called load again: calls=%d", got)
	}
}

func TestReadModelChangeListenerInvalidatesChannelDialogCache(t *testing.T) {
	dialogs := NewChannelDialogCache(16)
	dialogs.put(domain.ChannelDialog{UserID: 100, ChannelID: 7, TopMessageID: 1})
	dialogs.put(domain.ChannelDialog{UserID: 100, ChannelID: 8, TopMessageID: 2})
	dialogs.put(domain.ChannelDialog{UserID: 200, ChannelID: 7, TopMessageID: 3})

	listener := NewReadModelChangeListener("", ReadModelCacheSet{ChannelDialogs: dialogs}, nil)
	listener.handlePayload(`{"model":"dialog_light","owner_user_id":100,"peer_type":"channel","peer_id":7}`)
	if _, ok := dialogs.get(100, 7); ok {
		t.Fatalf("dialog_light 事件应失效精确 viewer/channel")
	}
	if got, ok := dialogs.get(100, 8); !ok || got.TopMessageID != 2 {
		t.Fatalf("其它频道不应失效: %+v ok=%v", got, ok)
	}
	if got, ok := dialogs.get(200, 7); !ok || got.TopMessageID != 3 {
		t.Fatalf("其它 viewer 不应失效: %+v ok=%v", got, ok)
	}

	listener.handlePayload(`{"model":"channel_member","owner_user_id":200,"peer_type":"channel","peer_id":7}`)
	if _, ok := dialogs.get(200, 7); ok {
		t.Fatalf("channel_member 事件应失效对应 viewer/channel")
	}

	listener.handlePayload(`{"model":"channel_base","owner_user_id":0,"peer_type":"channel","peer_id":8}`)
	if _, ok := dialogs.get(100, 8); ok {
		t.Fatalf("channel_base 事件应失效该频道所有 viewer dialog")
	}
}
