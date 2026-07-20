package domain

import (
	"strings"
	"testing"
)

func TestServiceIdentityAndLoginMessageUseOwpenGramBrand(t *testing.T) {
	serviceUser := OfficialSystemUser()
	if serviceUser.FirstName != "OwpenGram" || serviceUser.Username != "owpengram" {
		t.Fatalf("service user = %+v, want OwpenGram identity", serviceUser)
	}
	message, err := OfficialLoginCodeMessage(42, "12345", 1)
	if err != nil {
		t.Fatalf("build login message: %v", err)
	}
	if !strings.Contains(message.Body, "OwpenGram") || strings.Contains(strings.ToLower(message.Body), "telegram") {
		t.Fatalf("login message exposes wrong brand: %q", message.Body)
	}
}
