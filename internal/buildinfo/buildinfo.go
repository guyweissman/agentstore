// Package buildinfo exposes version metadata stamped into the binary at build
// time. Release builds (GoReleaser) set these via -ldflags -X; plain
// `go install` leaves them at their defaults and falls back to the module
// version recorded in the binary's build info.
package buildinfo

import "runtime/debug"

// Set via -ldflags at release build time. Do not set these by hand.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// Version returns the release version, e.g. "v0.1.0". For `go install`-ed
// builds (where ldflags are not applied) it falls back to the module version
// recorded by the Go toolchain.
func Version() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}

// Commit returns the git commit the binary was built from, or "" if unknown.
func Commit() string { return commit }

// Date returns the build timestamp, or "" if unknown.
func Date() string { return date }
