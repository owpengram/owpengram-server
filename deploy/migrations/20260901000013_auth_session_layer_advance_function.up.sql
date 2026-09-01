-- Advance one explicit invokeWithLayer observation without paying one client
-- round trip for every lock, comparison and projection update. The caller
-- still owns the surrounding transaction/savepoint so identity_changed can
-- roll the complete attempt back and retry with a fresh READ COMMITTED view.

CREATE OR REPLACE FUNCTION public.telesrv_advance_auth_session_layer(
    p_raw_auth_key_id bigint,
    p_session_id bigint,
    p_layer integer,
    p_msg_id bigint,
    p_expires_at timestamptz
)
RETURNS TABLE (
    advance_status text,
    current_layer integer,
    current_msg_id bigint,
    current_observation_id bigint,
    current_expires_at timestamptz,
    shared_default boolean,
    applied boolean
)
LANGUAGE plpgsql
AS $$
DECLARE
    v_now timestamptz := now();
    v_hint_expiry integer;
    v_hint_perm_id bigint;
    v_hint_bound boolean := false;
    v_hint_has_identity boolean := false;
    v_hint_identity_id bigint;
    v_raw_expiry integer;
    v_actual_perm_id bigint;
    v_actual_bound boolean := false;
    v_actual_has_identity boolean := false;
    v_actual_identity_id bigint;
    v_perm_expiry integer;
    v_current_found boolean := false;
    v_current_layer integer := 0;
    v_current_msg_id bigint := 0;
    v_current_observation_id bigint := 0;
    v_current_expires_at timestamptz := p_expires_at;
    v_shared_default boolean := false;
    v_observation_id bigint;
    v_key_ids bigint[];
    v_updated integer;
BEGIN
    IF p_layer <= 0
       OR p_msg_id <= 0
       OR p_msg_id % 4 <> 0
       OR (p_msg_id & 4294967295) = 0
       OR p_expires_at IS NULL
       OR NOT v_now < p_expires_at
       OR p_expires_at - interval '301 seconds' > v_now + interval '30 seconds'
    THEN
        RETURN QUERY SELECT
            'evidence_invalid'::text, 0, 0::bigint, 0::bigint,
            COALESCE(p_expires_at, v_now), false, false;
        RETURN;
    END IF;

    -- Lock-free identity hint. It is deliberately revalidated after the raw
    -- row lock; a newly committed first bind returns identity_changed rather
    -- than taking a permanent identity lock in raw->identity order.
    SELECT key.expires_at, binding.perm_auth_key_id
    INTO v_hint_expiry, v_hint_perm_id
    FROM public.auth_keys AS key
    LEFT JOIN public.temp_auth_key_bindings AS binding
      ON binding.temp_auth_key_id = key.auth_key_id
    WHERE key.auth_key_id = p_raw_auth_key_id;
    IF NOT FOUND THEN
        RETURN QUERY SELECT
            'auth_key_not_found'::text, 0, 0::bigint, 0::bigint,
            p_expires_at, false, false;
        RETURN;
    END IF;

    v_hint_bound := v_hint_perm_id IS NOT NULL;
    v_hint_has_identity := v_hint_bound OR v_hint_expiry = 0;
    IF v_hint_has_identity THEN
        v_hint_identity_id := CASE
            WHEN v_hint_bound THEN v_hint_perm_id
            ELSE p_raw_auth_key_id
        END;
        PERFORM pg_advisory_xact_lock(
            1096111176::integer,
            hashint8(v_hint_identity_id)::integer
        );
    END IF;

    SELECT key.expires_at
    INTO v_raw_expiry
    FROM public.auth_keys AS key
    WHERE key.auth_key_id = p_raw_auth_key_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN QUERY SELECT
            'auth_key_not_found'::text, 0, 0::bigint, 0::bigint,
            p_expires_at, false, false;
        RETURN;
    END IF;

    v_actual_perm_id := NULL;
    SELECT binding.perm_auth_key_id
    INTO v_actual_perm_id
    FROM public.temp_auth_key_bindings AS binding
    WHERE binding.temp_auth_key_id = p_raw_auth_key_id;
    v_actual_bound := FOUND;
    IF NOT v_actual_bound THEN
        v_actual_perm_id := p_raw_auth_key_id;
    END IF;
    v_actual_has_identity := v_actual_bound OR v_raw_expiry = 0;
    v_actual_identity_id := v_actual_perm_id;

    IF v_actual_has_identity <> v_hint_has_identity
       OR (v_actual_has_identity AND v_actual_identity_id <> v_hint_identity_id)
       OR v_actual_bound <> v_hint_bound
       OR v_raw_expiry <> v_hint_expiry
    THEN
        RETURN QUERY SELECT
            'identity_changed'::text, 0, 0::bigint, 0::bigint,
            p_expires_at, false, false;
        RETURN;
    END IF;

    IF v_actual_bound THEN
        SELECT key.expires_at
        INTO v_perm_expiry
        FROM public.auth_keys AS key
        WHERE key.auth_key_id = v_actual_perm_id
        FOR UPDATE;
        IF NOT FOUND OR v_raw_expiry <= 0 OR v_perm_expiry <> 0 THEN
            RETURN QUERY SELECT
                'binding_invalid'::text, 0, 0::bigint, 0::bigint,
                p_expires_at, false, false;
            RETURN;
        END IF;
    END IF;

    SELECT evidence.layer,
           evidence.msg_id,
           evidence.observation_id,
           evidence.expires_at
    INTO v_current_layer,
         v_current_msg_id,
         v_current_observation_id,
         v_current_expires_at
    FROM public.auth_key_session_layers AS evidence
    WHERE evidence.raw_auth_key_id = p_raw_auth_key_id
      AND evidence.session_id = p_session_id
    FOR UPDATE;
    v_current_found := FOUND;

    IF v_current_found AND v_now < v_current_expires_at THEN
        IF p_msg_id < v_current_msg_id THEN
            SELECT key.layer = v_current_layer
                   AND key.layer_observation_id = v_current_observation_id
            INTO v_shared_default
            FROM public.auth_keys AS key
            WHERE key.auth_key_id = v_actual_perm_id;
            RETURN QUERY SELECT
                'ok'::text, v_current_layer, v_current_msg_id,
                v_current_observation_id, v_current_expires_at,
                v_shared_default, false;
            RETURN;
        END IF;
        IF p_msg_id = v_current_msg_id THEN
            IF p_layer <> v_current_layer THEN
                RETURN QUERY SELECT
                    'conflict'::text, v_current_layer, v_current_msg_id,
                    v_current_observation_id, v_current_expires_at,
                    false, false;
                RETURN;
            END IF;
            SELECT key.layer = v_current_layer
                   AND key.layer_observation_id = v_current_observation_id
            INTO v_shared_default
            FROM public.auth_keys AS key
            WHERE key.auth_key_id = v_actual_perm_id;
            RETURN QUERY SELECT
                'ok'::text, v_current_layer, v_current_msg_id,
                v_current_observation_id, v_current_expires_at,
                v_shared_default, false;
            RETURN;
        END IF;
    END IF;

    v_observation_id := nextval('public.auth_key_layer_observation_seq');
    INSERT INTO public.auth_key_session_layers (
        raw_auth_key_id,
        session_id,
        layer,
        msg_id,
        observation_id,
        expires_at
    ) VALUES (
        p_raw_auth_key_id,
        p_session_id,
        p_layer,
        p_msg_id,
        v_observation_id,
        p_expires_at
    )
    ON CONFLICT (raw_auth_key_id, session_id) DO UPDATE SET
        layer = EXCLUDED.layer,
        msg_id = EXCLUDED.msg_id,
        observation_id = EXCLUDED.observation_id,
        expires_at = EXCLUDED.expires_at
    RETURNING layer, msg_id, observation_id, expires_at
    INTO v_current_layer,
         v_current_msg_id,
         v_current_observation_id,
         v_current_expires_at;

    v_key_ids := ARRAY[p_raw_auth_key_id];
    IF v_actual_perm_id <> p_raw_auth_key_id THEN
        v_key_ids := array_append(v_key_ids, v_actual_perm_id);
    END IF;
    UPDATE public.auth_keys AS key
    SET layer = p_layer,
        layer_observation_id = v_observation_id
    WHERE key.auth_key_id = ANY(v_key_ids)
      AND key.layer_observation_id < v_observation_id;
    GET DIAGNOSTICS v_updated = ROW_COUNT;
    IF v_updated <> cardinality(v_key_ids) THEN
        RAISE EXCEPTION
            'publish auth session Layer defaults updated % of % locked keys',
            v_updated,
            cardinality(v_key_ids)
            USING ERRCODE = '23000';
    END IF;

    UPDATE public.authorizations AS authz
    SET layer = p_layer
    WHERE authz.auth_key_id = ANY(v_key_ids);

    RETURN QUERY SELECT
        'ok'::text, v_current_layer, v_current_msg_id,
        v_current_observation_id, v_current_expires_at,
        true, true;
END;
$$;

COMMENT ON FUNCTION public.telesrv_advance_auth_session_layer(
    bigint, bigint, integer, bigint, timestamptz
) IS 'Atomically advances durable invokeWithLayer evidence while preserving auth identity lock order';
