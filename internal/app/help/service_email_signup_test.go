package help

import (
	"context"
	"encoding/json"
	"testing"
)

// TestAppConfigEmailSignupPhonePrefixes asserts email_signup_phone_prefixes
// is only present alongside email_signup_enabled=true, carries exactly the
// configured prefix list, and that changing the list bumps the hash so
// clients don't cache a stale list.
func TestAppConfigEmailSignupPhonePrefixes(t *testing.T) {
	ctx := context.Background()

	disabled := NewService(nil, nil)
	cfg, _, err := disabled.GetAppConfig(ctx, 0, 0)
	if err != nil {
		t.Fatalf("GetAppConfig (disabled): %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(cfg.JSON, &decoded); err != nil {
		t.Fatalf("app config json invalid: %v", err)
	}
	if _, present := decoded["email_signup_phone_prefixes"]; present {
		t.Fatalf("email_signup_phone_prefixes present while email signup disabled: %+v", decoded["email_signup_phone_prefixes"])
	}

	enabled := NewService(nil, nil,
		WithEmailSignupEnable(true),
		WithEmailSignupPhonePrefixes([]string{"888", "380", "373"}))
	cfg2, _, err := enabled.GetAppConfig(ctx, 0, 0)
	if err != nil {
		t.Fatalf("GetAppConfig (enabled): %v", err)
	}
	if err := json.Unmarshal(cfg2.JSON, &decoded); err != nil {
		t.Fatalf("app config json invalid: %v", err)
	}
	rawPrefixes, ok := decoded["email_signup_phone_prefixes"].([]any)
	if !ok || len(rawPrefixes) != 3 {
		t.Fatalf("email_signup_phone_prefixes = %+v, want [888 380 373]", decoded["email_signup_phone_prefixes"])
	}
	for i, want := range []string{"888", "380", "373"} {
		if rawPrefixes[i] != want {
			t.Fatalf("email_signup_phone_prefixes[%d] = %v, want %q", i, rawPrefixes[i], want)
		}
	}

	other := NewService(nil, nil,
		WithEmailSignupEnable(true),
		WithEmailSignupPhonePrefixes([]string{"888"}))
	cfg3, _, err := other.GetAppConfig(ctx, 0, 0)
	if err != nil {
		t.Fatalf("GetAppConfig (different prefixes): %v", err)
	}
	if cfg3.Hash == cfg2.Hash {
		t.Fatalf("hash unchanged (%d) despite a different prefix list, clients would stay cached on the old list", cfg3.Hash)
	}
}
