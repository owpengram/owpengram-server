//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || windows

package main

import "testing"

func TestProcessCPUSecondsAvailable(t *testing.T) {
	seconds, ok := processCPUSeconds()
	if !ok || seconds < 0 {
		t.Fatalf("process CPU seconds = %v, available=%v", seconds, ok)
	}
}
