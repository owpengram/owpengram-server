package domain

import "testing"

func TestContainsBlockedClientMarker(t *testing.T) {
	blocked := []string{
		"AliceAvatarHello1:MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEIMYERjud",
		"AliceAvatarRevoke1",
		"AliceJoinProof2:EZF4w0AYrgW693AG168Ijt3o:1793851541",
		"ALICE8W.MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE",
	}
	for _, s := range blocked {
		if !ContainsBlockedClientMarker(s) {
			t.Errorf("not blocked: %q", s)
		}
	}
	allowed := []string{"", "Alice", "Hi Alice, how are you?", "alice avatar hello", "ALICE"}
	for _, s := range allowed {
		if ContainsBlockedClientMarker(s) {
			t.Errorf("wrongly blocked: %q", s)
		}
	}
}
