package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// seedHeldClaim writes a live acceptance claim on acme-1 straight to disk, as
// another machine or session would have left it. It uses storage only, so the
// contract tests below do not lean on any executor test helper.
func seedHeldClaim(t *testing.T, store *storage.Store, c storage.FinishClaim) {
	t.Helper()
	path := storage.FinishClaimPath(store.ProjectDir("acme"), c.TrackerID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

// The executor commands' exit codes, through the same root the binary runs.
// Each case pins what the command returns today; none of them reaches a
// worker, a git remote or ssh.
func TestExecutorExitCodesEndToEnd(t *testing.T) {
	store := exitFixture(t)
	now := time.Now().UTC()

	cases := []struct {
		name  string
		claim *storage.FinishClaim
		args  []string
		want  int
	}{
		{"finish release of another host's live claim", &storage.FinishClaim{
			TrackerID: "acme-1", Host: "runner", PID: 4711,
			Started:   now.Add(-time.Minute).Format(time.RFC3339),
			Refreshed: now.Format(time.RFC3339),
		}, []string{"finish", "release", "acme-1", "--project", "acme"}, ExitConflict},
		{"finish release of this host's live claim without naming it", &storage.FinishClaim{
			TrackerID: "acme-1", Host: storage.Hostname(), PID: 1, Session: "sess-a",
			Started:   now.Add(-time.Minute).Format(time.RFC3339),
			Refreshed: now.Format(time.RFC3339),
		}, []string{"finish", "release", "acme-1", "--project", "acme"}, ExitConflict},
		{"finish claim of a held tracker", &storage.FinishClaim{
			TrackerID: "acme-1", Host: "runner", PID: 4711,
			Started:   now.Add(-time.Minute).Format(time.RFC3339),
			Refreshed: now.Format(time.RFC3339),
		}, []string{"finish", "claim", "acme-1", "--project", "acme"}, ExitError},
		{"finish status of a held tracker", nil, []string{"finish", "status", "acme-1", "--project", "acme"}, 0},
		{"finish status of an unknown tracker", nil, []string{"finish", "status", "nope", "--project", "acme"}, ExitNotFound},
		{"finish status in an unknown project", nil, []string{"finish", "status", "acme-1", "--project", "nope"}, ExitNotFound},
		{"finish claim of an unsafe id", nil, []string{"finish", "claim", "../x", "--project", "acme"}, ExitUsage},
		{"finish claim without a tracker", nil, []string{"finish", "claim"}, ExitUsage},
		{"finish unknown flag", nil, []string{"finish", "--bogus"}, ExitUsage},
		{"finish with a positional tracker", nil, []string{"finish", "acme-1"}, ExitError},
		{"finish record of an unknown tracker", nil, []string{"finish", "record", "nope", "--project", "acme"}, ExitError},
		{"work on an unknown id", nil, []string{"work", "nope-1"}, ExitError},
		{"work in an unknown project", nil, []string{"work", "nope", "acme-1"}, ExitNotFound},
		{"work on an unknown task", nil, []string{"work", "acme", "nope"}, ExitNotFound},
		{"work without a task", nil, []string{"work"}, ExitUsage},
		{"work unknown flag", nil, []string{"work", "--bogus"}, ExitUsage},
		{"run-epic on an unknown id", nil, []string{"run-epic", "nope-1"}, ExitError},
		{"run-epic in an unknown project", nil, []string{"run-epic", "nope", "acme-1"}, ExitNotFound},
		{"run-epic on an unknown tracker", nil, []string{"run-epic", "acme", "nope"}, ExitNotFound},
		{"run-epic unknown flag", nil, []string{"run-epic", "--bogus"}, ExitUsage},
		{"executor show", nil, []string{"executor", "show", "acme"}, 0},
		{"executor show of an unknown project", nil, []string{"executor", "show", "nope"}, ExitNotFound},
		{"executor show unknown flag", nil, []string{"executor", "show", "--bogus"}, ExitUsage},
		{"executor stats", nil, []string{"executor", "stats", "acme"}, 0},
		{"executor stats of an unknown project", nil, []string{"executor", "stats", "nope"}, ExitNotFound},
		{"executor doctor of an unknown project", nil, []string{"executor", "doctor", "nope"}, ExitNotFound},
		{"executor init of an unknown project", nil, []string{"executor", "init", "nope"}, ExitNotFound},
		{"executor with an unknown subcommand prints its help", nil, []string{"executor", "bogus"}, 0},
		{"worker-guard unknown flag", nil, []string{"worker-guard", "--bogus"}, ExitUsage},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_ = os.Remove(storage.FinishClaimPath(store.ProjectDir("acme"), "acme-1"))
			if c.claim != nil {
				seedHeldClaim(t, store, *c.claim)
			}
			code, stdout, _ := runRoot(t, c.args...)
			if code != c.want {
				t.Errorf("pm %s: exit %d, want %d", strings.Join(c.args, " "), code, c.want)
			}
			if code != 0 && stdout != "" {
				t.Errorf("pm %s: a failing command wrote to stdout: %q", strings.Join(c.args, " "), stdout)
			}
		})
	}
}

// The executor's command tree as the root registers it: every command path,
// its usage line, whether it is hidden, and the flags it defines itself.
// runctl, the board and the hook settings start `pm` as a process with these
// names and flags (the guard hook runs `<pm> worker-guard ...`), so a command
// that drops off the root or loses a flag breaks them without a compile error.
func TestExecutorCommandTree(t *testing.T) {
	want := []string{
		`executor | use "executor" | flags: `,
		`executor doctor | use "doctor [project]" | flags: --strict`,
		`executor init | use "init [project]" | flags: --dry-run`,
		`executor show | use "show [project]" | flags: `,
		`executor stats | use "stats [project]" | flags: `,
		`finish | use "finish [tracker]" | flags: --additional --dry-run --max-turns --model --no-sim --no-yolo --project/-p --sim --slot --timeout`,
		`finish claim | use "claim <tracker>" | flags: --session`,
		`finish record | use "record <tracker>" | flags: --claims-open --note --report --result --session`,
		`finish release | use "release <tracker>" | flags: --session --started`,
		`finish status | use "status <tracker>" | flags: `,
		`run-epic | use "run-epic [project] <tracker-id>" | flags: --additional --allow-dirty --base --dry-run --effort --independent --max-turns --model --no-pr --slot --then-finish --timeout --yolo`,
		`runs | use "runs" | flags: --json --local --project/-p --remote`,
		`work | use "work [project] <task-id>" | flags: --additional --allow-dirty --base --dry-run --effort --epic --max-turns --model --slot --timeout --yolo`,
		`worker-guard | use "worker-guard" | hidden | flags: --diff-base --fix-rounds --review-model --telemetry`,
	}

	var got []string
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		var flags []string
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			name := "--" + f.Name
			if f.Shorthand != "" {
				name += "/-" + f.Shorthand
			}
			flags = append(flags, name)
		})
		sort.Strings(flags)
		line := path + ` | use "` + c.Use + `"`
		if c.Hidden {
			line += " | hidden"
		}
		got = append(got, line+" | flags: "+strings.Join(flags, " "))
		for _, sub := range c.Commands() {
			walk(sub, path+" "+sub.Name())
		}
	}
	executorCommands := map[string]bool{
		"work": true, "run-epic": true, "finish": true, "runs": true, "executor": true, "worker-guard": true,
	}
	for _, c := range NewRootCmd().Commands() {
		if executorCommands[c.Name()] {
			walk(c, c.Name())
		}
	}

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("executor command tree changed\n got:\n  %s\nwant:\n  %s",
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}
