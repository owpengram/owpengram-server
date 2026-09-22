-- Once-per-cooldown free Stars claim (e.g. "/claim" on the built-in
-- @premiumbot). One row per account: claimed_at is the last successful
-- claim; the app layer decides eligibility by comparing it against its own
-- clock plus a configured cooldown (TELESRV_STARS_MONTHLY_CLAIM_INTERVAL),
-- so the cooldown length can change without a migration.

CREATE TABLE public.stars_monthly_claims (
    user_id bigint NOT NULL,
    claimed_at timestamp with time zone NOT NULL,
    CONSTRAINT stars_monthly_claims_pkey PRIMARY KEY (user_id),
    CONSTRAINT stars_monthly_claims_user_fkey FOREIGN KEY (user_id)
        REFERENCES public.users(id) ON DELETE CASCADE
);
