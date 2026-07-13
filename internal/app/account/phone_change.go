package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

type reliablePhoneChangeDispatcher interface {
	UsesReliableDispatch() bool
}

func (s *Service) PhoneChangeUsesReliableDispatch() bool {
	if s == nil {
		return false
	}
	reporter, ok := s.phoneChanges.(reliablePhoneChangeDispatcher)
	return ok && reporter.UsesReliableDispatch()
}

// SendChangePhoneCode 创建只允许当前 user + perm auth_key 消费的改号验证码。
// CodeStore 会按 purpose+user+auth_key+phone 原子轮换：同一作用域的新请求
// 立即使旧 hash 失效，避免 Android 返回重进页面时留下并行有效验证码。
// SessionID 被记录用于审计，但验证时不要求相等：同一设备在等待短信期间发生
// MTProto session 重建仍可完成流程；其它设备因 auth_key 不同无法复用。
func (s *Service) SendChangePhoneCode(ctx context.Context, userID int64, authKeyID [8]byte, sessionID int64, phone string) (string, domain.AuthCodeDelivery, error) {
	phone = domain.NormalizePhone(phone)
	if !domain.ValidPhone(phone) {
		return "", domain.AuthCodeDelivery{}, domain.ErrPhoneNumberInvalid
	}
	if _, err := s.phoneChangeCaller(ctx, userID, authKeyID); err != nil {
		return "", domain.AuthCodeDelivery{}, err
	}
	if existing, found, err := s.users.ByPhone(ctx, phone); err != nil {
		return "", domain.AuthCodeDelivery{}, err
	} else if found && existing.ID != 0 {
		return "", domain.AuthCodeDelivery{}, domain.ErrPhoneNumberOccupied
	}
	if s.codes == nil {
		return "", domain.AuthCodeDelivery{}, fmt.Errorf("phone change code service is not configured")
	}
	// Email-as-identity mode: this server has no real SMS delivery at all, so
	// letting an account switch to an arbitrary non-encoded number would just
	// hand out the universal dev code with no real verification. Only numbers
	// that decode back to an email are accepted; the code goes to that inbox.
	if s.emailSignupEnabled {
		return s.sendChangePhoneCodeByEmail(ctx, userID, authKeyID, sessionID, phone)
	}
	if strings.TrimSpace(s.phoneChangeCode) == "" {
		return "", domain.AuthCodeDelivery{}, fmt.Errorf("phone change code service is not configured")
	}
	hash, err := phoneChangeHash()
	if err != nil {
		return "", domain.AuthCodeDelivery{}, err
	}
	rec := store.PhoneCode{
		Version:     store.PhoneCodeVersionCurrent,
		Phone:       phone,
		Code:        s.phoneChangeCode,
		Channel:     store.PhoneCodeChannelPhone,
		Purpose:     store.PhoneCodePurposeChangePhone,
		UserID:      userID,
		AuthKeyID:   authKeyID,
		SessionID:   sessionID,
		MaxAttempts: s.phoneChangeMaxAttempts,
	}
	if err := s.codes.Set(ctx, hash, rec, s.phoneChangeCodeTTL); err != nil {
		return "", domain.AuthCodeDelivery{}, fmt.Errorf("store phone change code: %w", err)
	}
	return hash, domain.AuthCodeDelivery{Kind: domain.AuthCodeDeliverySMS, Length: len(rec.Code)}, nil
}

func (s *Service) sendChangePhoneCodeByEmail(ctx context.Context, userID int64, authKeyID [8]byte, sessionID int64, phone string) (string, domain.AuthCodeDelivery, error) {
	email, ok := domain.DecodeEmailPhone(phone)
	if !ok {
		return "", domain.AuthCodeDelivery{}, domain.ErrPhoneNumberInvalid
	}
	// The wire value never equals any stored users.phone once assigned (see
	// ChangePhone), so the generic ByPhone occupied check above is a no-op
	// for this path; the real "is this email already someone else's
	// account" guard is by SignupEmail instead.
	if existing, found, err := s.users.ByEmail(ctx, email); err != nil {
		return "", domain.AuthCodeDelivery{}, err
	} else if found && existing.ID != userID {
		return "", domain.AuthCodeDelivery{}, domain.ErrPhoneNumberOccupied
	}
	if s.loginEmailSender == nil {
		return "", domain.AuthCodeDelivery{}, fmt.Errorf("email signup sender is not configured")
	}
	code, err := randomDigits(6)
	if err != nil {
		return "", domain.AuthCodeDelivery{}, err
	}
	hash, err := phoneChangeHash()
	if err != nil {
		return "", domain.AuthCodeDelivery{}, err
	}
	ttl := s.phoneChangeCodeTTL
	rec := store.PhoneCode{
		Version:     store.PhoneCodeVersionCurrent,
		Phone:       phone,
		Code:        code,
		Channel:     store.PhoneCodeChannelEmailLogin,
		Purpose:     store.PhoneCodePurposeChangePhone,
		Email:       email,
		UserID:      userID,
		AuthKeyID:   authKeyID,
		SessionID:   sessionID,
		MaxAttempts: s.phoneChangeMaxAttempts,
	}
	if err := s.codes.Set(ctx, hash, rec, ttl); err != nil {
		return "", domain.AuthCodeDelivery{}, fmt.Errorf("store phone change code: %w", err)
	}
	if err := s.loginEmailSender.SendLoginCode(ctx, email, code, ttl); err != nil {
		return "", domain.AuthCodeDelivery{}, fmt.Errorf("send phone change email code: %w", err)
	}
	return hash, domain.AuthCodeDelivery{Kind: domain.AuthCodeDeliveryEmail, EmailPattern: emailPattern(email), Length: len(code)}, nil
}

// ChangePhone 验证作用域和验证码后执行原子改号。返回事件用于当前 session 的
// pts 簿记；其它 session 由 transactional outbox 投递 updateUserPhone。
func (s *Service) ChangePhone(ctx context.Context, userID int64, authKeyID, originRawAuthKeyID [8]byte, sessionID int64, phone, phoneCodeHash, code string, date int) (domain.PhoneChangeResult, error) {
	if strings.TrimSpace(phoneCodeHash) == "" || strings.TrimSpace(code) == "" {
		return domain.PhoneChangeResult{}, domain.ErrPhoneCodeEmpty
	}
	phone = domain.NormalizePhone(phone)
	if !domain.ValidPhone(phone) {
		return domain.PhoneChangeResult{}, domain.ErrPhoneNumberInvalid
	}
	if _, err := s.phoneChangeCaller(ctx, userID, authKeyID); err != nil {
		return domain.PhoneChangeResult{}, err
	}
	if s.codes == nil || s.phoneChanges == nil {
		return domain.PhoneChangeResult{}, fmt.Errorf("phone change service is not configured")
	}
	scope := store.PhoneCodeScope{
		Purpose:   store.PhoneCodePurposeChangePhone,
		UserID:    userID,
		AuthKeyID: authKeyID,
		Phone:     phone,
	}
	verified, err := s.codes.VerifyScoped(ctx, phoneCodeHash, scope, strings.TrimSpace(code), s.phoneChangeMaxAttempts)
	if err != nil {
		return domain.PhoneChangeResult{}, err
	}
	switch verified.Status {
	case store.LoginCodeVerifyMissing:
		return domain.PhoneChangeResult{}, domain.ErrPhoneCodeExpired
	case store.LoginCodeVerifyInvalid:
		return domain.PhoneChangeResult{}, domain.ErrPhoneCodeInvalid
	case store.LoginCodeVerifyAccepted:
	default:
		return domain.PhoneChangeResult{}, domain.ErrPhoneCodeInvalid
	}
	if existing, occupied, err := s.users.ByPhone(ctx, phone); err != nil {
		return domain.PhoneChangeResult{}, err
	} else if occupied && existing.ID != userID {
		return domain.PhoneChangeResult{}, domain.ErrPhoneNumberOccupied
	}
	consumed := verified.Record
	channelOK := consumed.Channel == store.PhoneCodeChannelPhone || consumed.Channel == store.PhoneCodeChannelEmailLogin
	if consumed.Version != store.PhoneCodeVersionCurrent || consumed.Scope() != scope || !channelOK {
		return domain.PhoneChangeResult{}, domain.ErrPhoneCodeInvalid
	}
	if date == 0 {
		date = int(time.Now().Unix())
	}
	req := domain.PhoneChangeRequest{
		UserID: userID,
		Phone:  phone,
		Date:   date,
		// Authorization/code scope is the stable business (perm) key, while dispatch exclusion
		// must use the physical raw key.  They differ on PFS/temp connections; conflating them
		// echoes updateUserPhone back to the initiating device and suppresses the wrong session.
		ExcludeAuthKeyID: originRawAuthKeyID,
		ExcludeSessionID: sessionID,
	}
	// Rebinding to a different email: the account keeps its short "888"
	// display number (or gets a fresh one), never the long wire value —
	// same reasoning as SignUp. ByPhone above is a no-op for this case since
	// the wire value never equals a stored users.phone; the real
	// already-bound-elsewhere guard is by SignupEmail.
	if s.emailSignupEnabled && domain.IsEmailSignupPhone(phone) {
		email, ok := domain.DecodeEmailPhone(phone)
		if !ok {
			return domain.PhoneChangeResult{}, domain.ErrPhoneNumberInvalid
		}
		email = domain.NormalizeEmailForPhone(email)
		if existing, found, err := s.users.ByEmail(ctx, email); err != nil {
			return domain.PhoneChangeResult{}, err
		} else if found && existing.ID != userID {
			return domain.PhoneChangeResult{}, domain.ErrPhoneNumberOccupied
		}
		displayPhone, err := s.assignEmailSignupDisplayPhone(ctx)
		if err != nil {
			return domain.PhoneChangeResult{}, err
		}
		req.Phone = displayPhone
		req.SignupEmail = email
	}
	result, err := s.phoneChanges.ChangePhone(ctx, req)
	if err != nil {
		return domain.PhoneChangeResult{}, err
	}
	if s.userCache != nil && result.User.ID != 0 {
		_ = s.userCache.Delete(ctx, []int64{result.User.ID})
	}
	return result, nil
}

func (s *Service) phoneChangeCaller(ctx context.Context, userID int64, authKeyID [8]byte) (domain.User, error) {
	if s == nil || s.users == nil || s.authorizations == nil || userID == 0 || authKeyID == ([8]byte{}) {
		return domain.User{}, domain.ErrPhoneChangeAuthInvalid
	}
	a, found, err := s.authorizations.ByAuthKey(ctx, authKeyID)
	if err != nil {
		return domain.User{}, err
	}
	if !found || a.UserID != userID || a.PasswordPending {
		return domain.User{}, domain.ErrPhoneChangeAuthInvalid
	}
	u, found, err := s.users.ByID(ctx, userID)
	if err != nil {
		return domain.User{}, err
	}
	if !found {
		return domain.User{}, domain.ErrPhoneChangeAuthInvalid
	}
	if u.Bot || domain.IsSystemUserID(u.ID) {
		return domain.User{}, domain.ErrPhoneChangeForbidden
	}
	return u, nil
}

// maxEmailSignupPhoneAttempts bounds assignEmailSignupDisplayPhone's
// collision-retry loop so a store failure can't spin forever.
const maxEmailSignupPhoneAttempts = 20

// assignEmailSignupDisplayPhone generates a short "888" display number for
// an email-signup account rebinding to a new email (see
// domain.NewEmailSignupDisplayPhone / auth.Service's SignUp counterpart),
// re-rolling on the astronomically unlikely collision with an existing
// account's phone.
func (s *Service) assignEmailSignupDisplayPhone(ctx context.Context) (string, error) {
	for i := 0; i < maxEmailSignupPhoneAttempts; i++ {
		candidate, err := domain.NewEmailSignupDisplayPhone()
		if err != nil {
			return "", err
		}
		if _, found, err := s.users.ByPhone(ctx, candidate); err != nil {
			return "", err
		} else if !found {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("assign email signup display phone: exhausted %d attempts", maxEmailSignupPhoneAttempts)
}

func phoneChangeHash() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate phone change hash: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
