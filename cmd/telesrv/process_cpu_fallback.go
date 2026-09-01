//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package main

func processCPUSeconds() (float64, bool) {
	return 0, false
}
