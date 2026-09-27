package procctl

import (
	"context"
	"fmt"
)

// EnsureDocker runs the same "docker compose up -d + wait for Postgres"
// step Restart/Update already run internally, for callers with no State of
// their own to pass in (cmd/telesrv-ctl).
func (m *Manager) EnsureDocker(ctx context.Context) (string, error) {
	return m.ensureDocker(ctx, m.loadState())
}

// Build rebuilds bin/owpengram-server and bin/owpengram-admin-panel from
// the current working tree.
func (m *Manager) Build(ctx context.Context) (string, error) {
	return m.buildBoth(ctx)
}

// Start is the non-destructive counterpart to Restart: it only builds and
// (re)launches whichever of owpengram-server/owpengram-admin-panel isn't
// already alive, leaving an already-running instance untouched -- unlike
// Restart/Update, which always kill and relaunch both. Mirrors
// tui-panel/server-panel.py's quickstart() launch step.
func (m *Manager) Start(ctx context.Context) (string, error) {
	st := m.loadState()
	dockerLog, err := m.ensureDocker(ctx, st)
	if err != nil {
		return dockerLog, err
	}
	buildLog, err := m.buildBoth(ctx)
	fullLog := dockerLog + "\n" + buildLog
	if err != nil {
		return fullLog, fmt.Errorf("build failed: %w", err)
	}
	if !pidAlive(st.ServerPID) {
		pid, err := m.launch(m.serverExe(), m.serverLog())
		if err != nil {
			return fullLog, fmt.Errorf("launch server failed: %w", err)
		}
		st.ServerPID = pid
		fullLog += fmt.Sprintf("\nowpengram-server launched, pid=%d.\n", pid)
	} else {
		fullLog += "\nowpengram-server already running.\n"
	}
	if !pidAlive(st.AdminPID) {
		pid, err := m.launch(m.adminExe(), m.adminLog())
		if err != nil {
			return fullLog, fmt.Errorf("launch admin panel failed: %w", err)
		}
		st.AdminPID = pid
		fullLog += fmt.Sprintf("owpengram-admin-panel launched, pid=%d.\n", pid)
	} else {
		fullLog += "owpengram-admin-panel already running.\n"
	}
	if err := m.saveState(st); err != nil {
		return fullLog, fmt.Errorf("save state: %w", err)
	}
	return fullLog, nil
}

// Stop kills both owpengram-server and owpengram-admin-panel if they're
// currently running, and clears their recorded PIDs. A no-op (not an error)
// when neither is running.
func (m *Manager) Stop() string {
	st := m.loadState()
	var log string
	if pidAlive(st.ServerPID) {
		killPID(st.ServerPID)
		log += fmt.Sprintf("owpengram-server (pid=%d) stopped.\n", st.ServerPID)
	}
	if pidAlive(st.AdminPID) {
		killPID(st.AdminPID)
		log += fmt.Sprintf("owpengram-admin-panel (pid=%d) stopped.\n", st.AdminPID)
	}
	if log == "" {
		log = "Nothing was running.\n"
	}
	st.ServerPID = 0
	st.AdminPID = 0
	_ = m.saveState(st)
	return log
}
