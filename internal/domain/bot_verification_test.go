package domain

import (
	"testing"
	"unicode/utf8"
)

func TestBotVerifierSettingsAcceptsDescriptionLongerThan70Runes(t *testing.T) {
	description := "This account is verified as official by the representatives of Telegram"
	if got := utf8.RuneCountInString(description); got != 71 {
		t.Fatalf("fixture length = %d, want 71", got)
	}

	settings := BotVerifierSettings{
		BotID:                      1,
		IconDocumentID:             2,
		CompanyName:                "Example Trust",
		DefaultDescription:         description,
		CanModifyCustomDescription: true,
	}
	if err := settings.Validate(); err != nil {
		t.Fatalf("Validate() rejected 71-rune default description: %v", err)
	}
	if got, err := settings.DescriptionFor(description); err != nil || got != description {
		t.Fatalf("DescriptionFor() = %q, %v; want fixture, nil", got, err)
	}
}
