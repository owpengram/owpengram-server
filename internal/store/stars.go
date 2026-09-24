package store

import (
	"context"
	"time"

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
	// ClaimMonthly atomically applies the once-per-cooldown free Stars claim
	// (e.g. @premiumbot's /claim): credits amount and records the claim only
	// when no prior claim exists or the prior one is older than cooldown,
	// all within one transaction (SELECT ... FOR UPDATE against the claim
	// row). claimed reports whether this call performed the credit; nextAt
	// is when the next claim becomes available either way.
	ClaimMonthly(ctx context.Context, userID, amount int64, date int, cooldown time.Duration) (bal domain.StarsBalance, claimed bool, nextAt time.Time, err error)
	// DeviceFingerprintGranted reports whether any account other than
	// excludeUserID has, from this exact device_model+system_version+
	// platform+ip fingerprint (an authorizations row), already received the
	// starting grant or a monthly claim. This is the live enforcement of
	// the same heuristic cmd/telesrv-admin's SharedDeviceGroup already
	// surfaces read-only: two accounts matching on every one of those four
	// attributes are treated as the same farmer. deviceModel and ip must be
	// non-empty -- callers never invoke this with an unknown fingerprint
	// (see app/stars.Service.GuardStartingGrant/GuardClaim).
	DeviceFingerprintGranted(ctx context.Context, excludeUserID int64, deviceModel, systemVersion, platform, ip string) (bool, error)
	// SkipStartingGrant idempotently marks the starting grant as already
	// handled without crediting anything (balance 0, granted=true), so
	// EnsureGrant's lazy first-read path never retries it. Used when
	// GuardStartingGrant withholds the grant for a duplicate device+IP.
	SkipStartingGrant(ctx context.Context, userID int64) error
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
