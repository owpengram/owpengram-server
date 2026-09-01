package domain

import "strings"

// ClientSessionMetadata is request-scoped initConnection/session context for
// in-process features such as localized service bots. It is deliberately not a
// durable message field and must never be exposed through the public Bot API.
type ClientSessionMetadata struct {
	AuthKeyID      [8]byte
	SessionID      int64
	SystemLangCode string
	LangPack       string
	LangCode       string
}

// PreferredLanguage returns a normalized BCP-47 primary language subtag.
// Telegram clients normally send lang_code, with system_lang_code as fallback.
func (m ClientSessionMetadata) PreferredLanguage() string {
	value := strings.TrimSpace(m.LangCode)
	if value == "" {
		value = strings.TrimSpace(m.SystemLangCode)
	}
	value = strings.ToLower(strings.ReplaceAll(value, "_", "-"))
	if at := strings.IndexByte(value, '-'); at >= 0 {
		value = value[:at]
	}
	return value
}
