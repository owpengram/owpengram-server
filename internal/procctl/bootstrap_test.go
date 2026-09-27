package procctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bootstrapTestTemplate = `## Admin -- panel auth
TELESRV_ADMIN_UI_PASSWORD=
TELESRV_ADMIN_UI_TOKEN=
TELESRV_ADMIN_API_TOKEN=
TELESRV_ADMIN_SESSION_KEY=
TELESRV_ADMIN_API_ADDR=

## Database -- Where the server stores its data.
TELESRV_IDENTITY_DIR=data/identity
`

func newBootstrapTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte(bootstrapTestTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewManager(dir)
}

func TestBootstrapEnvFreshInstallGeneratesEverything(t *testing.T) {
	m := newBootstrapTestManager(t)

	if m.IsInitialized() {
		t.Fatal("IsInitialized() == true before .env exists")
	}

	password, err := m.BootstrapEnv()
	if err != nil {
		t.Fatalf("BootstrapEnv: %v", err)
	}
	if password == "" {
		t.Fatal("expected a generated password on a fresh install")
	}
	if !m.IsInitialized() {
		t.Fatal("IsInitialized() == false after BootstrapEnv created .env")
	}

	values, err := m.readEnvFile()
	if err != nil {
		t.Fatal(err)
	}
	if values["TELESRV_ADMIN_UI_PASSWORD"] != password {
		t.Fatalf(".env TELESRV_ADMIN_UI_PASSWORD = %q, want the returned password %q", values["TELESRV_ADMIN_UI_PASSWORD"], password)
	}
	if len(values["TELESRV_ADMIN_API_TOKEN"]) != 64 {
		t.Fatalf("TELESRV_ADMIN_API_TOKEN = %q, want a 64-char hex string", values["TELESRV_ADMIN_API_TOKEN"])
	}
	if values["TELESRV_ADMIN_SESSION_KEY"] == "" {
		t.Fatal("TELESRV_ADMIN_SESSION_KEY was not generated")
	}
	if values["TELESRV_ADMIN_API_ADDR"] != "127.0.0.1:2599" {
		t.Fatalf("TELESRV_ADMIN_API_ADDR = %q, want the default 127.0.0.1:2599", values["TELESRV_ADMIN_API_ADDR"])
	}

	identityDir := filepath.Join(m.Root, "data", "identity")
	if _, err := os.Stat(filepath.Join(identityDir, setupPendingFileName)); err != nil {
		t.Errorf("setup-pending marker not created: %v", err)
	}
	pwFile, err := os.ReadFile(filepath.Join(identityDir, passwordTemporaryFileName))
	if err != nil {
		t.Fatalf("temporary-password marker not created: %v", err)
	}
	if string(pwFile) != password {
		t.Fatalf("temporary-password marker = %q, want %q (no trailing newline)", pwFile, password)
	}
}

func TestBootstrapEnvExistingCompleteEnvIsNoop(t *testing.T) {
	m := newBootstrapTestManager(t)
	if _, err := m.BootstrapEnv(); err != nil {
		t.Fatalf("initial BootstrapEnv: %v", err)
	}
	before, err := m.readEnvFile()
	if err != nil {
		t.Fatal(err)
	}

	password, err := m.BootstrapEnv()
	if err != nil {
		t.Fatalf("second BootstrapEnv: %v", err)
	}
	if password != "" {
		t.Fatalf("second BootstrapEnv on an already-complete .env generated a new password %q", password)
	}
	after, err := m.readEnvFile()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TELESRV_ADMIN_UI_PASSWORD", "TELESRV_ADMIN_API_TOKEN", "TELESRV_ADMIN_SESSION_KEY"} {
		if before[key] != after[key] {
			t.Errorf("%s changed across a no-op BootstrapEnv: %q -> %q", key, before[key], after[key])
		}
	}
}

func TestBootstrapEnvPatchesExistingEnvWithoutFreshInstallSentinels(t *testing.T) {
	m := newBootstrapTestManager(t)
	// Simulate a hand-written .env that predates these required secrets --
	// present (so IsInitialized() is true) but missing the fields telesrv-
	// admin refuses to boot without.
	if err := os.WriteFile(filepath.Join(m.Root, ".env"), []byte("TELESRV_IDENTITY_DIR=data/identity\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	password, err := m.BootstrapEnv()
	if err != nil {
		t.Fatalf("BootstrapEnv: %v", err)
	}
	if password == "" {
		t.Fatal("expected a generated password when neither password nor token was set")
	}

	identityDir := filepath.Join(m.Root, "data", "identity")
	if _, err := os.Stat(filepath.Join(identityDir, setupPendingFileName)); err == nil {
		t.Fatal("setup-pending marker must not be created when patching a pre-existing .env -- this is not a fresh install")
	}
	if _, err := os.Stat(filepath.Join(identityDir, passwordTemporaryFileName)); err == nil {
		t.Fatal("temporary-password marker must not be created when patching a pre-existing .env")
	}

	values, err := m.readEnvFile()
	if err != nil {
		t.Fatal(err)
	}
	if values["TELESRV_ADMIN_API_TOKEN"] == "" {
		t.Fatal("TELESRV_ADMIN_API_TOKEN was not patched into the existing .env")
	}
}

func TestBrowsableHostPortHelpersAreConsistentWithIdentityDirDefault(t *testing.T) {
	// identityDir must fall back to "data/identity" exactly like
	// config.go's own TELESRV_IDENTITY_DIR default when the key is absent.
	m := newBootstrapTestManager(t)
	dir := m.identityDir(map[string]string{})
	want := filepath.Join(m.Root, "data", "identity")
	if dir != want {
		t.Fatalf("identityDir() = %q, want %q", dir, want)
	}
	if !strings.Contains(bootstrapTestTemplate, "TELESRV_IDENTITY_DIR") {
		t.Fatal("test template drifted from what this test assumes")
	}
}
