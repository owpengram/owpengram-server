-- Configurable per-auction round duration for operator-authored auctions.
-- 0 preserves the legacy fixed cadence (starGiftAuctionRoundDuration = 3600s),
-- so existing revisions and official-gift imports are unaffected.
ALTER TABLE star_gift_catalog_revisions
    ADD COLUMN IF NOT EXISTS auction_round_duration integer NOT NULL DEFAULT 0;
