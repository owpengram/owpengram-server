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

func TestClassifyMessageText(t *testing.T) {
	cases := []struct {
		text string
		want MessageTextVerdict
	}{
		// Renamed protocol, same key format -> still blocked.
		{"BobHello7:MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEIMYERjudK_secZ7VHC3KwvhXzJg1SZz6sCDnziiSWzTVqtfahNrKkOuUHwH_ZyyIoGXvhxgh2ZxDZ3nYwjBlTw", MessageTextRawPublicKey},
		{"key MCowBQYDK2VuAyEAm6Ud2s8XcP9DJzUD1eGHFz9H1t3k8m7kYc0ZbNq3k0A=", MessageTextRawPublicKey},
		{"MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAu1SU1LfVLPHCozMxH2Mo", MessageTextRawPublicKey},
		{"AliceJoinProof2:EZF4w0AYrgW693AG168Ijt3o:1793851541", MessageTextClientMarker},
		{"-----BEGIN PGP PUBLIC KEY BLOCK-----\nmQINBF...", MessageTextArmoredKey},
		// Opaque token dominating the message -> reported.
		{"EZF4w0AYrgW693AG168Ijt3o:1793851541:319c9854898041419c536eb799693479:9609b77e4881884", MessageTextOpaqueToken},
		{"QUxJQ0VfQVVUT19XRUxDT01FX1YxCm93cGVuZ3JhbQoxMTkzCjE3ODAyNDUxOTQK", MessageTextOpaqueToken},
		// Ordinary content -> ok.
		{"Hello, how are you doing today? Let's meet at 5 near the station.", MessageTextOK},
		{"https://lacentraleduverre.com/miroirs-sur-mesure/65-miroir-sans-tain.html?srsltid=AYCX9vL8DGBeSPcjG0S02mpXrscWKbRy48", MessageTextOK},
		{"Check this video www.youtube.com/watch?v=dQw4w9WgXcQ&list=PLx0sYbCqOb8TBPRdmBHs5Iftvv9TPboYG", MessageTextOK},
		{"Мій номер картки не скажу, але ось код 123456", MessageTextOK},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MessageTextOK},
		{"short", MessageTextOK},
	}
	for _, c := range cases {
		if got := ClassifyMessageText(c.text); got != c.want {
			t.Errorf("ClassifyMessageText(%q) = %v, want %v", c.text, got, c.want)
		}
	}
	if !MessageTextRawPublicKey.Blocks() || !MessageTextClientMarker.Blocks() ||
		MessageTextOpaqueToken.Blocks() || MessageTextArmoredKey.Blocks() {
		t.Fatal("Blocks() mapping wrong")
	}
}
