package userprojection

import (
	"context"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/readmodelcache"
)

const (
	// DefaultPhotoCacheTTL 是头像投影缓存的兜底有效期；正常正确性依赖写入侧触发
	// read_model_versions/NOTIFY 后显式失效，TTL 只负责覆盖漏通知或手工改库。
	// 10s 会让 60s 的 10k 登录突发反复丢失稳定负结果，不能作为正常新鲜度机制。
	DefaultPhotoCacheTTL = 24 * time.Hour

	// DefaultPhotoCacheMaxEntries 覆盖 10k owner 的 profile/fallback 两种 key，
	// 并为共享对话引用保留余量。底层是逐项 LRU，不允许整表清空。
	DefaultPhotoCacheMaxEntries = 200_000
)

// combinedPhotoProvider 是同时具备 batch 与 kind 两种头像查询能力的底层 provider（postgres
// MediaStore 即满足）。
type combinedPhotoProvider interface {
	ProfilePhotoProvider
	ProfilePhotoKindProvider
}

type photoCacheKey struct {
	ownerType domain.PeerType
	ownerID   int64
	kind      domain.ProfilePhotoKind
}

// photoCacheValue 含负缓存:has=false 表示「已查过、该 owner 无此 kind 头像」,
// 避免无头像 owner 反复打 PG。
type photoCacheValue struct {
	ref domain.ProfilePhotoRef
	has bool
}

// CachedPhotoProvider 给 owner 的当前 profile/fallback 头像查询加一层短 TTL 进程内缓存,
// 由统一缓存原语承载(LRU 单条驱逐 / epoch 守卫 / clone;批量经 GetOrLoadBatch)。
// projectBatch / ForViewers 对每批 owner 固定打 2 次 CurrentProfilePhotosKind（profile+fallback），
// 且 base user 命中 redis 也不短路——高频「返回用户」的 RPC（getUsers 等）会把这两条头像查询
// 刷到与 RPC 同频。这里按 (ownerType, ownerID, kind) 缓存结果（含负结果），命中 owner 不进 PG。
//
// 注意：只缓存 owner-only 的 profile/fallback；personal photo 是 per-viewer 的（contacts.PersonalPhotos），
// 不经此 provider，故无跨 viewer 串号风险。隐私裁剪发生在投影之后、缓存之外，缓存的是 owner 原始
// 头像 ref 而非投影结果，故不会把某 viewer 的可见性固化给其他 viewer。
type CachedPhotoProvider struct {
	inner combinedPhotoProvider
	cache *readmodelcache.Cache[photoCacheKey, photoCacheValue]
}

// NewCachedPhotoProvider 包装底层 provider；ttl<=0 用 DefaultPhotoCacheTTL。
func NewCachedPhotoProvider(inner combinedPhotoProvider, ttl time.Duration) *CachedPhotoProvider {
	return NewCachedPhotoProviderWithMaxEntries(inner, ttl, DefaultPhotoCacheMaxEntries)
}

func NewCachedPhotoProviderWithMaxEntries(inner combinedPhotoProvider, ttl time.Duration, maxEntries int) *CachedPhotoProvider {
	return newCachedPhotoProvider(inner, ttl, maxEntries, nil)
}

// newCachedPhotoProviderWithClock 允许注入时钟,仅供测试确定地推进 TTL;now=nil 用真实时钟。
func newCachedPhotoProviderWithClock(inner combinedPhotoProvider, ttl time.Duration, now func() time.Time) *CachedPhotoProvider {
	return newCachedPhotoProvider(inner, ttl, DefaultPhotoCacheMaxEntries, now)
}

func newCachedPhotoProvider(inner combinedPhotoProvider, ttl time.Duration, maxEntries int, now func() time.Time) *CachedPhotoProvider {
	if inner == nil {
		return nil
	}
	if ttl <= 0 {
		ttl = DefaultPhotoCacheTTL
	}
	if maxEntries <= 0 {
		maxEntries = DefaultPhotoCacheMaxEntries
	}
	return &CachedPhotoProvider{
		inner: inner,
		cache: readmodelcache.New[photoCacheKey, photoCacheValue](readmodelcache.Config[photoCacheKey, photoCacheValue]{
			MaxEntries: maxEntries,
			TTL:        ttl,
			Now:        now,
			Clone:      clonePhotoCacheValue,
		}),
	}
}

// CurrentProfilePhotosKind 是被缓存的热路径：先按 owner 取缓存，仅未命中的 owner 批量查底层。
func (c *CachedPhotoProvider) CurrentProfilePhotosKind(ctx context.Context, ownerType domain.PeerType, ownerIDs []int64, kind domain.ProfilePhotoKind) (map[int64]domain.ProfilePhotoRef, error) {
	if c == nil {
		return nil, nil
	}
	keys := make([]photoCacheKey, 0, len(ownerIDs))
	for _, id := range ownerIDs {
		keys = append(keys, photoCacheKey{ownerType: ownerType, ownerID: id, kind: kind})
	}
	values, err := c.cache.GetOrLoadBatch(ctx, keys,
		func(photoCacheKey) (int64, bool) { return 0, true }, // 纯 TTL,无版本闸门
		func(ctx context.Context, missing []photoCacheKey) (map[photoCacheKey]photoCacheValue, error) {
			missingIDs := make([]int64, len(missing))
			for i, k := range missing {
				missingIDs[i] = k.ownerID
			}
			refs, err := c.inner.CurrentProfilePhotosKind(ctx, ownerType, missingIDs, kind)
			if err != nil {
				return nil, err
			}
			out := make(map[photoCacheKey]photoCacheValue, len(missing))
			for _, k := range missing {
				ref, has := refs[k.ownerID]
				out[k] = photoCacheValue{ref: ref, has: has}
			}
			return out, nil
		})
	if err != nil {
		return nil, err
	}
	out := make(map[int64]domain.ProfilePhotoRef, len(values))
	for key, v := range values {
		if v.has {
			out[key.ownerID] = v.ref
		}
	}
	return out, nil
}

// CurrentProfilePhotos 是非 kind 的回退路径——projectBatch 仅在 provider 不实现
// ProfilePhotoKindProvider 时才走它，而本装饰器实现了 kind 接口，故生产不会命中此方法；
// 直接透传，不缓存，保持行为不变。
func (c *CachedPhotoProvider) CurrentProfilePhotos(ctx context.Context, ownerType domain.PeerType, ownerIDs []int64) (map[int64]domain.ProfilePhotoRef, error) {
	return c.inner.CurrentProfilePhotos(ctx, ownerType, ownerIDs)
}

func (c *CachedPhotoProvider) InvalidateOwner(ownerType domain.PeerType, ownerID int64) {
	if c == nil || ownerID == 0 {
		return
	}
	c.cache.Invalidate(
		photoCacheKey{ownerType: ownerType, ownerID: ownerID, kind: domain.ProfilePhotoKindProfile},
		photoCacheKey{ownerType: ownerType, ownerID: ownerID, kind: domain.ProfilePhotoKindFallback},
	)
}

func (c *CachedPhotoProvider) FlushReadModelCache() {
	if c == nil {
		return
	}
	c.cache.Flush()
}

func clonePhotoCacheValue(v photoCacheValue) photoCacheValue {
	if v.ref.Stripped != nil {
		v.ref.Stripped = append([]byte(nil), v.ref.Stripped...)
	}
	return v
}
