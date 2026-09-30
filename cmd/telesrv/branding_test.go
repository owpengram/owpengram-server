package main

import (
	"testing"

	"telesrv/internal/branding"
)

// A prior version of this fix only overrode ProductName, leaving
// StarsName/PremiumName/the per-client app names stuck on the compiled-in
// "OwpenGram ..." default -- e.g. @premiumbot's /help text would buy
// "<custom name> Premium" but price it "in OwpenGram Stars". This pins
// every field deriving from the shared "OwpenGram" prefix.
func TestBrandedConfigRenamesEveryDerivedField(t *testing.T) {
	cfg := brandedConfig("LocalGram")

	want := branding.Config{
		ProductName:     "LocalGram",
		ProductUsername: branding.DefaultConfig().ProductUsername,
		DesktopAppName:  "LocalGram Desktop",
		AndroidAppName:  "LocalGram Android",
		IOSAppName:      "LocalGram iOS",
		MacOSAppName:    "LocalGram macOS",
		WebAAppName:     "LocalGram Web A",
		WebKAppName:     "LocalGram Web K",
		PremiumName:     "LocalGram Premium",
		StarsName:       "LocalGram Stars",
		PublicBaseURL:   branding.DefaultConfig().PublicBaseURL,
	}
	if cfg != want {
		t.Fatalf("brandedConfig(%q) = %+v, want %+v", "LocalGram", cfg, want)
	}
}

func TestBrandedConfigPassesBrandingValidate(t *testing.T) {
	if _, err := branding.Validate(brandedConfig("LocalGram")); err != nil {
		t.Fatalf("branding.Validate(brandedConfig(...)): %v", err)
	}
}
