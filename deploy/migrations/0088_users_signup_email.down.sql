DROP INDEX IF EXISTS users_signup_email_lower_unique_idx;
ALTER TABLE public.users DROP COLUMN signup_email;
