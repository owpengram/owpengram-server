package domain

import "testing"

func TestNormalizePhoneUsesCountryAwareE164Identity(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "iran redundant national trunk", input: "+98 0998 167 9461", want: "989981679461"},
		{name: "iran canonical", input: "+98 998 167 9461", want: "989981679461"},
		{name: "iran wire digits redundant trunk", input: "9809981679461", want: "989981679461"},
		{name: "italy significant leading zero", input: "+39 02 1234 5678", want: "390212345678"},
		{name: "china presentation", input: "+86 (188) 0000-0000", want: "8618800000000"},
		{name: "possible reserved NANP range", input: "+1 555 000 0001", want: "15550000001"},
		{name: "local number without country", input: "09981679461", want: ""},
		{name: "letters are not separators", input: "+98abc9981679461", want: ""},
		{name: "international prefix is not country code", input: "00989981679461", want: ""},
		{name: "reserved system identity", input: OfficialSystemPhone, want: OfficialSystemPhone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizePhone(test.input); got != test.want {
				t.Fatalf("NormalizePhone(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestNormalizePhoneAcceptsVirtual888LoginIdentityRange(t *testing.T) {
	for input, want := range map[string]string{
		"+888 12-34":      "8881234",
		"8880000":         "8880000",
		"888123456789012": "888123456789012",
	} {
		if got := NormalizePhone(input); got != want {
			t.Fatalf("NormalizePhone(%q) = %q, want %q", input, got, want)
		}
	}
	for _, phone := range []string{"888123", "8881234567890123", "+888abc1234"} {
		if got := NormalizePhone(phone); got != "" {
			t.Fatalf("NormalizePhone(%q) = %q, want empty", phone, got)
		}
	}
}

func TestValidPhoneRequiresCanonicalStorageShape(t *testing.T) {
	for _, phone := range []string{"989981679461", "390212345678", "8618800000000", "15550000001", "8880000", "888123456789012", OfficialSystemPhone} {
		if !ValidPhone(phone) {
			t.Fatalf("ValidPhone(%q) = false", phone)
		}
	}
	for _, phone := range []string{"+989981679461", "+8881234", "888123", "8881234567890123", "9809981679461", "09981679461", "", "+98abc9981679461"} {
		if ValidPhone(phone) {
			t.Fatalf("ValidPhone(%q) = true", phone)
		}
	}
}
