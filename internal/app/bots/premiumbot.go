package bots

import (
	"context"
	"fmt"
	"strings"
	"time"

	"telesrv/internal/branding"
	"telesrv/internal/domain"
)

const premiumBotHistoryLimit = 10

func premiumBotHelpText() string {
	return "Buy " + branding.ProductName() + " Premium for yourself or as a gift. All prices are in " + branding.StarsName() + ".\n\n" +
		"/premium - see available plans\n" +
		"/status - check your current Premium status\n" +
		"/history - your last Premium purchases\n" +
		"/gift - how to gift Premium to someone\n" +
		"/claim - claim your free monthly " + branding.StarsName() + "\n" +
		"/deposit - get your personal crypto deposit address\n" +
		"/help - show this message"
}

func premiumBotGiftText() string {
	return "To gift " + branding.ProductName() + " Premium, open the recipient's profile and choose \"Gift Premium\", or use Settings → Premium → Gift Premium in the app. Delivery and payment happen there -- this chat is for information only."
}

// NotifyDonationCredited satisfies donations.CreditNotifier: it's called
// once per deposit the crypto donations watcher just credited (see
// cmd/telesrv, which wires donationsService.SetNotifier(botsService)), and
// sends the donor a @premiumbot message saying so. Without this wired up,
// crediting still happens exactly the same -- the user just finds out only
// by checking /deposit or their Stars balance themselves, never proactively.
func (s *Service) NotifyDonationCredited(ctx context.Context, notice domain.DonationCreditNotice) {
	if s == nil || notice.UserID <= 0 || notice.Deposit.StarsCredited <= 0 {
		return
	}
	mu := s.serviceBotReplyLock(domain.PremiumBotUserID, notice.UserID)
	mu.Lock()
	defer mu.Unlock()
	amount := formatAssetAmount(notice.Deposit.AmountRaw, notice.AssetDecimals)
	text := fmt.Sprintf("Your deposit of %s %s on %s was confirmed and credited: +%d %s.",
		amount, notice.AssetSymbol, notice.ChainName, notice.Deposit.StarsCredited, branding.StarsName())
	s.sendServiceBotReply(ctx, domain.PremiumBotUserID, notice.UserID, botReply{Text: text})
}

// formatAssetAmount renders a raw smallest-unit amount (wei, or an ERC-20's
// base units) as a human string with the asset's actual decimal point.
// Deliberately duplicated from (rather than imported off)
// app/donations.FormatAssetAmount: bots depends on donations only through
// the narrow donationsSource/CreditNotifier ports, matching every other
// cross-service dependency in this file, never the concrete package.
func formatAssetAmount(amountRaw string, decimals int) string {
	if decimals <= 0 || len(amountRaw) == 0 {
		return amountRaw
	}
	neg := strings.HasPrefix(amountRaw, "-")
	digits := strings.TrimPrefix(amountRaw, "-")
	for len(digits) <= decimals {
		digits = "0" + digits
	}
	intPart, fracPart := digits[:len(digits)-decimals], strings.TrimRight(digits[len(digits)-decimals:], "0")
	out := intPart
	if fracPart != "" {
		out += "." + fracPart
	}
	if neg {
		out = "-" + out
	}
	return out
}

// respondAsPremium answers a private message to the built-in @premiumbot.
// Unlike @Stickers/@BotFather, this bot has no stateful multi-step flow: the
// server has no invoice-message media type to send a buyable card into a
// chat (that infrastructure doesn't exist here), so every command answers
// with plain text and points at the app's own working Settings -> Premium
// purchase/gift UI rather than pretending to replicate it in chat.
func (s *Service) respondAsPremium(userID int64, msg domain.Message) {
	mu := s.serviceBotReplyLock(domain.PremiumBotUserID, userID)
	mu.Lock()
	defer mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd, ok := parseBotCommand(strings.TrimSpace(msg.Body))
	if !ok {
		s.sendServiceBotReply(ctx, domain.PremiumBotUserID, userID, botReply{Text: premiumBotHelpText()})
		return
	}
	switch cmd {
	case "start", "help":
		s.sendServiceBotReply(ctx, domain.PremiumBotUserID, userID, botReply{Text: premiumBotHelpText()})
	case "premium":
		s.sendServiceBotReply(ctx, domain.PremiumBotUserID, userID, botReply{Text: s.premiumBotPlansText(ctx)})
	case "status":
		s.sendServiceBotReply(ctx, domain.PremiumBotUserID, userID, botReply{Text: s.premiumBotStatusText(ctx, userID)})
	case "history":
		s.sendServiceBotReply(ctx, domain.PremiumBotUserID, userID, botReply{Text: s.premiumBotHistoryText(ctx, userID)})
	case "gift":
		s.sendServiceBotReply(ctx, domain.PremiumBotUserID, userID, botReply{Text: premiumBotGiftText()})
	case "claim":
		s.sendServiceBotReply(ctx, domain.PremiumBotUserID, userID, botReply{Text: s.premiumBotClaimText(ctx, userID)})
	case "deposit":
		s.sendServiceBotReply(ctx, domain.PremiumBotUserID, userID, s.premiumBotDepositReply(ctx, userID))
	default:
		s.sendServiceBotReply(ctx, domain.PremiumBotUserID, userID, botReply{Text: "Unrecognized command. Send /help for a list of commands."})
	}
}

func (s *Service) premiumBotPlansText(ctx context.Context) string {
	if s.premium == nil {
		return "Premium plans are not available right now. Please try again later."
	}
	plans, err := s.premium.Plans(ctx)
	if err != nil || len(plans) == 0 {
		return "Premium plans are not available right now. Please try again later."
	}
	var b strings.Builder
	b.WriteString(branding.ProductName() + " Premium plans:\n\n")
	for _, plan := range plans {
		label := strings.TrimSpace(plan.Label)
		if label == "" {
			label = fmt.Sprintf("%d months", plan.Months)
		}
		fmt.Fprintf(&b, "• %s — %d %s\n", label, plan.AmountStars, branding.StarsName())
	}
	b.WriteString("\nOpen Settings → Premium in the app to subscribe or gift one of these plans.")
	return b.String()
}

func (s *Service) premiumBotStatusText(ctx context.Context, userID int64) string {
	if s.users == nil {
		return "Could not check your Premium status right now."
	}
	user, found, err := s.users.ByID(ctx, userID)
	if err != nil || !found {
		return "Could not check your Premium status right now."
	}
	if user.PremiumUntil > 0 && int64(user.PremiumUntil) > s.now().Unix() {
		until := time.Unix(int64(user.PremiumUntil), 0).UTC().Format("2006-01-02")
		return fmt.Sprintf("You have Premium until %s (UTC).", until)
	}
	return "You do not have an active Premium subscription. Send /premium to see available plans."
}

func (s *Service) premiumBotClaimText(ctx context.Context, userID int64) string {
	if s.stars == nil || s.starsMonthlyClaim <= 0 {
		return "The free " + branding.StarsName() + " claim is not available right now."
	}
	bal, claimed, nextAt, err := s.stars.ClaimMonthly(ctx, userID, s.starsMonthlyClaim, s.starsMonthlyClaimCooldown)
	if err != nil {
		return "Could not process your claim right now. Please try again later."
	}
	if claimed {
		return fmt.Sprintf("You claimed %d %s! Your balance is now %d %s.\n\nNext claim available on %s (UTC).",
			s.starsMonthlyClaim, branding.StarsName(), bal.Balance, branding.StarsName(), nextAt.UTC().Format("2006-01-02"))
	}
	return fmt.Sprintf("You already claimed your free %s. Next claim available on %s (UTC).", branding.StarsName(), nextAt.UTC().Format("2006-01-02"))
}

// premiumBotDepositReply answers /deposit with the user's permanent crypto
// deposit address (the same address on every chain listed) and which
// chains it actually works on right now. The address is assigned once, on
// first call, and reused forever after -- see donations.Service.AddressForUser.
//
// It returns a full botReply rather than plain text because this server has
// no markdown parser on the send path (see service_bot_entities.go): the
// address must be marked up as a domain.MessageEntityCode span, the same
// way tokenReply does for BotFather tokens, not wrapped in literal
// backticks the client would show verbatim.
func (s *Service) premiumBotDepositReply(ctx context.Context, userID int64) botReply {
	if s.donations == nil || !s.donations.Ready() {
		return botReply{Text: "Crypto deposits are not available right now."}
	}
	chains, err := s.donations.EnabledChains(ctx)
	if err != nil {
		return botReply{Text: "Could not load deposit info right now. Please try again later."}
	}
	var live []string
	for _, c := range chains {
		if c.Watchable() {
			live = append(live, fmt.Sprintf("%s (%s)", c.Name, c.NativeSymbol))
		}
	}
	if len(live) == 0 {
		return botReply{Text: "Crypto deposits are not available right now -- no chain is configured yet."}
	}
	address, err := s.donations.AddressForUser(ctx, userID)
	if err != nil || address == "" {
		return botReply{Text: "Could not assign your deposit address right now. Please try again later."}
	}
	head := "Your personal crypto deposit address:\n\n"
	tail := "\n\nThis address works on: " + strings.Join(live, ", ") + ".\n\n" +
		"Send ETH, USDT or USDC to it from any wallet. Once your deposit reaches enough confirmations, it's automatically converted to " + branding.StarsName() + " and credited to your balance -- no further action needed.\n\n" +
		"This is the same address every time you check /deposit -- do not send funds on a chain not listed above, they will not be credited."
	return botReply{
		Text: head + address + tail,
		Entities: []domain.MessageEntity{
			// address is ASCII (0x + 40 lowercase hex chars), so byte length
			// and UTF-16 length coincide -- same assumption tokenReply makes.
			{Type: domain.MessageEntityCode, Offset: len(head), Length: len(address)},
		},
	}
}

func (s *Service) premiumBotHistoryText(ctx context.Context, userID int64) string {
	if s.premium == nil {
		return "Purchase history is not available right now."
	}
	entitlements, err := s.premium.Entitlements(ctx, userID, premiumBotHistoryLimit)
	if err != nil {
		return "Could not load your purchase history right now."
	}
	if len(entitlements) == 0 {
		return "You have no Premium purchases yet. Send /premium to see available plans."
	}
	var b strings.Builder
	b.WriteString("Your last Premium entitlements:\n\n")
	for _, e := range entitlements {
		start := time.Unix(int64(e.StartsAt), 0).UTC().Format("2006-01-02")
		fmt.Fprintf(&b, "• %s — %d months, %s, status: %s\n", start, e.Months, string(e.Source), string(e.Status))
	}
	return b.String()
}
