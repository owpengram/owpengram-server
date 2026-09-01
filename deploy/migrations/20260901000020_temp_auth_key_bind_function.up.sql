-- Collapse the identity-sensitive auth.bindTempAuthKey write boundary into one
-- database call. The caller still owns the surrounding transaction/savepoint;
-- this function preserves the global identity -> raw temp -> permanent row
-- lock order shared with selector advance, revocation and direct deletion.

CREATE OR REPLACE FUNCTION public.telesrv_bind_temp_auth_key(
    p_temp_auth_key_id bigint,
    p_perm_auth_key_id bigint,
    p_nonce bigint,
    p_temp_session_id bigint,
    p_expires_at integer,
    p_encrypted_message bytea
)
RETURNS TABLE (
    bind_status text,
    merged_layer integer,
    merged_observation_id bigint
)
LANGUAGE plpgsql
AS $$
DECLARE
    v_temp_expiry integer;
    v_temp_layer integer;
    v_temp_observation_id bigint;
    v_current_perm_id bigint;
    v_perm_expiry integer;
    v_perm_layer integer;
    v_perm_observation_id bigint;
    v_merged_layer integer;
    v_merged_observation_id bigint;
    v_updated integer;
BEGIN
    IF p_expires_at <= 0 OR p_temp_auth_key_id = p_perm_auth_key_id THEN
        RETURN QUERY SELECT 'binding_invalid'::text, 0, 0::bigint;
        RETURN;
    END IF;

    PERFORM pg_advisory_xact_lock(
        1096111176::integer,
        hashint8(p_perm_auth_key_id)::integer
    );

    SELECT key.expires_at, key.layer, key.layer_observation_id
    INTO v_temp_expiry, v_temp_layer, v_temp_observation_id
    FROM public.auth_keys AS key
    WHERE key.auth_key_id = p_temp_auth_key_id
    FOR UPDATE;
    IF NOT FOUND OR v_temp_expiry <= 0 OR v_temp_expiry <> p_expires_at THEN
        RETURN QUERY SELECT 'binding_invalid'::text, 0, 0::bigint;
        RETURN;
    END IF;

    SELECT binding.perm_auth_key_id
    INTO v_current_perm_id
    FROM public.temp_auth_key_bindings AS binding
    WHERE binding.temp_auth_key_id = p_temp_auth_key_id;
    IF FOUND AND v_current_perm_id <> p_perm_auth_key_id THEN
        RETURN QUERY SELECT 'already_bound'::text, 0, 0::bigint;
        RETURN;
    END IF;

    SELECT key.expires_at, key.layer, key.layer_observation_id
    INTO v_perm_expiry, v_perm_layer, v_perm_observation_id
    FROM public.auth_keys AS key
    WHERE key.auth_key_id = p_perm_auth_key_id
    FOR UPDATE;
    IF NOT FOUND OR v_perm_expiry <> 0 THEN
        RETURN QUERY SELECT 'binding_invalid'::text, 0, 0::bigint;
        RETURN;
    END IF;

    IF v_temp_layer < 0
       OR v_perm_layer < 0
       OR v_temp_observation_id < 0
       OR v_perm_observation_id < 0
       OR (v_temp_observation_id > 0 AND v_temp_layer = 0)
       OR (v_perm_observation_id > 0 AND v_perm_layer = 0)
    THEN
        RETURN QUERY SELECT 'layer_invalid'::text, 0, 0::bigint;
        RETURN;
    END IF;

    IF v_temp_observation_id > v_perm_observation_id THEN
        v_merged_layer := v_temp_layer;
        v_merged_observation_id := v_temp_observation_id;
    ELSIF v_perm_observation_id > v_temp_observation_id THEN
        v_merged_layer := v_perm_layer;
        v_merged_observation_id := v_perm_observation_id;
    ELSIF v_temp_observation_id > 0 AND v_temp_layer <> v_perm_layer THEN
        RETURN QUERY SELECT 'layer_conflict'::text, 0, 0::bigint;
        RETURN;
    ELSIF v_temp_observation_id > 0 THEN
        v_merged_layer := v_temp_layer;
        v_merged_observation_id := v_temp_observation_id;
    ELSE
        v_merged_layer := v_perm_layer;
        v_merged_observation_id := 0;
    END IF;

    INSERT INTO public.temp_auth_key_bindings (
        temp_auth_key_id,
        perm_auth_key_id,
        nonce,
        temp_session_id,
        expires_at,
        encrypted_message
    ) VALUES (
        p_temp_auth_key_id,
        p_perm_auth_key_id,
        p_nonce,
        p_temp_session_id,
        p_expires_at,
        p_encrypted_message
    )
    ON CONFLICT (temp_auth_key_id) DO UPDATE SET
        nonce = EXCLUDED.nonce,
        temp_session_id = EXCLUDED.temp_session_id,
        expires_at = EXCLUDED.expires_at,
        encrypted_message = EXCLUDED.encrypted_message,
        created_at = now()
    WHERE public.temp_auth_key_bindings.perm_auth_key_id = EXCLUDED.perm_auth_key_id;
    GET DIAGNOSTICS v_updated = ROW_COUNT;
    IF v_updated <> 1 THEN
        RETURN QUERY SELECT 'binding_invalid'::text, 0, 0::bigint;
        RETURN;
    END IF;

    UPDATE public.auth_keys AS key
    SET layer = v_merged_layer,
        layer_observation_id = v_merged_observation_id
    WHERE key.auth_key_id = ANY(
        ARRAY[p_temp_auth_key_id, p_perm_auth_key_id]::bigint[]
    );
    GET DIAGNOSTICS v_updated = ROW_COUNT;
    IF v_updated <> 2 THEN
        RAISE EXCEPTION
            'merge bound auth key Layer defaults updated % of 2 locked keys',
            v_updated
            USING ERRCODE = '23000';
    END IF;

    UPDATE public.authorizations AS authz
    SET layer = v_merged_layer
    WHERE authz.auth_key_id = ANY(
        ARRAY[p_temp_auth_key_id, p_perm_auth_key_id]::bigint[]
    );

    RETURN QUERY SELECT 'ok'::text, v_merged_layer, v_merged_observation_id;
END;
$$;

COMMENT ON FUNCTION public.telesrv_bind_temp_auth_key(
    bigint, bigint, bigint, bigint, integer, bytea
) IS 'Atomically binds a temporary auth key and returns the committed Layer observation while preserving identity lock order';
