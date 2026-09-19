package store

import (
	"context"

	"telesrv/internal/domain"
)

// PremiumStore owns the durable Premium catalog, checkout forms and the
// ledger-linked entitlement history, plus the atomic purchase transition.
// Every write that changes whether a user is currently Premium updates
// users.premium_expires_at in the same transaction as the entitlement row --
// that column stays the single field the rest of the server reads to decide
// Premium status; premium_entitlements is additive history on top of it,
// never a second source of truth.
//
// Admin-initiated grants/revokes keep using the existing
// users.Service.GrantPremium path (see internal/app/users) rather than this
// store -- months=0 already clears premium, so a dedicated revoke method
// would just duplicate it. UpsertPremiumPlan and RefundPremiumPayment are
// not idempotent on their own: callers reach them through
// admin.Service.runCommand, which already provides command_id-keyed replay
// and the audit trail at that layer.
type PremiumStore interface {
	Plans(ctx context.Context) ([]domain.PremiumPlan, error)
	Plan(ctx context.Context, months int) (domain.PremiumPlan, bool, error)
	UpsertPremiumPlan(ctx context.Context, req domain.PremiumPlanUpsertRequest) (domain.PremiumPlan, error)
	IssuePremiumPaymentForm(ctx context.Context, form domain.PremiumPaymentForm) (domain.PremiumPaymentForm, error)
	PurchasePremium(ctx context.Context, req domain.PremiumPurchaseRequest) (domain.PremiumPurchaseResult, error)
	ActivePremiumEntitlements(ctx context.Context, userID int64, now int) ([]domain.PremiumEntitlement, error)
	PremiumEntitlements(ctx context.Context, userID int64, limit int) ([]domain.PremiumEntitlement, error)
	PremiumPayment(ctx context.Context, paymentIntentID int64) (domain.PremiumPaymentDetails, bool, error)
	RefundPremiumPayment(ctx context.Context, req domain.PremiumRefundRequest) (domain.PremiumPurchaseResult, error)
}
