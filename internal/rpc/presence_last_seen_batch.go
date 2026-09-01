package rpc

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/store"
)

const (
	defaultPresenceLastSeenBatchMax      = 512
	defaultPresenceLastSeenBatchWait     = time.Second
	defaultPresenceLastSeenBatchQueue    = 65_536
	defaultPresenceLastSeenBatchTimeout  = 5 * time.Second
	defaultPresenceLastSeenShutdownDrain = 10 * time.Second
	presenceLastSeenRetryInitial         = 50 * time.Millisecond
	presenceLastSeenRetryMaximum         = 2 * time.Second
)

var (
	errPresenceLastSeenBatchFull    = errors.New("presence last-seen batch queue full")
	errPresenceLastSeenBatchStopped = errors.New("presence last-seen batch dispatcher stopped")
)

type presenceLastSeenBatchUpdater interface {
	UpdateLastSeenBatch(ctx context.Context, updates []store.UserLastSeenUpdate) error
}

type presenceLastSeenBatchConfig struct {
	MaxSize      int
	MaxWait      time.Duration
	QueueSize    int
	QueryTimeout time.Duration
	DrainTimeout time.Duration
}

func normalizePresenceLastSeenBatchConfig(cfg presenceLastSeenBatchConfig) presenceLastSeenBatchConfig {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = defaultPresenceLastSeenBatchMax
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = defaultPresenceLastSeenBatchWait
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultPresenceLastSeenBatchQueue
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = defaultPresenceLastSeenBatchTimeout
	}
	if cfg.DrainTimeout <= 0 {
		cfg.DrainTimeout = defaultPresenceLastSeenShutdownDrain
	}
	return cfg
}

// presenceLastSeenBatchDispatcher is the single production writer for
// asynchronous lifecycle last-seen watermarks. Accepted work is retried with a
// bounded backoff and remains in memory until the database write plus exact
// cache invalidation succeeds, or the bounded shutdown drain expires.
type presenceLastSeenBatchDispatcher struct {
	updater presenceLastSeenBatchUpdater
	cfg     presenceLastSeenBatchConfig
	queue   chan store.UserLastSeenUpdate
	log     *zap.Logger
	metrics Metrics

	gate      sync.RWMutex
	accepting bool
}

func newPresenceLastSeenBatchDispatcher(
	updater presenceLastSeenBatchUpdater,
	cfg presenceLastSeenBatchConfig,
	log *zap.Logger,
	metrics Metrics,
) *presenceLastSeenBatchDispatcher {
	if updater == nil {
		return nil
	}
	cfg = normalizePresenceLastSeenBatchConfig(cfg)
	if log == nil {
		log = zap.NewNop()
	}
	if metrics == nil {
		metrics = NopMetrics{}
	}
	return &presenceLastSeenBatchDispatcher{
		updater:   updater,
		cfg:       cfg,
		queue:     make(chan store.UserLastSeenUpdate, cfg.QueueSize),
		log:       log,
		metrics:   metrics,
		accepting: true,
	}
}

func (d *presenceLastSeenBatchDispatcher) submit(update store.UserLastSeenUpdate) error {
	if d == nil || update.UserID == 0 || update.LastSeenAt <= 0 {
		return nil
	}
	d.gate.RLock()
	defer d.gate.RUnlock()
	if !d.accepting {
		return errPresenceLastSeenBatchStopped
	}
	d.metrics.PresenceLastSeenPending(1)
	select {
	case d.queue <- update:
		d.metrics.PresenceLastSeenSubmitted()
		return nil
	default:
		d.metrics.PresenceLastSeenPending(-1)
		d.metrics.PresenceLastSeenOverflow()
		return errPresenceLastSeenBatchFull
	}
}

func (d *presenceLastSeenBatchDispatcher) stopAccepting() {
	if d == nil {
		return
	}
	d.gate.Lock()
	d.accepting = false
	d.gate.Unlock()
}

func (d *presenceLastSeenBatchDispatcher) Run(ctx context.Context) {
	if d == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case first := <-d.queue:
			batch, canceled := d.collect(ctx, first)
			if canceled || !d.executeWithRetry(ctx, batch) {
				d.stopAccepting()
				d.drain(batch)
				return
			}
		case <-ctx.Done():
			d.stopAccepting()
			d.drain(nil)
			return
		}
	}
}

func (d *presenceLastSeenBatchDispatcher) collect(
	ctx context.Context,
	first store.UserLastSeenUpdate,
) ([]store.UserLastSeenUpdate, bool) {
	batch := make([]store.UserLastSeenUpdate, 0, d.cfg.MaxSize)
	batch = append(batch, first)
	if len(batch) >= d.cfg.MaxSize {
		return batch, false
	}
	timer := time.NewTimer(d.cfg.MaxWait)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	for len(batch) < d.cfg.MaxSize {
		select {
		case update := <-d.queue:
			batch = append(batch, update)
		case <-timer.C:
			return batch, false
		case <-ctx.Done():
			return batch, true
		}
	}
	return batch, false
}

func mergePresenceLastSeenBatch(updates []store.UserLastSeenUpdate) []store.UserLastSeenUpdate {
	latest := make(map[int64]int, len(updates))
	for _, update := range updates {
		if update.UserID == 0 || update.LastSeenAt <= 0 {
			continue
		}
		if current := latest[update.UserID]; update.LastSeenAt > current {
			latest[update.UserID] = update.LastSeenAt
		}
	}
	userIDs := make([]int64, 0, len(latest))
	for userID := range latest {
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	merged := make([]store.UserLastSeenUpdate, 0, len(userIDs))
	for _, userID := range userIDs {
		merged = append(merged, store.UserLastSeenUpdate{UserID: userID, LastSeenAt: latest[userID]})
	}
	return merged
}

func (d *presenceLastSeenBatchDispatcher) executeWithRetry(
	ctx context.Context,
	updates []store.UserLastSeenUpdate,
) bool {
	rawCount := len(updates)
	updates = mergePresenceLastSeenBatch(updates)
	if len(updates) == 0 {
		return true
	}
	backoff := presenceLastSeenRetryInitial
	for attempt := 1; ; attempt++ {
		queryCtx, cancel := context.WithTimeout(ctx, d.cfg.QueryTimeout)
		started := time.Now()
		err := d.updater.UpdateLastSeenBatch(queryCtx, updates)
		cancel()
		d.metrics.PresenceLastSeenBatch(len(updates), time.Since(started), err)
		if err == nil {
			d.metrics.PresenceLastSeenPending(-rawCount)
			return true
		}
		if attempt == 1 || attempt&(attempt-1) == 0 {
			d.log.Warn("presence last-seen batch failed; retrying",
				zap.Int("updates", len(updates)),
				zap.Int("attempt", attempt),
				zap.Duration("retry_after", backoff),
				zap.Error(err))
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return false
		}
		backoff *= 2
		if backoff > presenceLastSeenRetryMaximum {
			backoff = presenceLastSeenRetryMaximum
		}
	}
}

func (d *presenceLastSeenBatchDispatcher) drain(current []store.UserLastSeenUpdate) {
	drainCtx, cancel := context.WithTimeout(context.Background(), d.cfg.DrainTimeout)
	defer cancel()
	pending := append([]store.UserLastSeenUpdate(nil), current...)
	for {
		for len(pending) < d.cfg.MaxSize {
			select {
			case update := <-d.queue:
				pending = append(pending, update)
			default:
				break
			}
			if len(d.queue) == 0 {
				break
			}
		}
		if len(pending) > 0 {
			if !d.executeWithRetry(drainCtx, pending) {
				d.reportDrainDropped(len(pending) + len(d.queue))
				return
			}
			pending = pending[:0]
		}
		if len(d.queue) == 0 {
			return
		}
	}
}

func (d *presenceLastSeenBatchDispatcher) reportDrainDropped(count int) {
	if count <= 0 {
		return
	}
	d.metrics.PresenceLastSeenDrainDropped(count)
	d.metrics.PresenceLastSeenPending(-count)
	d.log.Error("presence last-seen shutdown drain expired", zap.Int("updates", count))
}
