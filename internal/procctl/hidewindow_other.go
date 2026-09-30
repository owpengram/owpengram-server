//go:build !windows

package procctl

import (
	"os/exec"
	"syscall"
)

// hideWindow is a no-op on non-Windows -- there is no console window to
// suppress. See hidewindow_windows.go for why this exists at all.
func hideWindow(cmd *exec.Cmd) {}

// detachFromConsole starts cmd in a new session (setsid), detaching it from
// the controlling terminal -- used only by launch() for the actual server/
// admin-panel processes, mirroring hidewindow_windows.go's detachFromConsole.
// Without this, owpengram-server/owpengram-admin-panel share start.sh's
// terminal session and, like any backgrounded command run without nohup/
// setsid/disown, can receive SIGHUP when that terminal hangs up (closing a
// terminal window, an SSH session dropping) -- killing a process that was
// meant to keep running detached in the background.
func detachFromConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
