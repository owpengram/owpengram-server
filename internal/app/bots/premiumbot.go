package bots

import (
	"context"
	"fmt"
	"strconv"
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

// NotifyStarsGrantWithheld satisfies stars.AntiAbuseNotifier: it's called
// once, right after SignUp, when app/stars.Service.GuardStartingGrant
// withheld the new account's starting grant because its device+IP
// fingerprint was already used by another account (see cmd/telesrv, which
// wires starsService.SetAntiAbuseNotifier(botsService)). Sends a one-time
// @premiumbot explanation so a legitimate new user isn't left wondering why
// their balance shows 0 instead of the advertised starting grant.
func (s *Service) NotifyStarsGrantWithheld(ctx context.Context, userID int64) {
	if s == nil || userID <= 0 {
		return
	}
	mu := s.serviceBotReplyLock(domain.PremiumBotUserID, userID)
	mu.Lock()
	defer mu.Unlock()
	// Deliberately vague about why: naming the device/IP check would just
	// tell a farmer exactly what to change (a VPN, a different phone
	// profile) to get past it next time.
	text := "Your starting " + branding.StarsName() + " bonus isn't available for this account. You can still buy " + branding.StarsName() + " and use every other feature normally."
	s.sendServiceBotReply(ctx, domain.PremiumBotUserID, userID, botReply{Text: text})
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
	// Deliberately worded the same as the "feature not configured" case
	// below: naming the device/IP check would just tell a farmer exactly
	// what to change (a VPN, a different phone profile) to get past it.
	if withheld, err := s.stars.GuardClaim(ctx, userID); err == nil && withheld {
		return "The free " + branding.StarsName() + " claim is not available for this account right now."
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
	starPrice := s.donations.StarPriceMicros()
	var lines []string
	for _, c := range chains {
		if !c.Watchable() {
			continue
		}
		assets := c.NativeSymbol
		if tokens, err := s.donations.ChainTokens(ctx, c.Key); err == nil {
			for _, t := range tokens {
				if t.Watchable() {
					assets += ", " + t.Symbol
				}
			}
		}
		line := "• " + c.Name + " — " + assets
		// Only quote a rate the watcher would actually credit at: a price
		// too low to be worth a single Star means deposits are detected and
		// then never credited, and promising a number here would be a lie.
		if stars := starsPerWholeUnit(c.ManualUSDRateMicros, starPrice); stars > 0 {
			line += "\n   1 " + c.NativeSymbol + " ≈ " + formatStarCount(stars) + " " + branding.StarsName()
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return botReply{Text: "Crypto deposits are not available right now -- no network is configured yet."}
	}
	address, err := s.donations.AddressForUser(ctx, userID)
	if err != nil || address == "" {
		return botReply{Text: "Could not assign your deposit address right now. Please try again later."}
	}

	head := "Your personal crypto deposit address:\n\n"
	tail := "\n\nSupported networks and coins:\n" + strings.Join(lines, "\n") +
		"\n\nRate: 1 " + branding.StarsName() + " = " + formatUSDMicros(starPrice) +
		" (" + formatStarCount(starsPerUSD(starPrice)) + " per $1)." +
		"\n\nSend any coin listed above to this address from any wallet. Once the deposit reaches enough confirmations it is converted automatically and credited to your balance -- nothing else to do." +
		"\n\nThis is the same address every time you check /deposit. Do not send anything on a network that is not listed above, and do not send a token that is not listed for that network -- those funds cannot be credited."
	return botReply{
		Text: head + address + tail,
		Entities: []domain.MessageEntity{
			// address is ASCII (0x + 40 lowercase hex chars), so byte length
			// and UTF-16 length coincide -- same assumption tokenReply makes.
			{Type: domain.MessageEntityCode, Offset: len(head), Length: len(address)},
		},
	}
}

// starsPerWholeUnit is how many Stars one whole coin buys at a chain's USD
// rate, rounded down exactly like the crediting path does.
func starsPerWholeUnit(rateMicros, starPriceMicros int64) int64 {
	if rateMicros <= 0 || starPriceMicros <= 0 {
		return 0
	}
	return rateMicros / starPriceMicros
}

// starsPerUSD is how many Stars one US dollar buys.
func starsPerUSD(starPriceMicros int64) int64 {
	if starPriceMicros <= 0 {
		return 0
	}
	return 1_000_000 / starPriceMicros
}

// formatUSDMicros renders a micro-dollar amount as a plain price, trimming
// the trailing zeros a fixed 6-decimal rendering would leave ("$0.005",
// not "$0.005000").
func formatUSDMicros(micros int64) string {
	if micros <= 0 {
		return "$0"
	}
	out := strconv.FormatFloat(float64(micros)/1_000_000, 'f', -1, 64)
	return "$" + out
}

// formatStarCount groups thousands so a six-figure quote stays readable.
func formatStarCount(n int64) string {
	digits := strconv.FormatInt(n, 10)
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteString(",")
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
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
