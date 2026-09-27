package procctl

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// AdminBreakGlassUsername is the fixed username paired with the generated
// (or operator-chosen) TELESRV_ADMIN_UI_PASSWORD -- mirrors
// tui-panel/server-panel.py's ADMIN_BREAK_GLASS_USERNAME.
const AdminBreakGlassUsername = "owpengram"

// setupPendingFileName/passwordTemporaryFileName must stay byte-identical to
// the private consts of the same name in internal/identity -- that package
// is what actually reads these files back (Store.SetupPending,
// Store.TemporaryPasswordMatches), so this is deliberately a duplicated
// literal, not a shared import: internal/procctl has no other reason to
// depend on internal/identity, and tui-panel/server-panel.py (the other
// writer of these same two files) already duplicates them the same way.
const (
	setupPendingFileName      = ".setup_pending"
	passwordTemporaryFileName = ".admin_password_temporary"
)

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// IsInitialized reports whether this install has ever been through Setup.
// Mirrors server-panel.py's is_initialized(): .env not existing yet is the
// one reliable signal -- everything else (bin/, containers) could plausibly
// be absent even on a configured install.
func (m *Manager) IsInitialized() bool {
	_, err := os.Stat(filepath.Join(m.Root, ".env"))
	return err == nil
}

// identityDir resolves TELESRV_IDENTITY_DIR the same way config.go's own
// default does, from an already-loaded .env values map.
func (m *Manager) identityDir(values map[string]string) string {
	dir := values["TELESRV_IDENTITY_DIR"]
	if dir == "" {
		dir = "data/identity"
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(m.Root, dir)
}

// BootstrapEnv creates .env from .env.example on a fresh install, or patches
// in whichever of the three secrets telesrv-admin refuses to boot without
// (TELESRV_ADMIN_API_TOKEN, TELESRV_ADMIN_SESSION_KEY, and a
// password/token pair) are missing from an .env that already exists. Direct
// Go port of tui-panel/server-panel.py's bootstrap_env() -- see that
// function's docstring for the full reasoning, especially around why
// patching secrets into a pre-existing .env must never be treated as a
// fresh install (there is likely already a real identity, real data, real
// users behind it).
//
// Returns the freshly generated admin password if one was generated (fresh
// install, or an existing .env that had neither a password nor a token), or
// "" if nothing needed generating -- an existing password is never read
// back for display either way.
func (m *Manager) BootstrapEnv() (string, error) {
	fresh := !m.IsInitialized()
	existing, err := m.readEnvFile()
	if err != nil {
		return "", err
	}

	values := map[string]string{}
	var generatedPassword string
	changed := false

	if existing["TELESRV_ADMIN_UI_PASSWORD"] == "" && existing["TELESRV_ADMIN_UI_TOKEN"] == "" {
		pw, err := randomURLSafe(12)
		if err != nil {
			return "", fmt.Errorf("generate admin password: %w", err)
		}
		generatedPassword = pw
		values["TELESRV_ADMIN_UI_PASSWORD"] = pw
		changed = true
	}
	if existing["TELESRV_ADMIN_API_TOKEN"] == "" {
		tok, err := randomHex(32)
		if err != nil {
			return "", fmt.Errorf("generate admin API token: %w", err)
		}
		values["TELESRV_ADMIN_API_TOKEN"] = tok
		changed = true
	}
	if existing["TELESRV_ADMIN_SESSION_KEY"] == "" {
		key, err := randomURLSafe(32)
		if err != nil {
			return "", fmt.Errorf("generate admin session key: %w", err)
		}
		values["TELESRV_ADMIN_SESSION_KEY"] = key
		changed = true
	}
	if existing["TELESRV_ADMIN_API_ADDR"] == "" {
		values["TELESRV_ADMIN_API_ADDR"] = "127.0.0.1:2599"
		changed = true
	}

	if !fresh && !changed {
		return "", nil // existing .env, every required secret was already set
	}

	if err := m.WriteEnvValues(values); err != nil {
		return "", err
	}

	if fresh {
		merged, err := m.readEnvFile()
		if err != nil {
			return "", err
		}
		dir := m.identityDir(merged)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create identity dir: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, setupPendingFileName), nil, 0o644); err != nil {
			return "", fmt.Errorf("write setup-pending marker: %w", err)
		}
		if generatedPassword != "" {
			if err := os.WriteFile(filepath.Join(dir, passwordTemporaryFileName), []byte(generatedPassword), 0o600); err != nil {
				return "", fmt.Errorf("write temporary password marker: %w", err)
			}
		}
	}

	return generatedPassword, nil
}
