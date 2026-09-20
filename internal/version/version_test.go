package version

import (
	"runtime/debug"
	"testing"
)

func infoWith(v string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: v}}, true
	}
}

func noInfo() (*debug.BuildInfo, bool) { return nil, false }

// The resolver decides what `pm --version` prints, and the two inputs it
// reads - the ldflags value and the binary's build info - are set by whoever
// built the binary, never by pm. Each case is one way pm reaches a user.
func TestResolve(t *testing.T) {
	for _, c := range []struct {
		name    string
		ldflags string
		read    func() (*debug.BuildInfo, bool)
		want    string
	}{
		{"ldflags wins over build info", "0.64.0", infoWith("v0.63.0"), "0.64.0"},
		{"ldflags wins with no build info", "0.64.0", noInfo, "0.64.0"},
		{"go install at a tag reports the tag", defaultVersion, infoWith("v0.64.0"), "v0.64.0"},
		{"go install off an untagged commit reports the pseudo-version", defaultVersion, infoWith("v0.0.0-20260920161641-52faa5da196d"), "v0.0.0-20260920161641-52faa5da196d"},
		{"a working-tree build reports devel", defaultVersion, infoWith("(devel)"), "(devel)"},
		{"no build info keeps the default", defaultVersion, noInfo, defaultVersion},
		{"build info without a main version keeps the default", defaultVersion, infoWith(""), defaultVersion},
		{"nil build info keeps the default", defaultVersion, func() (*debug.BuildInfo, bool) { return nil, true }, defaultVersion},
		// `make install VERSION=` and `VERSION= make install` both reach the
		// linker with an empty value, because make's ?= keeps an empty
		// VERSION from the environment or the command line. That is "nobody
		// said", not an answer, so build info still gets its turn.
		{"an empty ldflags value defers to build info", "", infoWith("v0.64.0"), "v0.64.0"},
		{"an empty ldflags value with no build info keeps the default", "", noInfo, defaultVersion},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := resolve(c.ldflags, c.read); got != c.want {
				t.Errorf("resolve(%q, ...) = %q, want %q", c.ldflags, got, c.want)
			}
		})
	}
}

// An empty result is not a cosmetic problem: cobra skips registering the
// --version flag when Command.Version is empty (internal/cmd/root.go), so
// `pm --version` fails with "unknown flag" instead of printing something
// blank. No combination of inputs may produce one.
func TestResolveNeverReturnsEmpty(t *testing.T) {
	reads := map[string]func() (*debug.BuildInfo, bool){
		"no info":       noInfo,
		"nil info":      func() (*debug.BuildInfo, bool) { return nil, true },
		"empty version": infoWith(""),
		"devel":         infoWith("(devel)"),
		"tag":           infoWith("v0.64.0"),
	}
	for _, ldflags := range []string{"", defaultVersion, "0.64.0"} {
		for name, read := range reads {
			if got := resolve(ldflags, read); got == "" {
				t.Errorf("resolve(%q, %s) returned an empty version", ldflags, name)
			}
		}
	}
}
