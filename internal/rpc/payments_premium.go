package rpc

import (
	"context"
	"fmt"

	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tgerr"

	"telesrv/internal/domain"
)

// onPaymentsGetPremiumGiftCodeOptions answers the Settings -> Premium ->
// Gift storefront: one Stars-denominated option per enabled plan. Fiat/app
// store checkout is not implemented, so every option is Stars-only.
func (r *Router) onPaymentsGetPremiumGiftCodeOptions(
	ctx context.Context,
	req *tg.PaymentsGetPremiumGiftCodeOptionsRequest,
) ([]tg.PremiumGiftCodeOption, error) {
	userID, authorized, err := r.currentUserID(ctx)
	if err != nil {
		return nil, internalErr()
	}
	if !authorized || userID == 0 {
		return nil, authKeyUnregisteredErr()
	}
	if r.userIsBot(ctx, userID) {
		return nil, botMethodInvalidErr()
	}
	if r.deps.Premium == nil {
		return []tg.PremiumGiftCodeOption{}, nil
	}
	if req != nil {
		if peer, ok := req.GetBoostPeer(); ok {
			if _, err := r.checkedDomainPeerFromInputPeer(ctx, userID, peer); err != nil {
				return nil, err
			}
			return nil, tgerr.New(400, "BOOST_PEER_INVALID")
		}
	}
	plans, err := r.deps.Premium.Plans(ctx)
	if err != nil {
		return nil, internalErr()
	}
	out := make([]tg.PremiumGiftCodeOption, 0, len(plans))
	for _, plan := range plans {
		if !plan.Valid() || !plan.Enabled {
			continue
		}
		out = append(out, tg.PremiumGiftCodeOption{
			Users: 1, Months: plan.Months,
			Currency: domain.PremiumCurrencyStars, Amount: plan.AmountStars,
		})
	}
	return out, nil
}

// premiumPaymentForm serves the Stars checkout form for both self-purchase
// (InputInvoiceStars/InputStorePaymentPremiumSubscription) and gifting
// Premium to a contact (InputInvoicePremiumGiftStars). Called by
// onPaymentsGetPaymentForm's dispatcher in payments_star_gifts.go, which
// owns that TL method since Premium and Star Gift checkout share it.
func (r *Router) premiumPaymentForm(ctx context.Context, userID int64, input tg.InputInvoiceClass) (tg.PaymentsPaymentFormClass, error) {
	if r.userIsBot(ctx, userID) {
		return nil, botMethodInvalidErr()
	}
	if r.deps.Premium == nil {
		return nil, notImplementedErr()
	}
	invoice, err := r.resolvePremiumInvoice(ctx, userID, input)
	if err != nil {
		return nil, err
	}
	recipientID := invoice.RecipientUserID
	if invoice.Kind == domain.PremiumPurchaseSelf {
		recipientID = userID
	}
	now := int(r.clock.Now().Unix())
	form, err := r.deps.Premium.IssuePaymentForm(ctx, domain.PremiumPaymentForm{
		BuyerUserID: userID, Kind: invoice.Kind, RecipientUserID: recipientID,
		Months: invoice.Months, DurationDays: invoice.DurationDays,
		AmountStars: invoice.AmountStars, PlanVersion: invoice.PlanVersion,
		Message: invoice.Message, IssuedAt: now,
		ExpiresAt: now + domain.PremiumPaymentFormTTLSeconds,
	})
	if err != nil {
		return nil, premiumPaymentErr(err)
	}
	botID := r.deps.Premium.BotUserID()
	userIDs := []int64{botID}
	if invoice.Kind == domain.PremiumPurchaseGift {
		userIDs = append(userIDs, recipientID)
	}
	users := r.domainUsersForIDs(ctx, userID, userIDs)
	hasBot := false
	for _, u := range users {
		hasBot = hasBot || u.ID == botID
	}
	if !hasBot {
		users = append(users, domain.PremiumBotUser())
	}
	wireInvoice := tg.Invoice{
		Currency: domain.PremiumCurrencyStars,
		Prices: []tg.LabeledPrice{{
			Label:  invoice.Title,
			Amount: invoice.AmountStars,
		}},
	}
	if invoice.Kind == domain.PremiumPurchaseSelf {
		wireInvoice.SetSubscriptionPeriod(invoice.DurationDays * 24 * 60 * 60)
	}
	return &tg.PaymentsPaymentFormStars{
		FormID: form.ID, BotID: botID, Title: invoice.Title,
		Description: invoice.Description, Invoice: wireInvoice,
		Users: tgUsersForViewer(userID, users),
	}, nil
}

// sendPremiumStarsForm settles a checkout form issued by
// premiumPaymentForm: debits Stars, grants (or extends) the recipient's
// Premium entitlement, and pushes an updateUser to their online sessions so
// an already-connected client reflects the new status immediately. Called
// by onPaymentsSendStarsForm's dispatcher in payments_star_gifts.go.
func (r *Router) sendPremiumStarsForm(ctx context.Context, userID, formID int64, input tg.InputInvoiceClass) (tg.PaymentsPaymentResultClass, error) {
	if formID == 0 {
		return nil, formIDEmptyErr()
	}
	if r.deps.Premium == nil {
		return nil, notImplementedErr()
	}
	invoice, err := r.resolvePremiumInvoice(ctx, userID, input)
	if err != nil {
		return nil, err
	}
	recipientID := invoice.RecipientUserID
	if invoice.Kind == domain.PremiumPurchaseSelf {
		recipientID = userID
	}
	result, err := r.deps.Premium.Purchase(ctx, domain.PremiumPurchaseRequest{
		BuyerUserID: userID, FormID: formID, Kind: invoice.Kind,
		RecipientUserID: recipientID, Months: invoice.Months,
		PlanVersion: invoice.PlanVersion, Message: invoice.Message,
		Date: int(r.clock.Now().Unix()),
	})
	if err != nil {
		return nil, premiumPaymentErr(err)
	}
	r.pushPremiumStatusUpdate(ctx, result.User)
	return &tg.PaymentsPaymentResult{Updates: &tg.Updates{Date: int(r.clock.Now().Unix())}}, nil
}

// resolvePremiumInvoice prices and validates a checkout request; the amount,
// duration and plan version always come from the server's own catalog, never
// from the client.
func (r *Router) resolvePremiumInvoice(
	ctx context.Context,
	userID int64,
	input tg.InputInvoiceClass,
) (domain.PremiumInvoice, error) {
	switch invoice := input.(type) {
	case *tg.InputInvoicePremiumGiftStars:
		if invoice == nil || invoice.Months <= 0 {
			return domain.PremiumInvoice{}, tgerr.New(400, "PREMIUM_GIFT_CODE_INVALID")
		}
		recipient, found, err := r.userFromInput(ctx, userID, invoice.UserID)
		if err != nil {
			return domain.PremiumInvoice{}, internalErr()
		}
		if !found || recipient.ID <= 0 || recipient.ID == userID || recipient.Bot ||
			recipient.Deleted || domain.IsSystemUserID(recipient.ID) {
			if recipient.ID == userID {
				return domain.PremiumInvoice{}, tgerr.New(400, "PREMIUM_GIFT_SELF_INVALID")
			}
			return domain.PremiumInvoice{}, userIDInvalidErr()
		}
		plan, err := r.deps.Premium.Plan(ctx, invoice.Months)
		if err != nil {
			return domain.PremiumInvoice{}, premiumPaymentErr(err)
		}
		message := domain.PremiumGiftMessage{}
		if text, ok := invoice.GetMessage(); ok {
			if len([]rune(text.Text)) > domain.MaxPremiumGiftMessageRunes {
				return domain.PremiumInvoice{}, messageTooLongErr()
			}
			message.Text = text.Text
			message.Entities = domainMessageEntitiesForViewer(userID, text.Entities)
			if len(message.Entities) != len(text.Entities) || !message.Valid() {
				return domain.PremiumInvoice{}, entityBoundsInvalidErr()
			}
		}
		return premiumInvoiceFromPlan(domain.PremiumPurchaseGift, recipient.ID, plan, message), nil

	case *tg.InputInvoiceStars:
		if invoice == nil {
			return domain.PremiumInvoice{}, tgerr.New(400, "INVOICE_INVALID")
		}
		purpose, ok := invoice.Purpose.(*tg.InputStorePaymentPremiumSubscription)
		if !ok || purpose == nil || purpose.Restore {
			return domain.PremiumInvoice{}, tgerr.New(400, "INVOICE_INVALID")
		}
		plans, err := r.deps.Premium.Plans(ctx)
		if err != nil || len(plans) == 0 {
			return domain.PremiumInvoice{}, premiumPaymentErr(domain.ErrPremiumPlanUnavailable)
		}
		// The native store purpose carries no month field. Mirror Telegram's
		// own upgrade bit: map a normal purchase to the shortest enabled
		// catalog plan and an upgrade to the longest.
		plan, ok := nativePremiumSubscriptionPlan(plans, purpose.Upgrade)
		if !ok {
			return domain.PremiumInvoice{}, premiumPaymentErr(domain.ErrPremiumPlanUnavailable)
		}
		return premiumInvoiceFromPlan(domain.PremiumPurchaseSelf, 0, plan, domain.PremiumGiftMessage{}), nil
	default:
		return domain.PremiumInvoice{}, notImplementedErr()
	}
}

func nativePremiumSubscriptionPlan(plans []domain.PremiumPlan, upgrade bool) (domain.PremiumPlan, bool) {
	var selected domain.PremiumPlan
	found := false
	for _, plan := range plans {
		if !plan.Valid() || !plan.Enabled {
			continue
		}
		if !found || (!upgrade && plan.Months < selected.Months) ||
			(upgrade && plan.Months > selected.Months) ||
			(plan.Months == selected.Months && plan.Version > selected.Version) {
			selected = plan
			found = true
		}
	}
	return selected, found
}

func premiumInvoiceFromPlan(kind domain.PremiumPurchaseKind, recipientID int64, plan domain.PremiumPlan, message domain.PremiumGiftMessage) domain.PremiumInvoice {
	return domain.PremiumInvoice{
		Kind: kind, RecipientUserID: recipientID,
		Months: plan.Months, DurationDays: plan.DurationDays, AmountStars: plan.AmountStars,
		PlanVersion: plan.Version, Message: message,
		Title:       fmt.Sprintf("Telegram Premium (%d months)", plan.Months),
		Description: domain.PremiumPurchaseDescription(kind, plan.Months, recipientID),
	}
}

func premiumPaymentErr(err error) error {
	switch {
	case err == nil:
		return nil
	case err == domain.ErrPremiumPlanUnavailable:
		return tgerr.New(400, "PREMIUM_PLAN_UNAVAILABLE")
	case err == domain.ErrPremiumRecipientInvalid:
		return userIDInvalidErr()
	case err == domain.ErrPremiumRecipientRestricted:
		return tgerr.New(403, "USER_PRIVACY_RESTRICTED")
	case err == domain.ErrPremiumGiftSelf:
		return tgerr.New(400, "PREMIUM_GIFT_SELF_INVALID")
	case err == domain.ErrPremiumGiftMessageInvalid:
		return entityBoundsInvalidErr()
	case err == domain.ErrPremiumFormExpired:
		return tgerr.New(400, "FORM_EXPIRED")
	case err == domain.ErrPremiumFormInvalid:
		return inputRequestInvalidErr()
	case err == domain.ErrPremiumFormAmountChanged:
		return starsFormAmountMismatchErr()
	case err == domain.ErrPremiumInvoiceAlreadyPaid:
		return tgerr.New(400, "INVOICE_ALREADY_PAID")
	case err == domain.ErrStarsInsufficient:
		return balanceTooLowErr()
	default:
		if until, ok := err.(domain.PremiumSubscriptionActiveError); ok {
			_ = until
			return tgerr.New(400, "PREMIUM_SUBSCRIPTION_ACTIVE")
		}
		return internalErr()
	}
}
