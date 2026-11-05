// Package version is the OwpenGram server's release line.
package version

import (
	"strconv"
	"strings"
)

// Release is the release this build belongs to: "O" and the number of the
// GitHub release tag it ships under, optionally with a minor part ("O8.1").
// Bump it for every release. The release scripts also set it at build time
// (-ldflags "-X telesrv/internal/version.Release=O9"), so an archive built
// with --version O9 reports O9 whatever the source says.
var Release = "O9"

// Parse splits a release name "ON" or "ON.M" (case-insensitive) into its
// major and minor numbers. ok is false when the name is not of that form --
// a local build with a made-up label, say.
func Parse(release string) (major, minor int, ok bool) {
	release = strings.TrimSpace(release)
	if len(release) < 2 || (release[0] != 'O' && release[0] != 'o') {
		return 0, 0, false
	}
	majorPart, minorPart, hasMinor := strings.Cut(release[1:], ".")
	major, ok = digits(majorPart)
	if !ok {
		return 0, 0, false
	}
	if hasMinor {
		if minor, ok = digits(minorPart); !ok {
			return 0, 0, false
		}
	}
	return major, minor, true
}

// Number is the major N of a release name "ON" / "ON.M", or 0 when the name
// is not of that form.
func Number(release string) int {
	major, _, ok := Parse(release)
	if !ok {
		return 0
	}
	return major
}

// Newer reports whether release a is a later release than b. A name that is
// not a release ("dev", "") is never newer than anything.
func Newer(a, b string) bool {
	aMajor, aMinor, aOK := Parse(a)
	if !aOK {
		return false
	}
	bMajor, bMinor, bOK := Parse(b)
	if !bOK {
		return true
	}
	if aMajor != bMajor {
		return aMajor > bMajor
	}
	return aMinor > bMinor
}

func digits(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}
