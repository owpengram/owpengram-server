package domain

import "testing"

func TestClientSessionMetadataPreferredLanguage(t *testing.T) {
	for _, test := range []struct {
		name string
		in   ClientSessionMetadata
		want string
	}{
		{name: "lang code wins", in: ClientSessionMetadata{LangCode: "RU-ru", SystemLangCode: "en-US"}, want: "ru"},
		{name: "system fallback", in: ClientSessionMetadata{SystemLangCode: "pt_BR"}, want: "pt"},
		{name: "empty", in: ClientSessionMetadata{}, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.in.PreferredLanguage(); got != test.want {
				t.Fatalf("PreferredLanguage() = %q, want %q", got, test.want)
			}
		})
	}
}
