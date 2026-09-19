-- Auction awards must show the winning bid, not the catalog starting price.
--
-- star_gift_catalog_revisions.stars is the auction's starting/min bid
-- (ensureStarGiftAuction copies it into star_gift_auctions.min_bid_amount), and
-- both the award service message and the saved-gift card used to project that
-- value, so every win rendered as "You won the auction with a bid of <min> Stars".
-- paid_stars records the real settled price for a saved gift; 0 keeps the legacy
-- behaviour of projecting the immutable catalog revision price.
ALTER TABLE peer_star_gifts
    ADD COLUMN IF NOT EXISTS paid_stars bigint NOT NULL DEFAULT 0 CHECK (paid_stars >= 0);

-- Backfill the saved gifts the auction engine already awarded. This is a plain
-- server-owned column read fresh on every payments.getSavedStarGifts, so the
-- corrected price reaches clients without any update sequence: there is nothing
-- cached to invalidate.
UPDATE peer_star_gifts p
SET paid_stars = a.bid_amount
FROM star_gift_auction_acquired a
WHERE a.saved_gift_id = p.id
  AND a.bid_amount > 0
  AND p.paid_stars <> a.bid_amount;

-- The award service message that was already delivered is deliberately left as it
-- was written. It carries its own immutable gift snapshot inside message_boxes.media
-- / private_messages.media, and a message body cannot be corrected here: a client
-- that already consumed the box learns about edits only through a continuous PTS and
-- a durable edit_message event, which a migration has no way to allocate. Rewriting
-- the JSONB in place would silently diverge server state from every client that had
-- already synced the old card. So historical cards stay immutable, and the fix lands
-- where it can be delivered honestly — the paid_stars column above, which the saved
-- gift card and every future award read.
