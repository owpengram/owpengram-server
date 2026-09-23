package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap/zaptest"

	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

// stubPremiumPlans is the minimum PremiumService the gift-code storefront
// needs: just a plan catalog.
type stubPremiumPlans struct{ plans []domain.PremiumPlan }

func (s *stubPremiumPlans) BotUserID() int64 { return domain.PremiumBotUser().ID }
func (s *stubPremiumPlans) Plans(context.Context) ([]domain.PremiumPlan, error) {
	return s.plans, nil
}
func (s *stubPremiumPlans) Plan(_ context.Context, months int) (domain.PremiumPlan, error) {
	for _, p := range s.plans {
		if p.Months == months {
			return p, nil
		}
	}
	return domain.PremiumPlan{}, domain.ErrPremiumPlanUnavailable
}
func (s *stubPremiumPlans) IssuePaymentForm(_ context.Context, form domain.PremiumPaymentForm) (domain.PremiumPaymentForm, error) {
	return form, nil
}
func (s *stubPremiumPlans) Purchase(context.Context, domain.PremiumPurchaseRequest) (domain.PremiumPurchaseResult, error) {
	return domain.PremiumPurchaseResult{}, nil
}

// TestPremiumGiftCodeOptionsAreHiddenFromAndroid pins the crash fix: DrKLO's
// GiftSheet builds premium tiers only from fiat options and skips every
// "XTR" one, then reloads the (already cached) options whenever its tier
// list came out empty -- a Stars-only catalog therefore recurses
// updatePremiumTiers -> loadGiftOptions -> updatePremiumTiers until the app
// dies with StackOverflowError as soon as "send a gift" is tapped. Android
// gets an empty catalog; every other client keeps the Stars options.
func TestPremiumGiftCodeOptionsAreHiddenFromAndroid(t *testing.T) {
	ctx := context.Background()
	userStore := memory.NewUserStore()
	user, err := userStore.Create(ctx, domain.User{AccessHash: 31, Phone: "15550007731", FirstName: "Buyer"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	premium := &stubPremiumPlans{plans: []domain.PremiumPlan{
		{Months: 3, DurationDays: 90, AmountStars: 300, Version: 1, Label: "3 months", Enabled: true},
		{Months: 12, DurationDays: 365, AmountStars: 900, Version: 1, Label: "12 months", Enabled: true},
	}}
	router := New(Config{}, Deps{
		Users:   appusers.NewService(userStore),
		Premium: premium,
	}, zaptest.NewLogger(t), clock.System)

	desktopCtx := WithClientInfo(WithUserID(ctx, user.ID), ClientInfo{Type: ClientTypeTDesktop})
	options, err := router.onPaymentsGetPremiumGiftCodeOptions(desktopCtx, &tg.PaymentsGetPremiumGiftCodeOptionsRequest{})
	if err != nil {
		t.Fatalf("desktop options: %v", err)
	}
	if len(options) != 2 {
		t.Fatalf("desktop options = %d, want one per enabled plan", len(options))
	}
	for _, option := range options {
		if option.Currency != domain.PremiumCurrencyStars || option.Users != 1 {
			t.Fatalf("desktop option = %+v, want a Stars option for one user", option)
		}
	}

	androidCtx := WithClientInfo(WithUserID(ctx, user.ID), ClientInfo{Type: ClientTypeAndroid})
	androidOptions, err := router.onPaymentsGetPremiumGiftCodeOptions(androidCtx, &tg.PaymentsGetPremiumGiftCodeOptionsRequest{})
	if err != nil {
		t.Fatalf("android options: %v", err)
	}
	if len(androidOptions) != 0 {
		t.Fatalf("android options = %+v, want none (DrKLO recurses on Stars-only tiers)", androidOptions)
	}
}
