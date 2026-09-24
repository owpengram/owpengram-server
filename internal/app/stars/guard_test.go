package stars

import (
	"context"
	"testing"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/memory"
)

// fakeGuardStore wraps the real memory store for everything else, but lets
// tests control DeviceFingerprintGranted's answer and observe
// SkipStartingGrant/its call arguments directly -- a real Postgres join is
// proven separately in internal/store/postgres/stars_integration_test.go.
type fakeGuardStore struct {
	store.StarsStore
	dup            bool
	dupErr         error
	dupCalled      bool
	dupDeviceModel string
	dupIP          string
	skippedUserIDs []int64
	skipErr        error
}

func (f *fakeGuardStore) DeviceFingerprintGranted(_ context.Context, _ int64, deviceModel, _, _, ip string) (bool, error) {
	f.dupCalled = true
	f.dupDeviceModel, f.dupIP = deviceModel, ip
	return f.dup, f.dupErr
}

func (f *fakeGuardStore) SkipStartingGrant(ctx context.Context, userID int64) error {
	f.skippedUserIDs = append(f.skippedUserIDs, userID)
	if f.skipErr != nil {
		return f.skipErr
	}
	// Forward to the embedded memory store so its own granted-flag state
	// (what the lazy EnsureGrant path checks) stays consistent, the same
	// way the real postgres store's single INSERT does both at once.
	return f.StarsStore.SkipStartingGrant(ctx, userID)
}

type fakeAuths struct {
	list []domain.Authorization
	err  error
}

func (f *fakeAuths) ListByUser(context.Context, int64) ([]domain.Authorization, error) {
	return f.list, f.err
}

type fakeAntiAbuseNotifier struct {
	withheld []int64
}

func (f *fakeAntiAbuseNotifier) NotifyStarsGrantWithheld(_ context.Context, userID int64) {
	f.withheld = append(f.withheld, userID)
}

func TestGuardStartingGrantWithholdsOnDuplicateFingerprint(t *testing.T) {
	fs := &fakeGuardStore{StarsStore: memory.NewStarsStore(), dup: true}
	notifier := &fakeAntiAbuseNotifier{}
	svc := NewService(fs, WithStartingGrant(1000))
	svc.SetAntiAbuseNotifier(notifier)

	withheld, err := svc.GuardStartingGrant(context.Background(), 42, "Pixel 8", "Android 15", "android", "203.0.113.9")
	if err != nil || !withheld {
		t.Fatalf("GuardStartingGrant = %v, %v, want true, nil", withheld, err)
	}
	if !fs.dupCalled || fs.dupDeviceModel != "Pixel 8" || fs.dupIP != "203.0.113.9" {
		t.Fatalf("DeviceFingerprintGranted called=%v device=%q ip=%q, want called with the given fingerprint", fs.dupCalled, fs.dupDeviceModel, fs.dupIP)
	}
	if len(fs.skippedUserIDs) != 1 || fs.skippedUserIDs[0] != 42 {
		t.Fatalf("skippedUserIDs = %v, want [42]", fs.skippedUserIDs)
	}
	if len(notifier.withheld) != 1 || notifier.withheld[0] != 42 {
		t.Fatalf("notifier.withheld = %v, want [42]", notifier.withheld)
	}

	// The lazy grant path must now see it as already handled (balance 0).
	bal, err := svc.GetBalance(context.Background(), 42)
	if err != nil || bal.Balance != 0 || !bal.Granted {
		t.Fatalf("balance after withheld grant = %+v err %v, want 0 granted", bal, err)
	}
}

func TestGuardStartingGrantAllowsUnknownFingerprint(t *testing.T) {
	fs := &fakeGuardStore{StarsStore: memory.NewStarsStore(), dup: true}
	svc := NewService(fs, WithStartingGrant(1000))

	withheld, err := svc.GuardStartingGrant(context.Background(), 42, "", "", "", "203.0.113.9")
	if err != nil || withheld {
		t.Fatalf("GuardStartingGrant with empty device = %v, %v, want false, nil", withheld, err)
	}
	if fs.dupCalled {
		t.Fatal("DeviceFingerprintGranted was queried for an unknown (empty-device) fingerprint, want it skipped entirely")
	}
}

func TestGuardStartingGrantDisabled(t *testing.T) {
	fs := &fakeGuardStore{StarsStore: memory.NewStarsStore(), dup: true}
	svc := NewService(fs, WithStartingGrant(1000), WithAntiFarmGuard(false))

	withheld, err := svc.GuardStartingGrant(context.Background(), 42, "Pixel 8", "Android 15", "android", "203.0.113.9")
	if err != nil || withheld {
		t.Fatalf("GuardStartingGrant with guard disabled = %v, %v, want false, nil", withheld, err)
	}
	if fs.dupCalled {
		t.Fatal("DeviceFingerprintGranted was queried with the guard disabled, want it skipped entirely")
	}
}

func TestGuardClaimUsesLatestActiveAuthorization(t *testing.T) {
	fs := &fakeGuardStore{StarsStore: memory.NewStarsStore(), dup: true}
	older := domain.Authorization{DeviceModel: "iPhone 12", SystemVersion: "iOS 17", Platform: "ios", IP: "198.51.100.1", ActiveAt: time.Unix(1000, 0)}
	newer := domain.Authorization{DeviceModel: "Pixel 8", SystemVersion: "Android 15", Platform: "android", IP: "203.0.113.9", ActiveAt: time.Unix(2000, 0)}
	auths := &fakeAuths{list: []domain.Authorization{older, newer}}
	svc := NewService(fs, WithStartingGrant(1000), WithAuthorizations(auths))

	withheld, err := svc.GuardClaim(context.Background(), 42)
	if err != nil || !withheld {
		t.Fatalf("GuardClaim = %v, %v, want true, nil", withheld, err)
	}
	if fs.dupDeviceModel != "Pixel 8" || fs.dupIP != "203.0.113.9" {
		t.Fatalf("guard checked device=%q ip=%q, want the most recently active authorization (Pixel 8 / 203.0.113.9)", fs.dupDeviceModel, fs.dupIP)
	}
}

func TestGuardClaimNoAuthorizationsFailsOpen(t *testing.T) {
	fs := &fakeGuardStore{StarsStore: memory.NewStarsStore(), dup: true}
	svc := NewService(fs, WithStartingGrant(1000), WithAuthorizations(&fakeAuths{}))

	withheld, err := svc.GuardClaim(context.Background(), 42)
	if err != nil || withheld {
		t.Fatalf("GuardClaim with no authorizations = %v, %v, want false, nil", withheld, err)
	}
	if fs.dupCalled {
		t.Fatal("DeviceFingerprintGranted was queried with no authorizations on record, want it skipped entirely")
	}
}

func TestGuardClaimWithoutAuthorizationsSourceFailsOpen(t *testing.T) {
	fs := &fakeGuardStore{StarsStore: memory.NewStarsStore(), dup: true}
	svc := NewService(fs, WithStartingGrant(1000)) // no WithAuthorizations

	withheld, err := svc.GuardClaim(context.Background(), 42)
	if err != nil || withheld {
		t.Fatalf("GuardClaim without an authorizations source = %v, %v, want false, nil", withheld, err)
	}
}
