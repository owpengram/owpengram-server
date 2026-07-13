package domain

import (
	"math/big"
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

// EncodeEmailPhone deterministically and reversibly encodes an email address
// into a synthetic "888"-prefixed all-digit phone number: the email's
// lowercased/trimmed UTF-8 bytes, read as a big-endian unsigned integer, then
// printed in decimal. This lets the existing phone-based sendCode/signUp/
// signIn/changePhone flow carry an email address end to end unchanged — no
// new TL constructors, no server-side reverse-lookup table required.
//
// ok is false if email is empty/invalid or the encoded result would not fit
// the users.phone column (MaxEmailSignupPhoneLen) — this comfortably covers
// realistic email addresses (roughly up to 80 bytes).
func EncodeEmailPhone(email string) (phone string, ok bool) {
	normalized := NormalizeEmailForPhone(email)
	if normalized == "" || !strings.Contains(normalized, "@") {
		return "", false
	}
	n := new(big.Int).SetBytes([]byte(normalized))
	digits := n.String()
	phone = EmailPhonePrefix + digits
	if len(phone) > MaxEmailSignupPhoneLen {
		return "", false
	}
	return phone, true
}

// DecodeEmailPhone reverses EncodeEmailPhone. ok is false if phone does not
// carry the "888" prefix or does not decode to a plausible email address.
func DecodeEmailPhone(phone string) (email string, ok bool) {
	phone = NormalizePhone(strings.TrimSpace(phone))
	digits, found := strings.CutPrefix(phone, EmailPhonePrefix)
	if !found || digits == "" {
		return "", false
	}
	n, valid := new(big.Int).SetString(digits, 10)
	if !valid {
		return "", false
	}
	decoded := string(n.Bytes())
	if decoded == "" || !strings.Contains(decoded, "@") {
		return "", false
	}
	return decoded, true
}

// NormalizeEmailForPhone lowercases and trims an email so the same address
// always encodes to the same synthetic phone number regardless of how the
// user typed it (e.g. on a different device).
func NormalizeEmailForPhone(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// IsEmailSignupPhone reports whether phone was produced by EncodeEmailPhone
// (i.e. carries the synthetic "888" prefix), without decoding it.
func IsEmailSignupPhone(phone string) bool {
	return strings.HasPrefix(NormalizePhone(strings.TrimSpace(phone)), EmailPhonePrefix)
}
