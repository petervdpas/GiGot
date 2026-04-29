package main

import (
	"fmt"
	"runtime/debug"
)

// resolveVersion returns the version string Execute prints. If a release
// build stamped main.appVersion via -ldflags "-X main.appVersion=<v>",
// that wins outright. Otherwise we enrich the dev sentinel with the VCS
// info Go 1.18+ embeds into every build automatically via
// debug.ReadBuildInfo, so a developer's local rebuild self-describes
// instead of always saying "0.0.0-dev".
//
// Output shape (SemVer build metadata via "+"):
//
//	"0.5.0"                          release, ldflag-stamped
//	"0.0.0-dev"                      no ldflag, no VCS info available
//	"0.0.0-dev+a1b2c3d"              local build, clean working tree
//	"0.0.0-dev+a1b2c3d.dirty"        local build, uncommitted changes
//
// `.dirty` (rather than `-dirty`) keeps the suffix inside the SemVer
// build-metadata segment instead of slipping into the pre-release one.
func resolveVersion() string {
	if appVersion != "0.0.0-dev" {
		return appVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return appVersion
	}
	var rev string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				rev = s.Value[:7]
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return appVersion
	}
	if dirty {
		return fmt.Sprintf("%s+%s.dirty", appVersion, rev)
	}
	return fmt.Sprintf("%s+%s", appVersion, rev)
}
