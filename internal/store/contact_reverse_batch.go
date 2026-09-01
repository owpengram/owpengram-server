package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"telesrv/internal/domain"
)

// ReverseContactBatchConfig bounds synchronous cross-request batching of the
// exact owner->viewer relationship facts used by privacy projection. MaxPairs
// limits the union sent to one database query; QueueSize limits accepted RPC
// requests rather than individual pairs.
type ReverseContactBatchConfig struct {
	MaxPairs     int
	MaxWait      time.Duration
	QueueSize    int
	QueryTimeout time.Duration
}

type reverseContactPair struct {
	ownerUserID  int64
	viewerUserID int64
}

type reverseContactBatchRequest struct {
	ctx          context.Context
	viewerUserID int64
	ownerUserIDs []int64
	result       chan reverseContactBatchResult
}

type reverseContactBatchResult struct {
	contacts map[int64]domain.Contact
	err      error
}

// BatchedReverseContactStore preserves ContactStore while replacing
// GetReverseContacts with an exact-pair batch coordinator. The embedded base
// remains authoritative for writes and all other reads. There is deliberately
// no direct-query fallback: overload and shared-query failures stay visible to
// callers instead of recreating the PostgreSQL connection storm this layer is
// intended to prevent.
type BatchedReverseContactStore struct {
	ContactStore
	sparse SparseReverseContactStore
	cfg    ReverseContactBatchConfig

	queue  chan reverseContactBatchRequest
	stop   chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
	gate   sync.RWMutex
	closed bool
}

func NewBatchedReverseContactStore(base ContactStore, cfg ReverseContactBatchConfig) (*BatchedReverseContactStore, error) {
	if base == nil {
		return nil, errors.New("initialize reverse-contact batcher: nil store")
	}
	sparse, ok := base.(SparseReverseContactStore)
	if !ok {
		return nil, errors.New("initialize reverse-contact batcher: store does not support sparse reverse reads")
	}
	if cfg.MaxPairs <= 0 || cfg.MaxPairs > 1<<16 {
		return nil, fmt.Errorf("initialize reverse-contact batcher: max pairs %d outside [1,65536]", cfg.MaxPairs)
	}
	if cfg.MaxWait <= 0 || cfg.MaxWait > 10*time.Millisecond {
		return nil, fmt.Errorf("initialize reverse-contact batcher: max wait %v outside (0,10ms]", cfg.MaxWait)
	}
	if cfg.QueueSize <= 0 || cfg.QueueSize > 1<<20 {
		return nil, fmt.Errorf("initialize reverse-contact batcher: queue size %d outside [1,1048576]", cfg.QueueSize)
	}
	if cfg.QueryTimeout <= 0 || cfg.QueryTimeout > 30*time.Second {
		return nil, fmt.Errorf("initialize reverse-contact batcher: query timeout %v outside (0,30s]", cfg.QueryTimeout)
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	s := &BatchedReverseContactStore{
		ContactStore: base,
		sparse:       sparse,
		cfg:          cfg,
		queue:        make(chan reverseContactBatchRequest, cfg.QueueSize),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
		cancel:       cancel,
	}
	go s.run(workerCtx)
	return s, nil
}

func (s *BatchedReverseContactStore) GetReverseContacts(
	ctx context.Context,
	viewerUserID int64,
	ownerUserIDs []int64,
) (map[int64]domain.Contact, error) {
	out := make(map[int64]domain.Contact, len(ownerUserIDs))
	if viewerUserID == 0 || len(ownerUserIDs) == 0 {
		return out, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	owners := canonicalPositiveInt64(ownerUserIDs)
	for start := 0; start < len(owners); start += s.cfg.MaxPairs {
		end := start + s.cfg.MaxPairs
		if end > len(owners) {
			end = len(owners)
		}
		loaded, err := s.readChunk(ctx, viewerUserID, owners[start:end])
		if err != nil {
			return nil, err
		}
		for ownerID, contact := range loaded {
			out[ownerID] = contact
		}
	}
	return out, nil
}

// ContactProjectionForViewerUserIDs preserves the optional sparse projection
// capability through this wrapper so the outer contact cache does not fall
// back to a dense viewers x targets query.
func (s *BatchedReverseContactStore) ContactProjectionForViewerUserIDs(
	ctx context.Context,
	requested map[int64][]int64,
) (domain.ContactProjectionBatch, error) {
	projection, ok := s.ContactStore.(SparseContactProjectionStore)
	if !ok {
		return domain.ContactProjectionBatch{}, errors.New("contact store does not support sparse projection")
	}
	return projection.ContactProjectionForViewerUserIDs(ctx, requested)
}

func (s *BatchedReverseContactStore) readChunk(
	ctx context.Context,
	viewerUserID int64,
	ownerUserIDs []int64,
) (map[int64]domain.Contact, error) {
	request := reverseContactBatchRequest{
		ctx:          ctx,
		viewerUserID: viewerUserID,
		ownerUserIDs: append([]int64(nil), ownerUserIDs...),
		result:       make(chan reverseContactBatchResult, 1),
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
		return result.contacts, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *BatchedReverseContactStore) Close() {
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

func (s *BatchedReverseContactStore) run(ctx context.Context) {
	defer close(s.done)
	var carry *reverseContactBatchRequest
	for {
		batch := make([]reverseContactBatchRequest, 0, 32)
		pairCount := 0
		if carry != nil {
			batch = append(batch, *carry)
			pairCount = len(carry.ownerUserIDs)
			carry = nil
		} else {
			select {
			case request := <-s.queue:
				batch = append(batch, request)
				pairCount = len(request.ownerUserIDs)
			case <-s.stop:
				s.failQueued(context.Canceled, nil)
				return
			}
		}

		timer := time.NewTimer(s.cfg.MaxWait)
	collect:
		for pairCount < s.cfg.MaxPairs {
			select {
			case request := <-s.queue:
				if pairCount+len(request.ownerUserIDs) > s.cfg.MaxPairs {
					carry = &request
					break collect
				}
				batch = append(batch, request)
				pairCount += len(request.ownerUserIDs)
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

func (s *BatchedReverseContactStore) execute(ctx context.Context, batch []reverseContactBatchRequest) {
	active := batch[:0]
	seen := make(map[reverseContactPair]struct{})
	requested := make(map[int64][]int64)
	for _, request := range batch {
		if err := request.ctx.Err(); err != nil {
			request.result <- reverseContactBatchResult{err: err}
			continue
		}
		active = append(active, request)
		for _, ownerID := range request.ownerUserIDs {
			pair := reverseContactPair{ownerUserID: ownerID, viewerUserID: request.viewerUserID}
			if _, duplicate := seen[pair]; duplicate {
				continue
			}
			seen[pair] = struct{}{}
			requested[ownerID] = append(requested[ownerID], request.viewerUserID)
		}
	}
	if len(active) == 0 {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
	loaded, err := s.sparse.GetReverseContactsForViewerUserIDs(queryCtx, requested)
	cancel()
	if err != nil {
		for _, request := range active {
			request.result <- reverseContactBatchResult{err: err}
		}
		return
	}
	for _, request := range active {
		contacts := make(map[int64]domain.Contact, len(request.ownerUserIDs))
		for _, ownerID := range request.ownerUserIDs {
			if contact, found := loaded[ownerID][request.viewerUserID]; found {
				contacts[ownerID] = contact
			}
		}
		request.result <- reverseContactBatchResult{contacts: contacts}
	}
}

func (s *BatchedReverseContactStore) failQueued(err error, pending []reverseContactBatchRequest) {
	for _, request := range pending {
		request.result <- reverseContactBatchResult{err: err}
	}
	for {
		select {
		case request := <-s.queue:
			request.result <- reverseContactBatchResult{err: err}
		default:
			return
		}
	}
}

func canonicalPositiveInt64(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

var _ ContactStore = (*BatchedReverseContactStore)(nil)
var _ SparseContactProjectionStore = (*BatchedReverseContactStore)(nil)
