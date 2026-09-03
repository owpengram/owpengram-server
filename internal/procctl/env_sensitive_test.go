package procctl

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadEnvGroupsSensitiveFlag guards against sensitiveKeyRe false-positive
// matches on the word "SECRET" in a name that means Telegram's secret-chat
// feature, not a credential (e.g. TELESRV_SECRET_CHAT_DELETE_FILE_AFTER_DOWNLOAD),
// while still flagging real credential-shaped names as sensitive.
func TestReadEnvGroupsSensitiveFlag(t *testing.T) {
	root := t.TempDir()
	tmpl := "## Storage & Media -- test group\n" +
		"# desc\n" +
		"TELESRV_SECRET_CHAT_DELETE_FILE_AFTER_DOWNLOAD=true\n" +
		"# desc\n" +
		"TELESRV_ADMIN_PASSWORD=\n" +
		"# desc\n" +
		"TELESRV_BOT_API_KEY=\n"
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte(tmpl), 0o644); err != nil {
		t.Fatalf("write .env.example: %v", err)
	}

	groups, err := NewManager(root).ReadEnvGroups()
	if err != nil {
		t.Fatalf("ReadEnvGroups: %v", err)
	}
	got := map[string]bool{}
	for _, g := range groups {
		for _, f := range g.Fields {
			got[f.Key] = f.Sensitive
		}
	}

	if got["TELESRV_SECRET_CHAT_DELETE_FILE_AFTER_DOWNLOAD"] {
		t.Errorf("TELESRV_SECRET_CHAT_DELETE_FILE_AFTER_DOWNLOAD marked sensitive, want not (it's a boolean toggle, not a credential)")
	}
	if !got["TELESRV_ADMIN_PASSWORD"] {
		t.Errorf("TELESRV_ADMIN_PASSWORD not marked sensitive, want sensitive")
	}
	if !got["TELESRV_BOT_API_KEY"] {
		t.Errorf("TELESRV_BOT_API_KEY not marked sensitive, want sensitive")
	}
}
