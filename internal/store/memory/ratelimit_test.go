package memory

import (
	"context"
	"testing"
	"time"
)

func TestRateLimiterAllowsUpToLimitThenBlocks(t *testing.T) {
	ctx := context.Background()
	l := NewRateLimiter()
	for i := 0; i < 3; i++ {
		ok, retry, err := l.Allow(ctx, "k", 3, time.Minute)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if !ok || retry != 0 {
			t.Fatalf("Allow #%d = %v, %d, want true, 0", i+1, ok, retry)
		}
	}
	ok, retry, err := l.Allow(ctx, "k", 3, time.Minute)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if ok {
		t.Fatal("4th Allow within a limit of 3 succeeded, want it refused")
	}
	if retry <= 0 || retry > 60 {
		t.Fatalf("retryAfterSeconds = %d, want a positive value within the 1-minute window", retry)
	}
}

func TestRateLimiterWindowResetsAfterExpiry(t *testing.T) {
	ctx := context.Background()
	l := NewRateLimiter()
	if ok, _, err := l.Allow(ctx, "k", 1, 10*time.Millisecond); err != nil || !ok {
		t.Fatalf("first Allow = %v, %v, want true, nil", ok, err)
	}
	if ok, _, err := l.Allow(ctx, "k", 1, 10*time.Millisecond); err != nil || ok {
		t.Fatalf("second Allow within the window = %v, %v, want false", ok, err)
	}
	time.Sleep(20 * time.Millisecond)
	ok, _, err := l.Allow(ctx, "k", 1, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("Allow after expiry: %v", err)
	}
	if !ok {
		t.Fatal("Allow after the window expired was refused, want a fresh window to allow it")
	}
}

func TestRateLimiterZeroCostOrLimitAlwaysAllows(t *testing.T) {
	ctx := context.Background()
	l := NewRateLimiter()
	if ok, _, err := l.AllowN(ctx, "k", 0, 1, time.Second); err != nil || !ok {
		t.Fatalf("AllowN cost=0 = %v, %v, want true, nil", ok, err)
	}
	if ok, _, err := l.AllowN(ctx, "k", 1, 0, time.Second); err != nil || !ok {
		t.Fatalf("AllowN limit=0 = %v, %v, want true, nil", ok, err)
	}
}

func TestRateLimiterSweepDropsExpiredWindows(t *testing.T) {
	l := NewRateLimiter()
	ctx := context.Background()
	if _, _, err := l.Allow(ctx, "k", 1, time.Millisecond); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if removed := l.sweep(time.Now()); removed != 1 {
		t.Fatalf("sweep removed %d windows, want 1", removed)
	}
	if len(l.windows) != 0 {
		t.Fatalf("windows map still has %d entries after sweep", len(l.windows))
	}
}
