package domain

import (
	"fmt"
	"math"
	"strings"
)

const officialWelcomeMessageTemplate = "👋 Welcome to OwpenGram!\n\nYou just signed in via %s.\n\nIf this wasn't you, revoke this session from Settings → Devices immediately."

// OfficialWelcomeMessage builds the account-visible incoming message sent
// from the official system account on every completed sign-in (SignUp and
// every subsequent SignIn/SignInWithEmail), regardless of delivery channel.
// Unlike OfficialLoginCodeMessage this never embeds a secret, so it is safe
// to send unconditionally — it exists to give the account owner (and, on a
// self-hosted single-admin server, that's usually also "the admin") a
// visible record of every session start.
func OfficialWelcomeMessage(userID int64, method string, date int) (Message, error) {
	method = strings.TrimSpace(method)
	if userID <= 0 || IsSystemUserID(userID) || method == "" || date < 0 || date > math.MaxInt32 {
		return Message{}, fmt.Errorf("%w: user=%d method=%q date=%d", ErrLoginCodeDeliveryInvalid, userID, method, date)
	}
	return Message{
		OwnerUserID: userID,
		Peer:        Peer{Type: PeerTypeUser, ID: OfficialSystemUserID},
		From:        Peer{Type: PeerTypeUser, ID: OfficialSystemUserID},
		Date:        date,
		Body:        fmt.Sprintf(officialWelcomeMessageTemplate, method),
	}, nil
}

// SignInMethodLabel returns the human-readable method name embedded in
// OfficialWelcomeMessage, derived from whether phone is an email-signup
// synthetic number (see EncodeEmailPhone) or a real phone number.
func SignInMethodLabel(phone string) string {
	if IsEmailSignupPhone(phone) {
		return "email"
	}
	return "phone number"
}
