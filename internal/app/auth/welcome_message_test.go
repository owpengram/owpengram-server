package auth

import (
	"context"
	"strings"
	"testing"

	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

func TestEmailSignupSignUpWritesWelcomeMessageMentioningEmail(t *testing.T) {
	ctx := context.Background()
	dialogs := memory.NewDialogStore()
	messages := memory.NewMessageStore(dialogs)
	sender := &testMailSender{}
	svc := NewService(memory.NewUserStore(), memory.NewAuthorizationStore(), memory.NewCodeStore(), nil, nil, "12345",
		WithLoginMessages(messages, dialogs),
		WithLoginEmail(LoginEmailOptions{Sender: sender}),
		WithEmailSignup(true))

	phone, ok := domain.EncodeEmailPhone("welcome@owpengram.local")
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
	u, _, err := svc.SignUp(ctx, domain.Authorization{}, phone, hash, "Welcome", "User")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	list, err := dialogs.ListByUser(ctx, u.ID, domain.DialogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list.Messages) != 1 {
		t.Fatalf("messages = %+v, want exactly the welcome message (email channel skips the code-echo message)", list.Messages)
	}
	if !strings.Contains(list.Messages[0].Body, "Welcome to OwpenGram") || !strings.Contains(list.Messages[0].Body, "via email") {
		t.Fatalf("welcome message body = %q, want greeting mentioning email", list.Messages[0].Body)
	}
}

func TestSignInWritesWelcomeMessageOnEveryLogin(t *testing.T) {
	ctx := context.Background()
	dialogs := memory.NewDialogStore()
	messages := memory.NewMessageStore(dialogs)
	delivery := memory.NewLoginCodeDeliveryStore(messages, memory.NewUpdateEventStore())
	svc := NewService(memory.NewUserStore(), memory.NewAuthorizationStore(), memory.NewCodeStore(), nil, nil, "12345",
		WithLoginMessages(messages, dialogs),
		WithLoginCodeDelivery(delivery),
	)
	var key [8]byte
	key[0] = 42

	hash, err := svc.SendCode(ctx, "+15550009911")
	if err != nil {
		t.Fatalf("SendCode signup: %v", err)
	}
	verifyCodeForSignUp(t, svc, "+15550009911", hash, "12345")
	u, _, err := svc.SignUp(ctx, domain.Authorization{AuthKeyID: key}, "+15550009911", hash, "Repeat", "Login")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	if err := svc.LogOut(ctx, key); err != nil {
		t.Fatalf("LogOut: %v", err)
	}

	// A second, independent login (different device/session) must also get a
	// fresh welcome message, not just the original SignUp.
	hash, err = svc.SendCode(ctx, "+15550009911")
	if err != nil {
		t.Fatalf("SendCode signin: %v", err)
	}
	var key2 [8]byte
	key2[0] = 43
	if _, _, _, err := svc.SignIn(ctx, domain.Authorization{AuthKeyID: key2}, "+15550009911", hash, "12345"); err != nil {
		t.Fatalf("SignIn: %v", err)
	}

	list, err := dialogs.ListByUser(ctx, u.ID, domain.DialogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list.Messages) != 1 || !strings.Contains(list.Messages[0].Body, "Welcome to OwpenGram") {
		t.Fatalf("messages = %+v, want the second sign-in's fresh welcome message as new top message", list.Messages)
	}
	// SignUp's welcome + login-code message, plus SendCode's re-delivered code
	// message, plus the second sign-in's welcome message.
	if list.Dialogs[0].UnreadCount < 3 {
		t.Fatalf("dialog unread = %+v, want at least 3 accumulated messages across both logins", list.Dialogs[0])
	}
}

func TestTwoFactorSignInDefersWelcomeMessageUntilPasswordCompletes(t *testing.T) {
	ctx := context.Background()
	passwords := memory.NewPasswordStore()
	dialogs := memory.NewDialogStore()
	messages := memory.NewMessageStore(dialogs)
	delivery := memory.NewLoginCodeDeliveryStore(messages, memory.NewUpdateEventStore())
	svc := NewService(memory.NewUserStore(), memory.NewAuthorizationStore(), memory.NewCodeStore(), nil, nil, "12345",
		WithPasswords(passwords),
		WithLoginMessages(messages, dialogs),
		WithLoginCodeDelivery(delivery),
	)
	var key [8]byte
	key[0] = 9

	hash, err := svc.SendCode(ctx, "+15550009922")
	if err != nil {
		t.Fatalf("SendCode signup: %v", err)
	}
	verifyCodeForSignUp(t, svc, "+15550009922", hash, "12345")
	u, _, err := svc.SignUp(ctx, domain.Authorization{AuthKeyID: key}, "+15550009922", hash, "Two", "Factor")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	if err := svc.LogOut(ctx, key); err != nil {
		t.Fatalf("LogOut: %v", err)
	}
	if err := passwords.Save(ctx, u.ID, domain.PasswordSettings{HasPassword: true}); err != nil {
		t.Fatalf("save password settings: %v", err)
	}

	afterSignUp, err := dialogs.ListByUser(ctx, u.ID, domain.DialogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListByUser after signup: %v", err)
	}
	unreadAfterSignUp := afterSignUp.Dialogs[0].UnreadCount

	hash, err = svc.SendCode(ctx, "+15550009922")
	if err != nil {
		t.Fatalf("SendCode signin: %v", err)
	}
	if _, _, _, err := svc.SignIn(ctx, domain.Authorization{AuthKeyID: key}, "+15550009922", hash, "12345"); err == nil {
		t.Fatalf("SignIn err = nil, want ErrSessionPasswordNeeded")
	}

	// Still pending 2FA: no welcome message yet, only the re-delivered code.
	pending, err := dialogs.ListByUser(ctx, u.ID, domain.DialogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListByUser pending: %v", err)
	}
	if strings.Contains(pending.Messages[0].Body, "Welcome to OwpenGram") {
		t.Fatalf("welcome message fired before password check completed: %+v", pending.Messages[0])
	}

	if err := svc.CompletePasswordSignIn(ctx, key); err != nil {
		t.Fatalf("CompletePasswordSignIn: %v", err)
	}

	done, err := dialogs.ListByUser(ctx, u.ID, domain.DialogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListByUser done: %v", err)
	}
	if len(done.Messages) != 1 || !strings.Contains(done.Messages[0].Body, "Welcome to OwpenGram") {
		t.Fatalf("messages after CompletePasswordSignIn = %+v, want fresh welcome message", done.Messages)
	}
	if done.Dialogs[0].UnreadCount <= unreadAfterSignUp {
		t.Fatalf("unread did not grow after CompletePasswordSignIn: before=%d after=%d", unreadAfterSignUp, done.Dialogs[0].UnreadCount)
	}
}
