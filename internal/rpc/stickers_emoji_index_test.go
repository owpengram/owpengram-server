package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap/zaptest"

	"telesrv/internal/domain"
)

func emojiStickerRouter(t *testing.T) *Router {
	t.Helper()
	files := &fakeFiles{
		docs: map[int64]domain.Document{
			201: {ID: 201, AccessHash: 1, MimeType: "application/x-tgsticker", Attributes: []domain.DocumentAttribute{{Kind: domain.DocAttrSticker}}},
			202: {ID: 202, AccessHash: 2, MimeType: "application/x-tgsticker", Attributes: []domain.DocumentAttribute{{Kind: domain.DocAttrSticker}}},
			301: {ID: 301, AccessHash: 3, MimeType: "application/x-tgsticker", Attributes: []domain.DocumentAttribute{{Kind: domain.DocAttrSticker}}},
			401: {ID: 401, AccessHash: 4, MimeType: "application/x-tgsticker", Attributes: []domain.DocumentAttribute{{Kind: domain.DocAttrSticker}}},
			402: {ID: 402, AccessHash: 5, MimeType: "application/x-tgsticker", Attributes: []domain.DocumentAttribute{{Kind: domain.DocAttrSticker}}},
			403: {ID: 403, AccessHash: 6, MimeType: "application/x-tgsticker", Attributes: []domain.DocumentAttribute{{Kind: domain.DocAttrSticker}}},
		},
		sets: map[domain.StickerSetKind][]domain.StickerSet{
			domain.StickerSetKindStickers: {
				{ID: 10, Hash: 1, Packs: []domain.StickerPack{
					{Emoticon: "👍", DocumentIDs: []int64{201, 202}},
					{Emoticon: "🔥", DocumentIDs: []int64{301}},
					{Emoticon: "👋", DocumentIDs: []int64{401}},
					{Emoticon: "⭐", DocumentIDs: []int64{402}},
					{Emoticon: "📂", DocumentIDs: []int64{403}},
				}},
				{ID: 12, Hash: 2, Archived: true, Packs: []domain.StickerPack{
					{Emoticon: "👍", DocumentIDs: []int64{999}}, // 归档集应被排除
				}},
			},
		},
	}
	return New(Config{}, Deps{Files: files}, zaptest.NewLogger(t), clock.System)
}

func stickerDocIDs(t *testing.T, res tg.MessagesStickersClass) []int64 {
	t.Helper()
	full, ok := res.(*tg.MessagesStickers)
	if !ok {
		t.Fatalf("res = %T, want *tg.MessagesStickers", res)
	}
	ids := make([]int64, 0, len(full.Stickers))
	for _, d := range full.Stickers {
		if doc, ok := d.(*tg.Document); ok {
			ids = append(ids, doc.ID)
		}
	}
	return ids
}

// TestMessagesGetStickersByEmoji 验证 emoji→贴纸索引检索 + 归档排除 + hash notModified
// + hash=0 永不返回 NotModified（崩溃安全契约）+ 变体选择符归一化。
func TestMessagesGetStickersByEmoji(t *testing.T) {
	r := emojiStickerRouter(t)
	ctx := WithUserID(context.Background(), 1000000001)

	first, err := r.onMessagesGetStickers(ctx, &tg.MessagesGetStickersRequest{Emoticon: "👍"})
	if err != nil {
		t.Fatalf("getStickers 👍: %v", err)
	}
	ids := stickerDocIDs(t, first)
	if len(ids) != 2 || ids[0] != 201 || ids[1] != 202 {
		t.Fatalf("👍 stickers = %v, want [201 202]（归档集 999 排除）", ids)
	}
	hash := first.(*tg.MessagesStickers).Hash
	if hash == 0 {
		t.Fatal("hash must be non-zero for cache round-trips")
	}

	// hash 命中 → NotModified。
	if again, err := r.onMessagesGetStickers(ctx, &tg.MessagesGetStickersRequest{Emoticon: "👍", Hash: hash}); err != nil {
		t.Fatalf("getStickers 👍 hash: %v", err)
	} else if _, ok := again.(*tg.MessagesStickersNotModified); !ok {
		t.Fatalf("re-get with hash = %T, want NotModified", again)
	}

	// 崩溃安全：hash=0 永远返回完整响应（DrKLO premium 预览强转，notModified 会闪退）。
	if zero, err := r.onMessagesGetStickers(ctx, &tg.MessagesGetStickersRequest{Emoticon: "👍", Hash: 0}); err != nil {
		t.Fatalf("getStickers 👍 hash=0: %v", err)
	} else if _, ok := zero.(*tg.MessagesStickers); !ok {
		t.Fatalf("hash=0 = %T, want full *tg.MessagesStickers (never NotModified)", zero)
	}

	// 另一个 emoji。
	if ids := stickerDocIDs(t, mustStickers(t, r, ctx, "🔥", 0)); len(ids) != 1 || ids[0] != 301 {
		t.Fatalf("🔥 stickers = %v, want [301]", ids)
	}

	// 变体选择符归一化：👍 + VS16 仍匹配。
	if ids := stickerDocIDs(t, mustStickers(t, r, ctx, "👍️", 0)); len(ids) != 2 {
		t.Fatalf("👍+VS16 stickers = %v, want 2 (variation selector 归一化)", ids)
	}

	// 未知 emoji → 空。
	if ids := stickerDocIDs(t, mustStickers(t, r, ctx, "🦄", 0)); len(ids) != 0 {
		t.Fatalf("unknown emoji stickers = %v, want empty", ids)
	}
}

// TestMessagesGetStickersSpecialCategories 固定 TDesktop、DrKLO Android 与
// Telegram-iOS 共用的特殊类别标记。这些标记不是普通复合 emoji：服务端应把它们
// 解析到对应的基础目录，同时不能拆分任意多 emoji 查询。
func TestMessagesGetStickersSpecialCategories(t *testing.T) {
	r := emojiStickerRouter(t)
	ctx := WithUserID(context.Background(), 1000000001)

	tests := []struct {
		name      string
		emoticon  string
		base      string
		wantDocID int64
	}{
		{name: "greeting", emoticon: "👋⭐️", base: "👋", wantDocID: 401},
		{name: "greeting_without_vs16", emoticon: "👋⭐", base: "👋", wantDocID: 401},
		{name: "premium_preview", emoticon: "⭐️⭐️", base: "⭐", wantDocID: 402},
		{name: "premium_preview_mixed_vs16", emoticon: "⭐⭐️", base: "⭐", wantDocID: 402},
		{name: "all_premium", emoticon: "📂⭐️", base: "📂", wantDocID: 403},
		{name: "all_premium_without_vs16", emoticon: "📂⭐", base: "📂", wantDocID: 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := mustStickers(t, r, ctx, tt.base, 0).(*tg.MessagesStickers)
			got := mustStickers(t, r, ctx, tt.emoticon, 0)
			ids := stickerDocIDs(t, got)
			if len(ids) != 1 || ids[0] != tt.wantDocID {
				t.Fatalf("%q stickers = %v, want [%d]", tt.emoticon, ids, tt.wantDocID)
			}
			full := got.(*tg.MessagesStickers)
			if full.Hash == 0 || full.Hash != base.Hash {
				t.Fatalf("%q hash = %d, base %q hash = %d", tt.emoticon, full.Hash, tt.base, base.Hash)
			}
			if again, err := r.onMessagesGetStickers(ctx, &tg.MessagesGetStickersRequest{
				Emoticon: tt.emoticon,
				Hash:     full.Hash,
			}); err != nil {
				t.Fatalf("getStickers %q hash: %v", tt.emoticon, err)
			} else if _, ok := again.(*tg.MessagesStickersNotModified); !ok {
				t.Fatalf("getStickers %q with matching hash = %T, want NotModified", tt.emoticon, again)
			}
		})
	}

	if ids := stickerDocIDs(t, mustStickers(t, r, ctx, "👋🔥", 0)); len(ids) != 0 {
		t.Fatalf("ordinary compound emoji stickers = %v, want empty", ids)
	}
}

// TestMessagesGetStickersGreetingCategoryIsBounded 固定 greeting 与普通 👋 搜索的
// 边界：每个贴纸集只贡献一个 greeting 代表项，且启动预取目录总数有界；普通搜索
// 不受影响，避免为了优化启动资源而缩窄用户主动搜索结果。
func TestMessagesGetStickersGreetingCategoryIsBounded(t *testing.T) {
	docs := make(map[int64]domain.Document)
	sets := make([]domain.StickerSet, 0, maxGreetingStickers+4)
	for i := 0; i < maxGreetingStickers+4; i++ {
		first := int64(10_000 + i*2)
		second := first + 1
		docs[first] = domain.Document{ID: first, AccessHash: first + 100_000, DCID: 2}
		docs[second] = domain.Document{ID: second, AccessHash: second + 100_000, DCID: 2}
		sets = append(sets, domain.StickerSet{
			ID:          int64(1_000 + i),
			Kind:        domain.StickerSetKindStickers,
			DocumentIDs: []int64{first, second},
			Packs: []domain.StickerPack{{
				Emoticon:    "👋",
				DocumentIDs: []int64{first, second},
			}},
		})
	}
	r := New(Config{}, Deps{Files: &fakeFiles{
		docs: docs,
		sets: map[domain.StickerSetKind][]domain.StickerSet{
			domain.StickerSetKindStickers: sets,
		},
	}}, zaptest.NewLogger(t), clock.System)
	ctx := WithUserID(context.Background(), 1000000001)

	greeting := mustStickers(t, r, ctx, "👋⭐️", 0).(*tg.MessagesStickers)
	greetingIDs := stickerDocIDs(t, greeting)
	if len(greetingIDs) != maxGreetingStickers {
		t.Fatalf("greeting documents = %d, want bounded %d", len(greetingIDs), maxGreetingStickers)
	}
	for i, id := range greetingIDs {
		want := int64(10_000 + i*2)
		if id != want {
			t.Fatalf("greeting document[%d] = %d, want per-set representative %d", i, id, want)
		}
	}

	wave := mustStickers(t, r, ctx, "👋", 0).(*tg.MessagesStickers)
	if got, want := len(stickerDocIDs(t, wave)), (maxGreetingStickers+4)*2; got != want {
		t.Fatalf("ordinary wave search documents = %d, want full %d", got, want)
	}
	if greeting.Hash == 0 || greeting.Hash == wave.Hash {
		t.Fatalf("greeting/wave hashes = %d/%d, want independent non-zero catalogs", greeting.Hash, wave.Hash)
	}
	if cached, err := r.onMessagesGetStickers(ctx, &tg.MessagesGetStickersRequest{
		Emoticon: "👋⭐",
		Hash:     greeting.Hash,
	}); err != nil {
		t.Fatalf("get bounded greeting with hash: %v", err)
	} else if _, ok := cached.(*tg.MessagesStickersNotModified); !ok {
		t.Fatalf("bounded greeting matching hash = %T, want NotModified", cached)
	}
}

func mustStickers(t *testing.T, r *Router, ctx context.Context, emoticon string, hash int64) tg.MessagesStickersClass {
	t.Helper()
	res, err := r.onMessagesGetStickers(ctx, &tg.MessagesGetStickersRequest{Emoticon: emoticon, Hash: hash})
	if err != nil {
		t.Fatalf("getStickers %q: %v", emoticon, err)
	}
	return res
}
