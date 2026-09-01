DROP FUNCTION IF EXISTS public.telesrv_bump_channel_membership_read_models(bigint, bigint[]);

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
        IF TG_OP = 'INSERT' OR (new_user_id, new_channel_id) IS DISTINCT FROM (old_user_id, old_channel_id) THEN
            PERFORM public.telesrv_bump_read_model_version('channel_member', new_user_id, 'channel', new_channel_id);
            PERFORM public.telesrv_bump_dialog_light(new_user_id, 'channel', new_channel_id);
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_notify_channel_participants_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_channel_id bigint;
    new_channel_id bigint;
BEGIN
    IF TG_OP = 'DELETE' THEN old_channel_id := OLD.channel_id;
    ELSIF TG_OP = 'INSERT' THEN new_channel_id := NEW.channel_id;
    ELSE old_channel_id := OLD.channel_id; new_channel_id := NEW.channel_id;
    END IF;
    IF old_channel_id IS NOT NULL THEN
        PERFORM public.telesrv_bump_read_model_version('channel_participants', 0, 'channel', old_channel_id);
    END IF;
    IF new_channel_id IS NOT NULL AND new_channel_id IS DISTINCT FROM old_channel_id THEN
        PERFORM public.telesrv_bump_read_model_version('channel_participants', 0, 'channel', new_channel_id);
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
        old_owner_id := OLD.user_id; old_channel_id := OLD.channel_id;
        PERFORM public.telesrv_bump_dialog_light(old_owner_id, 'channel', old_channel_id);
    END IF;
    IF TG_OP <> 'DELETE' THEN
        new_owner_id := NEW.user_id; new_channel_id := NEW.channel_id;
        IF TG_OP = 'INSERT' OR (new_owner_id, new_channel_id) IS DISTINCT FROM (old_owner_id, old_channel_id) THEN
            PERFORM public.telesrv_bump_dialog_light(new_owner_id, 'channel', new_channel_id);
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_notify_channel_active_memberships_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_owner bigint;
    new_owner bigint;
    changed boolean;
BEGIN
    IF TG_OP = 'DELETE' THEN
        old_owner := OLD.user_id;
        IF old_owner <> 0 THEN PERFORM public.telesrv_bump_read_model_version('channel_active_memberships', old_owner, 'user', old_owner); END IF;
        RETURN NULL;
    END IF;
    new_owner := NEW.user_id;
    IF TG_OP = 'INSERT' THEN
        IF new_owner <> 0 THEN PERFORM public.telesrv_bump_read_model_version('channel_active_memberships', new_owner, 'user', new_owner); END IF;
        RETURN NULL;
    END IF;
    old_owner := OLD.user_id;
    changed := OLD.user_id IS DISTINCT FROM NEW.user_id OR OLD.channel_id IS DISTINCT FROM NEW.channel_id OR
               OLD.status IS DISTINCT FROM NEW.status OR OLD.deleted IS DISTINCT FROM NEW.deleted;
    IF changed THEN
        IF old_owner <> 0 THEN PERFORM public.telesrv_bump_read_model_version('channel_active_memberships', old_owner, 'user', old_owner); END IF;
        IF new_owner <> 0 AND new_owner IS DISTINCT FROM old_owner THEN
            PERFORM public.telesrv_bump_read_model_version('channel_active_memberships', new_owner, 'user', new_owner);
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

DROP FUNCTION IF EXISTS public.telesrv_membership_batch_active();
