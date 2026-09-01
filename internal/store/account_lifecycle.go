package store

import (
	"context"
	"time"

	"telesrv/internal/domain"
)

// AccountLifecycleStore owns the durable account-deletion decision and logical
// tombstone boundary.
type AccountLifecycleStore interface {
	AccountDeletionSnapshot(ctx context.Context, userID int64) (domain.AccountDeletionSnapshot, bool, error)
	ScheduleAccountDeletion(ctx context.Context, req domain.ScheduleAccountDeletion) (domain.AccountDeletionRequest, bool, error)
	PendingAccountDeletionByHash(ctx context.Context, userID int64, digest [32]byte) (domain.AccountDeletionRequest, bool, error)
	ExecuteAccountDeletion(ctx context.Context, userID int64, source domain.AccountDeletionSource, reason string, now time.Time) (domain.AccountDeletionResult, error)
	CancelAccountDeletion(ctx context.Context, userID int64, digest [32]byte, now time.Time) ([]domain.Authorization, error)
	DueAccountDeletions(ctx context.Context, now time.Time, limit int) ([]domain.AccountDeletionCandidate, error)
}
