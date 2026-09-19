// Package premium implements the application boundary for Telegram Premium
// purchases and gifts settled against the local Stars ledger.
package premium

import (
	"context"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// StarsBalanceReader keeps Premium on the existing Stars account lifecycle:
// GetBalance applies the configured one-time starting grant before a form
// can be paid, while settlement itself locks and debits the same rows
// atomically inside PremiumStore.
type StarsBalanceReader interface {
	GetBalance(ctx context.Context, userID int64) (domain.StarsBalance, error)
}

type Service struct {
	store store.PremiumStore
	stars StarsBalanceReader
}

func NewService(st store.PremiumStore, stars StarsBalanceReader) *Service {
	return &Service{store: st, stars: stars}
}

func (s *Service) BotUserID() int64 {
	return domain.PremiumBotUserID
}

func (s *Service) Plans(ctx context.Context) ([]domain.PremiumPlan, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	plans, err := s.store.Plans(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.PremiumPlan, 0, len(plans))
	for _, plan := range plans {
		if plan.Enabled && plan.Valid() {
			out = append(out, plan)
		}
	}
	return out, nil
}

func (s *Service) Plan(ctx context.Context, months int) (domain.PremiumPlan, error) {
	if s == nil || s.store == nil || months <= 0 {
		return domain.PremiumPlan{}, domain.ErrPremiumPlanUnavailable
	}
	plan, found, err := s.store.Plan(ctx, months)
	if err != nil {
		return domain.PremiumPlan{}, err
	}
	if !found || !plan.Enabled || !plan.Valid() {
		return domain.PremiumPlan{}, domain.ErrPremiumPlanUnavailable
	}
	return plan, nil
}

func (s *Service) IssuePaymentForm(ctx context.Context, form domain.PremiumPaymentForm) (domain.PremiumPaymentForm, error) {
	if s == nil || s.store == nil || !form.Valid() {
		return domain.PremiumPaymentForm{}, domain.ErrPremiumFormInvalid
	}
	if s.stars != nil {
		if _, err := s.stars.GetBalance(ctx, form.BuyerUserID); err != nil {
			return domain.PremiumPaymentForm{}, err
		}
	}
	return s.store.IssuePremiumPaymentForm(ctx, form)
}

func (s *Service) Purchase(ctx context.Context, req domain.PremiumPurchaseRequest) (domain.PremiumPurchaseResult, error) {
	if s == nil || s.store == nil {
		return domain.PremiumPurchaseResult{}, domain.ErrPremiumPlanUnavailable
	}
	return s.store.PurchasePremium(ctx, req)
}

func (s *Service) ActiveEntitlements(ctx context.Context, userID int64, now int) ([]domain.PremiumEntitlement, error) {
	if s == nil || s.store == nil || userID <= 0 {
		return nil, nil
	}
	return s.store.ActivePremiumEntitlements(ctx, userID, now)
}

func (s *Service) Entitlements(ctx context.Context, userID int64, limit int) ([]domain.PremiumEntitlement, error) {
	if s == nil || s.store == nil || userID <= 0 {
		return nil, nil
	}
	return s.store.PremiumEntitlements(ctx, userID, limit)
}

func (s *Service) Payment(ctx context.Context, paymentIntentID int64) (domain.PremiumPaymentDetails, bool, error) {
	if s == nil || s.store == nil || paymentIntentID <= 0 {
		return domain.PremiumPaymentDetails{}, false, nil
	}
	return s.store.PremiumPayment(ctx, paymentIntentID)
}

// Catalog returns every plan, enabled and disabled, for the operator's plan
// management page. Plans (above) filters to what the storefront may sell.
func (s *Service) Catalog(ctx context.Context) ([]domain.PremiumPlan, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	return s.store.Plans(ctx)
}

func (s *Service) UpsertPlan(ctx context.Context, req domain.PremiumPlanUpsertRequest) (domain.PremiumPlan, error) {
	if s == nil || s.store == nil {
		return domain.PremiumPlan{}, domain.ErrPremiumPlanUnavailable
	}
	return s.store.UpsertPremiumPlan(ctx, req)
}

func (s *Service) Refund(ctx context.Context, req domain.PremiumRefundRequest) (domain.PremiumPurchaseResult, error) {
	if s == nil || s.store == nil {
		return domain.PremiumPurchaseResult{}, domain.ErrPremiumPaymentNotFound
	}
	return s.store.RefundPremiumPayment(ctx, req)
}
