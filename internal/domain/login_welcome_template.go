package domain

import "strings"

// LoginMethod distinguishes the two sign-in channels the login-welcome
// message template can be customized per: phone (SMS/app-code) and email
// (email-signup accounts, see SignInMethodLabel). There is no third axis --
// no signup-vs-signin distinction, no 2FA-vs-not distinction -- because
// recordWelcomeMessage's callers never carry more than this.
type LoginMethod string

const (
	LoginMethodPhone LoginMethod = "phone"
	LoginMethodEmail LoginMethod = "email"
)

// LoginMethodFromLabel maps SignInMethodLabel's human-readable string back
// to a LoginMethod, so callers that already computed the label (for the
// {{...}} template's own historical "via %s" wording) don't need to
// recompute it from the User a second time.
func LoginMethodFromLabel(label string) LoginMethod {
	if label == "email" {
		return LoginMethodEmail
	}
	return LoginMethodPhone
}

// DefaultWelcomeMessagePhoneTemplate and DefaultWelcomeMessageEmailTemplate
// are the built-in, final-fallback copy for the login-notification message
// sent from the official system account (777000) on every completed
// sign-in. They are deliberately separate strings (not one template with a
// substituted method name) so each reads naturally in its own channel.
//
// {{server_name}} is replaced with the server's current effective display
// name (see ResolveWelcomeMessageTemplate / RenderWelcomeMessageTemplate).
const (
	DefaultWelcomeMessagePhoneTemplate = "👋 Welcome to {{server_name}}!\n\nA new sign-in to your account was just completed using your phone number.\n\nIf this was you, no action is needed. If it wasn't, please revoke this session immediately from Settings → Privacy and Security → Active Sessions."

	DefaultWelcomeMessageEmailTemplate = "👋 Welcome to {{server_name}}!\n\nA new sign-in to your account was just completed using your email address.\n\nIf this was you, no action is needed. If it wasn't, please revoke this session immediately from Settings → Privacy and Security → Active Sessions."
)

// ResolveWelcomeMessageTemplate picks the final template body for the given
// login method, in order: an explicit admin-panel override (panelOverride,
// as stored raw in identity.Info -- empty means "not configured"), then an
// explicit env-var default (envDefault, empty means "not configured"), then
// the compiled-in default for that method. It is a pure function so the
// precedence logic can be unit-tested without touching the identity store
// or config -- those live in internal/app/auth, which calls this on every
// recordWelcomeMessage invocation (never cached) so an admin-panel edit
// takes effect immediately.
func ResolveWelcomeMessageTemplate(method LoginMethod, panelOverride, envDefault string) string {
	if t := strings.TrimSpace(panelOverride); t != "" {
		return panelOverride
	}
	if t := strings.TrimSpace(envDefault); t != "" {
		return envDefault
	}
	if method == LoginMethodEmail {
		return DefaultWelcomeMessageEmailTemplate
	}
	return DefaultWelcomeMessagePhoneTemplate
}

// RenderWelcomeMessageTemplate substitutes the {{server_name}} placeholder
// in template with the server's current effective display name. It is a
// literal, single-placeholder replacement -- no templating engine, since
// there's exactly one substitution to make.
func RenderWelcomeMessageTemplate(template string) string {
	return strings.ReplaceAll(template, "{{server_name}}", officialSystemDisplayName())
}
