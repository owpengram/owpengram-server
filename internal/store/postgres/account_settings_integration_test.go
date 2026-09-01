package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

// TestAccountSettingsRoundTripPostgres 回归迁移 0004：账号级单例设置真实持久化
// （globalPrivacy/accountTTL/sensitive/contactSignUp），含 not-found→默认、upsert 覆盖。
func TestAccountSettingsRoundTripPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewPasswordStore(pool)

	users := NewUserStore(pool)
	suffix := randomSuffix(t)
	u, err := users.Create(ctx, domain.User{AccessHash: 91, Phone: "+1664" + suffix + "01", FirstName: "AcctSettings"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM account_settings WHERE user_id = $1", u.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", u.ID)
	})

	// 未持久化：not found。
	if _, found, err := store.GetAccountSettings(ctx, u.ID); err != nil || found {
		t.Fatalf("get before save = found %v err %v, want not found", found, err)
	}

	want := domain.AccountSettings{
		GlobalPrivacy: domain.GlobalPrivacy{
			ArchiveAndMuteNewNoncontactPeers: true,
			HideReadMarks:                    true,
			DisplayGiftsButton:               true,
			NoncontactPeersPaidStars:         75,
			DisallowedGifts: domain.DisallowedGifts{
				UnlimitedStargifts: true,
				PremiumGifts:       true,
			},
		},
		AccountTTLDays:          30,
		SensitiveContentEnabled: true,
		ContactSignUpSilent:     true,
	}
	if err := store.SaveAccountSettings(ctx, u.ID, want); err != nil {
		t.Fatalf("save account settings: %v", err)
	}
	got, found, err := store.GetAccountSettings(ctx, u.ID)
	if err != nil || !found {
		t.Fatalf("get after save = found %v err %v", found, err)
	}
	if got != want {
		t.Fatalf("account settings round-trip = %+v, want %+v", got, want)
	}

	// upsert 覆盖：改若干字段再读回。
	want.GlobalPrivacy.HideReadMarks = false
	want.AccountTTLDays = 365
	want.SensitiveContentEnabled = false
	if err := store.SaveAccountSettings(ctx, u.ID, want); err != nil {
		t.Fatalf("re-save account settings: %v", err)
	}
	got, found, err = store.GetAccountSettings(ctx, u.ID)
	if err != nil || !found {
		t.Fatalf("get after re-save = found %v err %v", found, err)
	}
	if got != want {
		t.Fatalf("account settings after upsert = %+v, want %+v", got, want)
	}
}
