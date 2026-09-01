package auth

import (
	"context"
	"strings"
	"testing"

	"telesrv/internal/domain"
	"telesrv/internal/identity"
	"telesrv/internal/store/memory"
)

// TestLoginCodeMessageTemplateResolutionPrecedence exercises
// WithLoginCodeMessageTemplate/resolveLoginCodeMessageTemplate's precedence
// (panel override > env default > compiled-in default) end to end through
// SignUp's bootstrap recordLoginMessage path (phone channel, no owner/dialog
// yet -- see service.go's rec.Channel == codeChannelPhone branch), mirroring
// how welcome_message_test.go exercises WithLoginWelcomeMessages.
func TestLoginCodeMessageTemplateResolutionPrecedence(t *testing.T) {
	ctx := context.Background()

	newSvc := func(store *identity.Store, envDefault string) (*Service, *memory.MessageStore) {
		dialogs := memory.NewDialogStore()
		messages := memory.NewMessageStore(dialogs)
		return NewService(memory.NewUserStore(), memory.NewAuthorizationStore(), memory.NewCodeStore(), nil, nil, "12345",
			WithLoginMessages(messages, dialogs),
			WithLoginCodeMessageTemplate(store, envDefault),
		), messages
	}

	// No override, no env default: falls back to the compiled-in default.
	t.Run("compiled-in default", func(t *testing.T) {
		svc, messages := newSvc(nil, "")
		u := signUpPhoneForLoginCodeMessage(t, ctx, svc, "+15550771001")
		body := codeMessageBody(t, ctx, messages, u.ID)
		if !strings.Contains(body, "Login code: 12345") {
			t.Fatalf("body = %q, want compiled-in default rendering", body)
		}
	})

	// Env default set, no panel override: env default wins.
	t.Run("env default", func(t *testing.T) {
		svc, messages := newSvc(nil, "Env says your code is {{code}}.")
		u := signUpPhoneForLoginCodeMessage(t, ctx, svc, "+15550771002")
		body := codeMessageBody(t, ctx, messages, u.ID)
		if body != "Env says your code is 12345." {
			t.Fatalf("body = %q, want env-default rendering", body)
		}
	})

	// Panel override set: wins over both the env default and the compiled-in
	// default, and is read fresh (not cached) -- see resolveLoginCodeMessageTemplate.
	t.Run("panel override wins and is read fresh", func(t *testing.T) {
		store := identity.NewStore(t.TempDir())
		svc, messages := newSvc(store, "Env says your code is {{code}}.")

		u := signUpPhoneForLoginCodeMessage(t, ctx, svc, "+15550771003")
		body := codeMessageBody(t, ctx, messages, u.ID)
		if body != "Env says your code is 12345." {
			t.Fatalf("body before override = %q, want env-default rendering", body)
		}

		if err := store.SetLoginCodeMessageTemplate("Panel says your code is {{code}}."); err != nil {
			t.Fatalf("SetLoginCodeMessageTemplate: %v", err)
		}
		u2 := signUpPhoneForLoginCodeMessage(t, ctx, svc, "+15550771004")
		body2 := codeMessageBody(t, ctx, messages, u2.ID)
		if body2 != "Panel says your code is 12345." {
			t.Fatalf("body after override = %q, want panel-override rendering with no restart", body2)
		}
	})

	// A saved-but-somehow-invalid panel override (missing {{code}} --
	// bypassing the admin-API validation, e.g. a hand-edited identity.json)
	// must never reach a client with the code silently missing: defense in
	// depth falls back to the compiled-in default instead.
	t.Run("invalid panel override falls back safely", func(t *testing.T) {
		store := identity.NewStore(t.TempDir())
		if err := store.SetLoginCodeMessageTemplate("no placeholder here"); err != nil {
			t.Fatalf("SetLoginCodeMessageTemplate: %v", err)
		}
		svc, messages := newSvc(store, "")
		u := signUpPhoneForLoginCodeMessage(t, ctx, svc, "+15550771005")
		body := codeMessageBody(t, ctx, messages, u.ID)
		if !strings.Contains(body, "12345") {
			t.Fatalf("body = %q, want the code delivered via fallback despite invalid override", body)
		}
	})
}

func signUpPhoneForLoginCodeMessage(t *testing.T, ctx context.Context, svc *Service, phone string) domain.User {
	t.Helper()
	hash, err := svc.SendCode(ctx, phone)
	if err != nil {
		t.Fatalf("SendCode: %v", err)
	}
	verifyCodeForSignUp(t, svc, phone, hash, "12345")
	u, _, err := svc.SignUp(ctx, domain.Authorization{}, phone, hash, "Code", "Template")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	return u
}

// codeMessageBody finds the login-code delivery message among the user's
// full message history (not the dialog summary, which only tracks each
// peer's single top message -- SignUp's welcome message overwrites the
// 777000 dialog's top message right after the login-code one is written).
func codeMessageBody(t *testing.T, ctx context.Context, messages *memory.MessageStore, userID int64) string {
	t.Helper()
	list, err := messages.ListByUser(ctx, userID, domain.MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	for _, msg := range list.Messages {
		if strings.Contains(msg.Body, "12345") {
			return msg.Body
		}
	}
	t.Fatalf("no login-code message (containing 12345) found among %d messages for user %d", len(list.Messages), userID)
	return ""
}
