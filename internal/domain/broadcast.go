package domain

import (
	"errors"
	"time"
)

// BroadcastTargetMode selects who a broadcast's recipients are.
type BroadcastTargetMode string

const (
	// BroadcastTargetAll snapshots every non-bot, non-system account as of
	// creation time (mirrors the exclusion cmd/telesrv-admin's CountAccounts
	// already applies: real users only, not @BotFather/@Stickers/@ChatBot/777000
	// itself) by recording the current max user id and enumerating up to it
	// incrementally, rather than resolving the whole list inline.
	BroadcastTargetAll BroadcastTargetMode = "all"
	// BroadcastTargetSelected sends only to the operator-picked user list
	// carried on the create request.
	BroadcastTargetSelected BroadcastTargetMode = "selected"
)

// BroadcastRecipientStatus is one recipient row's delivery state.
type BroadcastRecipientStatus string

const (
	BroadcastRecipientPending BroadcastRecipientStatus = "pending"
	// BroadcastRecipientProcessing means a delivery worker currently holds a
	// time-bounded lease on this row (see LeaseToken/LeaseUntil). If the
	// worker dies before finishing, the lease simply expires and another
	// worker cycle reclaims the row -- no separate crash-recovery pass needed.
	BroadcastRecipientProcessing BroadcastRecipientStatus = "processing"
	BroadcastRecipientSent       BroadcastRecipientStatus = "sent"
	// BroadcastRecipientFailed is terminal: MaxBroadcastRecipientAttempts was
	// reached, so the worker stops retrying this row. A blocked or deleted
	// recipient must not spin forever alongside everyone else's real deliveries.
	BroadcastRecipientFailed BroadcastRecipientStatus = "failed"
)

// MaxBroadcastRecipientAttempts bounds retries per recipient before the
// worker gives up and marks the row permanently failed.
const MaxBroadcastRecipientAttempts = 5

// MaxBroadcastMessageBytes caps a broadcast's message body, matching the
// broadcasts.message CHECK added in
// deploy/migrations/20260901000024_broadcast_lease_delivery_and_entities.up.sql.
const MaxBroadcastMessageBytes = 4096

// MaxBroadcastSelectedRecipients caps how many user ids one "selected"-mode
// broadcast may carry in its create request, so a hand-built recipient list
// can't smuggle in an "all users" sized payload through the wrong target mode.
const MaxBroadcastSelectedRecipients = 200

// Broadcast is one admin-triggered system message campaign, sent from
// OfficialSystemUserID (777000) to every recipient targeted by TargetMode.
//
// For BroadcastTargetAll, recipient rows are not all inserted at creation:
// SnapshotMaxUserID/EnumerationCursorUserID/EnumerationDone track an
// incremental keyset walk over the users table (see
// store.BroadcastStore.MaterializeBroadcastRecipients), so creating a
// campaign for a large user base is a single cheap insert, not one giant
// blocking transaction. MaterializedCount is how many recipient rows exist
// so far; TargetCount is the (possibly still-growing, for "all") total this
// campaign is aimed at. SentCount/FailedCount are maintained incrementally
// by the delivery worker as it closes out each recipient row.
type Broadcast struct {
	ID                int64
	Message           string
	Entities          []MessageEntity
	TargetMode        BroadcastTargetMode
	TargetCount       int64
	MaterializedCount int64
	SentCount         int64
	FailedCount       int64
	EnumerationDone   bool
	CreatedBy         string
	CreatedAt         time.Time
}

// BroadcastRecipient is one durable outbox row: one user's delivery state
// for one broadcast.
//
// A worker claims a batch of eligible rows by writing LeaseToken/LeaseUntil
// (see store.BroadcastStore.ClaimBroadcastRecipients), delivers the message,
// then either closes the row as 'sent' (recording PrivateMessageID/
// MessageBoxID/Pts, the same identifiers domain.Message carries, so a
// campaign's delivery history is independently auditable without joining
// back through the shared message store) or releases it back to 'pending'
// (or terminally 'failed', once MaxBroadcastRecipientAttempts is reached) on
// error. A lease that is never renewed simply expires, so a worker that
// crashes mid-delivery cannot leave a row stuck in 'processing' forever.
type BroadcastRecipient struct {
	ID          int64
	BroadcastID int64
	UserID      int64
	Status      BroadcastRecipientStatus
	Attempts    int
	// NextAttemptAt gates retries with exponential backoff after a failed
	// delivery; a 'pending' row isn't eligible for claiming again until then.
	NextAttemptAt time.Time
	LeaseToken    string
	LeaseUntil    *time.Time
	LastError     string
	// PrivateMessageID/MessageBoxID/Pts identify the delivered message once
	// Status is 'sent'. A pre-migration row that was marked 'sent' before
	// this tracking existed carries all three as zero -- see the CHECK
	// constraint added in
	// deploy/migrations/20260901000024_broadcast_lease_delivery_and_entities.up.sql,
	// which treats that as a legitimate legacy/untracked case.
	PrivateMessageID int64
	MessageBoxID     int
	Pts              int
	SentAt           *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

var (
	ErrBroadcastInvalid          = errors.New("broadcast invalid")
	ErrBroadcastMessageEmpty     = errors.New("broadcast message is empty")
	ErrBroadcastMessageTooLong   = errors.New("broadcast message exceeds the maximum length")
	ErrBroadcastNoRecipients     = errors.New("broadcast has no recipients")
	ErrBroadcastRecipientInvalid = errors.New("broadcast recipient invalid")
	ErrBroadcastNotFound         = errors.New("broadcast not found")
	// ErrBroadcastLeaseLost means the delivery worker's lease on a recipient
	// row was reclaimed (expired and re-claimed by another cycle, or the row
	// otherwise changed underneath it) before delivery finished. The caller
	// should simply drop the result: the row is someone else's to finish now.
	ErrBroadcastLeaseLost = errors.New("broadcast recipient lease lost")
)
