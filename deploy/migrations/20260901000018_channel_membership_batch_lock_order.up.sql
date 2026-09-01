-- 20260901000017 initially ordered version keys lexically, placing
-- channel_active_memberships before the member/dialog keys. Ordinary single-row
-- membership writes acquire those keys in the opposite order, so a concurrent
-- createChannel and batch invite could deadlock. Use the canonical physical
-- mutation order: participants -> member -> dialog -> owner -> active index.

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
