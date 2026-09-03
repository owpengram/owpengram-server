-- account_settings gained global gift-reception switches (Layer 228
-- disallowedGifts) in code (internal/domain/account.go's DisallowedGifts,
-- read/written by internal/store/postgres/account.go's GetAccountSettings/
-- GetAccountSettingsBatch/SaveAccountSettings) without a matching migration
-- ever being added -- every account_settings read/write has been failing
-- with "column does not exist" on any database that never got these columns,
-- which breaks not just gift settings but every RPC that loads cached
-- account settings (privacy, account TTL, contact sign-up notification,
-- sendMessage's privacy check, etc).
ALTER TABLE public.account_settings
    ADD COLUMN IF NOT EXISTS disallow_unlimited_stargifts    boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS disallow_limited_stargifts      boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS disallow_unique_stargifts       boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS disallow_premium_gifts          boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS disallow_stargifts_from_channels boolean NOT NULL DEFAULT false;
