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

// detachFromConsole fully detaches cmd from whatever console launched it --
// used only by launch() for owpengram-server.exe/owpengram-admin-panel.exe
// themselves, never for the short-lived helper commands hideWindow above is
// also used for (those are meant to finish within the launching console's
// lifetime; detaching them would be pointless).
//
// Without this, a plain child process on Windows stays attached to its
// parent's console. telesrv-ctl.exe exits almost immediately after Start()
// returns (its default command is fire-and-forget), so by the time a user
// closes the window that ran owpengram-server.bat/start.bat, the two
// running server processes are the only things still attached to that
// console -- and Windows delivers CTRL_CLOSE_EVENT to every attached
// process when its window closes. Neither binary installs a console
// control handler, so the runtime's default one turns that into an
// immediate exit: the server dies the instant the launching window closes,
// even though it was started as a detached background process.
//
// CREATE_NEW_CONSOLE (not DETACHED_PROCESS) deliberately gives the process
// a console of its own rather than none at all: portable edition's
// embedded-postgres dependency (github.com/fergusstrange/embedded-postgres)
// shells out to pg_ctl/postgres with no SysProcAttr of its own, and a
// grandchild started with no console to inherit gets a brand new *visible*
// one from Windows -- that surfaced as an empty "...\pg_ctl" window
// popping up on every start once owpengram-server.exe itself had no
// console left to silently hand down. Pairing CREATE_NEW_CONSOLE with
// HideWindow keeps that new console (and everything it silently inherits
// into, pg_ctl/postgres included) invisible, while still being a distinct
// console object from whatever launched telesrv-ctl -- so closing that
// original window has nothing left to send CTRL_CLOSE_EVENT to here.
// CREATE_NEW_PROCESS_GROUP additionally makes it immune to Ctrl+C/Ctrl+Break
// sent to whatever process group launched it.
func detachFromConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	const (
		createNewConsole      = 0x00000010
		createNewProcessGroup = 0x00000200
	)
	cmd.SysProcAttr.CreationFlags |= createNewConsole | createNewProcessGroup
	cmd.SysProcAttr.HideWindow = true
}
