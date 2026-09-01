package store

import (
	"context"
	"time"

	"telesrv/internal/domain"
)

// BroadcastStore persists system broadcast campaigns and their durable
// per-recipient delivery outbox.
//
// A BroadcastTargetAll campaign is not fully enumerated at creation:
// CreateBroadcast only snapshots the target user-id range (the current max
// user id) and returns. MaterializeBroadcastRecipients then advances that
// campaign's enumeration a bounded batch at a time, so a huge user base
// never blocks the admin's create call, or any one worker cycle, on a
// single giant INSERT. ClaimBroadcastRecipients/ReleaseBroadcastRecipient/
// CompleteBroadcastRecipient implement a lease-based handoff for the
// delivery half of the cycle: a worker claims a bounded batch of eligible
// rows under a time-limited lease, and either completes or releases each
// one it processes. A lease that is never renewed simply expires, so a
// worker crash mid-cycle cannot strand a row in 'processing' forever, and
// two workers can never believe they both hold the same row's lease at once.
type BroadcastStore interface {
	// PreviewBroadcastRecipients validates and counts the intended recipient
	// set without creating anything -- for "selected" mode this also
	// validates every id names a real, non-bot, non-system account.
	PreviewBroadcastRecipients(ctx context.Context, mode domain.BroadcastTargetMode, selectedUserIDs []int64) (int64, error)
	// CreateBroadcast inserts the broadcast row. For BroadcastTargetAll this
	// only snapshots the current max user id and target count; no recipient
	// rows are inserted here (see MaterializeBroadcastRecipients). For
	// BroadcastTargetSelected, the given ids are validated and their
	// recipient rows are inserted immediately, since that list is already
	// bounded by domain.MaxBroadcastSelectedRecipients.
	CreateBroadcast(ctx context.Context, message string, entities []domain.MessageEntity, mode domain.BroadcastTargetMode, selectedUserIDs []int64, createdBy string) (domain.Broadcast, error)
	// MaterializeBroadcastRecipients advances one "all"-mode campaign's
	// enumeration by up to limit newly-inserted recipient rows, and reports
	// how many were inserted. A campaign with nothing left to enumerate (or
	// no "all"-mode campaign still enumerating at all) returns 0, nil.
	MaterializeBroadcastRecipients(ctx context.Context, limit int) (int, error)
	// ClaimBroadcastRecipients atomically leases up to limit eligible rows
	// (pending, or processing under an expired lease) to leaseToken for
	// lease, returning each claim together with its broadcast's message and
	// entities so the caller can deliver without a second round trip.
	ClaimBroadcastRecipients(ctx context.Context, leaseToken string, limit int, lease time.Duration) ([]BroadcastRecipientClaim, error)
	// CompleteBroadcastRecipient closes a claimed row as delivered, recording
	// the message identifiers the send produced, and advances its
	// broadcast's sent_count. It is a no-op returning
	// domain.ErrBroadcastLeaseLost if the claim's lease was lost (expired
	// and reclaimed, or otherwise no longer matches) in the meantime --
	// safe to call even after a duplicate/idempotent resend, since the
	// caller is expected to tolerate that error.
	CompleteBroadcastRecipient(ctx context.Context, claim BroadcastRecipientClaim, privateMessageID int64, messageBoxID int, pts int) error
	// ReleaseBroadcastRecipient returns a claimed row to 'pending' (with
	// backoff) after a failed delivery attempt, or to the terminal 'failed'
	// once domain.MaxBroadcastRecipientAttempts is reached, and advances its
	// broadcast's failed_count in that terminal case.
	ReleaseBroadcastRecipient(ctx context.Context, claim BroadcastRecipientClaim, cause string) error
	// ListBroadcasts pages broadcasts newest-first.
	ListBroadcasts(ctx context.Context, beforeID int64, limit int) ([]domain.Broadcast, bool, error)
	// BroadcastByID returns one broadcast.
	BroadcastByID(ctx context.Context, id int64) (domain.Broadcast, bool, error)
}

// BroadcastRecipientClaim is one recipient row leased for delivery, carrying
// its broadcast's message text and entities so the worker doesn't need a
// second lookup before sending.
type BroadcastRecipientClaim struct {
	RecipientID int64
	BroadcastID int64
	UserID      int64
	Attempts    int
	LeaseToken  string
	Message     string
	Entities    []domain.MessageEntity
}
