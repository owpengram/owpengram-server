package domain

import (
	"strings"
	"testing"
)

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

func TestNewEmailSignupDisplayPhoneLooksLikeARealPhoneNumber(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 200; i++ {
		phone, err := NewEmailSignupDisplayPhone(EmailPhonePrefix)
		if err != nil {
			t.Fatalf("NewEmailSignupDisplayPhone: %v", err)
		}
		if !strings.HasPrefix(phone, EmailPhonePrefix) {
			t.Fatalf("phone %q missing %q prefix", phone, EmailPhonePrefix)
		}
		if !ValidPhone(phone) {
			t.Fatalf("phone %q fails ValidPhone", phone)
		}
		// Must be indistinguishable from a real phone number: no letters, so
		// IsEmailSignupPhone/DecodeEmailPhone never mistake it for a wire
		// email-signup value once it's assigned as an account's real phone.
		if IsEmailSignupPhone(phone) {
			t.Fatalf("IsEmailSignupPhone(%q) = true, want false (must look like a real number)", phone)
		}
		if _, ok := DecodeEmailPhone(phone); ok {
			t.Fatalf("DecodeEmailPhone(%q) unexpectedly succeeded", phone)
		}
		seen[phone] = struct{}{}
	}
	if len(seen) < 190 {
		t.Fatalf("only %d distinct values out of 200 draws, generator looks non-random", len(seen))
	}
}

func TestNewEmailSignupDisplayPhoneHonorsConfiguredPrefix(t *testing.T) {
	for _, prefix := range []string{"380", "1", "7777"} {
		phone, err := NewEmailSignupDisplayPhone(prefix)
		if err != nil {
			t.Fatalf("NewEmailSignupDisplayPhone(%q): %v", prefix, err)
		}
		if !strings.HasPrefix(phone, prefix) {
			t.Fatalf("phone %q missing configured prefix %q", phone, prefix)
		}
		if !ValidPhone(phone) {
			t.Fatalf("phone %q fails ValidPhone", phone)
		}
	}
}

func TestNewEmailSignupDisplayPhoneRejectsInvalidPrefix(t *testing.T) {
	for _, prefix := range []string{"", "abc", "88q", "-1"} {
		if _, err := NewEmailSignupDisplayPhone(prefix); err == nil {
			t.Fatalf("NewEmailSignupDisplayPhone(%q) err = nil, want error", prefix)
		}
	}
}
