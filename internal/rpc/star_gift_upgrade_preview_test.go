package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/iamxvbaba/td/clock"
	"go.uber.org/zap/zaptest"

	"telesrv/internal/domain"
)

// previewGifts answers only the one call the handler makes; the embedded nil
// interface makes any other call fail loudly instead of being silently ignored.
type previewGifts struct {
	GiftsService
	preview domain.StarGiftUpgradePreview
	found   bool
}

func (g previewGifts) CollectiblePreviewSample(context.Context, int64) (domain.StarGiftUpgradePreview, bool, error) {
	return g.preview, g.found, nil
}

// Web K reads the upgrade button's price from next_prices[0] and throws on an
// empty list, so the preview must carry the price in effect without inventing
// a price decay for clients that show one whenever `prices` is filled.
func TestGetStarGiftUpgradePreviewCarriesTheCurrentPrice(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := New(Config{}, Deps{Gifts: previewGifts{
		preview: domain.StarGiftUpgradePreview{GiftID: 7, UpgradeStars: 80},
		found:   true,
	}}, zaptest.NewLogger(t), fixedClock{now: now})

	got, err := r.onPaymentsGetStarGiftUpgradePreview(context.Background(), 7)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(got.NextPrices) != 1 || got.NextPrices[0].UpgradeStars != 80 || got.NextPrices[0].Date != int(now.Unix()) {
		t.Fatalf("next_prices = %+v, want the single current price 80 at %d", got.NextPrices, now.Unix())
	}
	if len(got.Prices) != 0 {
		t.Fatalf("prices = %+v, want none (a flat price has no schedule to show)", got.Prices)
	}
}

func TestGetStarGiftUpgradePreviewUnknownGift(t *testing.T) {
	r := New(Config{}, Deps{Gifts: previewGifts{}}, zaptest.NewLogger(t), clock.System)
	if _, err := r.onPaymentsGetStarGiftUpgradePreview(context.Background(), 7); err == nil {
		t.Fatal("unknown gift answered without an error")
	}
}
