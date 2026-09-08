package domain

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/nyaruka/phonenumbers"
)

var (
	ErrPasswordHashInvalid     = errors.New("password hash invalid")
	ErrSRPIDInvalid            = errors.New("srp id invalid")
	ErrSRPPasswordChanged      = errors.New("srp password changed")
	ErrNewSettingsInvalid      = errors.New("new password settings invalid")
	ErrNewSaltInvalid          = errors.New("new password salt invalid")
	ErrPasswordRecoveryNA      = errors.New("password recovery not available")
	ErrRecoveryCodeEmpty       = errors.New("recovery code empty")
	ErrRecoveryCodeInvalid     = errors.New("recovery code invalid")
	ErrPasswordRecoveryExpired = errors.New("password recovery expired")
	ErrEmailCodeInvalid        = errors.New("email code invalid")
	ErrEmailInvalid            = errors.New("email invalid")
	ErrEmailNotAllowed         = errors.New("email not allowed")
	ErrEmailOccupied           = errors.New("email occupied")
	ErrSessionPasswordNeeded   = errors.New("session password needed")
	ErrPhoneNumberInvalid      = errors.New("phone number invalid")
	ErrPhoneNumberOccupied     = errors.New("phone number occupied")
	ErrPhoneCodeEmpty          = errors.New("phone code empty")
	ErrPhoneCodeInvalid        = errors.New("phone code invalid")
	ErrPhoneCodeExpired        = errors.New("phone code expired")
	ErrPhoneChangeAuthInvalid  = errors.New("phone change auth invalid")
	ErrPhoneChangeForbidden    = errors.New("phone change forbidden")
)

type AuthCodeDeliveryKind string

const (
	AuthCodeDeliveryPhone              AuthCodeDeliveryKind = "phone"
	AuthCodeDeliverySMS                AuthCodeDeliveryKind = "sms"
	AuthCodeDeliveryEmail              AuthCodeDeliveryKind = "email"
	AuthCodeDeliveryEmailSetupRequired AuthCodeDeliveryKind = "email_setup_required"
)

type AuthCodeDelivery struct {
	Kind         AuthCodeDeliveryKind
	EmailPattern string
	Length       int
}

// PasswordKDFAlgo 是业务层的 SRP KDF 算法描述，不依赖 tg.*。
type PasswordKDFAlgo struct {
	Salt1 []byte
	Salt2 []byte
	G     int
	P     []byte
}

// SecurePasswordKDFAlgo 是 Telegram Passport secure secret 的 KDF 算法描述。
type SecurePasswordKDFAlgo struct {
	Kind string
	Salt []byte
}

// PasswordCheck 是 inputCheckPasswordEmpty/inputCheckPasswordSRP 的业务层表达。
type PasswordCheck struct {
	Empty bool
	SRPID int64
	A     []byte
	M1    []byte
}

// PasswordInputSettings 是 account.passwordInputSettings 的业务层表达。
type PasswordInputSettings struct {
	NewAlgo         *PasswordKDFAlgo
	NewPasswordHash []byte
	Hint            string
	HasHint         bool
	Email           string
	HasEmail        bool
}

// PrivatePasswordSettings 是 account.passwordSettings 的业务层表达。
type PrivatePasswordSettings struct {
	Email string
}

type PasswordResetKind string

const (
	PasswordResetOK            PasswordResetKind = "ok"
	PasswordResetRequestedWait PasswordResetKind = "requested_wait"
	PasswordResetFailedWait    PasswordResetKind = "failed_wait"
)

type PasswordResetResult struct {
	Kind      PasswordResetKind
	UntilDate int
	RetryDate int
}

// PasswordSettings 是账号 2FA/SRP 配置。默认 HasPassword=false。
type PasswordSettings struct {
	HasRecovery             bool
	HasSecureValues         bool
	HasPassword             bool
	CurrentAlgo             *PasswordKDFAlgo
	SRPB                    []byte
	SRPID                   int64
	Hint                    string
	EmailUnconfirmedPattern string
	RecoveryEmail           string
	// LoginEmail 是已确认的登录邮箱地址（服务端私有，永不直接下发；下发的是掩码后的
	// LoginEmailPattern）。它独立于 2FA 恢复邮箱 RecoveryEmail：账号可只设登录邮箱而无 2FA。
	LoginEmail        string
	LoginEmailPattern string
	NewAlgo           PasswordKDFAlgo
	NewSecureAlgo     SecurePasswordKDFAlgo
	SecureRandom      []byte
	PendingResetDate  int

	// Server-only SRP fields. They are persisted but never exposed to rpc/tg conversion.
	SRPVerifier []byte
	SRPBSecret  []byte
}

// RevenueWithdrawalPasswordState carries only the durable 2FA facts required
// by high-risk payout admission. It intentionally excludes password material.
type RevenueWithdrawalPasswordState struct {
	HasPassword       bool
	PasswordChangedAt time.Time
}

// ReactionNotifyFrom stores one account-level reaction notification scope.
type ReactionNotifyFrom string

const (
	ReactionNotifyFromNone     ReactionNotifyFrom = "none"
	ReactionNotifyFromContacts ReactionNotifyFrom = "contacts"
	ReactionNotifyFromAll      ReactionNotifyFrom = "all"
)

// ReactionsNotifySettings stores the account reaction notification settings
// consumed by account.get/setReactionsNotifySettings.
type ReactionsNotifySettings struct {
	MessagesFrom  ReactionNotifyFrom
	StoriesFrom   ReactionNotifyFrom
	PollVotesFrom ReactionNotifyFrom
	ShowPreviews  bool
}

// PaidReactionPrivacyKind stores the account default paid reaction privacy.
type PaidReactionPrivacyKind string

const (
	PaidReactionPrivacyDefault   PaidReactionPrivacyKind = "default"
	PaidReactionPrivacyAnonymous PaidReactionPrivacyKind = "anonymous"
	PaidReactionPrivacyPeer      PaidReactionPrivacyKind = "peer"
)

// PaidReactionPrivacy is the domain representation of tg.PaidReactionPrivacy.
type PaidReactionPrivacy struct {
	Kind PaidReactionPrivacyKind
	Peer *Peer
}

// AccountReactionSettings groups account-level reaction preferences.
type AccountReactionSettings struct {
	Notify          ReactionsNotifySettings
	DefaultReaction MessageReaction
	PaidPrivacy     PaidReactionPrivacy
}

func DefaultAccountReactionSettings() AccountReactionSettings {
	return AccountReactionSettings{
		Notify: ReactionsNotifySettings{
			MessagesFrom:  ReactionNotifyFromContacts,
			StoriesFrom:   ReactionNotifyFromContacts,
			PollVotesFrom: ReactionNotifyFromContacts,
			ShowPreviews:  true,
		},
		DefaultReaction: MessageReaction{Type: MessageReactionEmoji, Emoticon: "👍"},
		PaidPrivacy:     PaidReactionPrivacy{Kind: PaidReactionPrivacyDefault},
	}
}

// DefaultAccountTTLDays 是账号自毁默认期限（无显式设置时）。与历史固定回显一致。
const (
	DefaultAccountTTLDays = 365
	// MaxAccountTTLDays prevents an untrusted int32 TL value from producing an
	// out-of-range PostgreSQL interval/timestamp during deadline maintenance.
	MaxAccountTTLDays = 3650
)

// DisallowedGifts stores the Layer 228 global gift-reception switches.
type DisallowedGifts struct {
	UnlimitedStargifts   bool
	LimitedStargifts     bool
	UniqueStargifts      bool
	PremiumGifts         bool
	StargiftsFromChannel bool
}

func (g DisallowedGifts) Zero() bool {
	return !g.UnlimitedStargifts && !g.LimitedStargifts && !g.UniqueStargifts &&
		!g.PremiumGifts && !g.StargiftsFromChannel
}

// GlobalPrivacy 是 globalPrivacySettings 的业务层表达（账号级隐私开关）。
type GlobalPrivacy struct {
	ArchiveAndMuteNewNoncontactPeers bool
	KeepArchivedUnmuted              bool
	KeepArchivedFolders              bool
	HideReadMarks                    bool
	NewNoncontactPeersRequirePremium bool
	DisplayGiftsButton               bool
	DisallowedGifts                  DisallowedGifts
	// NoncontactPeersPaidStars：非联系人给本人发消息所需 Stars 数。Stars 账本尚未实现，
	// 此处仅做忠实持久化（往返不丢值），不参与计费逻辑。
	NoncontactPeersPaidStars int64
}

// AccountSettings 聚合账号级单例设置（每用户一行）：全局隐私、账号自毁期限、
// 敏感内容开关、联系人注册通知静音。对应 account.get/set GlobalPrivacySettings、
// get/set AccountTTL、get/set ContentSettings、get/set ContactSignUpNotification。
type AccountSettings struct {
	GlobalPrivacy           GlobalPrivacy
	AccountTTLDays          int
	SensitiveContentEnabled bool
	// ContactSignUpSilent 对应 account.setContactSignUpNotification 的 silent 形参：
	// true=联系人注册时不通知本人。getContactSignUpNotification 直接返回该值。
	ContactSignUpSilent bool
}

// DefaultAccountSettings 是未持久化时的账号设置默认值（与历史回显 stub 行为一致：
// 全局隐私全关、TTL 365 天、敏感内容关、联系人注册通知开启）。
func DefaultAccountSettings() AccountSettings {
	return AccountSettings{
		AccountTTLDays: DefaultAccountTTLDays,
	}
}

// NormalizedTTLDays 返回钳制后的账号自毁期限（0/越界回落默认）。
func (s AccountSettings) NormalizedTTLDays() int {
	if s.AccountTTLDays <= 0 || s.AccountTTLDays > MaxAccountTTLDays {
		return DefaultAccountTTLDays
	}
	return s.AccountTTLDays
}

// MaskEmail 把邮箱地址按 Telegram pattern 习惯掩码（首尾各保留一位本地名，如
// a***z@x.com），用于 account.password.login_email_pattern / auth.sentCodeTypeEmailCode
// 等只能暴露掩码的下发点。空串返回空串。
func MaskEmail(email string) string {
	if email == "" {
		return ""
	}
	at := strings.Index(email, "@")
	if at <= 1 {
		return email
	}
	name := email[:at]
	return name[:1] + "***" + name[len(name)-1:] + email[at:]
}

// virtualLoginPhoneMinDigits/virtualLoginPhoneMaxDigits bound the "888"-prefixed
// virtual login identity range NormalizePhone accepts without going through
// libphonenumber (real E.164 numbers never start with 888). This is a login
// identity concept only -- distinct from, and independent of, ownership of any
// purchasable collectible-phone asset with the same digit shape.
const (
	virtualLoginPhoneMinDigits = 7
	virtualLoginPhoneMaxDigits = 15
)

// PhoneDigits removes presentation punctuation from a phone number. It is
// intentionally not an identity canonicalizer: callers that select accounts,
// issue codes, or persist users must use NormalizePhone and ValidPhone.
func PhoneDigits(phone string) string {
	var b strings.Builder
	b.Grow(len(phone))
	seenDigit := false
	seenPlus := false
	for _, r := range phone {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			seenDigit = true
		case r == '+':
			if seenPlus || seenDigit {
				return ""
			}
			seenPlus = true
		case unicode.IsSpace(r), r == '-', r == '(', r == ')', r == '.', r == '/':
			// Presentation separators accepted by official clients and contact UIs.
		default:
			return ""
		}
	}
	return b.String()
}

// NormalizePhone returns the one persisted login identity. Virtual +888
// identities are independent of the collectible-phone registry and accept
// 7-15 canonical digits. Ordinary international numbers use E.164 digits
// without the leading '+'. Their parsing is deliberately country-aware so a
// national trunk prefix is removed only where the numbering plan says it is a
// prefix. For example, both +98 0998 167 9461 and +98 998 167 9461 become
// 989981679461, while Italy's significant leading zero in +39 02 ... is retained.
//
// Email-signup synthetic numbers (EncodeEmailPhone, "888" prefix plus at least
// one letter) are a separate exception, kept as-is (lower+trim only, no digit
// stripping) -- otherwise DecodeEmailPhone could never recover the email from
// an already letter-stripped value. A real phone is always pure digits, so
// this check never misclassifies one.
//
// IsPossibleNumber is the structural gate rather than IsValidNumber. It keeps
// syntactically possible reserved/test ranges usable without accepting local
// numbers that omit their country calling code or numbers outside E.164's
// length/plan metadata.
func NormalizePhone(phone string) string {
	if IsEmailSignupPhone(phone) {
		return strings.ToLower(strings.TrimSpace(phone))
	}
	digits := PhoneDigits(phone)
	if digits == "" {
		return ""
	}
	// Every syntactically valid +888 virtual number is an independent login
	// identity; minting or owning the same collectible-phone value is not a
	// prerequisite. users.phone therefore takes lookup precedence over any
	// optional collectible alias registry.
	if len(digits) >= virtualLoginPhoneMinDigits &&
		len(digits) <= virtualLoginPhoneMaxDigits &&
		strings.HasPrefix(digits, "888") {
		return digits
	}
	// 42777 is the reserved, non-login phone of the built-in service identity.
	// It predates the ordinary E.164 user invariant and remains resolvable only
	// so auth can reject it as a system account instead of treating it as free.
	if digits == OfficialSystemPhone {
		return digits
	}
	number, err := phonenumbers.Parse("+"+digits, phonenumbers.UNKNOWN_REGION)
	if err != nil || !phonenumbers.IsPossibleNumber(number) {
		return ""
	}
	canonical := strings.TrimPrefix(phonenumbers.Format(number, phonenumbers.E164), "+")
	if canonical == "" || len(canonical) > 15 {
		return ""
	}
	return canonical
}

// ValidPhone reports whether phone is already in the persisted canonical form.
// Callers accepting user input normalize first, then validate, so equivalent
// international spellings converge before lookup, rate limiting, OTP delivery,
// and uniqueness checks. Email-signup synthetic numbers keep their own
// lower+trim canonical form (see NormalizePhone).
func ValidPhone(phone string) bool {
	if IsEmailSignupPhone(phone) {
		if len(phone) < 5 || len(phone) > 200 {
			return false
		}
		for _, r := range phone {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
				return false
			}
		}
		return true
	}
	canonical := NormalizePhone(phone)
	return canonical != "" && canonical == phone
}
