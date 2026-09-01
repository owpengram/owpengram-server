package loadharness

import (
	"path/filepath"
	"testing"

	"telesrv/internal/domain"
)

func TestSessionDirectoryForManifestIsolatesNamedBundles(t *testing.T) {
	tests := []struct {
		manifest string
		want     string
	}{
		{manifest: filepath.Join("data", "load500", "manifest.json"), want: "sessions"},
		{manifest: filepath.Join("data", "manifest-50.json"), want: "sessions-manifest-50"},
		{manifest: filepath.Join("data", "manifest-500.json"), want: "sessions-manifest-500"},
	}
	for _, test := range tests {
		t.Run(test.want, func(t *testing.T) {
			if got := sessionDirectoryForManifest(test.manifest); got != test.want {
				t.Fatalf("session directory = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDefaultProvisionPhonePrefixProducesCanonicalPossibleNumber(t *testing.T) {
	cfg := ProvisionConfig{PhonePrefix: DefaultPhonePrefix, FirstNamePrefix: "Load"}
	record := desiredSessionRecord(0, 0, 0, cfg)
	if got, want := domain.NormalizePhone(record.Phone), "15550000001"; got != want {
		t.Fatalf("normalized default phone = %q, want %q (wire %q)", got, want, record.Phone)
	}
}

func TestDesiredSessionRecordUsesManifestNamespace(t *testing.T) {
	cfg := ProvisionConfig{ManifestPath: filepath.Join("data", "manifest-500.json"), PhonePrefix: "+155500", FirstNamePrefix: "Load"}
	record := desiredSessionRecord(12, 12, 1, cfg)
	want := filepath.ToSlash(filepath.Join("sessions-manifest-500", "session-0012-device-1.bin"))
	if record.SessionFile != want {
		t.Fatalf("session file = %q, want %q", record.SessionFile, want)
	}
}
