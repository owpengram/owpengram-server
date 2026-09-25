package donations

import "math/big"

// DefaultUSDPerStarMicros is what one Star costs when a donor buys it with
// crypto here: $0.005, i.e. 200 Stars per US dollar. Telegram's own
// smallest pack is 100 Stars for $2.05 ($0.0205 each), so this is
// deliberately about four times cheaper. Override with
// TELESRV_STARS_USD_PRICE_MICROS.
const DefaultUSDPerStarMicros = 5000 // $0.005 * 1e6

// microsPerWhole scales a "USD per one whole unit" rate (native currency or
// a stablecoin) by 1e6, matching DonationChain.ManualUSDRateMicros and
// DonationToken pricing throughout this package.
const microsPerWhole = 1_000_000

// usdMicrosForAmount converts a raw on-chain amount (smallest unit, e.g.
// wei) into USD, scaled by 1e6, given the asset's decimals and its price in
// USD-per-whole-unit (also scaled by 1e6). Pure integer math throughout --
// no float ever touches a monetary amount.
func usdMicrosForAmount(amountRaw *big.Int, decimals int, usdRateMicros int64) int64 {
	if amountRaw == nil || amountRaw.Sign() <= 0 || decimals < 0 || usdRateMicros <= 0 {
		return 0
	}
	// usdMicros = amountRaw * usdRateMicros / 10^decimals
	num := new(big.Int).Mul(amountRaw, big.NewInt(usdRateMicros))
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	result := new(big.Int).Quo(num, den)
	if !result.IsInt64() {
		return 0 // absurdly large deposit; refuse to overflow rather than mis-credit.
	}
	return result.Int64()
}

// starsForUSDMicros converts a priced deposit into Stars at this service's
// configured Star price, rounding down -- a donor is never credited more
// Stars than their deposit was actually worth.
func (s *Service) starsForUSDMicros(usdMicros int64) int64 {
	if usdMicros <= 0 {
		return 0
	}
	return usdMicros / s.starPriceMicros()
}

// starPriceMicros is the configured price of one Star in micro-dollars,
// falling back to the default for a zero-value service (tests that build a
// Service literal without options).
func (s *Service) starPriceMicros() int64 {
	if s == nil || s.usdPerStarMicros <= 0 {
		return DefaultUSDPerStarMicros
	}
	return s.usdPerStarMicros
}

// StarPriceMicros exposes the configured Star price so the admin panel and
// the @premiumbot quote the same number this package credits at.
func (s *Service) StarPriceMicros() int64 { return s.starPriceMicros() }
