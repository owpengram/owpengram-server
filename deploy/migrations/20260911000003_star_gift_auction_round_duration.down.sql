-- Revert configurable per-auction round duration.
ALTER TABLE star_gift_catalog_revisions
    DROP COLUMN IF EXISTS auction_round_duration;
