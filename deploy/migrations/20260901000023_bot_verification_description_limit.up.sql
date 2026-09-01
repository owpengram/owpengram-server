ALTER TABLE public.bot_verifier_settings
    DROP CONSTRAINT bot_verifier_settings_default_description_check,
    ADD CONSTRAINT bot_verifier_settings_default_description_check
        CHECK (octet_length(default_description) <= 512);

ALTER TABLE public.custom_verification_requests
    DROP CONSTRAINT custom_verification_requests_requested_description_check,
    ADD CONSTRAINT custom_verification_requests_requested_description_check
        CHECK (octet_length(requested_description) <= 512);
