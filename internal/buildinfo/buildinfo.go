// Package buildinfo holds what a release stamps into the binary (goreleaser ldflags).
package buildinfo

import "runtime/debug"

var (
	Version = "dev"
	Commit  = ""
)

// String is "VERSION (COMMIT)"; a `go build` from a checkout falls back to the VCS stamp.
func String() string {
	commit := Commit
	if commit == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" && len(s.Value) >= 7 {
					commit = s.Value[:7]
				}
			}
		}
	}
	if commit == "" {
		return Version
	}
	return Version + " (" + commit + ")"
}
