-- Rebrand the built-in official system account (777000) from the upstream
-- gramsrv default "Telegram"/@telegram to this fork's own identity. Existing
-- databases already seeded by migration 0001 need this as a follow-up
-- UPDATE; migration 0001 itself is never replayed on an already-migrated
-- database.
UPDATE public.users
SET first_name = 'OwpenGram', username = 'owpengram', updated_at = now()
WHERE id = 777000;
