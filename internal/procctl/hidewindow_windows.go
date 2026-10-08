//go:build windows

package procctl

import (
	"os/exec"
	"syscall"
)

// hideWindow stops the spawned process from popping a console window on
// Windows. Every exec.Command in this package (tasklist, taskkill, docker,
// git, go build) is a console-subsystem binary -- when the caller (this
// admin panel process) has no console of its own to inherit (the normal
// case once it's running detached, per procctl's own launch()), Windows
// implicitly creates a brand new one for each child. With the live
// Services-tab polling calling tasklist + docker compose ps every few
// seconds, that showed up as a terminal window flashing on screen
// repeatedly. CREATE_NO_WINDOW suppresses it without changing anything
// about how the child runs or what output it produces.
func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}
