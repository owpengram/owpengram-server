package procctl

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHandlePendingAdminRestartNoop(t *testing.T) {
	m := NewManager(t.TempDir())
	restarted, err := m.HandlePendingAdminRestart(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if restarted {
		t.Fatal("expected no-op when no restart was requested")
	}
}

func TestHandlePendingAdminRestartFlagRoundTrip(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, stateFileName)
	if err := os.WriteFile(statePath, []byte(`{"server_pid":123,"admin_pid":0,"pending_admin_restart":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	st := m.loadState()
	if !st.PendingAdminRestart {
		t.Fatal("expected PendingAdminRestart to round-trip from JSON")
	}
	if st.ServerPID != 123 {
		t.Fatalf("ServerPID = %d, want 123 (existing fields must survive)", st.ServerPID)
	}
}
