package procctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findRepoRoot walks up from the test's working directory looking for
// .env.example -- the same file server-panel.py parses -- so this test
// exercises the real file, not a synthetic fixture that could drift from it.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".env.example")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal(".env.example not found above test directory")
		}
		dir = parent
	}
}

func TestReadEnvGroupsParsesRealTemplate(t *testing.T) {
	root := findRepoRoot(t)
	m := NewManager(root)
	groups, err := m.ReadEnvGroups()
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) == 0 {
		t.Fatal("expected at least one group from .env.example")
	}
	found := map[string]bool{}
	for _, g := range groups {
		if g.Title == "" {
			t.Errorf("group with empty title, description=%q", g.Description)
		}
		if len(g.Fields) == 0 {
			t.Errorf("group %q has no fields", g.Title)
		}
		for _, f := range g.Fields {
			if !strings.HasPrefix(f.Key, "TELESRV_") {
				t.Errorf("field key %q missing TELESRV_ prefix", f.Key)
			}
			found[f.Key] = true
		}
	}
	// A couple of fields we know exist in the real file (checked above) --
	// pins the parser to the actual format, not just "produces something".
	for _, key := range []string{"TELESRV_LISTEN", "TELESRV_ADVERTISE_IP", "TELESRV_DC"} {
		if !found[key] {
			t.Errorf("expected field %s in parsed groups", key)
		}
	}
	// The advanced/internal-tuning section (after the "# ===..." banner) is
	// deliberately excluded -- mirrors server-panel.py's parse_env_template.
	if found["TELESRV_MTPROTO_RPC_MAX_INFLIGHT"] {
		t.Error("advanced/internal field leaked into panel-visible groups")
	}
}

func TestWriteEnvValuesRoundTrip(t *testing.T) {
	root := findRepoRoot(t)
	tmpl, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), tmpl, 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir)
	groups, err := m.ReadEnvGroups()
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) == 0 {
		t.Fatal("no groups parsed")
	}

	if err := m.WriteEnvValues(map[string]string{
		"TELESRV_ADVERTISE_IP": "203.0.113.5",
		"TELESRV_DC":           "3",
	}); err != nil {
		t.Fatal(err)
	}

	groups2, err := m.ReadEnvGroups()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, g := range groups2 {
		for _, f := range g.Fields {
			values[f.Key] = f.Value
		}
	}
	if values["TELESRV_ADVERTISE_IP"] != "203.0.113.5" {
		t.Errorf("TELESRV_ADVERTISE_IP = %q, want 203.0.113.5", values["TELESRV_ADVERTISE_IP"])
	}
	if values["TELESRV_DC"] != "3" {
		t.Errorf("TELESRV_DC = %q, want 3", values["TELESRV_DC"])
	}
	// A field never touched by WriteEnvValues should still read back its
	// template default -- confirms the file's other lines/comments/layout
	// survived the rewrite untouched.
	if values["TELESRV_LISTEN"] == "" {
		t.Error("TELESRV_LISTEN lost its default after an unrelated field was written")
	}

	envPath := filepath.Join(dir, ".env")
	envData, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envData), "TELESRV_ADVERTISE_IP=203.0.113.5") {
		t.Error(".env does not contain the written value verbatim")
	}
	// The file's descriptive comments (the reason server-panel.py rewrites
	// from the template instead of dumping bare key=value pairs) must
	// survive too.
	if !strings.Contains(string(envData), "Your server's public IP address.") {
		t.Error(".env lost .env.example's comment lines on write")
	}
}

// TestWriteEnvValuesPreservesUnrelatedCustomValues guards against a real
// regression: a save that only touches one section's keys used to reset
// every OTHER already-customized key (e.g. TELESRV_ADMIN_UI_PASSWORD) back
// to .env.example's bare template default, because the old implementation
// fell back to the template line instead of the current .env value for any
// key missing from that save's payload.
func TestWriteEnvValuesPreservesUnrelatedCustomValues(t *testing.T) {
	root := findRepoRoot(t)
	tmpl, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), tmpl, 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir)

	// First save sets the admin password, as a one-time setup step would.
	if err := m.WriteEnvValues(map[string]string{
		"TELESRV_ADMIN_UI_PASSWORD": "s3cret",
	}); err != nil {
		t.Fatal(err)
	}

	// A later, unrelated save (e.g. the Storage page) that never mentions
	// the password must not disturb it.
	if err := m.WriteEnvValues(map[string]string{
		"TELESRV_STORAGE_MAX_TOTAL_BYTES": "209715200",
	}); err != nil {
		t.Fatal(err)
	}

	groups, err := m.ReadEnvGroups()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, g := range groups {
		for _, f := range g.Fields {
			values[f.Key] = f.Value
		}
	}
	if values["TELESRV_ADMIN_UI_PASSWORD"] != "s3cret" {
		t.Errorf("TELESRV_ADMIN_UI_PASSWORD = %q, want it preserved as \"s3cret\" after an unrelated save", values["TELESRV_ADMIN_UI_PASSWORD"])
	}
	if values["TELESRV_STORAGE_MAX_TOTAL_BYTES"] != "209715200" {
		t.Errorf("TELESRV_STORAGE_MAX_TOTAL_BYTES = %q, want 209715200", values["TELESRV_STORAGE_MAX_TOTAL_BYTES"])
	}
}
