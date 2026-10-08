//go:build windows

package embeddedpg

import (
	"os/exec"
	"syscall"
)

// hideWindow stops pg_ctl from popping a console window -- same reasoning
// and same pattern as internal/procctl's own hideWindow, applied here since
// stopStaleInstance runs this on every single embedded-PostgreSQL start, not
// just an error path.
func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}
