package version

import (
	"runtime/debug"
	"testing"
)

// The resolver decides what `pm --version` prints, and the two inputs it
// reads - the ldflags value and the binary's build info - are set by whoever
// built the binary, never by pm. Each combination is one way pm reaches a
// user: make install, go install at a tag, go build from a checkout, and a
// binary whose build info the toolchain did not record.
func TestResolve(t *testing.T) {
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: v}}, true
		}
	}
	none := func() (*debug.BuildInfo, bool) { return nil, false }

	for _, c := range []struct {
		name    string
		ldflags string
		read    func() (*debug.BuildInfo, bool)
		want    string
	}{
		{"ldflags wins over build info", "0.64.0", info("v0.63.0"), "0.64.0"},
		{"ldflags wins with no build info", "0.64.0", none, "0.64.0"},
		{"go install at a tag reports the tag", defaultVersion, info("v0.64.0"), "v0.64.0"},
		{"go build from a checkout reports devel", defaultVersion, info("(devel)"), "(devel)"},
		{"no build info keeps the default", defaultVersion, none, defaultVersion},
		{"empty main version keeps the default", defaultVersion, info(""), defaultVersion},
		{"nil build info keeps the default", defaultVersion, func() (*debug.BuildInfo, bool) { return nil, true }, defaultVersion},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := resolve(c.ldflags, c.read); got != c.want {
				t.Errorf("resolve(%q, ...) = %q, want %q", c.ldflags, got, c.want)
			}
		})
	}
}

// init runs resolve against the real build info, so Version must never come
// back empty - the three readers print it straight into a command's version
// string, an MCP handshake and the board's footer.
func TestVersionIsNeverEmpty(t *testing.T) {
	if Version == "" {
		t.Error("Version is empty")
	}
}
