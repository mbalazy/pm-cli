package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// findCheck returns the first finding whose message contains sub.
func findCheck(checks []check, sub string) *check {
	for i := range checks {
		if strings.Contains(checks[i].Msg, sub) {
			return &checks[i]
		}
	}
	return nil
}

func levelOf(t *testing.T, checks []check, sub string) checkLevel {
	t.Helper()
	c := findCheck(checks, sub)
	if c == nil {
		t.Fatalf("no finding mentioning %q\nfindings: %s", sub, formatChecks(checks))
	}
	return c.Level
}

func formatChecks(checks []check) string {
	var b strings.Builder
	for _, c := range checks {
		b.WriteString("\n  [" + c.Level.tag() + "] " + c.Msg)
	}
	return b.String()
}

func TestDoctorNoExecutorBlock(t *testing.T) {
	checks := runExecutorDoctor(&storage.Project{Name: "Bare", Path: t.TempDir()})
	if len(checks) != 1 || checks[0].Level != levelWarn {
		t.Fatalf("expected a single warning, got %s", formatChecks(checks))
	}
	if failed(checks, false) {
		t.Error("a missing block must not fail by default")
	}
	if !failed(checks, true) {
		t.Error("--strict must fail on the warning")
	}
}

func TestDoctorMissingProjectPath(t *testing.T) {
	proj := &storage.Project{Name: "X", Executor: &storage.Executor{Enabled: true}}
	checks := runExecutorDoctor(proj)
	if levelOf(t, checks, "no `path`") != levelError {
		t.Error("a project with no path must be an ERROR - nothing else can resolve")
	}
}

func TestDoctorHealthyProfile(t *testing.T) {
	// The playbook names every script and quotes no runtime identifier.
	proj, _ := handoffProject(t, "# playbook\nmeasure-element.py measures text; read-rn-logs.sh tails JS console.\n")
	checks := runExecutorDoctor(proj)

	for _, c := range checks {
		if c.Level != levelOK {
			t.Errorf("unexpected finding on a healthy profile: [%s] %s", c.Level.tag(), c.Msg)
		}
	}
	if failed(checks, true) {
		t.Error("a healthy profile must pass even under --strict")
	}
}

func TestDoctorPlaybookMissingIsError(t *testing.T) {
	proj, _ := handoffProject(t, "x")
	proj.Executor.Handoff.Playbook = ".claude/does-not-exist.md"

	checks := runExecutorDoctor(proj)
	if levelOf(t, checks, "handoff.playbook does not exist") != levelError {
		t.Error("a declared-but-absent playbook is a binary fact and must be an ERROR")
	}
	if !failed(checks, false) {
		t.Error("an ERROR must fail without --strict")
	}
}

func TestDoctorUnknownRuntimeSkillIsError(t *testing.T) {
	proj, _ := handoffProject(t, "x")
	proj.Executor.Handoff.RuntimeSkill = "no-such-skill"

	if levelOf(t, runExecutorDoctor(proj), "not found under .claude/skills") != levelError {
		t.Error("a declared-but-absent runtime skill must be an ERROR")
	}
}

func TestDoctorDeadContextRepoIsError(t *testing.T) {
	proj, _ := handoffProject(t, "x")
	proj.Executor.ContextRepos = map[string]string{"backend": "/definitely/not/here"}

	if levelOf(t, runExecutorDoctor(proj), "context_repos[backend] does not exist") != levelError {
		t.Error("a dead context repo must be an ERROR - the worker is promised it can read it")
	}
}

// The gap that produced this feature: scripts ship with the runtime skill, the
// playbook never names them, so an odbiór redoes their work by hand.
func TestDoctorUnmentionedScriptIsWarning(t *testing.T) {
	proj, _ := handoffProject(t, "# playbook\nonly read-rn-logs.sh is described here\n")
	checks := runExecutorDoctor(proj)

	if levelOf(t, checks, "playbook never mentions measure-element.py") != levelWarn {
		t.Error("an unmentioned script is a heuristic over prose - WARN, not ERROR")
	}
	if findCheck(checks, "never mentions read-rn-logs.sh") != nil {
		t.Error("a mentioned script must not be flagged")
	}
	if failed(checks, false) {
		t.Error("heuristics must not fail the command by default")
	}
	if !failed(checks, true) {
		t.Error("--strict must promote the warning to a failure")
	}
}

// The drift check: the playbook copies a simulator id / port that pm already
// resolves from the slots, so it lies the moment a slot changes.
func TestDoctorRuntimeDriftIsWarning(t *testing.T) {
	proj, _ := handoffProject(t,
		"# playbook\nmeasure-element.py, read-rn-logs.sh\nSecondary runtime: sim 2CE98C80-9633 on Metro 8090.\n")
	checks := runExecutorDoctor(proj)

	udid := findCheck(checks, "playbook hardcodes SIM_UDID (slot 1)")
	if udid == nil {
		t.Fatalf("expected the UDID drift finding, got %s", formatChecks(checks))
	}
	if udid.Level != levelWarn {
		t.Error("drift is a heuristic over markdown - WARN")
	}
	if !strings.Contains(udid.Msg, ":3") {
		t.Errorf("the finding must name the line so a human can judge it: %q", udid.Msg)
	}
	if !strings.Contains(udid.Hint, "Secondary runtime") {
		t.Errorf("the hint must quote the offending line: %q", udid.Hint)
	}
	if findCheck(checks, "playbook hardcodes PORT (slot 1)") == nil {
		t.Error("expected the port drift finding too")
	}
	// Slot 2's values are absent from this playbook.
	if findCheck(checks, "slot 2)") != nil {
		t.Error("values that do not appear in the playbook must not be flagged")
	}
}

// Short values ("1", "on") would match almost any prose - they are skipped so
// the heuristic stays quiet enough to be trusted.
func TestDoctorDriftSkipsShortValues(t *testing.T) {
	proj, _ := handoffProject(t, "# playbook\nmeasure-element.py read-rn-logs.sh\nset DEBUG to on when 1 fails\n")
	proj.Executor.Worktrees = []storage.WorktreeSlot{{Path: "../a", Env: map[string]string{"DEBUG": "on", "N": "1"}}}

	if c := findCheck(runExecutorDoctor(proj), "hardcodes"); c != nil {
		t.Errorf("short values must not produce drift findings, got: %s", c.Msg)
	}
}

func TestDoctorSlotPathNotAWorktree(t *testing.T) {
	proj, repo := handoffProject(t, "measure-element.py read-rn-logs.sh")
	// Point slot 1 at a real directory that is not a git worktree - exactly
	// what EnsureWorktree refuses at run time.
	proj.Executor.Worktrees = []storage.WorktreeSlot{{Path: repo, Env: map[string]string{"PORT": "8090"}}}

	if levelOf(t, runExecutorDoctor(proj), "is not a git worktree") != levelError {
		t.Error("a slot path that every --additional run would reject must be an ERROR")
	}
}

func TestDoctorUncreatedSlotIsFine(t *testing.T) {
	proj, _ := handoffProject(t, "measure-element.py read-rn-logs.sh")
	checks := runExecutorDoctor(proj)

	if levelOf(t, checks, "slot 1 not created yet") != levelOK {
		t.Error("an absent slot is created on the first --additional run - not a problem")
	}
}

func TestRenderDoctorCounts(t *testing.T) {
	checks := []check{
		{levelOK, "fine", ""},
		{levelWarn, "iffy", "do something"},
		{levelError, "broken", ""},
	}
	out := renderDoctor("demo", checks, false)
	if !strings.Contains(out, "1 error(s), 1 warning(s)") {
		t.Errorf("bad summary line:\n%s", out)
	}
	if !strings.Contains(out, "-> do something") {
		t.Errorf("hint not rendered:\n%s", out)
	}
	if strings.Contains(out, "--strict") {
		t.Error("the strict note must only show under --strict")
	}
	if !strings.Contains(renderDoctor("demo", checks, true), "[--strict: warnings fail]") {
		t.Error("expected the strict note")
	}
}

// TestDoctorCmdReturnsErrorInsteadOfExiting: a failing report must surface as
// a returned error (exit code via main), never an os.Exit inside RunE - that
// skipped deferred cleanups and made the command untestable.
func TestDoctorCmdReturnsErrorInsteadOfExiting(t *testing.T) {
	store, slug := tempStore(t)
	proj, _ := store.GetProject(slug)
	proj.Executor = &storage.Executor{
		Enabled: true,
		Handoff: storage.Handoff{Playbook: ".claude/does-not-exist.md"},
	}
	if err := store.UpdateProject(slug, proj); err != nil {
		t.Fatal(err)
	}

	cmd := newExecutorDoctorCmd(store)
	cmd.SetArgs([]string{slug})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("doctor with a dangling playbook must return an error")
	}
	if !errors.Is(err, errFound) {
		t.Fatalf("want errFound, got %v", err)
	}
}

func TestDoctorLandingStatusNotInProjectStatuses(t *testing.T) {
	t.Run("missing independent status warns without failing", func(t *testing.T) {
		proj, _ := handoffProject(t, "# playbook\nmeasure-element.py, read-rn-logs.sh\n")
		proj.Statuses = []string{"todo", "doing", "merged", "done"} // no `pushed`

		checks := runExecutorDoctor(proj)
		c := findCheck(checks, "landing status not in the project's statuses: pushed")
		if c == nil {
			t.Fatalf("expected a finding for the unlisted landing status: %s", formatChecks(checks))
		}
		if c.Level != levelWarn {
			t.Error("the run still works (it degrades to done_status), so this is a WARN, not an ERROR")
		}
		if !strings.Contains(c.Hint, "claiming a merge that never happened") {
			t.Errorf("the hint must say what goes wrong, got %q", c.Hint)
		}
		if failed(checks, false) {
			t.Error("a warning must not fail the command by default")
		}
	})

	t.Run("a fully listed profile says nothing", func(t *testing.T) {
		proj, _ := handoffProject(t, "# playbook\nmeasure-element.py, read-rn-logs.sh\n")
		if c := findCheck(runExecutorDoctor(proj), "landing status"); c != nil {
			t.Errorf("unexpected finding: [%s] %s", c.Level.tag(), c.Msg)
		}
	})
}
