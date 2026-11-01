// Package version is the OwpenGram server's release line.
package version

import (
	"strconv"
	"strings"
)

// Release is the release this build belongs to: "O" and the number of the
// GitHub release tag it ships under. Bump it for every release. The release
// scripts also set it at build time
// (-ldflags "-X telesrv/internal/version.Release=O9"), so an archive built
// with --version O9 reports O9 whatever the source says.
var Release = "O8"

// Number is the N of a release name "ON" (case-insensitive), or 0 when the
// name is not of that form -- a local build with a made-up label, say.
func Number(release string) int {
	release = strings.TrimSpace(release)
	if len(release) < 2 || (release[0] != 'O' && release[0] != 'o') {
		return 0
	}
	n, err := strconv.Atoi(release[1:])
	if err != nil || n < 0 {
		return 0
	}
	return n
}
