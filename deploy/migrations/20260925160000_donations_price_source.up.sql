-- Per-network block explorer and automatic price source.
--
-- explorer_url turns a recorded deposit into a clickable transaction in the
-- admin panel ("https://etherscan.io" + "/tx/0x..."), which otherwise leaves
-- an operator copying a hash into a search box by hand.
--
-- price_source/price_source_id let a chain's USD rate be refreshed
-- automatically instead of being typed once and silently going stale:
-- '' keeps the manual rate an operator entered, 'coingecko' refreshes
-- manual_usd_rate_micros from CoinGecko's free simple/price endpoint using
-- price_source_id as the coin id ('ethereum', 'binancecoin', ...).
-- price_updated_at records the last successful refresh, so a stale feed is
-- visible rather than silently pricing deposits off a week-old number.
ALTER TABLE public.donation_chains
    ADD COLUMN explorer_url text DEFAULT '' NOT NULL,
    ADD COLUMN price_source text DEFAULT '' NOT NULL,
    ADD COLUMN price_source_id text DEFAULT '' NOT NULL,
    ADD COLUMN price_updated_at timestamp with time zone;
