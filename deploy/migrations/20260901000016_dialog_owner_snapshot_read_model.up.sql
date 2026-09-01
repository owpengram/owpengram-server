-- Owner collection generation for the Redis-backed messages.getDialogs header
-- snapshot. Shared channel mutations remain channel_base-only; membership and
-- owner-local dialog mutations advance this O(1) owner token.

CREATE OR REPLACE FUNCTION public.telesrv_bump_dialog_owner(p_owner_user_id bigint)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    IF COALESCE(p_owner_user_id, 0) = 0 THEN
        RETURN;
    END IF;

    PERFORM public.telesrv_bump_read_model_version(
        'dialog_owner', p_owner_user_id, 'user', p_owner_user_id
    );
END;
$$;

-- dialog_owner is the owner-wide aggregate of every exact dialog_light
-- dependency. Extending the common helper closes private top-message,
-- reaction, contact/profile and future exact-dialog invalidation paths without
-- copying owner bumps into every trigger.
CREATE OR REPLACE FUNCTION public.telesrv_bump_dialog_light(
    p_owner_user_id bigint,
    p_peer_type text,
    p_peer_id bigint
)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    IF COALESCE(p_owner_user_id, 0) = 0
       OR COALESCE(p_peer_id, 0) = 0
       OR COALESCE(p_peer_type, '') = ''
    THEN
        RETURN;
    END IF;

    PERFORM public.telesrv_bump_read_model_version(
        'dialog_light', p_owner_user_id, p_peer_type, p_peer_id
    );
    PERFORM public.telesrv_bump_dialog_owner(p_owner_user_id);
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_seed_dialog_owner_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM public.telesrv_bump_dialog_owner(NEW.id);
    RETURN NULL;
END;
$$;

CREATE TRIGGER users_dialog_owner_read_model
AFTER INSERT ON public.users
FOR EACH ROW EXECUTE FUNCTION public.telesrv_seed_dialog_owner_read_model();

CREATE OR REPLACE FUNCTION public.telesrv_notify_dialog_light_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_owner_id bigint;
    old_peer_type text;
    old_peer_id bigint;
    new_owner_id bigint;
    new_peer_type text;
    new_peer_id bigint;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_owner_id := OLD.user_id;
        old_peer_type := OLD.peer_type;
        old_peer_id := OLD.peer_id;
        PERFORM public.telesrv_bump_dialog_light(old_owner_id, old_peer_type, old_peer_id);
    END IF;

    IF TG_OP <> 'DELETE' THEN
        new_owner_id := NEW.user_id;
        new_peer_type := NEW.peer_type;
        new_peer_id := NEW.peer_id;
        IF TG_OP = 'INSERT'
           OR (new_owner_id, new_peer_type, new_peer_id)
              IS DISTINCT FROM (old_owner_id, old_peer_type, old_peer_id)
        THEN
            PERFORM public.telesrv_bump_dialog_light(new_owner_id, new_peer_type, new_peer_id);
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_notify_channel_dialog_light_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_owner_id bigint;
    old_channel_id bigint;
    new_owner_id bigint;
    new_channel_id bigint;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_owner_id := OLD.user_id;
        old_channel_id := OLD.channel_id;
        PERFORM public.telesrv_bump_dialog_light(old_owner_id, 'channel', old_channel_id);
    END IF;

    IF TG_OP <> 'DELETE' THEN
        new_owner_id := NEW.user_id;
        new_channel_id := NEW.channel_id;
        IF TG_OP = 'INSERT'
           OR (new_owner_id, new_channel_id) IS DISTINCT FROM (old_owner_id, old_channel_id)
        THEN
            PERFORM public.telesrv_bump_dialog_light(new_owner_id, 'channel', new_channel_id);
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_notify_channel_member_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_channel_id bigint;
    old_user_id bigint;
    new_channel_id bigint;
    new_user_id bigint;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_channel_id := OLD.channel_id;
        old_user_id := OLD.user_id;
        PERFORM public.telesrv_bump_read_model_version('channel_member', old_user_id, 'channel', old_channel_id);
        PERFORM public.telesrv_bump_dialog_light(old_user_id, 'channel', old_channel_id);
    END IF;

    IF TG_OP <> 'DELETE' THEN
        new_channel_id := NEW.channel_id;
        new_user_id := NEW.user_id;
        IF TG_OP = 'INSERT'
           OR (new_user_id, new_channel_id) IS DISTINCT FROM (old_user_id, old_channel_id)
        THEN
            PERFORM public.telesrv_bump_read_model_version('channel_member', new_user_id, 'channel', new_channel_id);
            PERFORM public.telesrv_bump_dialog_light(new_user_id, 'channel', new_channel_id);
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

INSERT INTO public.read_model_versions (
    model, owner_user_id, peer_type, peer_id, version, hash, updated_at
)
SELECT 'dialog_owner', u.id, 'user', u.id, 1,
       public.telesrv_random_read_model_hash(), now()
FROM public.users AS u
ON CONFLICT (model, owner_user_id, peer_type, peer_id) DO NOTHING;
