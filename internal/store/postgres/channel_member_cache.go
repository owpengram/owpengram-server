package postgres

import (
	"context"

	"telesrv/internal/domain"
	"telesrv/internal/readmodelcache"
)

type channelMemberCacheKey struct {
	channelID int64
	userID    int64
}

// ChannelMemberCache 是 channel_members 表一行的进程内 read-model 缓存,
// 由统一缓存原语 readmodelcache.Cache 承载(LRU 单条驱逐 / epoch 守卫 / singleflight 内建)。
//
// 它只在连接池读路径上使用,事务内全部绕过;一致性由 read_model trigger + LISTEN/NOTIFY
// 失效驱动。channel_members 同时承载 member access 与 read boundary 两类状态,因此 listener
// 断线重连会整表 flush,避免通知丢失时产生权限/可见性旧读。epoch 守卫确保被踢成员的失效
// 不会被一次在飞 load 的 stale Active 行覆盖——否则被踢成员可继续读历史,构成访问控制旁路。
type ChannelMemberCache struct {
	cache *readmodelcache.Cache[channelMemberCacheKey, domain.ChannelMember]
}

// NewChannelMemberCache 创建容量为 max 的成员缓存;max<=0 返回 nil(禁用,调用方按 nil 跳过)。
// domain.ChannelMember 为扁平值(无共享可变字段),故不设 Clone。
func NewChannelMemberCache(max int) *ChannelMemberCache {
	cache := readmodelcache.New[channelMemberCacheKey, domain.ChannelMember](readmodelcache.Config[channelMemberCacheKey, domain.ChannelMember]{
		MaxEntries: max,
	})
	if cache == nil {
		return nil
	}
	return &ChannelMemberCache{cache: cache}
}

func (c *ChannelMemberCache) get(channelID, userID int64) (domain.ChannelMember, bool) {
	if c == nil || channelID == 0 || userID == 0 {
		return domain.ChannelMember{}, false
	}
	return c.cache.Peek(channelMemberCacheKey{channelID: channelID, userID: userID})
}

func (c *ChannelMemberCache) getOrLoad(ctx context.Context, channelID, userID int64, load func() (domain.ChannelMember, error)) (domain.ChannelMember, error) {
	if c == nil {
		return load()
	}
	return c.cache.GetOrLoad(ctx, channelMemberCacheKey{channelID: channelID, userID: userID}, load)
}

func (c *ChannelMemberCache) put(member domain.ChannelMember) {
	if c == nil || member.ChannelID == 0 || member.UserID == 0 {
		return
	}
	c.cache.Store(channelMemberCacheKey{channelID: member.ChannelID, userID: member.UserID}, member)
}

func (c *ChannelMemberCache) cacheEpoch() uint64 {
	if c == nil {
		return 0
	}
	return c.cache.LoadEpoch()
}

// putIfEpoch prevents a materialized owner snapshot that raced a membership
// invalidation from restoring stale access rights after the listener advanced
// the cache epoch.
func (c *ChannelMemberCache) putIfEpoch(member domain.ChannelMember, loadEpoch uint64) {
	if c == nil || member.ChannelID == 0 || member.UserID == 0 {
		return
	}
	c.cache.StoreIfEpoch(
		channelMemberCacheKey{channelID: member.ChannelID, userID: member.UserID},
		member,
		loadEpoch,
	)
}

func (c *ChannelMemberCache) delete(channelID, userID int64) {
	if c == nil || channelID == 0 || userID == 0 {
		return
	}
	c.cache.Invalidate(channelMemberCacheKey{channelID: channelID, userID: userID})
}

func (c *ChannelMemberCache) deleteChannel(channelID int64) {
	if c == nil || channelID == 0 {
		return
	}
	c.cache.InvalidateWhere(func(k channelMemberCacheKey) bool { return k.channelID == channelID })
}

func (c *ChannelMemberCache) flush() {
	if c == nil {
		return
	}
	c.cache.Flush()
}
