package postgres

import (
	"context"
	"os"
	"testing"

	"go.uber.org/zap/zaptest"

	"telesrv/internal/app/donations"
	"telesrv/internal/domain"
)

// TestDonationsPriceRefreshWritesRate proves the automatic price path end to
// end against real Postgres and the real price API: a chain configured with
// a CoinGecko id gets a live USD rate written to it, while a chain left on
// a manual rate is never touched. Opt-in (network) via
// TELESRV_TEST_LIVE_PRICES=1.
func TestDonationsPriceRefreshWritesRate(t *testing.T) {
	if os.Getenv("TELESRV_TEST_LIVE_PRICES") != "1" {
		t.Skip("set TELESRV_TEST_LIVE_PRICES=1 to hit the real CoinGecko API")
	}
	pool := testPool(t)
	ctx := context.Background()

	suffix := randomSuffix(t)
	testKey, err := donations.ParseEncryptionKey(testDonationWalletKey)
	if err != nil {
		t.Fatalf("parse test wallet key: %v", err)
	}
	store := NewDonationStore(pool)
	svc, err := donations.NewService(ctx, store, testKey)
	if err != nil {
		t.Fatalf("new donations service: %v", err)
	}

	autoKey := "priceauto" + suffix[:6]
	manualKey := "pricemanual" + suffix[:6]
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM donation_chains WHERE chain_key IN ($1,$2)", autoKey, manualKey)
	})
	if _, err := svc.CreateChain(ctx, domain.DonationChain{
		Key: autoKey, Name: "Auto priced", ChainID: 1, RPCURL: "https://example.invalid",
		NativeSymbol: "ETH", NativeDecimals: 18, ConfirmationsRequired: 12,
		ManualUSDRateMicros: 1_000_000, Enabled: true,
		PriceSource: domain.DonationPriceSourceCoinGecko, PriceSourceID: "ethereum",
	}); err != nil {
		t.Fatalf("create auto-priced chain: %v", err)
	}
	if _, err := svc.CreateChain(ctx, domain.DonationChain{
		Key: manualKey, Name: "Manually priced", ChainID: 1, RPCURL: "https://example.invalid",
		NativeSymbol: "ETH", NativeDecimals: 18, ConfirmationsRequired: 12,
		ManualUSDRateMicros: 1_234_000_000, Enabled: true,
	}); err != nil {
		t.Fatalf("create manual chain: %v", err)
	}

	if err := svc.RefreshPrices(ctx, zaptest.NewLogger(t)); err != nil {
		t.Fatalf("refresh prices: %v", err)
	}

	auto, found, err := store.DonationChain(ctx, autoKey)
	if err != nil || !found {
		t.Fatalf("reload auto chain: %v (found=%v)", err, found)
	}
	if auto.ManualUSDRateMicros == 1_000_000 {
		t.Fatalf("auto-priced chain still has its seed rate %d -- the refresher wrote nothing", auto.ManualUSDRateMicros)
	}
	if auto.ManualUSDRateMicros <= 0 {
		t.Fatalf("auto-priced chain rate = %d, want a positive live price", auto.ManualUSDRateMicros)
	}
	if auto.PriceUpdatedAt.IsZero() {
		t.Fatal("auto-priced chain has no price_updated_at, so staleness is invisible in the panel")
	}
	t.Logf("auto-priced rate: %d micros ($%.2f per ETH), updated %s",
		auto.ManualUSDRateMicros, float64(auto.ManualUSDRateMicros)/1e6, auto.PriceUpdatedAt)

	manual, found, err := store.DonationChain(ctx, manualKey)
	if err != nil || !found {
		t.Fatalf("reload manual chain: %v (found=%v)", err, found)
	}
	if manual.ManualUSDRateMicros != 1_234_000_000 {
		t.Fatalf("manually priced chain rate = %d, want it untouched at 1234000000", manual.ManualUSDRateMicros)
	}
	if !manual.PriceUpdatedAt.IsZero() {
		t.Fatalf("manually priced chain got a price_updated_at (%s), want none", manual.PriceUpdatedAt)
	}
}
