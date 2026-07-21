-- One-time backfill: give every existing non-bot account the "UtyaDuck"
-- sticker pack (773947703670341644, data/sticker-seed/telegram_stickers_export)
-- as an installed default, so the sticker panel isn't empty out of the box.
-- New signups get the same pack going forward via application code
-- (internal/app/auth/service.go, Service.SignUp), not future migrations.
INSERT INTO public.user_sticker_sets (owner_user_id, sticker_set_id, set_kind, archived, installed_date, order_value, updated_at)
SELECT id, 773947703670341644, 'stickers', false, extract(epoch FROM now())::int, (extract(epoch FROM now())::bigint << 32), now()
FROM public.users
WHERE is_bot = false
ON CONFLICT (owner_user_id, sticker_set_id) DO NOTHING;
