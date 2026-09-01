DROP TRIGGER IF EXISTS users_dialog_owner_read_model ON public.users;
DROP FUNCTION IF EXISTS public.telesrv_seed_dialog_owner_read_model();

DELETE FROM public.read_model_versions WHERE model = 'dialog_owner';

CREATE OR REPLACE FUNCTION public.telesrv_notify_dialog_light_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    owner_id bigint;
    peer_type text;
    peer_id bigint;
BEGIN
    IF TG_OP = 'DELETE' THEN
        owner_id := OLD.user_id;
        peer_type := OLD.peer_type;
        peer_id := OLD.peer_id;
    ELSE
        owner_id := NEW.user_id;
        peer_type := NEW.peer_type;
        peer_id := NEW.peer_id;
    END IF;

    PERFORM public.telesrv_bump_dialog_light(owner_id, peer_type, peer_id);
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_notify_channel_dialog_light_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    owner_id bigint;
    channel_id bigint;
BEGIN
    IF TG_OP = 'DELETE' THEN
        owner_id := OLD.user_id;
        channel_id := OLD.channel_id;
    ELSE
        owner_id := NEW.user_id;
        channel_id := NEW.channel_id;
    END IF;

    PERFORM public.telesrv_bump_dialog_light(owner_id, 'channel', channel_id);
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_notify_channel_member_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    channel_id bigint;
    user_id bigint;
BEGIN
    IF TG_OP = 'DELETE' THEN
        channel_id := OLD.channel_id;
        user_id := OLD.user_id;
    ELSE
        channel_id := NEW.channel_id;
        user_id := NEW.user_id;
    END IF;

    PERFORM public.telesrv_bump_read_model_version('channel_member', user_id, 'channel', channel_id);
    PERFORM public.telesrv_bump_dialog_light(user_id, 'channel', channel_id);
    RETURN NULL;
END;
$$;

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
END;
$$;

DROP FUNCTION IF EXISTS public.telesrv_bump_dialog_owner(bigint);
