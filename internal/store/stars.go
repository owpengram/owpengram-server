package store

import (
	"context"

	"telesrv/internal/domain"
)

// StarsStore persists the Stars local ledger: per-user balance + transaction
// history. Debit/credit/grant must each complete atomically within a single
// transaction (balance and transactions must never drift apart).
type StarsStore interface {
	// GetBalance returns the account's current balance; a missing row returns
	// the zero value (Balance 0, Granted false).
	GetBalance(ctx context.Context, userID int64) (domain.StarsBalance, error)
	// EnsureGrant idempotently applies the one-time starting grant: only when
	// never granted before does it credit amount, set granted=true and write
	// a grant transaction, all within one transaction. Returns the latest
	// balance plus whether this call actually performed the grant.
	EnsureGrant(ctx context.Context, userID, amount int64, date int) (domain.StarsBalance, bool, error)
	// Credit credits the account (amount>0) and writes a transaction
	// (amount=+x) within one transaction; a missing balance row is created.
	Credit(ctx context.Context, userID, amount int64, reason domain.StarsTransactionReason, peer domain.Peer, date int, title, desc string) (domain.StarsBalance, error)
	// Debit checks sufficiency with SELECT ... FOR UPDATE within one
	// transaction before deducting (amount>0) and writing a transaction
	// (amount=-x). Insufficient balance returns domain.ErrStarsInsufficient.
	Debit(ctx context.Context, userID, amount int64, reason domain.StarsTransactionReason, peer domain.Peer, date int, title, desc string) (domain.StarsBalance, error)
	// ListTransactions keyset-paginates by direction and order, returning one
	// page of transactions plus the current balance.
	ListTransactions(ctx context.Context, userID int64, query domain.StarsTransactionQuery) (domain.StarsTransactionPage, error)
}

// StarsPurchaseStore owns fiat self-topup, friend-gift and giveaway-launch
// aggregates. Successful settlement commits the affected ledger/message box
// atomically; exact form retries return the original receipt.
//
// No implementation exists yet -- the fiat Stars checkout flow (invoice
// forms, provider payment, the bilateral gift receipt) is a separate,
// not-yet-ported feature. app/stars.Service works fine without one: every
// method that needs it checks for nil and returns
// domain.ErrStarsPurchaseFormInvalid, so the base ledger (grant/credit/debit/
// history) is fully usable on its own in the meantime.
type StarsPurchaseStore interface {
	IssueStarsPurchaseForm(context.Context, domain.StarsPurchaseForm) (domain.StarsPurchaseForm, error)
	PurchaseStars(context.Context, domain.StarsPurchaseRequest) (domain.StarsPurchaseResult, error)
}

// StarsGiveawayStore exposes the viewer-specific state of launch cards
// without forcing lightweight purchase-store fakes to implement the read
// model. Also unimplemented for now -- see StarsPurchaseStore.
type StarsGiveawayStore interface {
	GetStarsGiveawayInfo(context.Context, int64, int64, int, int) (domain.StarsGiveawayInfo, error)
}
