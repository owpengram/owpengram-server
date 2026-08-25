package main

import "runtime/debug"

// gitCommit/buildTime can be set via -ldflags "-X main.gitCommit=... -X
// main.buildTime=...", mirroring cmd/telesrv/buildinfo.go -- but in
// practice neither procctl's goBuild (used by the admin panel's own
// Restart/Update) nor a plain `go build` sets them, so this normally falls
// back to Go's automatic VCS stamping (debug.ReadBuildInfo's vcs.revision),
// which needs nothing extra to work from a git checkout.
var (
	gitCommit = ""
	buildTime = ""
)

type buildMetadata struct {
	Commit    string
	Dirty     bool
	BuildTime string
}

// shortCommit is what the sidebar footer shows next to "Version: O7" -- the
// full hash is one click away in git log, the footer just needs enough to
// tell two builds apart at a glance.
func (m buildMetadata) shortCommit() string {
	if len(m.Commit) > 7 {
		return m.Commit[:7]
	}
	return m.Commit
}

func currentBuildMetadata() buildMetadata {
	meta := buildMetadata{Commit: gitCommit, BuildTime: buildTime}
	if info, ok := debug.ReadBuildInfo(); ok {
		settings := map[string]string{}
		for _, setting := range info.Settings {
			settings[setting.Key] = setting.Value
		}
		if meta.Commit == "" {
			meta.Commit = settings["vcs.revision"]
		}
		meta.Dirty = settings["vcs.modified"] == "true"
	}
	return meta
}
