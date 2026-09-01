package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"telesrv/internal/domain"
)

// ReadModelVersionBatchConfig bounds synchronous cross-request batching of
// durable read-model hash misses. MaxKeys limits the union sent to one database
// query; QueueSize limits accepted requests, not individual keys.
type ReadModelVersionBatchConfig struct {
	MaxKeys      int
	MaxWait      time.Duration
	QueueSize    int
	QueryTimeout time.Duration
}

type readModelVersionBatchRequest struct {
	ctx    context.Context
	keys   []ReadModelKey
	result chan readModelVersionBatchResult
}

type readModelVersionBatchResult struct {
	hashes map[ReadModelKey]int64
	err    error
}

// BatchedReadModelVersionStore combines contemporaneous exact-key misses into
// one base ReadModelHashes call. It is deliberately below
// CachedReadModelVersionStore: the cache still owns exact-key inflight
// singleflight, NOTIFY updates and reconnect flushes, while this layer only
// reduces cold-burst database acquisitions. There is no direct-query fallback
// when the bounded queue or shared query fails.
type BatchedReadModelVersionStore struct {
	base ReadModelVersionStore
	cfg  ReadModelVersionBatchConfig

	queue  chan readModelVersionBatchRequest
	stop   chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
	gate   sync.RWMutex
	closed bool
}

func NewBatchedReadModelVersionStore(
	base ReadModelVersionStore,
	cfg ReadModelVersionBatchConfig,
) (*BatchedReadModelVersionStore, error) {
	if base == nil {
		return nil, errors.New("initialize read-model version batcher: nil store")
	}
	if cfg.MaxKeys <= 0 || cfg.MaxKeys > 1<<16 {
		return nil, fmt.Errorf("initialize read-model version batcher: max keys %d outside [1,65536]", cfg.MaxKeys)
	}
	if cfg.MaxWait <= 0 || cfg.MaxWait > 10*time.Millisecond {
		return nil, fmt.Errorf("initialize read-model version batcher: max wait %v outside (0,10ms]", cfg.MaxWait)
	}
	if cfg.QueueSize <= 0 || cfg.QueueSize > 1<<20 {
		return nil, fmt.Errorf("initialize read-model version batcher: queue size %d outside [1,1048576]", cfg.QueueSize)
	}
	if cfg.QueryTimeout <= 0 || cfg.QueryTimeout > 30*time.Second {
		return nil, fmt.Errorf("initialize read-model version batcher: query timeout %v outside (0,30s]", cfg.QueryTimeout)
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	s := &BatchedReadModelVersionStore{
		base:   base,
		cfg:    cfg,
		queue:  make(chan readModelVersionBatchRequest, cfg.QueueSize),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		cancel: cancel,
	}
	go s.run(workerCtx)
	return s, nil
}

func (s *BatchedReadModelVersionStore) ReadModelHash(
	ctx context.Context,
	model string,
	ownerUserID int64,
	peerType domain.PeerType,
	peerID int64,
) (int64, bool, error) {
	if model == "" {
		return 0, false, nil
	}
	key := ReadModelKey{Model: model, OwnerUserID: ownerUserID, PeerType: peerType, PeerID: peerID}
	rows, err := s.ReadModelHashes(ctx, []ReadModelKey{key})
	if err != nil {
		return 0, false, err
	}
	hash := rows[key]
	return hash, hash != 0, nil
}

func (s *BatchedReadModelVersionStore) ReadModelHashes(
	ctx context.Context,
	keys []ReadModelKey,
) (map[ReadModelKey]int64, error) {
	out := make(map[ReadModelKey]int64, len(keys))
	if s == nil || s.base == nil || len(keys) == 0 {
		return out, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	unique := make([]ReadModelKey, 0, len(keys))
	seen := make(map[ReadModelKey]struct{}, len(keys))
	for _, key := range keys {
		if key.Model == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	for start := 0; start < len(unique); start += s.cfg.MaxKeys {
		end := start + s.cfg.MaxKeys
		if end > len(unique) {
			end = len(unique)
		}
		rows, err := s.readChunk(ctx, unique[start:end])
		if err != nil {
			return nil, err
		}
		for key, hash := range rows {
			out[key] = hash
		}
	}
	return out, nil
}

func (s *BatchedReadModelVersionStore) readChunk(
	ctx context.Context,
	keys []ReadModelKey,
) (map[ReadModelKey]int64, error) {
	request := readModelVersionBatchRequest{
		ctx:    ctx,
		keys:   append([]ReadModelKey(nil), keys...),
		result: make(chan readModelVersionBatchResult, 1),
	}
	s.gate.RLock()
	if s.closed {
		s.gate.RUnlock()
		return nil, context.Canceled
	}
	select {
	case s.queue <- request:
	case <-ctx.Done():
		s.gate.RUnlock()
		return nil, ctx.Err()
	}
	s.gate.RUnlock()

	select {
	case result := <-request.result:
		return result.hashes, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *BatchedReadModelVersionStore) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.gate.Lock()
		s.closed = true
		close(s.stop)
		s.cancel()
		s.gate.Unlock()
		<-s.done
	})
}

func (s *BatchedReadModelVersionStore) run(ctx context.Context) {
	defer close(s.done)
	var carry *readModelVersionBatchRequest
	for {
		batch := make([]readModelVersionBatchRequest, 0, 32)
		keyCount := 0
		if carry != nil {
			batch = append(batch, *carry)
			keyCount = len(carry.keys)
			carry = nil
		} else {
			select {
			case request := <-s.queue:
				batch = append(batch, request)
				keyCount = len(request.keys)
			case <-s.stop:
				s.failQueued(context.Canceled, nil)
				return
			}
		}

		timer := time.NewTimer(s.cfg.MaxWait)
	collect:
		for keyCount < s.cfg.MaxKeys {
			select {
			case request := <-s.queue:
				if keyCount+len(request.keys) > s.cfg.MaxKeys {
					carry = &request
					break collect
				}
				batch = append(batch, request)
				keyCount += len(request.keys)
			case <-timer.C:
				break collect
			case <-s.stop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				if carry != nil {
					batch = append(batch, *carry)
					carry = nil
				}
				s.failQueued(context.Canceled, batch)
				return
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		s.execute(ctx, batch)
	}
}

func (s *BatchedReadModelVersionStore) execute(ctx context.Context, batch []readModelVersionBatchRequest) {
	active := batch[:0]
	union := make([]ReadModelKey, 0)
	seen := make(map[ReadModelKey]struct{})
	for _, request := range batch {
		if err := request.ctx.Err(); err != nil {
			request.result <- readModelVersionBatchResult{err: err}
			continue
		}
		active = append(active, request)
		for _, key := range request.keys {
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			union = append(union, key)
		}
	}
	if len(active) == 0 {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
	loaded, err := s.base.ReadModelHashes(queryCtx, union)
	cancel()
	if err != nil {
		for _, request := range active {
			request.result <- readModelVersionBatchResult{err: err}
		}
		return
	}
	for _, request := range active {
		rows := make(map[ReadModelKey]int64, len(request.keys))
		for _, key := range request.keys {
			if hash, found := loaded[key]; found {
				rows[key] = hash
			}
		}
		request.result <- readModelVersionBatchResult{hashes: rows}
	}
}

func (s *BatchedReadModelVersionStore) failQueued(err error, pending []readModelVersionBatchRequest) {
	for _, request := range pending {
		request.result <- readModelVersionBatchResult{err: err}
	}
	for {
		select {
		case request := <-s.queue:
			request.result <- readModelVersionBatchResult{err: err}
		default:
			return
		}
	}
}

var _ ReadModelVersionStore = (*BatchedReadModelVersionStore)(nil)
