package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

func TestAuthorizationLoginAgeAndPasswordPromotionCAS(t *testing.T) {
	ctx := context.Background()
	auths := NewAuthorizationStore()
	key := [8]byte{0x91}
	old := time.Now().Add(-48 * time.Hour)

	if err := auths.Bind(ctx, domain.Authorization{
		AuthKeyID: key, UserID: 101, CreatedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	first, found, err := auths.ByAuthKey(ctx, key)
	if err != nil || !found || !first.CreatedAt.After(old) {
		t.Fatalf("first login authorization=%+v found=%v err=%v", first, found, err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := auths.Bind(ctx, domain.Authorization{AuthKeyID: key, UserID: 101}); err != nil {
		t.Fatal(err)
	}
	second, _, _ := auths.ByAuthKey(ctx, key)
	if !second.CreatedAt.After(first.CreatedAt) {
		t.Fatalf("same-owner login kept created_at=%v, want newer than %v", second.CreatedAt, first.CreatedAt)
	}

	if err := auths.Bind(ctx, domain.Authorization{
		AuthKeyID: key, UserID: 101, PasswordPending: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := auths.Bind(ctx, domain.Authorization{
		AuthKeyID: key, UserID: 202, PasswordPending: true,
	}); err != nil {
		t.Fatal(err)
	}
	pendingB, _, _ := auths.ByAuthKey(ctx, key)
	if err := auths.MarkPasswordPassed(ctx, key, 101); !errors.Is(err, store.ErrAuthorizationStateChanged) {
		t.Fatalf("stale A proof err=%v, want state changed", err)
	}
	stillPendingB, _, _ := auths.ByAuthKey(ctx, key)
	if stillPendingB.UserID != 202 || !stillPendingB.PasswordPending {
		t.Fatalf("stale A proof changed B authorization: %+v", stillPendingB)
	}
	time.Sleep(2 * time.Millisecond)
	if err := auths.MarkPasswordPassed(ctx, key, 202); err != nil {
		t.Fatalf("promote B: %v", err)
	}
	passedB, _, _ := auths.ByAuthKey(ctx, key)
	if passedB.PasswordPending || !passedB.CreatedAt.After(pendingB.CreatedAt) {
		t.Fatalf("promoted B authorization=%+v, want fresh fully-authorized session", passedB)
	}
}
