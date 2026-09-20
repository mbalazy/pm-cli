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
// module version the toolchain recorded is used - a tag such as "v0.64.0" for
// `go install ...@v0.64.0`, or "(devel)" for a build from a working tree.
// When there is no build info at all, or it carries no main version (a test
// binary, or a toolchain that recorded none), the default stands.
func resolve(ldflags string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if ldflags != defaultVersion {
		return ldflags
	}
	info, ok := readBuildInfo()
	if !ok || info == nil || info.Main.Version == "" {
		return defaultVersion
	}
	return info.Main.Version
}
