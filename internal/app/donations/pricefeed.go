package donations

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// coinGeckoSimplePriceURL is CoinGecko's free, key-less spot price endpoint.
// One request covers every coin id at once, so a deployment watching six
// networks still makes a single call per refresh -- well inside the free
// tier's rate limit even at a one-minute interval.
const coinGeckoSimplePriceURL = "https://api.coingecko.com/api/v3/simple/price"

// priceFetchTimeout bounds one refresh: a hanging price API must never stall
// the refresher loop, and a missed refresh is harmless (the previous rate
// stays in place and its age is visible as price_updated_at in the panel).
const priceFetchTimeout = 20 * time.Second

// StartPriceRefresher keeps every auto-priced chain's USD rate fresh until
// ctx is canceled, refreshing once immediately so a newly configured feed
// doesn't wait a full interval for its first number. Chains left on a
// manual rate are never touched.
func (s *Service) StartPriceRefresher(ctx context.Context, interval time.Duration, log *zap.Logger) {
	if s == nil || s.store == nil {
		return
	}
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	if log == nil {
		log = zap.NewNop()
	}
	if err := s.RefreshPrices(ctx, log); err != nil && ctx.Err() == nil {
		log.Warn("initial donation price refresh failed", zap.Error(err))
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := s.RefreshPrices(ctx, log); err != nil && ctx.Err() == nil {
			log.Warn("donation price refresh failed", zap.Error(err))
		}
	}
}

// RefreshPrices fetches one spot price per auto-priced chain and writes it
// back. A chain whose id the source doesn't know is reported and left on
// its previous rate rather than being zeroed -- a zero rate would silently
// stop crediting every deposit on that chain.
func (s *Service) RefreshPrices(ctx context.Context, log *zap.Logger) error {
	if s == nil || s.store == nil {
		return domain.ErrDonationWalletNotConfigured
	}
	if log == nil {
		log = zap.NewNop()
	}
	chains, err := s.store.EnabledDonationChains(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(chains))
	seen := map[string]bool{}
	for _, chain := range chains {
		if !chain.AutoPriced() || seen[chain.PriceSourceID] {
			continue
		}
		seen[chain.PriceSourceID] = true
		ids = append(ids, chain.PriceSourceID)
	}
	if len(ids) == 0 {
		return nil
	}

	prices, err := fetchCoinGeckoPrices(ctx, ids)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, chain := range chains {
		if !chain.AutoPriced() {
			continue
		}
		usd, ok := prices[chain.PriceSourceID]
		if !ok || usd <= 0 {
			log.Warn("price source returned no usable price; keeping the previous rate",
				zap.String("donation_chain", chain.Key), zap.String("price_source_id", chain.PriceSourceID))
			continue
		}
		rateMicros := int64(math.Round(usd * microsPerWhole))
		if rateMicros <= 0 {
			continue
		}
		if err := s.store.SetDonationChainPrice(ctx, chain.Key, rateMicros, now); err != nil {
			log.Warn("store refreshed donation price failed", zap.String("donation_chain", chain.Key), zap.Error(err))
			continue
		}
		log.Debug("donation chain price refreshed",
			zap.String("donation_chain", chain.Key), zap.Int64("usd_rate_micros", rateMicros))
	}
	return nil
}

// PreviewPrice fetches one coin id's current USD price without storing
// anything, so the admin panel can tell an operator whether the id they
// typed actually resolves before they save a chain against it.
func (s *Service) PreviewPrice(ctx context.Context, sourceID string) (usdMicros int64, err error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return 0, fmt.Errorf("donations: price source id is required")
	}
	prices, err := fetchCoinGeckoPrices(ctx, []string{sourceID})
	if err != nil {
		return 0, err
	}
	usd, ok := prices[sourceID]
	if !ok || usd <= 0 {
		return 0, fmt.Errorf("donations: price source has no price for %q", sourceID)
	}
	return int64(math.Round(usd * microsPerWhole)), nil
}

func fetchCoinGeckoPrices(ctx context.Context, ids []string) (map[string]float64, error) {
	ctx, cancel := context.WithTimeout(ctx, priceFetchTimeout)
	defer cancel()

	endpoint := coinGeckoSimplePriceURL + "?" + url.Values{
		"ids":           {strings.Join(ids, ",")},
		"vs_currencies": {"usd"},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("donations: fetch prices: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("donations: price source returned %s", resp.Status)
	}
	var body map[string]map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("donations: decode prices: %w", err)
	}
	out := make(map[string]float64, len(body))
	for id, quote := range body {
		out[id] = quote["usd"]
	}
	return out, nil
}
