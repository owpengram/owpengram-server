-- Donations now ship with zero pre-populated chains: the admin panel's
-- Donations page has an "Add chain" menu (curated presets with a known
-- public RPC, or a fully custom entry) instead of six pre-seeded rows --
-- mostly disabled, RPC-less placeholders -- that an operator had to notice
-- and edit one by one. Remove the original seed rows this feature shipped
-- with (20260924140000_donations.up.sql), except any chain that already has
-- real donation history: a chain a donor has actually sent funds to is
-- never removed out from under that history, only ever disabled.
DELETE FROM public.donation_tokens
WHERE chain_key NOT IN (SELECT DISTINCT chain_key FROM public.donation_deposits);

DELETE FROM public.donation_chains
WHERE chain_key NOT IN (SELECT DISTINCT chain_key FROM public.donation_deposits);
