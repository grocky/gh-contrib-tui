// Package version reports which build of the tool is running.
package version

import "runtime/debug"

const devel = "(devel)"

// String describes the build. linked is the version stamped in with -ldflags
// by release builds and the Homebrew formula; info is the result of
// debug.ReadBuildInfo, nil when it reported none.
//
// Without ldflags the module version still says enough: `go install ...@v0.1.0`
// records v0.1.0, and a local build in a checkout records a pseudo-version
// naming the commit.
func String(linked string, info *debug.BuildInfo) string {
	if linked != "" {
		return linked
	}
	if info != nil && info.Main.Version != "" {
		return info.Main.Version
	}
	return devel
}
