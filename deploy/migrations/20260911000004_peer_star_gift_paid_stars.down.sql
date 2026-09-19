-- Drop the settled-price column. The up migration only ever added it and backfilled
-- it from star_gift_auction_acquired, so dropping it restores the previous shape
-- exactly: the card falls back to projecting the catalog revision price.
ALTER TABLE peer_star_gifts
    DROP COLUMN IF EXISTS paid_stars;
