package donations

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestCoinGeckoLivePrices talks to the real CoinGecko endpoint this package
// depends on, because every part of it that could silently rot -- the URL,
// the response shape, and whether a coin id still resolves -- lives outside
// this repo. A wrong id doesn't fail loudly anywhere else: the refresher
// just keeps the previous rate forever. Opt-in (network access) via
// TELESRV_TEST_LIVE_PRICES=1, skipped by default so an offline build or a
// rate-limited CI run never fails on it.
func TestCoinGeckoLivePrices(t *testing.T) {
	if os.Getenv("TELESRV_TEST_LIVE_PRICES") != "1" {
		t.Skip("set TELESRV_TEST_LIVE_PRICES=1 to hit the real CoinGecko API")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Exactly the ids the admin panel's presets ship with.
	ids := []string{"ethereum", "binancecoin", "polygon-ecosystem-token"}
	prices, err := fetchCoinGeckoPrices(ctx, ids)
	if err != nil {
		t.Fatalf("fetch prices: %v", err)
	}
	for _, id := range ids {
		usd, ok := prices[id]
		if !ok {
			t.Fatalf("price source no longer knows the coin id %q (the panel's preset would silently never refresh)", id)
		}
		if usd <= 0 {
			t.Fatalf("price for %q = %v, want a positive USD price", id, usd)
		}
		t.Logf("%s = $%v", id, usd)
	}
}
