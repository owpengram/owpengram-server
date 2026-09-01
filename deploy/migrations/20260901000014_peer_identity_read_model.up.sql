-- Viewer-independent peer decorations are projected on every peer-bearing RPC
-- response. Keep one durable version token for username registry vectors and
-- third-party bot-verification marks so positive and negative L1 entries can be
-- reused without one PostgreSQL query per response page.

CREATE OR REPLACE FUNCTION public.telesrv_bump_peer_identity(
    p_peer_type text,
    p_peer_id bigint
) RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    IF COALESCE(p_peer_id, 0) = 0 OR p_peer_type NOT IN ('user', 'channel') THEN
        RETURN;
    END IF;

    PERFORM public.telesrv_bump_read_model_version(
        'peer_identity', 0, p_peer_type, p_peer_id
    );
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_notify_peer_identity_row()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM public.telesrv_bump_peer_identity(OLD.peer_type, OLD.peer_id);
        RETURN OLD;
    END IF;

    IF TG_OP = 'UPDATE'
       AND (OLD.peer_type, OLD.peer_id) IS DISTINCT FROM (NEW.peer_type, NEW.peer_id) THEN
        PERFORM public.telesrv_bump_peer_identity(OLD.peer_type, OLD.peer_id);
    END IF;
    PERFORM public.telesrv_bump_peer_identity(NEW.peer_type, NEW.peer_id);
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS peer_usernames_peer_identity_changed ON public.peer_usernames;
CREATE TRIGGER peer_usernames_peer_identity_changed
AFTER INSERT OR UPDATE OR DELETE ON public.peer_usernames
FOR EACH ROW EXECUTE FUNCTION public.telesrv_notify_peer_identity_row();

DROP TRIGGER IF EXISTS custom_verifications_peer_identity_changed ON public.custom_verifications;
CREATE TRIGGER custom_verifications_peer_identity_changed
AFTER INSERT OR UPDATE OR DELETE ON public.custom_verifications
FOR EACH ROW EXECUTE FUNCTION public.telesrv_notify_peer_identity_row();

-- Seed every durable peer, including peers with no registry row/mark. This is
-- what makes an empty result a versioned negative fact rather than a TTL guess.
INSERT INTO public.read_model_versions (
    model, owner_user_id, peer_type, peer_id, version, hash, updated_at
)
SELECT 'peer_identity', 0, 'user', u.id, 1,
       public.telesrv_random_read_model_hash(), now()
FROM public.users u
ON CONFLICT (model, owner_user_id, peer_type, peer_id) DO NOTHING;

INSERT INTO public.read_model_versions (
    model, owner_user_id, peer_type, peer_id, version, hash, updated_at
)
SELECT 'peer_identity', 0, 'channel', c.id, 1,
       public.telesrv_random_read_model_hash(), now()
FROM public.channels c
ON CONFLICT (model, owner_user_id, peer_type, peer_id) DO NOTHING;
