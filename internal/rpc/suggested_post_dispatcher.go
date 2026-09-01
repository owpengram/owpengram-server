package rpc

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// SuggestedPostDispatcher publishes scheduled suggestions and resolves paid
// escrow after the minimum live age (or refunds it when the post is deleted).
// Store-side per-key claims and row locks make multiple server instances safe.
type SuggestedPostDispatcher struct {
	router   *Router
	log      *zap.Logger
	interval time.Duration
	batch    int
	enqueue  func(context.Context, int64, domain.ToggleSuggestedPostApprovalResult) error
}

func NewSuggestedPostDispatcher(router *Router, log *zap.Logger) *SuggestedPostDispatcher {
	if log == nil {
		log = zap.NewNop()
	}
	return &SuggestedPostDispatcher{
		router: router, log: log, interval: time.Second, batch: 50,
		enqueue: func(ctx context.Context, originUserID int64, result domain.ToggleSuggestedPostApprovalResult) error {
			router.enqueueSuggestedPostApprovalFanout(ctx, originUserID, result)
			return nil
		},
	}
}

func (d *SuggestedPostDispatcher) Run(ctx context.Context) {
	if d == nil || d.router == nil {
		return
	}
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.DispatchOnce(ctx)
		}
	}
}

func (d *SuggestedPostDispatcher) DispatchOnce(ctx context.Context) bool {
	service, ok := d.router.deps.Channels.(suggestedPostApprovalService)
	if !ok {
		return false
	}
	results, dispatchErr := service.ProcessSuggestedPostLifecycle(ctx, domain.SuggestedPostLifecycleRequest{Now: int(d.router.clock.Now().Unix()), Limit: d.batch})
	enqueued := false
	for _, result := range results {
		if err := d.enqueue(ctx, 0, result); err != nil {
			dispatchErr = errors.Join(dispatchErr, err)
			continue
		}
		enqueued = true
	}
	if dispatchErr != nil {
		d.log.Warn("dispatch suggested post lifecycle", zap.Error(dispatchErr))
	}
	return enqueued
}
