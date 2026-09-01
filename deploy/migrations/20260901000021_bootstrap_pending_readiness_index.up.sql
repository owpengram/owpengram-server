CREATE INDEX IF NOT EXISTS bootstrap_update_jobs_pending_auth_idx
    ON public.bootstrap_update_jobs (user_id, auth_key_id, id)
    WHERE (status)::text = 'pending'::text;
