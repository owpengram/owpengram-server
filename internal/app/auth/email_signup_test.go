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

	// SignUp itself is completely untouched by email-signup: same call, same
	// phone (the 888-encoded value), no email-specific parameter anywhere.
	created, _, err := svc.SignUp(ctx, domain.Authorization{}, phone, hash, "New", "User")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	if created.Phone != phone {
		t.Fatalf("created.Phone = %q, want %q", created.Phone, phone)
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
