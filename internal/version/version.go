// Package version reports the version this binary carries: the Makefile's
// VERSION stamped in at link time, or - for a binary built without those
// ldflags, such as one from `go install` - the module version the toolchain
// recorded in the build info.
package version

import "runtime/debug"

// defaultVersion is what Version holds when nothing set it at link time.
const defaultVersion = "dev"

// Version is the version this binary reports. The Makefile stamps it at link
// time (`-ldflags "-X github.com/mbalazy/pm-cli/internal/version.Version=0.64.0"`),
// which is the path `make install` takes. A binary built any other way -
// `go install github.com/mbalazy/pm-cli/cmd/pm@latest`, or a plain `go build` -
// carries no ldflags, so init asks the binary's own build info instead of
// reporting "dev" at a tagged release.
var Version = defaultVersion

func init() { Version = resolve(Version, debug.ReadBuildInfo) }

// resolve reports the version to display. An ldflags value wins outright: it
// is the most specific thing anyone said about this build. Otherwise the
// module version the toolchain recorded is used - the tag for
// `go install ...@v0.64.0`, a pseudo-version for an untagged commit, and
// "(devel)" for a build from a working tree (which is also what `go test`
// binaries report).
//
// It never returns an empty string, and that is the point of the first
// branch: cobra does not register a --version flag when Command.Version is
// empty, so an empty value does not print blank, it deletes `pm --version`
// from the CLI. An empty ldflags value is reachable - make's `?=` keeps an
// empty VERSION from the environment or the command line - so it is treated
// as "nobody said", not as an answer.
func resolve(ldflags string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if ldflags != defaultVersion && ldflags != "" {
		return ldflags
	}
	info, ok := readBuildInfo()
	if !ok || info == nil || info.Main.Version == "" {
		return defaultVersion
	}
	return info.Main.Version
}
