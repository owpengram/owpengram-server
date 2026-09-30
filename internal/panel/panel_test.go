package panel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telesrv/internal/procctl"
)

const panelTestEnvTemplate = `## Edition -- How this install gets its PostgreSQL and blob storage.
TELESRV_EDITION=

## Admin -- Admin panel bind address and credentials.
TELESRV_ADMIN_UI_ADDR=0.0.0.0:2600
TELESRV_ADMIN_UI_PASSWORD=
TELESRV_ADMIN_API_TOKEN=

## Network -- Where the server listens and what it advertises.
TELESRV_ADVERTISE_IP=203.0.113.5
TELESRV_LISTEN=0.0.0.0:2398
`

func newTestModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte(panelTestEnvTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := procctl.NewManager(dir)
	return newModel(context.Background(), mgr)
}

func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want panel.Model", next)
	}
	return got
}

func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// View must never panic before the first background refresh lands -- a
// real run always sees this window (Init fires the refresh async, and the
// terminal renders at least one frame before the first message arrives).
func TestViewRendersBeforeDataLoads(t *testing.T) {
	m := newTestModel(t)
	if got := m.View(); got == "" {
		t.Fatal("View() returned empty string before data loaded")
	}
}

func TestDashboardCursorWrapsBothDirections(t *testing.T) {
	m := newTestModel(t)
	m.dash.loaded = true // skip past the "Loading..." early-return in view

	if m.dash.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.dash.cursor)
	}
	m = send(t, m, key("up"))
	if want := len(dashboardMenu) - 1; m.dash.cursor != want {
		t.Fatalf("cursor after up from 0 = %d, want %d (wrap to last)", m.dash.cursor, want)
	}
	m = send(t, m, key("down"))
	if m.dash.cursor != 0 {
		t.Fatalf("cursor after down from last = %d, want 0 (wrap to first)", m.dash.cursor)
	}
}

func TestDashboardMenuShortcutSwitchesToLogsScreen(t *testing.T) {
	m := newTestModel(t)
	m.dash.loaded = true

	// menuLogs is 1-indexed as "5" in the UI (Start/Stop/Restart/Update/Logs).
	m = send(t, m, key("5"))
	if m.dash.cursor != menuLogs {
		t.Fatalf("cursor after pressing 5 = %d, want menuLogs (%d)", m.dash.cursor, menuLogs)
	}
	// dashboardState.trigger returned a switchScreenMsg via a tea.Cmd;
	// Model.Update only changes m.current once that message is actually
	// delivered back through Update, exactly as the real event loop does.
	_, cmd := m.Update(key("5"))
	if cmd == nil {
		t.Fatal("expected a tea.Cmd from pressing the Logs shortcut")
	}
	msg := cmd()
	m = send(t, m, msg)
	if m.current != screenLogs {
		t.Fatalf("current screen = %v, want screenLogs", m.current)
	}
	if !m.logs.ready && m.logs.err == nil && len(m.logs.lines) == 0 {
		// Nothing to assert on content (no server has ever run in this
		// temp dir), just that entering the screen didn't panic and left
		// it in a sane, non-ready-but-not-broken state until the first
		// logDataMsg lands.
	}
}

func TestEnvEditorNavigationSkipsGroupHeaders(t *testing.T) {
	m := newTestModel(t)
	m.current = screenEnv
	m.env = newEnvState(m.mgr)

	if len(m.env.rows) == 0 {
		t.Fatal("expected at least one row from the test .env.example groups")
	}
	if m.env.rows[m.env.cursor].fieldIdx < 0 {
		t.Fatal("cursor landed on a group header row, want a field row")
	}

	start := m.env.cursor
	m.env, _ = m.env.updateBrowsing(m, key("down"))
	if m.env.cursor == start {
		// Only one field total is a legitimate reason this wouldn't move;
		// the test template has multiple groups/fields, so this is real.
		t.Fatalf("cursor did not move on down from row %d", start)
	}
	if m.env.rows[m.env.cursor].fieldIdx < 0 {
		t.Fatalf("cursor landed on a group header row %d after moving down", m.env.cursor)
	}
}

func TestEnvEditorEnterEntersEditModeWithCurrentValue(t *testing.T) {
	m := newTestModel(t)
	m.current = screenEnv
	m.env = newEnvState(m.mgr)

	row := m.env.rows[m.env.cursor]
	field := m.env.field(row)
	m.env.values[field.Key] = "example-value"

	m.env, _ = m.env.updateBrowsing(m, key("enter"))
	if !m.env.editing {
		t.Fatal("expected editing=true after enter on a field row")
	}
	if got := m.env.input.Value(); got != "example-value" {
		t.Fatalf("input value = %q, want %q", got, "example-value")
	}

	m.env, _ = m.env.updateEditing(key("esc"))
	if m.env.editing {
		t.Fatal("expected editing=false after esc")
	}
}

func TestEditionScreenAppliesChoiceOnEnter(t *testing.T) {
	m := newTestModel(t)
	m.current = screenEdition
	m.ed = newEditionState(m.mgr)
	m.ed.cursor = 1 // "Classic" (stored as "standard")

	var cmd tea.Cmd
	m.ed, cmd = m.ed.update(m, key("enter"))
	if cmd == nil {
		t.Fatal("expected a tea.Cmd from applying an edition choice")
	}
	msg := cmd()
	m.ed, _ = m.ed.update(m, msg)

	if !m.ed.saved {
		t.Fatalf("edition apply did not report saved; message=%q", m.ed.message)
	}
	got, ok := m.mgr.Edition()
	if !ok || got != "standard" {
		t.Fatalf("Manager.Edition() = %q, %v, want \"standard\", true", got, ok)
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"a\nb\nc\n", "c"},
		{"a\n\n\n", "a"},
		{"only one line", "only one line"},
	}
	for _, tc := range cases {
		if got := lastNonEmptyLine(tc.in); got != tc.want {
			t.Errorf("lastNonEmptyLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
