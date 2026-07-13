package auth

import (
	"context"
	"testing"

	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

func TestEmailSignupSendCodeRoutesFreshSignupToEmail(t *testing.T) {
	ctx := context.Background()
	users := memory.NewUserStore()
	authz := memory.NewAuthorizationStore()
	sender := &testMailSender{}
	svc := NewService(users, authz, memory.NewCodeStore(), nil, nil, "12345",
		WithLoginEmail(LoginEmailOptions{Sender: sender}),
		WithEmailSignup(true))

	phone, ok := domain.EncodeEmailPhone("newuser@owpengram.local")
	if !ok {
		t.Fatalf("EncodeEmailPhone: ok=false")
	}

	hash, err := svc.SendCode(ctx, phone)
	if err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	if sender.to != "newuser@owpengram.local" {
		t.Fatalf("sender.to = %q, want newuser@owpengram.local", sender.to)
	}
	delivery, found, err := svc.CodeDelivery(ctx, hash)
	if err != nil || !found {
		t.Fatalf("CodeDelivery found=%v err=%v", found, err)
	}
	if delivery.Kind != domain.AuthCodeDeliveryEmail {
		t.Fatalf("delivery.Kind = %v, want AuthCodeDeliveryEmail", delivery.Kind)
	}

	// The email-signup path must reuse the stock auth.signIn/SignInWithEmail
	// flow unchanged: a fresh (never-registered) 888 phone reports needSignUp.
	_, _, needSignUp, err := svc.SignInWithEmail(ctx, domain.Authorization{}, phone, hash, sender.code)
	if err != nil {
		t.Fatalf("SignInWithEmail: %v", err)
	}
	if !needSignUp {
		t.Fatalf("needSignUp = false, want true for a brand-new email-signup account")
	}

	// SignUp itself is called exactly like the plain-phone path (same phone
	// argument, no email-specific parameter anywhere), but the account it
	// creates gets a short, normal-looking "888" display number instead of
	// storing the long email-encoded wire value as its permanent phone; the
	// email->user association lives in SignupEmail instead.
	created, _, err := svc.SignUp(ctx, domain.Authorization{}, phone, hash, "New", "User")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	if created.Phone == phone || !domain.ValidPhone(created.Phone) || domain.IsEmailSignupPhone(created.Phone) {
		t.Fatalf("created.Phone = %q, want a short all-digit 888 display number distinct from the wire value %q", created.Phone, phone)
	}
	if created.SignupEmail != "newuser@owpengram.local" {
		t.Fatalf("created.SignupEmail = %q, want newuser@owpengram.local", created.SignupEmail)
	}
}

// Regression test: TELESRV_LOGIN_EMAIL_REQUIRE_SETUP=true (a real deployment
// combo, not just EMAIL_SIGNUP_ENABLE alone) used to permanently reject
// SignUp for every email-signup account with ErrCodeInvalid, because SignUp's
// "must have a verified/pending login email" gate only recognized the legacy
// VerifiedEmail/PendingEmail fields, which the email-signup path never sets.
func TestEmailSignupSignUpSucceedsWithLoginEmailRequireSetupAlsoOn(t *testing.T) {
	ctx := context.Background()
	users := memory.NewUserStore()
	authz := memory.NewAuthorizationStore()
	sender := &testMailSender{}
	svc := NewService(users, authz, memory.NewCodeStore(), nil, nil, "12345",
		WithLoginEmail(LoginEmailOptions{Sender: sender, RequireSetup: true, Enabled: true}),
		WithEmailSignup(true))

	phone, ok := domain.EncodeEmailPhone("requiresetup@owpengram.local")
	if !ok {
		t.Fatalf("EncodeEmailPhone: ok=false")
	}

	hash, err := svc.SendCode(ctx, phone)
	if err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	if _, _, needSignUp, err := svc.SignInWithEmail(ctx, domain.Authorization{}, phone, hash, sender.code); err != nil || !needSignUp {
		t.Fatalf("SignInWithEmail: needSignUp=%v err=%v", needSignUp, err)
	}
	created, _, err := svc.SignUp(ctx, domain.Authorization{}, phone, hash, "Needs", "Setup")
	if err != nil {
		t.Fatalf("SignUp: %v (this is the loop bug if it fails with ErrCodeInvalid)", err)
	}
	if created.Phone == phone || !domain.ValidPhone(created.Phone) || domain.IsEmailSignupPhone(created.Phone) {
		t.Fatalf("created.Phone = %q, want a short all-digit 888 display number distinct from the wire value %q", created.Phone, phone)
	}
	if created.SignupEmail != "requiresetup@owpengram.local" {
		t.Fatalf("created.SignupEmail = %q, want requiresetup@owpengram.local", created.SignupEmail)
	}
}

func TestEmailSignupSendCodeIgnoredWhenPhoneIsNotEncoded(t *testing.T) {
	ctx := context.Background()
	users := memory.NewUserStore()
	authz := memory.NewAuthorizationStore()
	sender := &testMailSender{}
	svc := NewService(users, authz, memory.NewCodeStore(), nil, nil, "12345",
		WithLoginEmail(LoginEmailOptions{Sender: sender}),
		WithEmailSignup(true))

	hash, err := svc.SendCode(ctx, "+15550019999")
	if err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	if sender.to != "" {
		t.Fatalf("sender.to = %q, want empty (real phone must fall through to the normal dev-code path)", sender.to)
	}
	delivery, found, err := svc.CodeDelivery(ctx, hash)
	if err != nil || !found {
		t.Fatalf("CodeDelivery found=%v err=%v", found, err)
	}
	if delivery.Kind == domain.AuthCodeDeliveryEmail {
		t.Fatalf("delivery.Kind = Email, want a non-email fallback for a real phone number")
	}
}
