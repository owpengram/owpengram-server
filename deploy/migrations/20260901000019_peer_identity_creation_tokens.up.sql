-- 20260901000014 seeded only the peers that existed when that migration ran. Real account
-- provisioning and channel creation after deployment therefore had no durable
-- token, so their empty username/verification facts were deliberately
-- uncacheable. Maintain the token at the peer lifecycle boundary instead.

CREATE OR REPLACE FUNCTION public.telesrv_seed_peer_identity_on_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_TABLE_NAME = 'users' THEN
        PERFORM public.telesrv_bump_peer_identity('user', NEW.id);
    ELSIF TG_TABLE_NAME = 'channels' THEN
        PERFORM public.telesrv_bump_peer_identity('channel', NEW.id);
    ELSE
        RAISE EXCEPTION 'unsupported peer identity seed table: %', TG_TABLE_NAME;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS users_peer_identity_created ON public.users;
CREATE TRIGGER users_peer_identity_created
AFTER INSERT ON public.users
FOR EACH ROW EXECUTE FUNCTION public.telesrv_seed_peer_identity_on_insert();

DROP TRIGGER IF EXISTS channels_peer_identity_created ON public.channels;
CREATE TRIGGER channels_peer_identity_created
AFTER INSERT ON public.channels
FOR EACH ROW EXECUTE FUNCTION public.telesrv_seed_peer_identity_on_insert();

-- Use the same bump helper rather than a silent INSERT so already-running
-- instances that cached a missing/zero token receive exact-key invalidation.
DO $$
DECLARE
    peer_row record;
BEGIN
    FOR peer_row IN
        SELECT u.id
        FROM public.users u
        WHERE NOT EXISTS (
            SELECT 1 FROM public.read_model_versions v
            WHERE v.model = 'peer_identity'
              AND v.owner_user_id = 0
              AND v.peer_type = 'user'
              AND v.peer_id = u.id
        )
        ORDER BY u.id
    LOOP
        PERFORM public.telesrv_bump_peer_identity('user', peer_row.id);
    END LOOP;

    FOR peer_row IN
        SELECT c.id
        FROM public.channels c
        WHERE NOT EXISTS (
            SELECT 1 FROM public.read_model_versions v
            WHERE v.model = 'peer_identity'
              AND v.owner_user_id = 0
              AND v.peer_type = 'channel'
              AND v.peer_id = c.id
        )
        ORDER BY c.id
    LOOP
        PERFORM public.telesrv_bump_peer_identity('channel', peer_row.id);
    END LOOP;
END;
$$;
