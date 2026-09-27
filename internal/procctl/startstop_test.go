package procctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStopNoopWhenNothingRunning(t *testing.T) {
	m := NewManager(t.TempDir())
	log := m.Stop()
	if !strings.Contains(log, "Nothing was running") {
		t.Fatalf("Stop() = %q, want a nothing-running message", log)
	}
}

func TestStopClearsStalePIDsWithoutErroring(t *testing.T) {
	dir := t.TempDir()
	// A PID essentially guaranteed not to be alive on any real machine --
	// Stop() must treat this the same as "not running" rather than failing.
	statePath := filepath.Join(dir, stateFileName)
	if err := os.WriteFile(statePath, []byte(`{"server_pid":999999,"admin_pid":999998}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	_ = m.Stop()
	st := m.loadState()
	if st.ServerPID != 0 || st.AdminPID != 0 {
		t.Fatalf("Stop() left state ServerPID=%d AdminPID=%d, want both cleared", st.ServerPID, st.AdminPID)
	}
}
