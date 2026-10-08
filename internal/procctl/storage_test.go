package procctl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStorageConfigured(t *testing.T) {
	for _, tt := range []struct {
		name, env string
		want      bool
	}{
		{"fresh .env", "TELESRV_ADMIN_UI_PASSWORD=x\n", false},
		{"mode chosen in the wizard", "TELESRV_POSTGRES_MODE=embedded\n", true},
		{"old docker install", "TELESRV_POSTGRES_DSN=postgres://x\n", true},
		{"old portable install", "TELESRV_EDITION=portable\n", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(tt.env), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := NewManager(dir).StorageConfigured(); got != tt.want {
				t.Fatalf("StorageConfigured = %v, want %v", got, tt.want)
			}
		})
	}
	if NewManager(t.TempDir()).StorageConfigured() {
		t.Fatal("a missing .env counts as configured")
	}
}

// The first run on an old install turns the retired edition switch into the
// explicit mode, because saving any setting drops TELESRV_EDITION from .env.
func TestBootstrapEnvMigratesLegacyEdition(t *testing.T) {
	for edition, want := range map[string]string{"portable": "embedded", "standard": "external"} {
		m := newBootstrapTestManager(t)
		template := bootstrapTestTemplate + "# TELESRV_POSTGRES_MODE=embedded\n"
		if err := os.WriteFile(filepath.Join(m.Root, ".env.example"), []byte(template), 0o644); err != nil {
			t.Fatal(err)
		}
		old := "TELESRV_ADMIN_UI_PASSWORD=pw\nTELESRV_ADMIN_API_TOKEN=t\nTELESRV_ADMIN_SESSION_KEY=k\nTELESRV_ADMIN_API_ADDR=127.0.0.1:2599\nTELESRV_EDITION=" + edition + "\n"
		if err := os.WriteFile(filepath.Join(m.Root, ".env"), []byte(old), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := m.BootstrapEnv(); err != nil {
			t.Fatal(err)
		}
		if got := m.EnvValue("TELESRV_POSTGRES_MODE"); got != want {
			t.Fatalf("edition %q: mode = %q, want %q", edition, got, want)
		}
	}
}
