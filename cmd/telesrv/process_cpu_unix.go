//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import "golang.org/x/sys/unix"

func processCPUSeconds() (float64, bool) {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return 0, false
	}
	ns := unix.TimevalToNsec(usage.Utime) + unix.TimevalToNsec(usage.Stime)
	if ns < 0 {
		return 0, false
	}
	return float64(ns) / 1e9, true
}
