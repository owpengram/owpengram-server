package postgres

import (
	"context"

	"telesrv/internal/domain"
	"telesrv/internal/readmodelcache"
)

// ChannelRowCache 是「共享频道行」(channels 表一行 → domain.Channel) 的进程内强一致缓存,
// 由统一缓存原语 readmodelcache.Cache 承载(LRU 单条驱逐 / epoch 守卫 / singleflight 内建)。
//
// 只缓存全 viewer 通用、变更不频繁的频道行；per-viewer 的 member(权限/读态) 由
// ChannelMemberCache 以 (channel_id,user_id) 单独隔离,dialog(top/未读)、boost 仍实时查 PG。
//
// 一致性靠统一 read-model PG LISTEN/NOTIFY 实时失效(见 ReadModelChangeListener)而非 TTL：
// channels 表上的 read-model 触发器在提交时发布 channel_base 事件,listener 收到即 delete
// 对应条目；listener (重)连接时整表 flush,兜住断连窗口内丢失的通知。无 TTL,故 epoch 守卫
// 是防永久 stale 的唯一保障(已内建于原语)。
//
// 仅在连接池句柄(非事务)上消费(见 ChannelStore.cacheActive)：事务内一律实时读,保证读己写。
type ChannelRowCache struct {
	cache *readmodelcache.Cache[int64, domain.Channel]
}

// NewChannelRowCache 创建容量为 max 的频道行缓存；max<=0 返回 nil(表示禁用,调用方按 nil 跳过)。
func NewChannelRowCache(max int) *ChannelRowCache {
	cache := readmodelcache.New[int64, domain.Channel](readmodelcache.Config[int64, domain.Channel]{
		MaxEntries: max,
		Clone:      cloneChannelRow,
	})
	if cache == nil {
		return nil
	}
	return &ChannelRowCache{cache: cache}
}

func (c *ChannelRowCache) get(id int64) (domain.Channel, bool) {
	if c == nil || id == 0 {
		return domain.Channel{}, false
	}
	return c.cache.Peek(id)
}

func (c *ChannelRowCache) getOrLoad(ctx context.Context, id int64, load func() (domain.Channel, error)) (domain.Channel, error) {
	if c == nil {
		return load()
	}
	return c.cache.GetOrLoad(ctx, id, load)
}

// getOrLoadBatch resolves a page of shared channel rows with one database read
// for all misses. A zero-ID Channel is the negative-cache sentinel for an ID
// that disappeared between the owner-state query and shared-row hydration;
// channel_base invalidation removes both positive and negative entries.
func (c *ChannelRowCache) getOrLoadBatch(
	ctx context.Context,
	ids []int64,
	load func(context.Context, []int64) (map[int64]domain.Channel, error),
) (map[int64]domain.Channel, error) {
	if c == nil {
		return load(ctx, ids)
	}
	return c.cache.GetOrLoadBatch(
		ctx,
		ids,
		func(int64) (int64, bool) { return 0, true },
		func(ctx context.Context, missing []int64) (map[int64]domain.Channel, error) {
			loaded, err := load(ctx, missing)
			if err != nil {
				return nil, err
			}
			// GetOrLoadBatch requires an explicit value for every key so absent
			// rows do not immediately stampede the database again.
			for _, id := range missing {
				if _, ok := loaded[id]; !ok {
					loaded[id] = domain.Channel{}
				}
			}
			return loaded, nil
		},
	)
}

func (c *ChannelRowCache) put(ch domain.Channel) {
	if c == nil || ch.ID == 0 {
		return
	}
	c.cache.Store(ch.ID, ch)
}

// delete 失效单个频道(listener 收到该 id 的变更通知时调用)。
func (c *ChannelRowCache) delete(id int64) {
	if c == nil || id == 0 {
		return
	}
	c.cache.Invalidate(id)
}

// flush 清空整表(listener 每次 (重)连接、建立 LISTEN 后调用,兜住断连窗口内丢失的通知)。
func (c *ChannelRowCache) flush() {
	if c == nil {
		return
	}
	c.cache.Flush()
}

// cloneChannelRow 深拷频道行的可变字段(切片),使缓存与调用方互不别名污染。
func cloneChannelRow(ch domain.Channel) domain.Channel {
	if ch.PhotoStripped != nil {
		ch.PhotoStripped = append([]byte(nil), ch.PhotoStripped...)
	}
	if ch.ReactionPolicy.Emoticons != nil {
		ch.ReactionPolicy.Emoticons = append([]string(nil), ch.ReactionPolicy.Emoticons...)
	}
	if ch.ReactionPolicy.CustomEmojiIDs != nil {
		ch.ReactionPolicy.CustomEmojiIDs = append([]int64(nil), ch.ReactionPolicy.CustomEmojiIDs...)
	}
	ch.Wallpaper = domain.CloneWallpaperPtr(ch.Wallpaper)
	return ch
}
