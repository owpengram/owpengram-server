-- Email-signup accounts (see internal/domain.EncodeEmailPhone) now get a
-- short, normal-looking random "888" phone number in users.phone instead of
-- storing the email-derived string there directly (that string only ever
-- travels on the wire during auth.sendCode/signIn/signUp, exactly like a real
-- phone number would). signup_email is the durable email->user reverse
-- lookup that replaces the old "phone IS the email encoding" identity: it is
-- what SendCode/SignIn use to find a returning email-signup account by the
-- (deterministic) wire value decoded back to an email.
ALTER TABLE public.users ADD COLUMN signup_email character varying(200) NOT NULL DEFAULT '';

-- Case-insensitive, ignores the empty default so real phone-number accounts
-- (the overwhelming majority) never touch this index.
CREATE UNIQUE INDEX users_signup_email_lower_unique_idx ON public.users (lower(signup_email)) WHERE signup_email <> '';
