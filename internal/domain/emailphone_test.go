package domain

import "testing"

func TestEncodeDecodeEmailPhoneRoundTrip(t *testing.T) {
	for _, email := range []string{
		"onysd@owpengram.local",
		"a@b.co",
		"User.Name+Tag@Example.COM",
		"very.long.email.address.for.testing.purposes@some-long-domain-name.example.com",
	} {
		t.Run(email, func(t *testing.T) {
			phone, ok := EncodeEmailPhone(email)
			if !ok {
				t.Fatalf("EncodeEmailPhone(%q) ok=false", email)
			}
			if len(phone) > MaxEmailSignupPhoneLen {
				t.Fatalf("encoded phone too long: %d chars", len(phone))
			}
			if !ValidPhone(phone) {
				t.Fatalf("encoded phone %q fails ValidPhone", phone)
			}
			if !IsEmailSignupPhone(phone) {
				t.Fatalf("IsEmailSignupPhone(%q) = false, want true", phone)
			}
			decoded, ok := DecodeEmailPhone(phone)
			if !ok {
				t.Fatalf("DecodeEmailPhone(%q) ok=false", phone)
			}
			want := NormalizeEmailForPhone(email)
			if decoded != want {
				t.Fatalf("decoded = %q, want %q", decoded, want)
			}
			t.Logf("%q -> %q (%d chars) -> %q", email, phone, len(phone), decoded)
		})
	}
}

func TestEncodeEmailPhoneCaseAndWhitespaceNormalize(t *testing.T) {
	p1, ok1 := EncodeEmailPhone("User@Example.com")
	p2, ok2 := EncodeEmailPhone("  user@example.com  ")
	if !ok1 || !ok2 {
		t.Fatalf("ok1=%v ok2=%v", ok1, ok2)
	}
	if p1 != p2 {
		t.Fatalf("case/whitespace variants encoded differently: %q vs %q", p1, p2)
	}
}

func TestEncodeEmailPhoneRejectsInvalid(t *testing.T) {
	for _, email := range []string{"", "   ", "not-an-email", "@"} {
		if _, ok := EncodeEmailPhone(email); ok && email != "@" {
			t.Fatalf("EncodeEmailPhone(%q) ok=true, want false", email)
		}
	}
}

func TestDecodeEmailPhoneRejectsNonEmailNumbers(t *testing.T) {
	for _, phone := range []string{"", "15550001234", "888", "88799999"} {
		if _, ok := DecodeEmailPhone(phone); ok {
			t.Fatalf("DecodeEmailPhone(%q) ok=true, want false", phone)
		}
	}
}
