package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// runConfigCmd executes `pm config ...` through the real parent command, so the
// subcommand resolves exactly as it does in production.
func runConfigCmd(t *testing.T, store storage.TaskStore, args ...string) (string, error) {
	t.Helper()
	cmd := newConfigCmd(store)
	var out strings.Builder
	// Never a nil slice: cobra falls back to os.Args[1:] when args is nil,
	// feeding the test binary's own flags to the command.
	cmd.SetArgs(append([]string{}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return out.String(), err
}

const configWithRemote = `remotes:
  - name: nimbus
    ssh: nimbus
    pm: /home/runner/go/bin/pm
    root: /home/runner/.claude/pm
`

func writeStoreConfig(t *testing.T, store *storage.Store, body string) {
	t.Helper()
	if err := os.WriteFile(store.ConfigPath(), []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestConfigShowNoFile(t *testing.T) {
	store, _ := tempStore(t)

	out, err := runConfigCmd(t, store, "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !strings.Contains(out, "no file") {
		t.Errorf("output does not say the file is absent:\n%s", out)
	}
	// The path is what a user needs in order to create the file.
	if !strings.Contains(out, store.ConfigPath()) {
		t.Errorf("output does not name the path it looked at:\n%s", out)
	}
}

func TestConfigShowRendersRemotes(t *testing.T) {
	store, _ := tempStore(t)
	writeStoreConfig(t, store, configWithRemote)

	out, err := runConfigCmd(t, store, "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	for _, want := range []string{
		store.ConfigPath(),
		"Remote runners (1)",
		"nimbus",
		"/home/runner/go/bin/pm",
		"/home/runner/.claude/pm",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no file") {
		t.Errorf("output claims there is no file:\n%s", out)
	}
}

// A broken config must fail the command, not print an empty registry: reading
// "zero remote runners" off a typo is exactly the silent degradation the
// storage layer refuses.
func TestConfigShowBadFileErrors(t *testing.T) {
	store, _ := tempStore(t)
	writeStoreConfig(t, store, "remotes:\n  - name: runner\n   ssh: broken\n")

	out, err := runConfigCmd(t, store, "show")
	if err == nil {
		t.Fatalf("want an error, got output:\n%s", out)
	}
	if !strings.Contains(err.Error(), store.ConfigPath()) {
		t.Errorf("error %q does not name the file", err)
	}
}

func TestConfigShowEmptyRegistry(t *testing.T) {
	store, _ := tempStore(t)
	writeStoreConfig(t, store, "remotes: []\n")

	out, err := runConfigCmd(t, store, "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	// A file listing no remotes is not the same as no file, and the output
	// must not claim otherwise.
	if strings.Contains(out, "no file") {
		t.Errorf("a present-but-empty config printed as missing:\n%s", out)
	}
	if !strings.Contains(out, "none declared") {
		t.Errorf("output does not report an empty registry:\n%s", out)
	}
}

// The cockpit block prints as resolved: defaults when the file has none, the
// file's values over them when it does.
func TestConfigShowRendersCockpit(t *testing.T) {
	store, _ := tempStore(t)

	out, err := runConfigCmd(t, store, "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	for _, want := range []string{
		"## Cockpit",
		"none named",
		"doing idle 7d",
		"cutoff hour: 18:00",
		"every 30m0s within 07:00-20:00",
		"needs_me solo_reports landed_no_pr focus in_progress waiting changes stuck_projects -new_since_cutoff -recent",
		"pm git github -slack -report",
		"sidebar: columns · repos on · sort worst · width default",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	writeStoreConfig(t, store, "cockpit:\n  groups:\n    acme: {name: Acme}\n    orbit: {}\n  cutoff_hour: 20\n  sections:\n    recent: true\n  sidebar:\n    width: 240\n")
	out, err = runConfigCmd(t, store, "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	for _, want := range []string{
		"groups (2, manual sidebar order)",
		"-  orbit: orbit",
		"orbit: orbit",
		"acme: Acme",
		"cutoff hour: 20:00",
		"stuck_projects -new_since_cutoff recent",
		"width 240px",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
