-- Batch membership mutations are one durable state transition. Row triggers
-- remain authoritative for ordinary statements, while the explicitly scoped
-- batch path suppresses their per-row write amplification and advances every
-- distinct dependency once before the transaction may commit.

CREATE OR REPLACE FUNCTION public.telesrv_membership_batch_active()
RETURNS boolean
LANGUAGE sql
STABLE
AS $$
    SELECT COALESCE(current_setting('telesrv.membership_batch_mode', true), '') = 'on'
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
    IF public.telesrv_membership_batch_active() THEN
        RETURN NULL;
    END IF;

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

CREATE OR REPLACE FUNCTION public.telesrv_notify_channel_participants_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_channel_id bigint;
    new_channel_id bigint;
BEGIN
    IF public.telesrv_membership_batch_active() THEN
        RETURN NULL;
    END IF;

    IF TG_OP = 'DELETE' THEN
        old_channel_id := OLD.channel_id;
    ELSIF TG_OP = 'INSERT' THEN
        new_channel_id := NEW.channel_id;
    ELSE
        old_channel_id := OLD.channel_id;
        new_channel_id := NEW.channel_id;
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
    IF public.telesrv_membership_batch_active() THEN
        RETURN NULL;
    END IF;

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

CREATE OR REPLACE FUNCTION public.telesrv_notify_channel_active_memberships_read_model()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_owner bigint;
    new_owner bigint;
    changed boolean;
BEGIN
    IF public.telesrv_membership_batch_active() THEN
        RETURN NULL;
    END IF;

    IF TG_OP = 'DELETE' THEN
        old_owner := OLD.user_id;
        IF old_owner <> 0 THEN
            PERFORM public.telesrv_bump_read_model_version('channel_active_memberships', old_owner, 'user', old_owner);
        END IF;
        RETURN NULL;
    END IF;

    new_owner := NEW.user_id;
    IF TG_OP = 'INSERT' THEN
        IF new_owner <> 0 THEN
            PERFORM public.telesrv_bump_read_model_version('channel_active_memberships', new_owner, 'user', new_owner);
        END IF;
        RETURN NULL;
    END IF;

    old_owner := OLD.user_id;
    changed :=
        OLD.user_id IS DISTINCT FROM NEW.user_id OR
        OLD.channel_id IS DISTINCT FROM NEW.channel_id OR
        OLD.status IS DISTINCT FROM NEW.status OR
        OLD.deleted IS DISTINCT FROM NEW.deleted;
    IF changed THEN
        IF old_owner <> 0 THEN
            PERFORM public.telesrv_bump_read_model_version('channel_active_memberships', old_owner, 'user', old_owner);
        END IF;
        IF new_owner <> 0 AND new_owner IS DISTINCT FROM old_owner THEN
            PERFORM public.telesrv_bump_read_model_version('channel_active_memberships', new_owner, 'user', new_owner);
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION public.telesrv_bump_channel_membership_read_models(
    p_channel_id bigint,
    p_user_ids bigint[]
)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    changed record;
BEGIN
    IF COALESCE(p_channel_id, 0) <= 0 OR COALESCE(cardinality(p_user_ids), 0) = 0 THEN
        RETURN;
    END IF;

    FOR changed IN
        WITH requested AS MATERIALIZED (
            SELECT DISTINCT user_id
            FROM unnest(p_user_ids) AS input(user_id)
            WHERE user_id > 0
        ), keys AS MATERIALIZED (
            SELECT 10 AS priority, 'channel_participants'::text AS model, 0::bigint AS owner_user_id,
                   'channel'::text AS peer_type, p_channel_id AS peer_id
            UNION
            SELECT 20, 'channel_member', user_id, 'channel', p_channel_id FROM requested
            UNION
            SELECT 30, 'dialog_light', user_id, 'channel', p_channel_id FROM requested
            UNION
            SELECT 40, 'dialog_owner', user_id, 'user', user_id FROM requested
            UNION
            SELECT 50, 'channel_active_memberships', user_id, 'user', user_id FROM requested
        ), bumped AS (
            INSERT INTO public.read_model_versions (
                model, owner_user_id, peer_type, peer_id, version, hash, updated_at
            )
            SELECT model, owner_user_id, peer_type, peer_id, 1,
                   public.telesrv_random_read_model_hash(), now()
            FROM keys
            ORDER BY priority, owner_user_id, peer_type, peer_id
            ON CONFLICT (model, owner_user_id, peer_type, peer_id) DO UPDATE SET
                version = public.read_model_versions.version + 1,
                hash = public.telesrv_random_read_model_hash(),
                updated_at = EXCLUDED.updated_at
            RETURNING model, owner_user_id, peer_type, peer_id, version, hash
        )
        SELECT model, owner_user_id, peer_type, peer_id, version, hash
        FROM bumped
        ORDER BY model, owner_user_id, peer_type, peer_id
    LOOP
        PERFORM pg_notify(
            'telesrv_read_model_changed',
            json_build_object(
                'model', changed.model,
                'owner_user_id', changed.owner_user_id,
                'peer_type', changed.peer_type,
                'peer_id', changed.peer_id,
                'version', changed.version,
                'hash', changed.hash
            )::text
        );
    END LOOP;
END;
$$;
