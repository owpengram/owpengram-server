-- Add /claim (the once-per-cooldown free Stars claim from
-- 20260922120000_stars_monthly_claim) to @premiumbot's command menu.
-- bots.commands from 20260909000001_premium_subscriptions predates that
-- feature and never listed it, so the client's "/" autocomplete never
-- showed it even though the bot already answers it. bot_info_version bumps
-- so an already-cached client's getFullUser re-fetches the updated list.

UPDATE public.bots
SET commands = '[
        {"command": "start", "description": "open the Premium storefront"},
        {"command": "premium", "description": "buy Premium for yourself"},
        {"command": "gift", "description": "gift Premium to someone"},
        {"command": "status", "description": "check your Premium status"},
        {"command": "history", "description": "show your purchase history"},
        {"command": "claim", "description": "claim your free monthly Stars"},
        {"command": "help", "description": "show help"}
    ]'::jsonb,
    updated_at = now()
WHERE bot_user_id = 1250000017;

UPDATE public.users
SET bot_info_version = bot_info_version + 1,
    updated_at = now()
WHERE id = 1250000017;
