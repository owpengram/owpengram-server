package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"telesrv/internal/store"
)

// AuthKeySessionLayerBatchConfig bounds the synchronous cross-session batch.
// A batch never contains the same raw auth-key/session identity twice and a
// caller does not return until its batch has committed or failed.
type AuthKeySessionLayerBatchConfig struct {
	MaxSize      int
	MaxWait      time.Duration
	QueueSize    int
	QueryTimeout time.Duration
}

type authKeySessionLayerBatchKey struct {
	rawAuthKeyID [8]byte
	sessionID    int64
}

type authKeySessionLayerBatchRequest struct {
	ctx          context.Context
	rawAuthKeyID [8]byte
	sessionID    int64
	layer        int
	msgID        int64
	expiresAt    time.Time
	result       chan authKeySessionLayerBatchResult
}

type authKeySessionLayerBatchResult struct {
	current store.AuthKeySessionLayer
	fast    bool
	err     error
}

// BatchedAuthKeySessionLayerStore preserves AuthKeySessionLayerStore semantics
// while combining contemporaneous same-Layer fast attempts for distinct
// sessions into one PostgreSQL statement. A miss is resolved synchronously by
// the original full identity transaction before the caller returns.
type BatchedAuthKeySessionLayerStore struct {
	base   *AuthKeyStore
	cfg    AuthKeySessionLayerBatchConfig
	queue  chan authKeySessionLayerBatchRequest
	stop   chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
	gate   sync.RWMutex
	closed bool
}

func NewBatchedAuthKeySessionLayerStore(
	base *AuthKeyStore,
	cfg AuthKeySessionLayerBatchConfig,
) (*BatchedAuthKeySessionLayerStore, error) {
	if base == nil || base.db == nil {
		return nil, errors.New("initialize auth key session Layer batcher: nil store")
	}
	if cfg.MaxSize <= 0 || cfg.MaxSize > 4096 {
		return nil, fmt.Errorf("initialize auth key session Layer batcher: max size %d outside [1,4096]", cfg.MaxSize)
	}
	if cfg.MaxWait <= 0 || cfg.MaxWait > 10*time.Millisecond {
		return nil, fmt.Errorf("initialize auth key session Layer batcher: max wait %v outside (0,10ms]", cfg.MaxWait)
	}
	if cfg.QueueSize < cfg.MaxSize || cfg.QueueSize > 1<<20 {
		return nil, fmt.Errorf("initialize auth key session Layer batcher: queue size %d outside [%d,%d]", cfg.QueueSize, cfg.MaxSize, 1<<20)
	}
	if cfg.QueryTimeout <= 0 || cfg.QueryTimeout > 30*time.Second {
		return nil, fmt.Errorf("initialize auth key session Layer batcher: query timeout %v outside (0,30s]", cfg.QueryTimeout)
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	s := &BatchedAuthKeySessionLayerStore{
		base: base, cfg: cfg,
		queue: make(chan authKeySessionLayerBatchRequest, cfg.QueueSize),
		stop:  make(chan struct{}), done: make(chan struct{}), cancel: cancel,
	}
	go s.run(workerCtx)
	return s, nil
}

func (s *BatchedAuthKeySessionLayerStore) GetSessionLayer(
	ctx context.Context,
	rawAuthKeyID [8]byte,
	sessionID int64,
) (store.AuthKeySessionLayer, bool, error) {
	return s.base.GetSessionLayer(ctx, rawAuthKeyID, sessionID)
}

func (s *BatchedAuthKeySessionLayerStore) AdvanceSessionLayer(
	ctx context.Context,
	rawAuthKeyID [8]byte,
	sessionID int64,
	layer int,
	msgID int64,
) (store.AuthKeySessionLayer, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	expiresAt, validMessageID := store.AuthKeySessionLayerExpiry(msgID)
	if layer <= 0 || !validMessageID {
		return store.AuthKeySessionLayer{}, false, store.ErrAuthKeySessionLayerInvalid
	}
	request := authKeySessionLayerBatchRequest{
		ctx: ctx, rawAuthKeyID: rawAuthKeyID, sessionID: sessionID,
		layer: layer, msgID: msgID, expiresAt: expiresAt,
		result: make(chan authKeySessionLayerBatchResult, 1),
	}
	s.gate.RLock()
	if s.closed {
		s.gate.RUnlock()
		return store.AuthKeySessionLayer{}, false, context.Canceled
	}
	select {
	case s.queue <- request:
	case <-ctx.Done():
		s.gate.RUnlock()
		return store.AuthKeySessionLayer{}, false, ctx.Err()
	}
	s.gate.RUnlock()

	// Once accepted by the bounded queue, wait for the worker's definitive
	// commit/error. This prevents a canceled caller from turning the submitted
	// selector into an unobserved asynchronous best-effort write.
	result := <-request.result
	if result.err != nil {
		return store.AuthKeySessionLayer{}, false, result.err
	}
	if result.fast {
		return result.current, true, nil
	}
	if err := ctx.Err(); err != nil {
		return store.AuthKeySessionLayer{}, false, err
	}
	return s.base.advanceSessionLayerFull(ctx, rawAuthKeyID, sessionID, layer, msgID, expiresAt)
}

func (s *BatchedAuthKeySessionLayerStore) DeleteSessionLayer(
	ctx context.Context,
	rawAuthKeyID [8]byte,
	sessionID int64,
) (bool, error) {
	return s.base.DeleteSessionLayer(ctx, rawAuthKeyID, sessionID)
}

func (s *BatchedAuthKeySessionLayerStore) DeleteExpiredSessionLayers(ctx context.Context, limit int) (int, error) {
	return s.base.DeleteExpiredSessionLayers(ctx, limit)
}

func (s *BatchedAuthKeySessionLayerStore) Close() {
	s.once.Do(func() {
		s.gate.Lock()
		s.closed = true
		close(s.stop)
		s.cancel()
		s.gate.Unlock()
		<-s.done
	})
}

func (s *BatchedAuthKeySessionLayerStore) run(ctx context.Context) {
	defer close(s.done)
	pending := make([]authKeySessionLayerBatchRequest, 0, s.cfg.MaxSize)
	for {
		if len(pending) == 0 {
			select {
			case request := <-s.queue:
				pending = append(pending, request)
			case <-s.stop:
				s.failQueued(context.Canceled, pending)
				return
			}
		}

		if len(pending) < s.cfg.MaxSize {
			timer := time.NewTimer(s.cfg.MaxWait)
		collect:
			for len(pending) < s.cfg.MaxSize {
				select {
				case request := <-s.queue:
					pending = append(pending, request)
				case <-timer.C:
					break collect
				case <-s.stop:
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					s.failQueued(context.Canceled, pending)
					return
				}
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}

		batch, remaining := selectDistinctLayerAdvanceBatch(pending, s.cfg.MaxSize)
		pending = remaining
		s.execute(ctx, batch)
	}
}

func selectDistinctLayerAdvanceBatch(
	pending []authKeySessionLayerBatchRequest,
	maxSize int,
) ([]authKeySessionLayerBatchRequest, []authKeySessionLayerBatchRequest) {
	batch := make([]authKeySessionLayerBatchRequest, 0, min(maxSize, len(pending)))
	remaining := make([]authKeySessionLayerBatchRequest, 0, len(pending))
	seen := make(map[authKeySessionLayerBatchKey]struct{}, min(maxSize, len(pending)))
	for _, request := range pending {
		if len(batch) >= maxSize {
			remaining = append(remaining, request)
			continue
		}
		key := authKeySessionLayerBatchKey{rawAuthKeyID: request.rawAuthKeyID, sessionID: request.sessionID}
		if _, exists := seen[key]; exists {
			remaining = append(remaining, request)
			continue
		}
		seen[key] = struct{}{}
		batch = append(batch, request)
	}
	return batch, remaining
}

func (s *BatchedAuthKeySessionLayerStore) execute(ctx context.Context, batch []authKeySessionLayerBatchRequest) {
	active := batch[:0]
	for _, request := range batch {
		if err := request.ctx.Err(); err != nil {
			request.result <- authKeySessionLayerBatchResult{err: err}
			continue
		}
		active = append(active, request)
	}
	if len(active) == 0 {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
	results, err := s.base.tryAdvanceSessionLayersSameLayer(queryCtx, active)
	cancel()
	if err != nil {
		for _, request := range active {
			request.result <- authKeySessionLayerBatchResult{err: err}
		}
		return
	}
	for index, request := range active {
		request.result <- results[index]
	}
}

func (s *BatchedAuthKeySessionLayerStore) failQueued(err error, pending []authKeySessionLayerBatchRequest) {
	for _, request := range pending {
		request.result <- authKeySessionLayerBatchResult{err: err}
	}
	for {
		select {
		case request := <-s.queue:
			request.result <- authKeySessionLayerBatchResult{err: err}
		default:
			return
		}
	}
}

func (s *AuthKeyStore) tryAdvanceSessionLayersSameLayer(
	ctx context.Context,
	requests []authKeySessionLayerBatchRequest,
) ([]authKeySessionLayerBatchResult, error) {
	results := make([]authKeySessionLayerBatchResult, len(requests))
	if len(requests) == 0 {
		return results, nil
	}
	rawIDs := make([]int64, len(requests))
	sessionIDs := make([]int64, len(requests))
	layers := make([]int32, len(requests))
	msgIDs := make([]int64, len(requests))
	expiresAts := make([]time.Time, len(requests))
	seen := make(map[authKeySessionLayerBatchKey]struct{}, len(requests))
	for index, request := range requests {
		key := authKeySessionLayerBatchKey{rawAuthKeyID: request.rawAuthKeyID, sessionID: request.sessionID}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("advance same-Layer auth key session batch: duplicate identity at index %d", index)
		}
		seen[key] = struct{}{}
		rawIDs[index] = authKeyIDToInt64(request.rawAuthKeyID)
		sessionIDs[index] = request.sessionID
		layers[index] = int32(request.layer)
		msgIDs[index] = request.msgID
		expiresAts[index] = request.expiresAt
	}
	rows, err := s.db.Query(ctx, `
WITH input AS (
  SELECT *
  FROM unnest(
    $1::bigint[],
    $2::bigint[],
    $3::integer[],
    $4::bigint[],
    $5::timestamptz[]
  ) WITH ORDINALITY AS value(raw_id, session_id, layer, msg_id, expires_at, ordinal)
), identity AS MATERIALIZED (
  SELECT input.*,
         defaults.layer AS default_layer,
         defaults.layer_observation_id AS default_observation_id
  FROM input
  JOIN auth_keys AS raw
    ON raw.auth_key_id = input.raw_id
  LEFT JOIN temp_auth_key_bindings AS binding
    ON binding.temp_auth_key_id = raw.auth_key_id
  JOIN auth_keys AS defaults
    ON defaults.auth_key_id = COALESCE(binding.perm_auth_key_id, raw.auth_key_id)
  WHERE binding.temp_auth_key_id IS NULL
     OR (raw.expires_at > 0 AND defaults.expires_at = 0)
), candidates AS MATERIALIZED (
  SELECT identity.ordinal,
         identity.msg_id,
         identity.expires_at,
         identity.default_layer,
         identity.default_observation_id,
         evidence.raw_auth_key_id,
         evidence.session_id,
         evidence.layer,
         evidence.observation_id
  FROM identity
  JOIN auth_key_session_layers AS evidence
    ON evidence.raw_auth_key_id = identity.raw_id
   AND evidence.session_id = identity.session_id
  WHERE evidence.layer = identity.layer
    AND evidence.msg_id < identity.msg_id
    AND evidence.expires_at > now()
    AND identity.layer > 0
    AND identity.msg_id > 0
    AND identity.msg_id % 4 = 0
    AND (identity.msg_id & 4294967295) <> 0
    AND identity.expires_at > now()
    AND identity.expires_at - interval '301 seconds' <= now() + interval '30 seconds'
  ORDER BY evidence.raw_auth_key_id, evidence.session_id
  FOR UPDATE OF evidence
), advanced AS (
  UPDATE auth_key_session_layers AS evidence
  SET msg_id = candidates.msg_id,
      expires_at = candidates.expires_at
  FROM candidates
  WHERE evidence.raw_auth_key_id = candidates.raw_auth_key_id
    AND evidence.session_id = candidates.session_id
  RETURNING candidates.ordinal,
            candidates.default_layer,
            candidates.default_observation_id,
            evidence.layer,
            evidence.msg_id,
            evidence.observation_id,
            evidence.expires_at
)
SELECT ordinal,
       layer,
       msg_id,
       observation_id,
       expires_at,
       default_layer = layer AND default_observation_id = observation_id
FROM advanced
ORDER BY ordinal
`, rawIDs, sessionIDs, layers, msgIDs, expiresAts)
	if err != nil {
		return nil, fmt.Errorf("advance same-Layer auth key session batch: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			ordinal int64
			current store.AuthKeySessionLayer
		)
		if err := rows.Scan(
			&ordinal,
			&current.Layer,
			&current.MessageID,
			&current.ObservationID,
			&current.ExpiresAt,
			&current.SharedDefault,
		); err != nil {
			return nil, fmt.Errorf("scan same-Layer auth key session batch: %w", err)
		}
		index := int(ordinal - 1)
		if index < 0 || index >= len(results) || results[index].fast {
			return nil, fmt.Errorf("advance same-Layer auth key session batch: invalid ordinal %d", ordinal)
		}
		results[index] = authKeySessionLayerBatchResult{current: current, fast: true}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("advance same-Layer auth key session batch rows: %w", err)
	}
	return results, nil
}

var _ store.AuthKeySessionLayerStore = (*BatchedAuthKeySessionLayerStore)(nil)
