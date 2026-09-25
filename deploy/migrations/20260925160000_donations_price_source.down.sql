ALTER TABLE public.donation_chains
    DROP COLUMN IF EXISTS explorer_url,
    DROP COLUMN IF EXISTS price_source,
    DROP COLUMN IF EXISTS price_source_id,
    DROP COLUMN IF EXISTS price_updated_at;
