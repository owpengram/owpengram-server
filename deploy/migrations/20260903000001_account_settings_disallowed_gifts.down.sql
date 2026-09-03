ALTER TABLE public.account_settings
    DROP COLUMN IF EXISTS disallow_unlimited_stargifts,
    DROP COLUMN IF EXISTS disallow_limited_stargifts,
    DROP COLUMN IF EXISTS disallow_unique_stargifts,
    DROP COLUMN IF EXISTS disallow_premium_gifts,
    DROP COLUMN IF EXISTS disallow_stargifts_from_channels;
