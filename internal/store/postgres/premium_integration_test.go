package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

// TestPremiumPurchasePostgres regresses the Premium migration's atomic
// checkout semantics against a real PG: self-purchase debits Stars and
// grants/extends users.premium_expires_at, a gift purchase does the same for
// a different recipient, a replayed form_id returns the original result
// without debiting twice, and an under-funded buyer is rejected without
// touching the ledger or the catalog.
func TestPremiumPurchasePostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	stars := NewStarsStore(pool)
	premiumStore := NewPremiumStore(pool)
	users := NewUserStore(pool)
	suffix := randomSuffix(t)

	buyer, err := users.Create(ctx, domain.User{AccessHash: 1, Phone: "+1666" + suffix + "01", FirstName: "PremiumBuyer"})
	if err != nil {
		t.Fatalf("create buyer: %v", err)
	}
	friend, err := users.Create(ctx, domain.User{AccessHash: 2, Phone: "+1666" + suffix + "02", FirstName: "PremiumFriend"})
	if err != nil {
		t.Fatalf("create friend: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM premium_entitlements WHERE user_id IN ($1,$2)", buyer.ID, friend.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM premium_payment_intents WHERE buyer_user_id IN ($1,$2)", buyer.ID, friend.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM stars_transactions WHERE user_id IN ($1,$2)", buyer.ID, friend.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM stars_balances WHERE user_id IN ($1,$2)", buyer.ID, friend.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id IN ($1,$2)", buyer.ID, friend.ID)
	})

	if _, _, err := stars.EnsureGrant(ctx, buyer.ID, 5000, 1700000000); err != nil {
		t.Fatalf("grant buyer stars: %v", err)
	}

	plan, found, err := premiumStore.Plan(ctx, 3)
	if err != nil || !found {
		t.Fatalf("load seeded 3-month plan: found=%v err=%v", found, err)
	}

	newForm := func(kind domain.PremiumPurchaseKind, recipientID int64, issuedAt int) domain.PremiumPaymentForm {
		return domain.PremiumPaymentForm{
			BuyerUserID: buyer.ID, Kind: kind, RecipientUserID: recipientID,
			Months: plan.Months, DurationDays: plan.DurationDays, AmountStars: plan.AmountStars,
			PlanVersion: plan.Version, IssuedAt: issuedAt, ExpiresAt: issuedAt + domain.PremiumPaymentFormTTLSeconds,
		}
	}

	// Self-purchase.
	selfForm, err := premiumStore.IssuePremiumPaymentForm(ctx, newForm(domain.PremiumPurchaseSelf, buyer.ID, 1700000001))
	if err != nil {
		t.Fatalf("issue self form: %v", err)
	}
	result, err := premiumStore.PurchasePremium(ctx, domain.PremiumPurchaseRequest{
		BuyerUserID: buyer.ID, FormID: selfForm.ID, Kind: domain.PremiumPurchaseSelf,
		RecipientUserID: buyer.ID, Months: plan.Months, PlanVersion: plan.Version, Date: 1700000002,
	})
	if err != nil {
		t.Fatalf("purchase self: %v", err)
	}
	if result.Duplicate {
		t.Fatalf("first self purchase reported Duplicate=true")
	}
	if result.Balance.Balance != 5000-plan.AmountStars {
		t.Fatalf("balance after self purchase = %d, want %d", result.Balance.Balance, 5000-plan.AmountStars)
	}
	if !result.User.PremiumActiveAt(1700000002) {
		t.Fatalf("buyer not premium right after purchase")
	}
	wantExpiry := 1700000002 + plan.DurationDays*24*60*60
	if result.User.PremiumUntil != wantExpiry {
		t.Fatalf("buyer premium_until = %d, want %d", result.User.PremiumUntil, wantExpiry)
	}

	// Replay of the same form_id must not debit again.
	replay, err := premiumStore.PurchasePremium(ctx, domain.PremiumPurchaseRequest{
		BuyerUserID: buyer.ID, FormID: selfForm.ID, Kind: domain.PremiumPurchaseSelf,
		RecipientUserID: buyer.ID, Months: plan.Months, PlanVersion: plan.Version, Date: 1700000003,
	})
	if err != nil {
		t.Fatalf("replay self purchase: %v", err)
	}
	if !replay.Duplicate {
		t.Fatalf("replayed purchase not reported as Duplicate")
	}
	if replay.Balance.Balance != 5000-plan.AmountStars {
		t.Fatalf("balance after replay = %d, want unchanged %d", replay.Balance.Balance, 5000-plan.AmountStars)
	}

	// Gift purchase to a different recipient.
	remaining := 5000 - plan.AmountStars
	giftForm, err := premiumStore.IssuePremiumPaymentForm(ctx, newForm(domain.PremiumPurchaseGift, friend.ID, 1700000004))
	if err != nil {
		t.Fatalf("issue gift form: %v", err)
	}
	giftResult, err := premiumStore.PurchasePremium(ctx, domain.PremiumPurchaseRequest{
		BuyerUserID: buyer.ID, FormID: giftForm.ID, Kind: domain.PremiumPurchaseGift,
		RecipientUserID: friend.ID, Months: plan.Months, PlanVersion: plan.Version, Date: 1700000005,
	})
	if err != nil {
		t.Fatalf("purchase gift: %v", err)
	}
	if giftResult.User.ID != friend.ID || !giftResult.User.PremiumActiveAt(1700000005) {
		t.Fatalf("gift recipient not premium: %+v", giftResult.User)
	}
	if giftResult.Balance.Balance != remaining-plan.AmountStars {
		t.Fatalf("balance after gift = %d, want %d", giftResult.Balance.Balance, remaining-plan.AmountStars)
	}

	entitlements, err := premiumStore.PremiumEntitlements(ctx, buyer.ID, 10)
	if err != nil {
		t.Fatalf("list buyer entitlements: %v", err)
	}
	if len(entitlements) != 2 {
		t.Fatalf("buyer entitlements = %d, want 2 (own purchase + as gift source)", len(entitlements))
	}

	active, err := premiumStore.ActivePremiumEntitlements(ctx, friend.ID, 1700000005)
	if err != nil || len(active) != 1 {
		t.Fatalf("friend active entitlements = %d err=%v, want 1", len(active), err)
	}

	// Insufficient balance: buyer has some Stars left but not enough for
	// another plan purchase after two debits; drain then attempt once more.
	drainForm, err := premiumStore.IssuePremiumPaymentForm(ctx, newForm(domain.PremiumPurchaseSelf, buyer.ID, 1700000006))
	if err != nil {
		t.Fatalf("issue drain form: %v", err)
	}
	if _, err := stars.Debit(ctx, buyer.ID, remaining-plan.AmountStars, domain.StarsReasonAdjust, domain.Peer{}, 1700000006, "drain", ""); err != nil {
		t.Fatalf("drain buyer balance: %v", err)
	}
	if _, err := premiumStore.PurchasePremium(ctx, domain.PremiumPurchaseRequest{
		BuyerUserID: buyer.ID, FormID: drainForm.ID, Kind: domain.PremiumPurchaseSelf,
		RecipientUserID: buyer.ID, Months: plan.Months, PlanVersion: plan.Version, Date: 1700000007,
	}); err != domain.ErrStarsInsufficient {
		t.Fatalf("purchase with insufficient stars err = %v, want ErrStarsInsufficient", err)
	}
}
