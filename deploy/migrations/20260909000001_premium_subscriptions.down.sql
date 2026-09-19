-- Remove the built-in @premiumbot seed. Its private history is left alone:
-- chat rows reference the account, and dropping them would rewrite users'
-- dialogs.
DELETE FROM public.read_model_versions
WHERE owner_user_id = 1250000017 AND peer_type = 'user' AND peer_id = 1250000017;

DELETE FROM public.peer_usernames
WHERE peer_type = 'user' AND peer_id = 1250000017;

DELETE FROM public.bots WHERE bot_user_id = 1250000017;

ALTER TABLE public.stars_transactions
    DROP CONSTRAINT IF EXISTS stars_transactions_premium_payment_fkey,
    DROP CONSTRAINT IF EXISTS stars_transactions_premium_recipient_valid,
    DROP CONSTRAINT IF EXISTS stars_transactions_premium_months_valid,
    DROP COLUMN IF EXISTS premium_payment_intent_id,
    DROP COLUMN IF EXISTS premium_recipient_user_id,
    DROP COLUMN IF EXISTS premium_months;

DROP TABLE IF EXISTS public.premium_entitlements;
DROP TABLE IF EXISTS public.premium_payment_intents;
DROP TABLE IF EXISTS public.premium_plans;

ALTER TABLE public.users
    DROP COLUMN IF EXISTS premium_updated_at;
