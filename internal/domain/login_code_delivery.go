package domain

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// loginCodeTemplateCodePlaceholder marks where the actual OTP code is
// substituted into a (possibly admin-edited) login-code message template.
// Unlike the old hardcoded "Login code: %s" format, the template body is no
// longer fixed, so the substituted code's bold MessageEntity offset/length
// must be computed dynamically from wherever the placeholder actually lands
// -- see OfficialLoginCodeMessage. It must appear exactly once in any
// template that reaches OfficialLoginCodeMessage (see
// ValidateLoginCodeMessageTemplate): zero occurrences would silently drop
// the code from the message entirely, and two-or-more is ambiguous about
// which occurrence is "the" code.
const loginCodeTemplateCodePlaceholder = "{{code}}"

// DefaultLoginCodeMessageTemplate is the built-in, final-fallback copy for
// the 777000 login-code delivery message. It is sent for every login code
// regardless of delivery channel (phone SMS or email) -- see the
// LoginCodeDeliveryStore implementations in internal/store/{postgres,memory},
// which all pass the same code through unconditionally, and
// internal/app/auth's recordLoginMessage (the new-account bootstrap path).
// Unlike DefaultWelcomeMessage{Phone,Email}Template there is only one
// template: the message never varies by channel. Supports {{server_name}}
// (see RenderWelcomeMessageTemplate) and requires the {{code}} placeholder
// exactly once (see ValidateLoginCodeMessageTemplate).
const DefaultLoginCodeMessageTemplate = `Login code: {{code}}. Do not give this code to anyone, even if they say they are from {{server_name}}!

This code can be used to log in to your {{server_name}} account. We never ask it for anything else.

If you didn't request this code by trying to log in on another device, simply ignore this message.`

// ErrLoginCodeMessageTemplateMissingCode is returned when a candidate
// login-code message template does not contain the {{code}} placeholder
// exactly once -- see ValidateLoginCodeMessageTemplate. The admin-API layer
// (cmd/telesrv-admin) must reject a save with this error outright rather
// than silently accepting it: a template with zero {{code}} occurrences
// would never deliver the actual OTP to the user at all.
var ErrLoginCodeMessageTemplateMissingCode = errors.New("login code message template must contain the {{code}} placeholder exactly once")

// ValidateLoginCodeMessageTemplate requires the {{code}} placeholder to
// appear exactly once. Zero occurrences is a functional break (the OTP
// itself would never reach the user), and two-or-more is ambiguous (which
// occurrence gets the bold entity and the substitution?) -- both are
// rejected outright, never silently patched around by e.g. appending the
// code somewhere the admin didn't put it.
func ValidateLoginCodeMessageTemplate(template string) error {
	if strings.Count(template, loginCodeTemplateCodePlaceholder) != 1 {
		return ErrLoginCodeMessageTemplateMissingCode
	}
	return nil
}

// ResolveLoginCodeMessageTemplate picks the final template body, in order:
// an explicit admin-panel override (panelOverride, as stored raw in
// identity.Info -- empty means "not configured"), then an explicit env-var
// default (envDefault, empty means "not configured"), then the compiled-in
// DefaultLoginCodeMessageTemplate. It is a pure function so the precedence
// logic can be unit-tested without touching the identity store or config --
// those live in internal/app/auth, which resolves this fresh on every
// login-code delivery (never cached) so an admin-panel edit takes effect
// immediately, mirroring ResolveWelcomeMessageTemplate. Unlike that
// resolver there is no per-method branching: every login code, regardless
// of delivery channel, uses the same template.
//
// This does not itself validate the {{code}} placeholder -- callers that
// persist an override (the admin API) must call
// ValidateLoginCodeMessageTemplate before saving. OfficialLoginCodeMessage
// re-validates whatever it resolves to anyway, as defense in depth against
// an invalid value that reached here some other way (a hand-edited
// identity.json, an out-of-band env var change).
func ResolveLoginCodeMessageTemplate(panelOverride, envDefault string) string {
	if t := strings.TrimSpace(panelOverride); t != "" {
		return panelOverride
	}
	if t := strings.TrimSpace(envDefault); t != "" {
		return envDefault
	}
	return DefaultLoginCodeMessageTemplate
}

// LoginCodeDeliveryRequest describes one durable 777000 login-code delivery.
// PhoneCodeHash is an opaque idempotency token and must never be persisted in
// plaintext; store implementations persist only its SHA-256 digest.
type LoginCodeDeliveryRequest struct {
	UserID        int64
	PhoneCodeHash string
	Code          string
	// Template is the already-resolved login-code message template (see
	// ResolveLoginCodeMessageTemplate) -- resolving it requires the identity
	// store and config, both of which live above internal/store, so callers
	// (internal/app/auth) do that and pass the final template text in here,
	// the same division of responsibility OfficialWelcomeMessage's body
	// parameter uses. Empty falls back to DefaultLoginCodeMessageTemplate
	// (see OfficialLoginCodeMessage).
	Template string
	Date     int
	// ExpiresAt is the unix second after which the compact idempotency receipt
	// may be reclaimed. It must cover the corresponding code's usable lifetime.
	ExpiresAt int64
}

// LoginCodeDeliveryResult returns the immutable first delivery. Created is
// false when the same phone_code_hash was already committed and replayed.
type LoginCodeDeliveryResult struct {
	Message Message
	Created bool
}

// OfficialLoginCodeMessage builds the account-visible incoming message from
// Telegram's official notification account. Persistence assigns ID, UID and
// Pts atomically.
//
// template is rendered ({{server_name}} substituted, then {{code}} replaced
// with the actual code) and the resulting bold MessageEntity is positioned
// dynamically from wherever {{code}} actually landed after substitution --
// never assumed from a fixed prefix, since template is admin-editable (see
// ValidateLoginCodeMessageTemplate). A template that is empty or fails
// validation falls back to DefaultLoginCodeMessageTemplate instead of ever
// shipping a message with no code in it.
func OfficialLoginCodeMessage(userID int64, template, code string, date int) (Message, error) {
	if userID <= 0 || IsSystemUserID(userID) || strings.TrimSpace(code) == "" || len(code) > 64 || date < 0 || date > math.MaxInt32 {
		return Message{}, fmt.Errorf("%w: user=%d code_length=%d date=%d", ErrLoginCodeDeliveryInvalid, userID, len(code), date)
	}
	if strings.TrimSpace(template) == "" || ValidateLoginCodeMessageTemplate(template) != nil {
		template = DefaultLoginCodeMessageTemplate
	}
	rendered := RenderWelcomeMessageTemplate(template)
	idx := strings.Index(rendered, loginCodeTemplateCodePlaceholder)
	if idx < 0 {
		// Unreachable in practice: template was just validated (or is the
		// compiled-in default) to contain the placeholder exactly once, and
		// {{server_name}} substitution cannot remove or relocate an
		// unrelated placeholder. Guarded anyway rather than ever ship a
		// message silently missing its code.
		rendered = DefaultLoginCodeMessageTemplate
		idx = strings.Index(rendered, loginCodeTemplateCodePlaceholder)
	}
	body := rendered[:idx] + code + rendered[idx+len(loginCodeTemplateCodePlaceholder):]
	return Message{
		OwnerUserID: userID,
		Peer:        Peer{Type: PeerTypeUser, ID: OfficialSystemUserID},
		From:        Peer{Type: PeerTypeUser, ID: OfficialSystemUserID},
		Date:        date,
		Body:        body,
		Entities: []MessageEntity{
			{Type: MessageEntityBold, Offset: automaticEntityUTF16Length(rendered[:idx]), Length: automaticEntityUTF16Length(code)},
		},
	}, nil
}
