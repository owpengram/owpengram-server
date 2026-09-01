package domain

import (
	"strings"
	"testing"
)

func TestServiceIdentityAndLoginMessageUseOwpenGramBrand(t *testing.T) {
	serviceUser := OfficialSystemUser()
	// No Username by design -- see OfficialSystemUser's doc comment: real
	// Telegram's own 777000 isn't @-addressable either.
	if serviceUser.FirstName != "OwpenGram" || serviceUser.Username != "" {
		t.Fatalf("service user = %+v, want OwpenGram identity with no username", serviceUser)
	}
	message, err := OfficialLoginCodeMessage(42, "", "12345", 1)
	if err != nil {
		t.Fatalf("build login message: %v", err)
	}
	if !strings.Contains(message.Body, "OwpenGram") || strings.Contains(strings.ToLower(message.Body), "telegram") {
		t.Fatalf("login message exposes wrong brand: %q", message.Body)
	}
}
