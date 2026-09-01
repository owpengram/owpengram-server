//go:build windows

package main

import "golang.org/x/sys/windows"

func processCPUSeconds() (float64, bool) {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0, false
	}
	ns := kernel.Nanoseconds() + user.Nanoseconds()
	if ns < 0 {
		return 0, false
	}
	return float64(ns) / 1e9, true
}
