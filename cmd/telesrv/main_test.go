package main

import (
	"testing"
	"time"

	telegramloginapp "telesrv/internal/app/telegramlogin"
)

// TestFastestPositiveDurationIgnoresNonPositiveValues guards a real reported
// bug: the storage retention sweep's ticker cadence used to be driven purely
// by the shared TELESRV_STORAGE_RETENTION_MAX_AGE default (e.g. 30 days),
// even when a much shorter TELESRV_STORAGE_RETENTION_MAX_AGE_<CATEGORY>
// override was configured -- so a category set to "1 minute" would still
// only actually get swept on the shared default's own slow cadence (or, if
// the shared default was 0, would disable the sweep outright). The worker
// must be handed the fastest positive age across the default and every
// override instead.
func TestFastestPositiveDurationIgnoresNonPositiveValues(t *testing.T) {
	cases := []struct {
		name string
		a, b time.Duration
		want time.Duration
	}{
		{"both positive, a smaller", 30 * 24 * time.Hour, time.Minute, time.Minute},
		{"both positive, b smaller", time.Minute, 30 * 24 * time.Hour, time.Minute},
		{"a zero (disabled), b positive", 0, time.Minute, time.Minute},
		{"a positive, b zero (disabled)", time.Minute, 0, time.Minute},
		{"a negative, b positive", -time.Hour, time.Minute, time.Minute},
		{"both zero (nothing configured)", 0, 0, 0},
		{"both negative", -time.Hour, -time.Minute, -time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fastestPositiveDuration(c.a, c.b); got != c.want {
				t.Fatalf("fastestPositiveDuration(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestTelegramLoginRPCDependencyPreservesDisabledNil(t *testing.T) {
	var disabled *telegramloginapp.Service
	if dependency := telegramLoginRPCDependency(disabled); dependency != nil {
		t.Fatalf("disabled Telegram Login dependency = %#v, want nil interface", dependency)
	}
}

func TestTelegramLoginRPCDependencyPreservesEnabledService(t *testing.T) {
	enabled := new(telegramloginapp.Service)
	if dependency := telegramLoginRPCDependency(enabled); dependency != enabled {
		t.Fatalf("enabled Telegram Login dependency = %#v, want %p", dependency, enabled)
	}
}
