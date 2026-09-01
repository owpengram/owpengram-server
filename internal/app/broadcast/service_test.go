package broadcast

import (
	"context"
	"errors"
	"testing"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

type fakeSender struct {
	sent    []domain.SendPrivateTextRequest
	failFor map[int64]bool // fail every send to this recipient user id
	nextID  int
}

func (f *fakeSender) SendPrivateText(_ context.Context, req domain.SendPrivateTextRequest) (domain.SendPrivateTextResult, error) {
	if f.failFor[req.RecipientUserID] {
		return domain.SendPrivateTextResult{}, errors.New("simulated send failure")
	}
	f.sent = append(f.sent, req)
	f.nextID++
	return domain.SendPrivateTextResult{
		RecipientMessage: domain.Message{
			ID:  f.nextID,
			UID: int64(f.nextID),
			Pts: f.nextID,
		},
	}, nil
}

func TestCreateValidatesInput(t *testing.T) {
	svc := NewService(memory.NewBroadcastStore(), WithMessageSender(&fakeSender{}))
	ctx := context.Background()

	if _, err := svc.Create(ctx, "  ", domain.BroadcastTargetAll, nil, "admin"); !errors.Is(err, domain.ErrBroadcastMessageEmpty) {
		t.Fatalf("empty message: err = %v, want ErrBroadcastMessageEmpty", err)
	}
	if _, err := svc.Create(ctx, "hi", domain.BroadcastTargetMode("bogus"), []int64{1}, "admin"); !errors.Is(err, domain.ErrBroadcastInvalid) {
		t.Fatalf("bad target mode: err = %v, want ErrBroadcastInvalid", err)
	}
	if _, err := svc.Create(ctx, "hi", domain.BroadcastTargetSelected, nil, "admin"); !errors.Is(err, domain.ErrBroadcastNoRecipients) {
		t.Fatalf("no recipients: err = %v, want ErrBroadcastNoRecipients", err)
	}
	tooMany := make([]int64, domain.MaxBroadcastSelectedRecipients+1)
	for i := range tooMany {
		tooMany[i] = int64(1000 + i)
	}
	if _, err := svc.Create(ctx, "hi", domain.BroadcastTargetSelected, tooMany, "admin"); !errors.Is(err, domain.ErrBroadcastInvalid) {
		t.Fatalf("too many recipients: err = %v, want ErrBroadcastInvalid", err)
	}

	// A duplicate id in the selected list is rejected outright, not silently
	// collapsed: the caller's list should already be a set.
	if _, err := svc.Create(ctx, "hi", domain.BroadcastTargetSelected, []int64{10, 20, 20}, "admin"); !errors.Is(err, domain.ErrBroadcastRecipientInvalid) {
		t.Fatalf("duplicate recipient: err = %v, want ErrBroadcastRecipientInvalid", err)
	}

	created, err := svc.Create(ctx, "  News! ", domain.BroadcastTargetSelected, []int64{10, 20}, "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Message != "News!" {
		t.Fatalf("Message = %q, want trimmed %q", created.Message, "News!")
	}
	if created.TargetCount != 2 {
		t.Fatalf("TargetCount = %d, want 2", created.TargetCount)
	}
}

func TestRunCycleDeliversAndCounts(t *testing.T) {
	store := memory.NewBroadcastStore()
	sender := &fakeSender{}
	svc := NewService(store, WithMessageSender(sender))
	ctx := context.Background()

	created, err := svc.Create(ctx, "Update available", domain.BroadcastTargetSelected, []int64{101, 102, 103}, "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := svc.RunCycle(ctx, "lease-1", 100, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if result.Sent != 3 {
		t.Fatalf("Sent = %d, want 3", result.Sent)
	}
	if len(sender.sent) != 3 {
		t.Fatalf("sender received %d sends, want 3", len(sender.sent))
	}
	for _, req := range sender.sent {
		if req.SenderUserID != domain.OfficialSystemUserID {
			t.Fatalf("SenderUserID = %d, want OfficialSystemUserID (%d)", req.SenderUserID, domain.OfficialSystemUserID)
		}
		if req.Message != "Update available" {
			t.Fatalf("Message = %q, want %q", req.Message, "Update available")
		}
	}

	got, found, err := svc.Get(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if got.SentCount != 3 || got.FailedCount != 0 {
		t.Fatalf("counts = sent:%d failed:%d, want sent:3 failed:0", got.SentCount, got.FailedCount)
	}

	// A second cycle finds nothing left pending.
	result, err = svc.RunCycle(ctx, "lease-2", 100, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("RunCycle (second): %v", err)
	}
	if result.Claimed != 0 {
		t.Fatalf("second cycle claimed = %d, want 0 (nothing pending)", result.Claimed)
	}
}

func TestRunCycleRetriesThenTerminatesFailures(t *testing.T) {
	store := memory.NewBroadcastStore()
	sender := &fakeSender{failFor: map[int64]bool{999: true}}
	svc := NewService(store, WithMessageSender(sender))
	ctx := context.Background()

	if _, err := svc.Create(ctx, "will fail", domain.BroadcastTargetSelected, []int64{999}, "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Run one cycle per attempt, up to the cap; the row must stay pending
	// (retried) below the cap and become terminal at it.
	for i := 0; i < domain.MaxBroadcastRecipientAttempts; i++ {
		result, err := svc.RunCycle(ctx, "lease", 100, 10, 30*time.Second)
		if err != nil {
			t.Fatalf("RunCycle attempt %d: %v", i+1, err)
		}
		if result.Sent != 0 || result.Failed != 1 {
			t.Fatalf("attempt %d: sent=%d failed=%d, want sent=0 failed=1 (always fails)", i+1, result.Sent, result.Failed)
		}
	}

	// One more cycle: the row is now terminal ('failed'), so nothing is left
	// to claim.
	result, err := svc.RunCycle(ctx, "lease-final", 100, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("RunCycle (final): %v", err)
	}
	if result.Claimed != 0 {
		t.Fatalf("final cycle claimed = %d, want 0 (recipient should be terminally failed)", result.Claimed)
	}
}

func TestCreateAllModeSnapshotsWithoutExplicitIDs(t *testing.T) {
	store := memory.NewBroadcastStore()
	store.SeedEligibleUsers([]int64{1, 2, 3})
	sender := &fakeSender{}
	svc := NewService(store, WithMessageSender(sender))
	ctx := context.Background()

	created, err := svc.Create(ctx, "hello all", domain.BroadcastTargetAll, nil, "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.TargetMode != domain.BroadcastTargetAll {
		t.Fatalf("TargetMode = %q, want all", created.TargetMode)
	}

	result, err := svc.RunCycle(ctx, "lease", 100, 100, 30*time.Second)
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if result.Materialized != 3 {
		t.Fatalf("Materialized = %d, want 3", result.Materialized)
	}
	if result.Sent != 3 {
		t.Fatalf("Sent = %d, want 3", result.Sent)
	}
}
