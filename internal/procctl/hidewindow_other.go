//go:build !windows

package procctl

import "os/exec"

// hideWindow is a no-op on non-Windows -- there is no console window to
// suppress. See hidewindow_windows.go for why this exists at all.
func hideWindow(cmd *exec.Cmd) {}
