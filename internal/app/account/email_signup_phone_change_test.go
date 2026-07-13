package account

import (
	"context"
	"testing"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/memory"
)

func newEmailSignupPhoneChangeFixture(t *testing.T) (phoneChangeFixture, *captureMailSender) {
	t.Helper()
	ctx := context.Background()
	users := memory.NewUserStore()
	auths := memory.NewAuthorizationStore()
	codes := memory.NewCodeStore()
	events := memory.NewUpdateEventStore()
	phone, ok := domain.EncodeEmailPhone("alice@owpengram.local")
	if !ok {
		t.Fatalf("EncodeEmailPhone: ok=false")
	}
	u, err := users.Create(ctx, domain.User{AccessHash: 101, Phone: phone, FirstName: "Alice"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	authKeyID := [8]byte{1, 2, 3, 4}
	if err := auths.Bind(ctx, domain.Authorization{AuthKeyID: authKeyID, UserID: u.ID, CreatedAt: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatalf("bind auth: %v", err)
	}
	changes := &recordingPhoneChangeStore{inner: memory.NewPhoneChangeStore(users, events)}
	sender := &captureMailSender{}
	service := NewService(
		memory.NewPasswordStore(),
		WithUsers(users),
		WithPhoneChange(changes, auths, codes, nil, "12345", time.Minute, 3),
		WithLoginEmailVerification(codes, sender, time.Minute, 3, 6),
		WithEmailSignup(true),
	)
	return phoneChangeFixture{ctx: ctx, service: service, users: users, auths: auths, codes: codes, events: events, user: u, authKeyID: authKeyID, changes: changes}, sender
}

func TestEmailSignupChangePhoneRoutesCodeToDecodedEmail(t *testing.T) {
	f, sender := newEmailSignupPhoneChangeFixture(t)
	newPhone, ok := domain.EncodeEmailPhone("newmail@owpengram.local")
	if !ok {
		t.Fatalf("EncodeEmailPhone: ok=false")
	}

	hash, delivery, err := f.service.SendChangePhoneCode(f.ctx, f.user.ID, f.authKeyID, 77, newPhone)
	if err != nil {
		t.Fatalf("SendChangePhoneCode: %v", err)
	}
	if delivery.Kind != domain.AuthCodeDeliveryEmail {
		t.Fatalf("delivery.Kind = %v, want AuthCodeDeliveryEmail", delivery.Kind)
	}
	if sender.to != "newmail@owpengram.local" {
		t.Fatalf("sender.to = %q, want newmail@owpengram.local", sender.to)
	}
	if len(sender.code) != 6 {
		t.Fatalf("sender.code = %q, want 6 digits", sender.code)
	}

	rec, found, err := f.codes.Get(f.ctx, hash)
	if err != nil || !found {
		t.Fatalf("load code found=%v err=%v", found, err)
	}
	if rec.Channel != store.PhoneCodeChannelEmailLogin || rec.Email != "newmail@owpengram.local" {
		t.Fatalf("stored code = %+v", rec)
	}

	rawAuthKeyID := [8]byte{8, 8, 8, 8}
	result, err := f.service.ChangePhone(f.ctx, f.user.ID, f.authKeyID, rawAuthKeyID, 88, newPhone, hash, sender.code, 1700000000)
	if err != nil {
		t.Fatalf("ChangePhone: %v", err)
	}
	if result.User.Phone != newPhone {
		t.Fatalf("result.User.Phone = %q, want %q", result.User.Phone, newPhone)
	}
}

func TestEmailSignupChangePhoneRejectsRealPhoneNumber(t *testing.T) {
	f, sender := newEmailSignupPhoneChangeFixture(t)

	_, _, err := f.service.SendChangePhoneCode(f.ctx, f.user.ID, f.authKeyID, 77, "+15550019999")
	if err != domain.ErrPhoneNumberInvalid {
		t.Fatalf("SendChangePhoneCode err = %v, want ErrPhoneNumberInvalid", err)
	}
	if sender.to != "" {
		t.Fatalf("sender.to = %q, want empty (no email should have been sent)", sender.to)
	}
}
