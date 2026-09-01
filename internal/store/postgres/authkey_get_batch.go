package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"telesrv/internal/store"
)

// AuthKeyGetBatchConfig bounds the synchronous first-frame auth-key lookup.
// Every accepted Get waits until its durable last_used_at touch has completed;
// this is not an asynchronous activity update.
type AuthKeyGetBatchConfig struct {
	MaxSize      int
	MaxWait      time.Duration
	QueueSize    int
	QueryTimeout time.Duration
}

type authKeyGetBatchRequest struct {
	ctx    context.Context
	id     [8]byte
	result chan authKeyGetBatchResult
}

type authKeyGetBatchResult struct {
	data  store.AuthKeyData
	found bool
	err   error
}

// BatchedAuthKeyStore preserves store.AuthKeyStore semantics while combining
// contemporaneous first-frame Get calls into one PostgreSQL UPDATE ...
// RETURNING statement. Save/revalidate/bind/client-info/delete remain direct
// authority operations on the base store.
type BatchedAuthKeyStore struct {
	base *AuthKeyStore
	cfg  AuthKeyGetBatchConfig

	touchQueue      chan authKeyGetBatchRequest
	revalidateQueue chan authKeyGetBatchRequest
	stop            chan struct{}
	cancel          context.CancelFunc
	once            sync.Once
	workers         sync.WaitGroup
	gate            sync.RWMutex
	closed          bool
}

func NewBatchedAuthKeyStore(base *AuthKeyStore, cfg AuthKeyGetBatchConfig) (*BatchedAuthKeyStore, error) {
	if base == nil || base.db == nil {
		return nil, errors.New("initialize auth-key get batcher: nil store")
	}
	if cfg.MaxSize <= 0 || cfg.MaxSize > 4096 {
		return nil, fmt.Errorf("initialize auth-key get batcher: max size %d outside [1,4096]", cfg.MaxSize)
	}
	if cfg.MaxWait <= 0 || cfg.MaxWait > 10*time.Millisecond {
		return nil, fmt.Errorf("initialize auth-key get batcher: max wait %v outside (0,10ms]", cfg.MaxWait)
	}
	if cfg.QueueSize < cfg.MaxSize || cfg.QueueSize > 1<<20 {
		return nil, fmt.Errorf("initialize auth-key get batcher: queue size %d outside [%d,%d]", cfg.QueueSize, cfg.MaxSize, 1<<20)
	}
	if cfg.QueryTimeout <= 0 || cfg.QueryTimeout > 30*time.Second {
		return nil, fmt.Errorf("initialize auth-key get batcher: query timeout %v outside (0,30s]", cfg.QueryTimeout)
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	s := &BatchedAuthKeyStore{
		base: base, cfg: cfg,
		touchQueue:      make(chan authKeyGetBatchRequest, cfg.QueueSize),
		revalidateQueue: make(chan authKeyGetBatchRequest, cfg.QueueSize),
		stop:            make(chan struct{}), cancel: cancel,
	}
	s.workers.Add(2)
	go s.run(workerCtx, s.touchQueue, true)
	go s.run(workerCtx, s.revalidateQueue, false)
	return s, nil
}

func (s *BatchedAuthKeyStore) Save(ctx context.Context, key store.AuthKeyData) error {
	return s.base.Save(ctx, key)
}

func (s *BatchedAuthKeyStore) Get(ctx context.Context, id [8]byte) (store.AuthKeyData, bool, error) {
	return s.lookup(ctx, id, s.touchQueue, true)
}

func (s *BatchedAuthKeyStore) lookup(
	ctx context.Context,
	id [8]byte,
	queue chan authKeyGetBatchRequest,
	waitDefinitive bool,
) (store.AuthKeyData, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request := authKeyGetBatchRequest{ctx: ctx, id: id, result: make(chan authKeyGetBatchResult, 1)}
	s.gate.RLock()
	if s.closed {
		s.gate.RUnlock()
		return store.AuthKeyData{}, false, context.Canceled
	}
	select {
	case queue <- request:
	case <-ctx.Done():
		s.gate.RUnlock()
		return store.AuthKeyData{}, false, ctx.Err()
	}
	s.gate.RUnlock()

	if waitDefinitive {
		// Get owns a durable activity touch. Once admitted, wait for its
		// definitive result even if the transport context is canceled, so a
		// submitted write is never left as unobserved best effort.
		result := <-request.result
		return result.data, result.found, result.err
	}
	select {
	case result := <-request.result:
		return result.data, result.found, result.err
	case <-ctx.Done():
		return store.AuthKeyData{}, false, ctx.Err()
	}
}

func (s *BatchedAuthKeyStore) Revalidate(ctx context.Context, id [8]byte) (store.AuthKeyData, bool, error) {
	return s.lookup(ctx, id, s.revalidateQueue, false)
}

func (s *BatchedAuthKeyStore) LoadBindingKeys(ctx context.Context, tempID, permID [8]byte) (store.AuthKeyBindingKeys, error) {
	return s.base.LoadBindingKeys(ctx, tempID, permID)
}

func (s *BatchedAuthKeyStore) UpdateClientInfo(ctx context.Context, id [8]byte, info store.AuthKeyClientInfo) error {
	return s.base.UpdateClientInfo(ctx, id, info)
}

func (s *BatchedAuthKeyStore) Delete(ctx context.Context, id [8]byte) error {
	return s.base.Delete(ctx, id)
}

func (s *BatchedAuthKeyStore) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.gate.Lock()
		s.closed = true
		close(s.stop)
		s.cancel()
		s.gate.Unlock()
		s.workers.Wait()
	})
}

func (s *BatchedAuthKeyStore) run(
	ctx context.Context,
	queue chan authKeyGetBatchRequest,
	touch bool,
) {
	defer s.workers.Done()
	pending := make([]authKeyGetBatchRequest, 0, s.cfg.MaxSize)
	for {
		if len(pending) == 0 {
			select {
			case request := <-queue:
				pending = append(pending, request)
			case <-s.stop:
				failAuthKeyGetQueued(queue, context.Canceled, nil)
				return
			}
		}
		if len(pending) < s.cfg.MaxSize {
			timer := time.NewTimer(s.cfg.MaxWait)
		collect:
			for len(pending) < s.cfg.MaxSize {
				select {
				case request := <-queue:
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
					failAuthKeyGetQueued(queue, context.Canceled, pending)
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
		batch := append([]authKeyGetBatchRequest(nil), pending...)
		pending = pending[:0]
		s.execute(ctx, batch, touch)
	}
}

func (s *BatchedAuthKeyStore) execute(ctx context.Context, batch []authKeyGetBatchRequest, touch bool) {
	active := batch[:0]
	ids := make([][8]byte, 0, len(batch))
	seen := make(map[[8]byte]struct{}, len(batch))
	for _, request := range batch {
		if err := request.ctx.Err(); err != nil {
			request.result <- authKeyGetBatchResult{err: err}
			continue
		}
		active = append(active, request)
		if _, duplicate := seen[request.id]; duplicate {
			continue
		}
		seen[request.id] = struct{}{}
		ids = append(ids, request.id)
	}
	if len(active) == 0 {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
	var (
		loaded map[[8]byte]store.AuthKeyData
		err    error
	)
	if touch {
		loaded, err = s.base.getManyAndTouch(queryCtx, ids)
	} else {
		loaded, err = s.base.getMany(queryCtx, ids)
	}
	cancel()
	if err != nil {
		for _, request := range active {
			request.result <- authKeyGetBatchResult{err: err}
		}
		return
	}
	for _, request := range active {
		data, found := loaded[request.id]
		request.result <- authKeyGetBatchResult{data: data, found: found}
	}
}

func failAuthKeyGetQueued(queue chan authKeyGetBatchRequest, err error, pending []authKeyGetBatchRequest) {
	for _, request := range pending {
		request.result <- authKeyGetBatchResult{err: err}
	}
	for {
		select {
		case request := <-queue:
			request.result <- authKeyGetBatchResult{err: err}
		default:
			return
		}
	}
}

func (s *AuthKeyStore) getManyAndTouch(ctx context.Context, ids [][8]byte) (map[[8]byte]store.AuthKeyData, error) {
	if len(ids) == 0 {
		return map[[8]byte]store.AuthKeyData{}, nil
	}
	keyIDs, requested := authKeyBatchIDs(ids)
	rows, err := s.db.Query(ctx, `
/* auth_key_get_batch */
UPDATE auth_keys
SET last_used_at = now()
WHERE auth_key_id = ANY($1::bigint[])
RETURNING auth_key_id, body, server_salt, created_at,
       expires_at, layer, layer_observation_id,
       device_model, platform, system_version, api_id, app_version
`, keyIDs)
	if err != nil {
		return nil, fmt.Errorf("batch get auth keys: %w", err)
	}
	return scanAuthKeyBatch(rows, requested, "batched auth key")
}

func (s *AuthKeyStore) getMany(ctx context.Context, ids [][8]byte) (map[[8]byte]store.AuthKeyData, error) {
	if len(ids) == 0 {
		return map[[8]byte]store.AuthKeyData{}, nil
	}
	keyIDs, requested := authKeyBatchIDs(ids)
	rows, err := s.db.Query(ctx, `
/* auth_key_revalidate_batch */
SELECT auth_key_id, body, server_salt, created_at,
       expires_at, layer, layer_observation_id,
       device_model, platform, system_version, api_id, app_version
FROM auth_keys
WHERE auth_key_id = ANY($1::bigint[])
`, keyIDs)
	if err != nil {
		return nil, fmt.Errorf("batch revalidate auth keys: %w", err)
	}
	return scanAuthKeyBatch(rows, requested, "revalidated auth key")
}

func authKeyBatchIDs(ids [][8]byte) ([]int64, map[[8]byte]struct{}) {
	keyIDs := make([]int64, 0, len(ids))
	requested := make(map[[8]byte]struct{}, len(ids))
	for _, id := range ids {
		if _, duplicate := requested[id]; duplicate {
			continue
		}
		requested[id] = struct{}{}
		keyIDs = append(keyIDs, authKeyIDToInt64(id))
	}
	return keyIDs, requested
}

func scanAuthKeyBatch(
	rows interface {
		Next() bool
		Scan(...any) error
		Err() error
		Close()
	},
	requested map[[8]byte]struct{},
	operation string,
) (map[[8]byte]store.AuthKeyData, error) {
	defer rows.Close()
	out := make(map[[8]byte]store.AuthKeyData, len(requested))
	for rows.Next() {
		data, scanErr := scanAuthKeyData(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan %s: %w", operation, scanErr)
		}
		if _, expected := requested[data.ID]; !expected {
			return nil, fmt.Errorf("%s returned unexpected id %x", operation, data.ID)
		}
		out[data.ID] = data
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", operation, err)
	}
	return out, nil
}

var _ store.AuthKeyStore = (*BatchedAuthKeyStore)(nil)
