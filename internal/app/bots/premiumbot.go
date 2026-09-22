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
	return "Buy " + branding.ProductName() + " Premium for yourself or as a gift. All prices are in Telegram Stars.\n\n" +
		"/premium - see available plans\n" +
		"/status - check your current Premium status\n" +
		"/history - your last Premium purchases\n" +
		"/gift - how to gift Premium to someone\n" +
		"/claim - claim your free monthly Stars\n" +
		"/help - show this message"
}

func premiumBotGiftText() string {
	return "To gift " + branding.ProductName() + " Premium, open the recipient's profile and choose \"Gift Premium\", or use Settings → Premium → Gift Premium in the app. Delivery and payment happen there -- this chat is for information only."
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
		fmt.Fprintf(&b, "• %s — %d Stars\n", label, plan.AmountStars)
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
		return "The free Stars claim is not available right now."
	}
	bal, claimed, nextAt, err := s.stars.ClaimMonthly(ctx, userID, s.starsMonthlyClaim, s.starsMonthlyClaimCooldown)
	if err != nil {
		return "Could not process your claim right now. Please try again later."
	}
	if claimed {
		return fmt.Sprintf("You claimed %d Stars! Your balance is now %d Stars.\n\nNext claim available on %s (UTC).",
			s.starsMonthlyClaim, bal.Balance, nextAt.UTC().Format("2006-01-02"))
	}
	return fmt.Sprintf("You already claimed your free Stars. Next claim available on %s (UTC).", nextAt.UTC().Format("2006-01-02"))
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
