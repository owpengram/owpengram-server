package domain

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// EmailPhonePrefix marks a "phone number" as a synthetic identity encoding an
// email address, not a real phone. It reuses Telegram's own +888 "Anonymous
// Number" range (already declared in this server's help.getAppConfig
// fragment_prefixes), so patched clients that already special-case 888
// numbers have a head start, and the range is guaranteed to never collide
// with a real assigned country code.
const EmailPhonePrefix = "888"

// MaxEmailSignupPhoneLen mirrors the users.phone column width (see migration
// 0087) and ValidPhone's upper bound.
const MaxEmailSignupPhoneLen = 200

// emailPhoneEscape marks a 2-character escape sequence standing in for one
// punctuation byte email addresses may contain but a "phone number" string
// otherwise can't (see NormalizePhone, which only preserves letters/digits
// for values recognized as email-signup phones). Every encoded value
// contains at least one 'q' (from the mandatory '@' escape), which is what
// lets IsEmailSignupPhone tell an encoded phone apart from a real,
// all-digit, "888"-area-code phone number without any extra bookkeeping.
const emailPhoneEscape = 'q'

var emailPhoneEscapeEncode = map[rune]byte{
	'@':              '0',
	'.':              '1',
	'-':              '2',
	'_':              '3',
	'+':              '4',
	emailPhoneEscape: '5',
}

var emailPhoneEscapeDecode = map[byte]rune{
	'0': '@',
	'1': '.',
	'2': '-',
	'3': '_',
	'4': '+',
	'5': emailPhoneEscape,
}

// EncodeEmailPhone deterministically and reversibly encodes an email address
// into a synthetic "888"-prefixed phone-number-shaped string: letters and
// digits pass through unchanged, and the handful of punctuation characters
// real email addresses use are each replaced by a 2-character escape
// ('q' + a digit). This keeps the encoded length close to the email's own
// length (unlike a byte-for-byte big-integer encoding, which runs ~2.4x
// longer), while staying fully reversible with no server-side lookup table
// and no new TL constructors — the existing phone-based sendCode/signUp/
// signIn/changePhone flow carries it end to end unchanged.
//
// ok is false if email is empty/invalid, contains a character outside
// [a-z0-9@._+-], or the encoded result would not fit the users.phone column
// (MaxEmailSignupPhoneLen).
func EncodeEmailPhone(email string) (phone string, ok bool) {
	normalized := NormalizeEmailForPhone(email)
	if normalized == "" || !strings.Contains(normalized, "@") {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(normalized) * 2)
	for _, r := range normalized {
		switch {
		case r >= 'a' && r <= 'z' && r != rune(emailPhoneEscape):
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			digit, escapable := emailPhoneEscapeEncode[r]
			if !escapable {
				return "", false
			}
			b.WriteByte(emailPhoneEscape)
			b.WriteByte(digit)
		}
	}
	phone = EmailPhonePrefix + b.String()
	if len(phone) > MaxEmailSignupPhoneLen {
		return "", false
	}
	return phone, true
}

// DecodeEmailPhone reverses EncodeEmailPhone. ok is false if phone does not
// carry the "888" prefix or does not decode to a plausible email address.
func DecodeEmailPhone(phone string) (email string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(phone))
	body, found := strings.CutPrefix(lower, EmailPhonePrefix)
	if !found || body == "" {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(body))
	runes := []rune(body)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r != rune(emailPhoneEscape) {
			b.WriteRune(r)
			continue
		}
		i++
		if i >= len(runes) {
			return "", false
		}
		digitByte, ok := asciiDigitByte(runes[i])
		if !ok {
			return "", false
		}
		decoded, known := emailPhoneEscapeDecode[digitByte]
		if !known {
			return "", false
		}
		b.WriteRune(decoded)
	}
	email = b.String()
	if email == "" || !strings.Contains(email, "@") {
		return "", false
	}
	return email, true
}

func asciiDigitByte(r rune) (byte, bool) {
	if r < '0' || r > '9' {
		return 0, false
	}
	return byte(r), true
}

// NormalizeEmailForPhone lowercases and trims an email so the same address
// always encodes to the same synthetic phone number regardless of how the
// user typed it (e.g. on a different device).
func NormalizeEmailForPhone(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// emailSignupDisplayPhoneDigits is how many random digits follow the prefix
// in a NewEmailSignupDisplayPhone result, e.g. prefix "888" + 8 digits =
// "88812345678" (formats on-screen as something like "+888 1234 5678").
const emailSignupDisplayPhoneDigits = 8

// NewEmailSignupDisplayPhone generates a short, all-digit phone number for an
// email-signup account's users.phone column, using the given prefix (one of
// the server's configured EmailSignupPhonePrefixes — see
// help.getAppConfig's email_signup_phone_prefixes, config.go). This is
// unrelated to EncodeEmailPhone's own fixed "888" wire prefix: that one only
// ever travels internally between client and server during sendCode/signIn
// and is never shown to anyone, so it has no reason to be configurable,
// unlike this display number, which is the account's actual, permanent,
// user-visible phone. The caller must separately persist the email->user
// association (see User.SignupEmail) for returning logins to be found.
// Because the result is all digits, IsEmailSignupPhone on it is always
// false: once assigned, it behaves exactly like a real phone number
// everywhere else in the system (contacts, search, ByPhone lookups).
func NewEmailSignupDisplayPhone(prefix string) (string, error) {
	if !isAllASCIIDigits(prefix) {
		return "", fmt.Errorf("email signup display phone prefix %q must be non-empty digits", prefix)
	}
	b := make([]byte, emailSignupDisplayPhoneDigits)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate email signup display phone: %w", err)
	}
	var out strings.Builder
	out.Grow(len(prefix) + emailSignupDisplayPhoneDigits)
	out.WriteString(prefix)
	for _, v := range b {
		out.WriteByte('0' + v%10)
	}
	return out.String(), nil
}

func isAllASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// IsEmailSignupPhone reports whether phone was produced by EncodeEmailPhone.
// Every encoded value contains at least one letter (the mandatory '@'
// escape's 'q' marker byte), which real, all-digit phone numbers — even
// ones that happen to start with the 888 area code — never do; this keeps
// the check unambiguous without any extra prefix bookkeeping.
func IsEmailSignupPhone(phone string) bool {
	lower := strings.ToLower(strings.TrimSpace(phone))
	if !strings.HasPrefix(lower, EmailPhonePrefix) {
		return false
	}
	for _, r := range lower {
		if r >= 'a' && r <= 'z' {
			return true
		}
	}
	return false
}
