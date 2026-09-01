package broadcast

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"go.uber.org/zap"
)

// WorkerConfig tunes the periodic materialize+delivery cycle. Non-positive
// or out-of-range fields fall back to the defaults below (matching the
// shipped TELESRV_BROADCAST_WORKER_* defaults).
type WorkerConfig struct {
	Interval         time.Duration
	Lease            time.Duration
	MaterializeBatch int
	DeliveryBatch    int
}

// Worker drains the broadcast delivery outbox and, for "all"-mode
// campaigns, the recipient-enumeration backlog.
//
// A broadcast is created together with only its target snapshot, never with
// the enumeration or the sends themselves: an admin creating a broadcast for
// every user must not wait on however long that would take. Both
// materialization and delivery are therefore a separate, retrying cycle over
// durable rows, and this worker is only its cadence.
type Worker struct {
	service *Service
	config  WorkerConfig
	log     *zap.Logger
}

// NewWorker creates the periodic worker.
func NewWorker(service *Service, config WorkerConfig, log *zap.Logger) *Worker {
	if config.Interval <= 0 {
		config.Interval = 3 * time.Second
	}
	if config.Lease <= 0 {
		config.Lease = 30 * time.Second
	}
	if config.MaterializeBatch <= 0 || config.MaterializeBatch > 1000 {
		config.MaterializeBatch = 200
	}
	if config.DeliveryBatch <= 0 || config.DeliveryBatch > 500 {
		config.DeliveryBatch = 50
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{service: service, config: config, log: log}
}

// Run advances one cycle immediately and then on every tick until ctx is
// done. A not-ready service (missing store/sender) exits immediately with
// one explicit log line instead of ticking forever over a no-op.
func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.service == nil || !w.service.Ready() {
		if w != nil && w.log != nil {
			w.log.Info("broadcast delivery worker disabled: not configured")
		}
		return
	}
	w.runOnce(ctx)
	ticker := time.NewTicker(w.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) {
	var tokenBytes [16]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		w.log.Error("generate broadcast lease token", zap.Error(err))
		return
	}
	result, err := w.service.RunCycle(ctx, hex.EncodeToString(tokenBytes[:]), w.config.MaterializeBatch, w.config.DeliveryBatch, w.config.Lease)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Warn("broadcast delivery cycle failed", zap.Error(err))
		}
		return
	}
	if result.Materialized > 0 || result.Claimed > 0 {
		w.log.Info("broadcast delivery cycle completed",
			zap.Int("materialized", result.Materialized),
			zap.Int("claimed", result.Claimed),
			zap.Int("sent", result.Sent),
			zap.Int("failed", result.Failed))
	}
}
