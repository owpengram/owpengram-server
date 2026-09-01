package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/store"
)

const maxAuthKeySessionLayerDeleteBatch = 100000

func (s *AuthKeyStore) GetSessionLayer(
	ctx context.Context,
	rawAuthKeyID [8]byte,
	sessionID int64,
) (store.AuthKeySessionLayer, bool, error) {
	var value store.AuthKeySessionLayer
	err := s.db.QueryRow(ctx, `
SELECT evidence.layer,
       evidence.msg_id,
       evidence.observation_id,
       evidence.expires_at,
       defaults.layer = evidence.layer
         AND defaults.layer_observation_id = evidence.observation_id
FROM auth_key_session_layers AS evidence
LEFT JOIN temp_auth_key_bindings AS binding
  ON binding.temp_auth_key_id = evidence.raw_auth_key_id
JOIN auth_keys AS defaults
  ON defaults.auth_key_id = COALESCE(binding.perm_auth_key_id, evidence.raw_auth_key_id)
WHERE evidence.raw_auth_key_id = $1
  AND evidence.session_id = $2
  AND evidence.expires_at > now()
`, authKeyIDToInt64(rawAuthKeyID), sessionID).Scan(
		&value.Layer,
		&value.MessageID,
		&value.ObservationID,
		&value.ExpiresAt,
		&value.SharedDefault,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.AuthKeySessionLayer{}, false, nil
	}
	if err != nil {
		return store.AuthKeySessionLayer{}, false, fmt.Errorf("get auth key session layer: %w", err)
	}
	return value, true, nil
}

// AdvanceSessionLayer enters the permanent identity advisory gate before any
// row lock when rawAuthKeyID is permanent or already-bound temporary. An
// initially-unbound temp key that becomes bound while the raw row is acquired
// rolls the attempt back and retries in the new identity. The session watermark
// and every currently bound shared default then commit in one transaction.
func (s *AuthKeyStore) AdvanceSessionLayer(
	ctx context.Context,
	rawAuthKeyID [8]byte,
	sessionID int64,
	layer int,
	msgID int64,
) (store.AuthKeySessionLayer, bool, error) {
	expiresAt, validMessageID := store.AuthKeySessionLayerExpiry(msgID)
	if layer <= 0 || !validMessageID {
		return store.AuthKeySessionLayer{}, false, store.ErrAuthKeySessionLayerInvalid
	}
	current, advanced, err := s.tryAdvanceSessionLayerSameLayer(
		ctx, authKeyIDToInt64(rawAuthKeyID), sessionID, layer, msgID, expiresAt,
	)
	if err != nil {
		return store.AuthKeySessionLayer{}, false, err
	}
	if advanced {
		return current, true, nil
	}
	return s.advanceSessionLayerFull(ctx, rawAuthKeyID, sessionID, layer, msgID, expiresAt)
}

func (s *AuthKeyStore) advanceSessionLayerFull(
	ctx context.Context,
	rawAuthKeyID [8]byte,
	sessionID int64,
	layer int,
	msgID int64,
	expiresAt time.Time,
) (store.AuthKeySessionLayer, bool, error) {
	var (
		current store.AuthKeySessionLayer
		applied bool
	)
	err := withAuthIdentityTx(ctx, s.db, "advance auth key session layer", func(tx pgx.Tx) error {
		var err error
		current, applied, err = advanceSessionLayerTx(
			ctx, tx, authKeyIDToInt64(rawAuthKeyID), sessionID, layer, msgID, expiresAt,
		)
		return err
	})
	if err != nil {
		return current, false, err
	}
	return current, applied, nil
}

// tryAdvanceSessionLayerSameLayer is the common invokeWithLayer path once an
// exact session has established its profile generation. It keeps the durable
// msg_id high-water mark exact while avoiding the identity gate, observation
// allocation and shared-default rewrites that are only needed when the Layer
// itself changes. The identity CTE admits only a structurally valid raw/bound
// key; every miss falls through to the full locked state machine.
func (s *AuthKeyStore) tryAdvanceSessionLayerSameLayer(
	ctx context.Context,
	rawID int64,
	sessionID int64,
	layer int,
	msgID int64,
	expiresAt time.Time,
) (store.AuthKeySessionLayer, bool, error) {
	var current store.AuthKeySessionLayer
	err := s.db.QueryRow(ctx, `
WITH identity AS MATERIALIZED (
  SELECT raw.auth_key_id,
         defaults.layer AS default_layer,
         defaults.layer_observation_id AS default_observation_id
  FROM auth_keys AS raw
  LEFT JOIN temp_auth_key_bindings AS binding
    ON binding.temp_auth_key_id = raw.auth_key_id
  JOIN auth_keys AS defaults
    ON defaults.auth_key_id = COALESCE(binding.perm_auth_key_id, raw.auth_key_id)
  WHERE raw.auth_key_id = $1
    AND (
      binding.temp_auth_key_id IS NULL
      OR (raw.expires_at > 0 AND defaults.expires_at = 0)
    )
), advanced AS (
  UPDATE auth_key_session_layers AS evidence
  SET msg_id = $4,
      expires_at = $5
  FROM identity
  WHERE evidence.raw_auth_key_id = $1
    AND evidence.session_id = $2
    AND evidence.layer = $3
    AND evidence.msg_id < $4
    AND evidence.expires_at > now()
    AND $3 > 0
    AND $4 > 0
    AND $4 % 4 = 0
    AND ($4 & 4294967295) <> 0
    AND $5 > now()
    AND $5 - interval '301 seconds' <= now() + interval '30 seconds'
  RETURNING evidence.layer,
            evidence.msg_id,
            evidence.observation_id,
            evidence.expires_at
)
SELECT advanced.layer,
       advanced.msg_id,
       advanced.observation_id,
       advanced.expires_at,
       identity.default_layer = advanced.layer
         AND identity.default_observation_id = advanced.observation_id
FROM advanced
CROSS JOIN identity
`, rawID, sessionID, layer, msgID, expiresAt).Scan(
		&current.Layer,
		&current.MessageID,
		&current.ObservationID,
		&current.ExpiresAt,
		&current.SharedDefault,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.AuthKeySessionLayer{}, false, nil
	}
	if err != nil {
		return store.AuthKeySessionLayer{}, false, fmt.Errorf("advance same-Layer auth key session watermark: %w", err)
	}
	return current, true, nil
}

func advanceSessionLayerTx(
	ctx context.Context,
	tx pgx.Tx,
	rawID int64,
	sessionID int64,
	layer int,
	msgID int64,
	expiresAt time.Time,
) (store.AuthKeySessionLayer, bool, error) {
	var (
		status  string
		current store.AuthKeySessionLayer
		applied bool
	)
	err := tx.QueryRow(ctx, `
SELECT advance_status,
       current_layer,
       current_msg_id,
       current_observation_id,
       current_expires_at,
       shared_default,
       applied
FROM public.telesrv_advance_auth_session_layer($1, $2, $3, $4, $5)
`, rawID, sessionID, layer, msgID, expiresAt).Scan(
		&status,
		&current.Layer,
		&current.MessageID,
		&current.ObservationID,
		&current.ExpiresAt,
		&current.SharedDefault,
		&applied,
	)
	if err != nil {
		return store.AuthKeySessionLayer{}, false, fmt.Errorf("advance auth key session layer: %w", err)
	}
	switch status {
	case "ok":
		return current, applied, nil
	case "identity_changed":
		return store.AuthKeySessionLayer{}, false, errAuthIdentityChanged
	case "auth_key_not_found":
		return store.AuthKeySessionLayer{}, false, store.ErrAuthKeyNotFound
	case "binding_invalid":
		return store.AuthKeySessionLayer{}, false, store.ErrAuthKeyBindingInvalid
	case "evidence_invalid":
		return store.AuthKeySessionLayer{}, false, store.ErrAuthKeySessionLayerInvalid
	case "conflict":
		return current, false, store.ErrAuthKeySessionLayerConflict
	default:
		return store.AuthKeySessionLayer{}, false, fmt.Errorf("advance auth key session layer: unknown database status %q", status)
	}
}

func (s *AuthKeyStore) DeleteSessionLayer(
	ctx context.Context,
	rawAuthKeyID [8]byte,
	sessionID int64,
) (bool, error) {
	tag, err := s.db.Exec(ctx, `
DELETE FROM auth_key_session_layers
WHERE raw_auth_key_id = $1 AND session_id = $2
`, authKeyIDToInt64(rawAuthKeyID), sessionID)
	if err != nil {
		return false, fmt.Errorf("delete auth key session layer: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *AuthKeyStore) DeleteExpiredSessionLayers(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	if limit > maxAuthKeySessionLayerDeleteBatch {
		limit = maxAuthKeySessionLayerDeleteBatch
	}
	var deleted int
	err := s.db.QueryRow(ctx, `
WITH candidates AS MATERIALIZED (
  SELECT raw_auth_key_id, session_id
  FROM auth_key_session_layers
  WHERE expires_at <= now()
  ORDER BY expires_at, raw_auth_key_id, session_id
  LIMIT $1
  FOR UPDATE SKIP LOCKED
), removed AS (
  DELETE FROM auth_key_session_layers AS evidence
  USING candidates
  WHERE evidence.raw_auth_key_id = candidates.raw_auth_key_id
    AND evidence.session_id = candidates.session_id
    AND evidence.expires_at <= now()
  RETURNING 1
)
SELECT count(*)::integer FROM removed
`, limit).Scan(&deleted)
	if err != nil {
		return 0, fmt.Errorf("delete expired auth key session layers: %w", err)
	}
	return deleted, nil
}
