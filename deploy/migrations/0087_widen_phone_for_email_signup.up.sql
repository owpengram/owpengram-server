-- Email-as-identity signup mode encodes an email address as a reversible
-- big-integer-decimal string behind an "888" prefix and stores it in
-- users.phone via the existing phone-number column/flow. A real phone number
-- never approaches this length, but an encoded email can (roughly 2.4 decimal
-- digits per source byte), so the original 32-char cap (sized only for real
-- phone numbers) is too narrow. 200 chars comfortably covers realistic email
-- addresses; see internal/domain.ValidPhone and internal/domain.EncodeEmailPhone.
ALTER TABLE public.users ALTER COLUMN phone TYPE character varying(200);
