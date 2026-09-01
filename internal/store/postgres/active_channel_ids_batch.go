package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"telesrv/internal/domain"
)

// ActiveChannelIDsBatchMetrics exposes bounded aggregate cold-loader signals;
// owner identities never become metric labels.
type ActiveChannelIDsBatchMetrics interface {
	ActiveChannelIDsBatch(selectors int, rows int, d time.Duration, err error)
	ActiveChannelIDsPending(delta int)
}

type ActiveChannelIDsBatchConfig struct {
	MaxSize      int
	MaxWait      time.Duration
	QueueSize    int
	QueryTimeout time.Duration
	Metrics      ActiveChannelIDsBatchMetrics
}

type activeChannelIDsSelector struct {
	userID         int64
	afterChannelID int64
	limit          int
}

type activeChannelIDsBatchRequest struct {
	selector activeChannelIDsSelector
	result   chan activeChannelIDsBatchResult
}

type activeChannelIDsBatchResult struct {
	channelIDs []int64
	err        error
}

type activeChannelIDsBatchBackend interface {
	listActiveChannelIDPages(context.Context, []activeChannelIDsSelector) ([][]int64, error)
}

// ActiveChannelIDsPageBatcher combines independent readiness cache misses into
// one PostgreSQL call. It is a synchronous bounded read source: failures are
// returned to every selector and never fall back to one query per account.
type ActiveChannelIDsPageBatcher struct {
	base   activeChannelIDsBatchBackend
	cfg    ActiveChannelIDsBatchConfig
	queue  chan activeChannelIDsBatchRequest
	stop   chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
	gate   sync.RWMutex
	closed bool
}

func NewActiveChannelIDsPageBatcher(
	base *ChannelStore,
	cfg ActiveChannelIDsBatchConfig,
) (*ActiveChannelIDsPageBatcher, error) {
	if base == nil || base.db == nil {
		return nil, errors.New("initialize active channel IDs batcher: nil store")
	}
	return newActiveChannelIDsPageBatcher(base, cfg)
}

func newActiveChannelIDsPageBatcher(
	base activeChannelIDsBatchBackend,
	cfg ActiveChannelIDsBatchConfig,
) (*ActiveChannelIDsPageBatcher, error) {
	if base == nil {
		return nil, errors.New("initialize active channel IDs batcher: nil backend")
	}
	if cfg.MaxSize <= 0 || cfg.MaxSize > 4096 {
		return nil, fmt.Errorf("initialize active channel IDs batcher: max size %d outside [1,4096]", cfg.MaxSize)
	}
	if cfg.MaxWait <= 0 || cfg.MaxWait > time.Second {
		return nil, fmt.Errorf("initialize active channel IDs batcher: max wait %v outside (0,1s]", cfg.MaxWait)
	}
	if cfg.QueueSize < cfg.MaxSize || cfg.QueueSize > 1<<20 {
		return nil, fmt.Errorf("initialize active channel IDs batcher: queue size %d outside [%d,%d]", cfg.QueueSize, cfg.MaxSize, 1<<20)
	}
	if cfg.QueryTimeout <= 0 || cfg.QueryTimeout > 30*time.Second {
		return nil, fmt.Errorf("initialize active channel IDs batcher: query timeout %v outside (0,30s]", cfg.QueryTimeout)
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	b := &ActiveChannelIDsPageBatcher{
		base: base, cfg: cfg,
		queue: make(chan activeChannelIDsBatchRequest, cfg.QueueSize),
		stop:  make(chan struct{}), done: make(chan struct{}), cancel: cancel,
	}
	go b.run(workerCtx)
	return b, nil
}

func (b *ActiveChannelIDsPageBatcher) ListActiveChannelIDsForUser(
	ctx context.Context,
	userID, afterChannelID int64,
	limit int,
) ([]int64, error) {
	if userID == 0 || afterChannelID < 0 {
		return nil, domain.ErrChannelInvalid
	}
	if limit <= 0 || limit > domain.MaxSynchronousChannelDialogFanout {
		limit = domain.MaxSynchronousChannelDialogFanout
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request := activeChannelIDsBatchRequest{
		selector: activeChannelIDsSelector{userID: userID, afterChannelID: afterChannelID, limit: limit},
		result:   make(chan activeChannelIDsBatchResult, 1),
	}
	b.gate.RLock()
	if b.closed {
		b.gate.RUnlock()
		return nil, context.Canceled
	}
	select {
	case b.queue <- request:
		if b.cfg.Metrics != nil {
			b.cfg.Metrics.ActiveChannelIDsPending(1)
		}
	case <-ctx.Done():
		b.gate.RUnlock()
		return nil, ctx.Err()
	}
	b.gate.RUnlock()

	select {
	case result := <-request.result:
		return result.channelIDs, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *ActiveChannelIDsPageBatcher) Close() {
	if b == nil {
		return
	}
	b.once.Do(func() {
		b.gate.Lock()
		b.closed = true
		close(b.stop)
		b.cancel()
		b.gate.Unlock()
		<-b.done
	})
}

func (b *ActiveChannelIDsPageBatcher) run(ctx context.Context) {
	defer close(b.done)
	pending := make([]activeChannelIDsBatchRequest, 0, b.cfg.MaxSize)
	for {
		if len(pending) == 0 {
			select {
			case request := <-b.queue:
				pending = append(pending, request)
			case <-b.stop:
				b.failQueued(context.Canceled, pending)
				return
			}
		}
		if len(pending) < b.cfg.MaxSize {
			timer := time.NewTimer(b.cfg.MaxWait)
		collect:
			for len(pending) < b.cfg.MaxSize {
				select {
				case request := <-b.queue:
					pending = append(pending, request)
				case <-timer.C:
					break collect
				case <-b.stop:
					stopAndDrainTimer(timer)
					b.failQueued(context.Canceled, pending)
					return
				}
			}
			stopAndDrainTimer(timer)
		}
		batch, remaining := selectDistinctActiveChannelIDsBatch(pending, b.cfg.MaxSize)
		pending = remaining
		b.execute(ctx, batch)
	}
}

func selectDistinctActiveChannelIDsBatch(
	pending []activeChannelIDsBatchRequest,
	maxSize int,
) ([]activeChannelIDsBatchRequest, []activeChannelIDsBatchRequest) {
	batch := make([]activeChannelIDsBatchRequest, 0, min(maxSize, len(pending)))
	remaining := make([]activeChannelIDsBatchRequest, 0, len(pending))
	seen := make(map[activeChannelIDsSelector]struct{}, min(maxSize, len(pending)))
	for _, request := range pending {
		if len(batch) >= maxSize {
			remaining = append(remaining, request)
			continue
		}
		if _, duplicate := seen[request.selector]; duplicate {
			remaining = append(remaining, request)
			continue
		}
		seen[request.selector] = struct{}{}
		batch = append(batch, request)
	}
	return batch, remaining
}

func (b *ActiveChannelIDsPageBatcher) execute(ctx context.Context, batch []activeChannelIDsBatchRequest) {
	if len(batch) == 0 {
		return
	}
	selectors := make([]activeChannelIDsSelector, len(batch))
	for index, request := range batch {
		selectors[index] = request.selector
	}
	started := time.Now()
	queryCtx, cancel := context.WithTimeout(ctx, b.cfg.QueryTimeout)
	pages, err := b.base.listActiveChannelIDPages(queryCtx, selectors)
	cancel()
	rows := 0
	if err == nil {
		if len(pages) != len(batch) {
			err = fmt.Errorf("list active channel IDs batch: result count %d, want %d", len(pages), len(batch))
		} else {
			for _, page := range pages {
				rows += len(page)
			}
		}
	}
	if b.cfg.Metrics != nil {
		b.cfg.Metrics.ActiveChannelIDsBatch(len(batch), rows, time.Since(started), err)
	}
	for index, request := range batch {
		result := activeChannelIDsBatchResult{err: err}
		if err == nil {
			result.channelIDs = pages[index]
		}
		request.result <- result
		if b.cfg.Metrics != nil {
			b.cfg.Metrics.ActiveChannelIDsPending(-1)
		}
	}
}

func (b *ActiveChannelIDsPageBatcher) failQueued(err error, pending []activeChannelIDsBatchRequest) {
	for _, request := range pending {
		b.failRequest(request, err)
	}
	for {
		select {
		case request := <-b.queue:
			b.failRequest(request, err)
		default:
			return
		}
	}
}

func (b *ActiveChannelIDsPageBatcher) failRequest(request activeChannelIDsBatchRequest, err error) {
	request.result <- activeChannelIDsBatchResult{err: err}
	if b.cfg.Metrics != nil {
		b.cfg.Metrics.ActiveChannelIDsPending(-1)
	}
}

func (s *ChannelStore) listActiveChannelIDPages(
	ctx context.Context,
	selectors []activeChannelIDsSelector,
) ([][]int64, error) {
	pages := make([][]int64, len(selectors))
	if len(selectors) == 0 {
		return pages, nil
	}
	userIDs := make([]int64, len(selectors))
	afterChannelIDs := make([]int64, len(selectors))
	limits := make([]int32, len(selectors))
	seen := make(map[activeChannelIDsSelector]struct{}, len(selectors))
	for index, selector := range selectors {
		if selector.userID == 0 || selector.afterChannelID < 0 || selector.limit <= 0 ||
			selector.limit > domain.MaxSynchronousChannelDialogFanout {
			return nil, fmt.Errorf("list active channel IDs batch: invalid selector at index %d", index)
		}
		if _, duplicate := seen[selector]; duplicate {
			return nil, fmt.Errorf("list active channel IDs batch: duplicate selector at index %d", index)
		}
		seen[selector] = struct{}{}
		userIDs[index] = selector.userID
		afterChannelIDs[index] = selector.afterChannelID
		limits[index] = int32(selector.limit)
	}
	rows, err := s.db.Query(ctx, `
WITH input AS (
    SELECT *
    FROM unnest($1::bigint[], $2::bigint[], $3::integer[])
         WITH ORDINALITY AS value(user_id, after_channel_id, page_limit, ordinal)
)
SELECT input.ordinal, visible.channel_id
FROM input
JOIN LATERAL (
    SELECT channel_id
    FROM (
        SELECT membership.channel_id
        FROM user_channel_member_index AS membership
        WHERE membership.user_id = input.user_id
          AND membership.status = 'active'
          AND NOT membership.deleted
        UNION
        SELECT mono.id
        FROM channels AS mono
        JOIN channels AS parent
          ON parent.id = mono.linked_monoforum_id
         AND NOT parent.deleted
         AND parent.broadcast_messages_allowed
         AND parent.linked_monoforum_id = mono.id
        WHERE mono.monoforum
          AND NOT mono.deleted
          AND (
              EXISTS (
                  SELECT 1
                  FROM channel_members AS admin
                  WHERE admin.channel_id = parent.id
                    AND admin.user_id = input.user_id
                    AND admin.status = 'active'
                    AND (
                        admin.role = 'creator'
                        OR (
                            admin.role = 'admin'
                            AND COALESCE((admin.admin_rights->>'ManageDirectMessages')::boolean, false)
                        )
                    )
              )
              OR EXISTS (
                  SELECT 1
                  FROM channel_messages AS message
                  WHERE message.channel_id = mono.id
                    AND message.saved_peer_type = 'user'
                    AND message.saved_peer_id = input.user_id
                    AND NOT message.deleted
              )
          )
    ) AS visible_channels
    WHERE channel_id > input.after_channel_id
    ORDER BY channel_id
    LIMIT input.page_limit
) AS visible ON true
ORDER BY input.ordinal, visible.channel_id`, userIDs, afterChannelIDs, limits)
	if err != nil {
		return nil, fmt.Errorf("list active channel IDs batch: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ordinal int64
		var channelID int64
		if err := rows.Scan(&ordinal, &channelID); err != nil {
			return nil, err
		}
		if ordinal <= 0 || ordinal > int64(len(pages)) {
			return nil, fmt.Errorf("list active channel IDs batch: invalid ordinal %d", ordinal)
		}
		page := pages[ordinal-1]
		selector := selectors[ordinal-1]
		if channelID <= selector.afterChannelID || (len(page) > 0 && channelID <= page[len(page)-1]) || len(page) >= selector.limit {
			return nil, fmt.Errorf("list active channel IDs batch: invalid page row for ordinal %d", ordinal)
		}
		pages[ordinal-1] = append(page, channelID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pages, nil
}
