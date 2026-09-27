//go:build !windows

package embeddedpg

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
