package rpc

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/iamxvbaba/td/tg"

	"telesrv/internal/domain"
)

// messages.getStickers(emoticon)：按 emoji 返回匹配的贴纸。emoji→贴纸映射来自各
// 贴纸集的 Packs（Emoticon→DocumentIDs）。为避免每次请求都 ListStickerSets + 遍历
// packs，用一个进程级 TTL 缓存索引；命中 hash 时返回 *NotModified 不解析任何文档。

const (
	// emojiStickerIndexTTL：贴纸集 seed 后基本静态，TTL 内复用索引免重复 PG 查询。
	// 安装/归档贴纸集后索引最多滞后该时长（贴纸搜索可接受）。
	emojiStickerIndexTTL = 5 * time.Minute
	// maxStickersPerEmoji 限制单个 emoji 返回的贴纸数。
	maxStickersPerEmoji = 100
	// maxGreetingStickers 限制官方客户端 greeting 类别的启动预取集合。普通 👋
	// 搜索仍保留完整结果；特殊 👋⭐ 类别只取不同贴纸集的代表项，避免客户端每次
	// 启动在几十个普通 wave pack 之间随机命中并逐步下载整个目录。
	maxGreetingStickers = 12
	// greetingStickerCategoryKey 是去掉 variation selector 后的官方 greeting 标记。
	greetingStickerCategoryKey = "👋⭐"
)

// emojiStickerIndex 是 emoji→贴纸文档 id 的 TTL 缓存索引。
type emojiStickerIndex struct {
	mu      sync.RWMutex
	now     func() time.Time
	ttl     time.Duration
	builtAt time.Time
	ready   bool
	byEmoji map[string][]int64
}

func newEmojiStickerIndex(now func() time.Time) *emojiStickerIndex {
	return &emojiStickerIndex{now: now, ttl: emojiStickerIndexTTL, byEmoji: map[string][]int64{}}
}

// lookup 返回某 emoji 的贴纸文档 id；索引未建或过期时用 build 重建。
// perf：重建在锁外执行（build 读目录缓存，I/O 不在临界区），避免 TTL 过期点把所有
// 并发请求堵在互斥锁上。并发 stale 请求可能各自 build 一次（目录缓存自身 singleflight
// 去重 PG，pack 遍历重复但廉价）。build 返回 nil（如目录读失败）时保留旧索引。
func (idx *emojiStickerIndex) lookup(emoticon string, build func() map[string][]int64) []int64 {
	idx.mu.RLock()
	fresh := idx.ready && idx.now().Sub(idx.builtAt) < idx.ttl
	if fresh {
		out := append([]int64(nil), idx.byEmoji[emoticon]...)
		idx.mu.RUnlock()
		return out
	}
	idx.mu.RUnlock()

	next := build() // 锁外重建

	idx.mu.Lock()
	defer idx.mu.Unlock()
	// 复查：可能已有并发者在锁外重建并先一步换入。
	stillStale := !idx.ready || idx.now().Sub(idx.builtAt) >= idx.ttl
	if stillStale && next != nil {
		idx.byEmoji = next
		idx.builtAt = idx.now()
		idx.ready = true
	}
	return append([]int64(nil), idx.byEmoji[emoticon]...)
}

// normalizeStickerEmoticon 去掉变体选择符（U+FE0F/U+FE0E）并裁剪空白，使客户端发的
// "👍️"（带 VS16）能匹配 pack 里的 "👍"。
func normalizeStickerEmoticon(e string) string {
	e = strings.ReplaceAll(e, "️", "")
	e = strings.ReplaceAll(e, "︎", "")
	return strings.TrimSpace(e)
}

// normalizeStickerSearchEmoticon 解析官方客户端通过 messages.getStickers
// 传递的特殊贴纸类别标记。TDesktop、DrKLO Android 与 Telegram-iOS 都使用
// wave+star 获取 greeting、double-star 获取 premium preview、folder+star 获取
// premium/cloud catalog；它们不是普通复合 emoji。greeting 在索引中维护独立的
// 有界代表集合，另外两个类别仍映射到 seed pack 的基础键。
//
// 只匹配这三个完整标记，不能把任意复合 emoji 拆分成单个 emoji，否则会改变普通
// sticker search 的精确匹配语义。先去掉 variation selector，可同时接纳三端的
// Unicode 表示差异。
func normalizeStickerSearchEmoticon(e string) string {
	e = normalizeStickerEmoticon(e)
	switch e {
	case greetingStickerCategoryKey:
		return greetingStickerCategoryKey
	case "⭐⭐":
		return "⭐"
	case "📂⭐":
		return "📂"
	default:
		return e
	}
}

func (r *Router) onMessagesGetStickers(ctx context.Context, req *tg.MessagesGetStickersRequest) (tg.MessagesStickersClass, error) {
	if req == nil || r.deps.Files == nil || r.emojiStickers == nil {
		return &tg.MessagesStickers{Hash: 0, Stickers: []tg.DocumentClass{}}, nil
	}
	searchKey := normalizeStickerSearchEmoticon(req.Emoticon)
	docIDs := r.emojiStickers.lookup(searchKey, func() map[string][]int64 {
		return r.buildEmojiStickerIndex(ctx)
	})
	limit := maxStickersPerEmoji
	if searchKey == greetingStickerCategoryKey {
		limit = maxGreetingStickers
	}
	if len(docIDs) > limit {
		docIDs = docIDs[:limit]
	}
	catalogHash := int64(tdesktopCountHash(docIDs))
	if req.Hash != 0 && req.Hash == catalogHash {
		// perf 短路：与客户端缓存一致，不解析任何文档。
		// 硬契约：仅在 hash!=0 时可返回 NotModified——DrKLO premium 预览贴纸预取发
		// hash=0 且无条件强转 TL_messages_stickers，对 notModified 会 ClassCastException
		// 闪退；hash=0 一律走下方完整响应。勿"优化"成对 hash=0 也返回 NotModified。
		return &tg.MessagesStickersNotModified{}, nil
	}
	if len(docIDs) == 0 {
		return &tg.MessagesStickers{Hash: catalogHash, Stickers: []tg.DocumentClass{}}, nil
	}
	docs, err := r.deps.Files.GetDocuments(ctx, docIDs)
	if err != nil {
		return nil, internalErr()
	}
	byID := documentsByID(docs)
	ordered := make([]domain.Document, 0, len(docIDs))
	for _, id := range docIDs { // 保持索引顺序
		if d, ok := byID[id]; ok {
			ordered = append(ordered, d)
		}
	}
	return &tg.MessagesStickers{Hash: catalogHash, Stickers: tgDocuments(ordered)}, nil
}

// buildEmojiStickerIndex 从所有（未归档）常规贴纸集的 Packs 构建 emoji→去重有序 docIDs。
// 返回 nil 表示构建失败（保留旧索引）。
func (r *Router) buildEmojiStickerIndex(ctx context.Context) map[string][]int64 {
	// perf：从目录缓存读集（与 getAllStickers/featured 共用，TTL 内不打 PG）。
	sets := r.stickerCatalogSets(ctx, domain.StickerSetKindStickers)
	if sets == nil {
		return nil // 目录读失败：保留旧索引（nil=不替换）
	}
	byEmoji := make(map[string][]int64)
	seen := make(map[string]map[int64]struct{})
	for _, s := range sets {
		if s.Archived {
			continue
		}
		greetingAdded := false
		for _, pack := range s.Packs {
			key := normalizeStickerEmoticon(pack.Emoticon)
			if key == "" {
				continue
			}
			dedup := seen[key]
			if dedup == nil {
				dedup = make(map[int64]struct{})
				seen[key] = dedup
			}
			for _, id := range pack.DocumentIDs {
				if id == 0 {
					continue
				}
				if _, ok := dedup[id]; ok {
					continue
				}
				dedup[id] = struct{}{}
				byEmoji[key] = append(byEmoji[key], id)
			}
			// greeting 是客户端启动预取目录，不等同于普通 👋 搜索。每个贴纸集
			// 只放一个代表项，随后在响应边界再限制总数；这样既保留多样性，也
			// 不会把所有普通 wave sticker 暴露为启动资源候选。
			if key == "👋" && !greetingAdded {
				greetingSeen := seen[greetingStickerCategoryKey]
				if greetingSeen == nil {
					greetingSeen = make(map[int64]struct{})
					seen[greetingStickerCategoryKey] = greetingSeen
				}
				for _, id := range pack.DocumentIDs {
					if id == 0 {
						continue
					}
					if _, ok := greetingSeen[id]; ok {
						continue
					}
					greetingSeen[id] = struct{}{}
					byEmoji[greetingStickerCategoryKey] = append(byEmoji[greetingStickerCategoryKey], id)
					greetingAdded = true
					break
				}
			}
		}
	}
	return byEmoji
}
