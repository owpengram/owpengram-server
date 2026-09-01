package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// BootstrapReadyBatchMetrics exposes only bounded aggregate signals. Selector
// identities are deliberately excluded from metrics.
type BootstrapReadyBatchMetrics interface {
	BootstrapReadyBatch(inputs int, matched int, d time.Duration, err error)
	BootstrapReadyPending(delta int)
}

type BootstrapReadyBatchConfig struct {
	MaxSize      int
	MaxWait      time.Duration
	QueueSize    int
	QueryTimeout time.Duration
	Metrics      BootstrapReadyBatchMetrics
}

type bootstrapReadyBatchKey struct {
	userID    int64
	authKeyID [8]byte
}

type bootstrapReadyBatchRequest struct {
	userID    int64
	authKeyID [8]byte
	sessionID int64
	result    chan bootstrapReadyBatchResult
}

type bootstrapReadyBatchResult struct {
	matched int
	err     error
}

type bootstrapReadyBatchBackend interface {
	store.BootstrapUpdateJobStore
	markReadyForSessions(context.Context, []bootstrapReadyBatchRequest) ([]int, error)
}

// BatchedBootstrapUpdateJobStore preserves the synchronous post-response
// delivery fence while combining independent readiness selectors into one
// PostgreSQL statement. Once accepted, a selector waits for a definitive
// commit/error; it is never converted into an unobserved background write.
type BatchedBootstrapUpdateJobStore struct {
	base   bootstrapReadyBatchBackend
	cfg    BootstrapReadyBatchConfig
	queue  chan bootstrapReadyBatchRequest
	stop   chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
	gate   sync.RWMutex
	closed bool
}

func NewBatchedBootstrapUpdateJobStore(
	base *BootstrapUpdateJobStore,
	cfg BootstrapReadyBatchConfig,
) (*BatchedBootstrapUpdateJobStore, error) {
	if base == nil || base.db == nil {
		return nil, errors.New("initialize bootstrap readiness batcher: nil store")
	}
	return newBatchedBootstrapUpdateJobStore(base, cfg)
}

func newBatchedBootstrapUpdateJobStore(
	base bootstrapReadyBatchBackend,
	cfg BootstrapReadyBatchConfig,
) (*BatchedBootstrapUpdateJobStore, error) {
	if base == nil {
		return nil, errors.New("initialize bootstrap readiness batcher: nil backend")
	}
	if cfg.MaxSize <= 0 || cfg.MaxSize > 4096 {
		return nil, fmt.Errorf("initialize bootstrap readiness batcher: max size %d outside [1,4096]", cfg.MaxSize)
	}
	if cfg.MaxWait <= 0 || cfg.MaxWait > time.Second {
		return nil, fmt.Errorf("initialize bootstrap readiness batcher: max wait %v outside (0,1s]", cfg.MaxWait)
	}
	if cfg.QueueSize < cfg.MaxSize || cfg.QueueSize > 1<<20 {
		return nil, fmt.Errorf("initialize bootstrap readiness batcher: queue size %d outside [%d,%d]", cfg.QueueSize, cfg.MaxSize, 1<<20)
	}
	if cfg.QueryTimeout <= 0 || cfg.QueryTimeout > 30*time.Second {
		return nil, fmt.Errorf("initialize bootstrap readiness batcher: query timeout %v outside (0,30s]", cfg.QueryTimeout)
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	s := &BatchedBootstrapUpdateJobStore{
		base: base, cfg: cfg,
		queue: make(chan bootstrapReadyBatchRequest, cfg.QueueSize),
		stop:  make(chan struct{}), done: make(chan struct{}), cancel: cancel,
	}
	go s.run(workerCtx)
	return s, nil
}

func (s *BatchedBootstrapUpdateJobStore) EnqueueLoginMessage(
	ctx context.Context,
	job domain.BootstrapUpdateJob,
) (domain.BootstrapUpdateJob, error) {
	return s.base.EnqueueLoginMessage(ctx, job)
}

func (s *BatchedBootstrapUpdateJobStore) MarkReadyForSession(
	ctx context.Context,
	userID int64,
	authKeyID [8]byte,
	sessionID int64,
) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request := bootstrapReadyBatchRequest{
		userID: userID, authKeyID: authKeyID, sessionID: sessionID,
		result: make(chan bootstrapReadyBatchResult, 1),
	}
	s.gate.RLock()
	if s.closed {
		s.gate.RUnlock()
		return 0, context.Canceled
	}
	select {
	case s.queue <- request:
		if s.cfg.Metrics != nil {
			s.cfg.Metrics.BootstrapReadyPending(1)
		}
	case <-ctx.Done():
		s.gate.RUnlock()
		return 0, ctx.Err()
	}
	s.gate.RUnlock()

	// Accepted work ignores later caller cancellation and waits for the worker's
	// definitive result. This prevents a physically delivered baseline from
	// leaving an unknown asynchronous readiness mutation behind.
	result := <-request.result
	return result.matched, result.err
}

func (s *BatchedBootstrapUpdateJobStore) ClaimReady(
	ctx context.Context,
	limit int,
	leaseTimeout time.Duration,
) ([]domain.BootstrapUpdateJob, error) {
	return s.base.ClaimReady(ctx, limit, leaseTimeout)
}

func (s *BatchedBootstrapUpdateJobStore) MarkPublished(ctx context.Context, id int64) error {
	return s.base.MarkPublished(ctx, id)
}

func (s *BatchedBootstrapUpdateJobStore) MarkFailed(ctx context.Context, id int64, lastError string) error {
	return s.base.MarkFailed(ctx, id, lastError)
}

func (s *BatchedBootstrapUpdateJobStore) Close() {
	s.once.Do(func() {
		s.gate.Lock()
		s.closed = true
		close(s.stop)
		s.cancel()
		s.gate.Unlock()
		<-s.done
	})
}

func (s *BatchedBootstrapUpdateJobStore) run(ctx context.Context) {
	defer close(s.done)
	pending := make([]bootstrapReadyBatchRequest, 0, s.cfg.MaxSize)
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
					stopAndDrainTimer(timer)
					s.failQueued(context.Canceled, pending)
					return
				}
			}
			stopAndDrainTimer(timer)
		}

		batch, remaining := selectDistinctBootstrapReadyBatch(pending, s.cfg.MaxSize)
		pending = remaining
		s.execute(ctx, batch)
	}
}

func stopAndDrainTimer(timer *time.Timer) {
	if timer != nil && !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func selectDistinctBootstrapReadyBatch(
	pending []bootstrapReadyBatchRequest,
	maxSize int,
) ([]bootstrapReadyBatchRequest, []bootstrapReadyBatchRequest) {
	batch := make([]bootstrapReadyBatchRequest, 0, min(maxSize, len(pending)))
	remaining := make([]bootstrapReadyBatchRequest, 0, len(pending))
	seen := make(map[bootstrapReadyBatchKey]struct{}, min(maxSize, len(pending)))
	for _, request := range pending {
		if len(batch) >= maxSize {
			remaining = append(remaining, request)
			continue
		}
		key := bootstrapReadyBatchKey{userID: request.userID, authKeyID: request.authKeyID}
		if _, exists := seen[key]; exists {
			remaining = append(remaining, request)
			continue
		}
		seen[key] = struct{}{}
		batch = append(batch, request)
	}
	return batch, remaining
}

func (s *BatchedBootstrapUpdateJobStore) execute(ctx context.Context, batch []bootstrapReadyBatchRequest) {
	if len(batch) == 0 {
		return
	}
	started := time.Now()
	queryCtx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
	results, err := s.base.markReadyForSessions(queryCtx, batch)
	cancel()
	matched := 0
	if err == nil {
		if len(results) != len(batch) {
			err = fmt.Errorf("mark bootstrap readiness batch: result count %d, want %d", len(results), len(batch))
		} else {
			for _, count := range results {
				matched += count
			}
		}
	}
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.BootstrapReadyBatch(len(batch), matched, time.Since(started), err)
	}
	for index, request := range batch {
		result := bootstrapReadyBatchResult{err: err}
		if err == nil {
			result.matched = results[index]
		}
		request.result <- result
		if s.cfg.Metrics != nil {
			s.cfg.Metrics.BootstrapReadyPending(-1)
		}
	}
}

func (s *BatchedBootstrapUpdateJobStore) failQueued(err error, pending []bootstrapReadyBatchRequest) {
	for _, request := range pending {
		s.failRequest(request, err)
	}
	for {
		select {
		case request := <-s.queue:
			s.failRequest(request, err)
		default:
			return
		}
	}
}

func (s *BatchedBootstrapUpdateJobStore) failRequest(request bootstrapReadyBatchRequest, err error) {
	request.result <- bootstrapReadyBatchResult{err: err}
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.BootstrapReadyPending(-1)
	}
}

func (s *BootstrapUpdateJobStore) markReadyForSessions(
	ctx context.Context,
	requests []bootstrapReadyBatchRequest,
) ([]int, error) {
	results := make([]int, len(requests))
	if len(requests) == 0 {
		return results, nil
	}
	userIDs := make([]int64, len(requests))
	authKeyIDs := make([]int64, len(requests))
	sessionIDs := make([]int64, len(requests))
	seen := make(map[bootstrapReadyBatchKey]struct{}, len(requests))
	for index, request := range requests {
		key := bootstrapReadyBatchKey{userID: request.userID, authKeyID: request.authKeyID}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("mark bootstrap readiness batch: duplicate fence at index %d", index)
		}
		seen[key] = struct{}{}
		userIDs[index] = request.userID
		authKeyIDs[index] = authKeyIDToInt64(request.authKeyID)
		sessionIDs[index] = request.sessionID
	}
	rows, err := s.db.Query(ctx, `
WITH input AS (
  SELECT *
  FROM unnest(
    $1::bigint[],
    $2::bigint[],
    $3::bigint[]
  ) WITH ORDINALITY AS value(user_id, auth_key_id, session_id, ordinal)
), candidates AS MATERIALIZED (
  SELECT input.ordinal,
         input.session_id,
         jobs.id
  FROM input
  JOIN bootstrap_update_jobs AS jobs
    ON jobs.user_id = input.user_id
   AND jobs.auth_key_id = input.auth_key_id
   AND jobs.status = 'pending'
  ORDER BY jobs.id, input.ordinal
  FOR UPDATE OF jobs
), updated AS (
  UPDATE bootstrap_update_jobs AS jobs
  SET status = 'ready',
      session_id = candidates.session_id,
      ready_at = now(),
      updated_at = now()
  FROM candidates
  WHERE jobs.id = candidates.id
  RETURNING candidates.ordinal
)
SELECT ordinal, count(*)::bigint
FROM updated
GROUP BY ordinal
ORDER BY ordinal`, userIDs, authKeyIDs, sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("mark bootstrap readiness batch: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ordinal, count int64
		if err := rows.Scan(&ordinal, &count); err != nil {
			return nil, fmt.Errorf("scan bootstrap readiness batch: %w", err)
		}
		index := int(ordinal - 1)
		if index < 0 || index >= len(results) || results[index] != 0 || count <= 0 {
			return nil, fmt.Errorf("mark bootstrap readiness batch: invalid ordinal/count %d/%d", ordinal, count)
		}
		results[index] = int(count)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mark bootstrap readiness batch rows: %w", err)
	}
	return results, nil
}

var _ store.BootstrapUpdateJobStore = (*BatchedBootstrapUpdateJobStore)(nil)
