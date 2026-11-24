package auth

import (
	"context"
	"errors"
	"testing"

	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

type fakeFreezes struct{ frozen map[int64]bool }

func (f *fakeFreezes) AccountFreeze(_ context.Context, userID int64) (domain.AccountFreeze, bool, error) {
	if f.frozen[userID] {
		return domain.AccountFreeze{UserID: userID, Frozen: true}, true, nil
	}
	return domain.AccountFreeze{}, false, nil
}

func TestFrozenAccountCannotLogIn(t *testing.T) {
	ctx := context.Background()
	freezes := &fakeFreezes{frozen: map[int64]bool{}}
	dialogs := memory.NewDialogStore()
	messages := memory.NewMessageStore(dialogs)
	delivery := memory.NewLoginCodeDeliveryStore(messages, memory.NewUpdateEventStore())
	svc := NewService(memory.NewUserStore(), memory.NewAuthorizationStore(), memory.NewCodeStore(), nil, nil, "12345",
		WithLoginCodeDelivery(delivery),
		WithFrozenLoginBlock(freezes))
	const phone = "+15550009001"

	hash, err := svc.SendCode(ctx, phone)
	if err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	verifyCodeForSignUp(t, svc, phone, hash, "12345")
	u, _, err := svc.SignUp(ctx, domain.Authorization{AuthKeyID: [8]byte{1}}, phone, hash, "Frozen", "")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	// A code issued before the freeze must not complete a login after it.
	hash2, err := svc.SendCode(ctx, phone)
	if err != nil {
		t.Fatalf("SendCode before freeze: %v", err)
	}
	freezes.frozen[u.ID] = true
	if _, _, _, err := svc.SignIn(ctx, domain.Authorization{AuthKeyID: [8]byte{2}}, phone, hash2, "12345"); !errors.Is(err, ErrAccountBanned) {
		t.Fatalf("SignIn of frozen account err = %v, want ErrAccountBanned", err)
	}
	if _, found, _ := svc.UserID(ctx, [8]byte{2}); found {
		t.Fatal("frozen account got a new authorization")
	}
	// No new login code is issued at all.
	if _, err := svc.SendCode(ctx, phone); !errors.Is(err, ErrAccountBanned) {
		t.Fatalf("SendCode for frozen account err = %v, want ErrAccountBanned", err)
	}

	// Unfreeze restores login.
	delete(freezes.frozen, u.ID)
	hash3, err := svc.SendCode(ctx, phone)
	if err != nil {
		t.Fatalf("SendCode after unfreeze: %v", err)
	}
	if _, _, _, err := svc.SignIn(ctx, domain.Authorization{AuthKeyID: [8]byte{3}}, phone, hash3, "12345"); err != nil {
		t.Fatalf("SignIn after unfreeze: %v", err)
	}
}

// Production signs up and logs in by email (the wire "phone" is an encoded
// email); a frozen account must be refused there too, both before a code is
// mailed and with a code mailed before the freeze.
func TestFrozenEmailAccountCannotLogIn(t *testing.T) {
	ctx := context.Background()
	freezes := &fakeFreezes{frozen: map[int64]bool{}}
	sender := &testMailSender{}
	dialogs := memory.NewDialogStore()
	delivery := memory.NewLoginCodeDeliveryStore(memory.NewMessageStore(dialogs), memory.NewUpdateEventStore())
	svc := NewService(memory.NewUserStore(), memory.NewAuthorizationStore(), memory.NewCodeStore(), nil, nil, "12345",
		WithLoginCodeDelivery(delivery),
		WithLoginEmail(LoginEmailOptions{Sender: sender, Enabled: true}),
		WithEmailSignup(true),
		WithFrozenLoginBlock(freezes))
	phone, ok := domain.EncodeEmailPhone("frozen@owpengram.local")
	if !ok {
		t.Fatal("EncodeEmailPhone failed")
	}

	hash, err := svc.SendCode(ctx, phone)
	if err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	if _, _, _, err := svc.SignInWithEmail(ctx, domain.Authorization{}, phone, hash, sender.code); err != nil {
		t.Fatalf("SignInWithEmail: %v", err)
	}
	u, _, err := svc.SignUp(ctx, domain.Authorization{AuthKeyID: [8]byte{1}}, phone, hash, "Frozen", "")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	hash2, err := svc.SendCode(ctx, phone)
	if err != nil {
		t.Fatalf("SendCode before freeze: %v", err)
	}
	freezes.frozen[u.ID] = true
	if _, _, _, err := svc.SignInWithEmail(ctx, domain.Authorization{AuthKeyID: [8]byte{2}}, phone, hash2, sender.code); !errors.Is(err, ErrAccountBanned) {
		t.Fatalf("SignInWithEmail of frozen account err = %v, want ErrAccountBanned", err)
	}
	if _, err := svc.SendCode(ctx, phone); !errors.Is(err, ErrAccountBanned) {
		t.Fatalf("SendCode for frozen email account err = %v, want ErrAccountBanned", err)
	}
}
