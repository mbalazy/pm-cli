package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
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

// TestDoctorNoExecutorBlock: a bare project is the FRESH project, and doctor
// used to stop at "no executor block" and call it healthy - while its default
// `statuses` list holds neither landing status, so its first real `pm run-epic`
// dies at the gate. The block warning alone does not say that.
func TestDoctorNoExecutorBlock(t *testing.T) {
	checks := runExecutorDoctor(&storage.Project{Name: "Bare", Path: t.TempDir()})

	if levelOf(t, checks, "no `executor` block") != levelWarn {
		t.Error("a missing block is a completeness warning, not an error")
	}
	// DefaultStatuses is [todo doing waiting done] - no `merged`, no `pushed`.
	for _, want := range []string{"merged", "pushed"} {
		c := findCheck(checks, "landing status not in the project's statuses: "+want)
		if c == nil {
			t.Fatalf("a fresh project must be told %q is missing: %s", want, formatChecks(checks))
		}
		if c.Level != levelWarn {
			t.Errorf("landing status %q: want WARN, got %s", want, c.Level.tag())
		}
	}
	if failed(checks, false) {
		t.Error("a missing block must not fail by default")
	}
	if !failed(checks, true) {
		t.Error("--strict must fail on the warnings")
	}
}

// TestDoctorBaselineUnset: the missing baseline is the most expensive omission
// the executor journal records (workers blame pre-existing breakage on their
// own diff), and it was invisible - doctor reported 0 errors on a project that
// had gone seven runs without one.
func TestDoctorBaselineUnset(t *testing.T) {
	t.Run("unset warns", func(t *testing.T) {
		proj, _ := handoffProject(t, "# playbook\nmeasure-element.py, read-rn-logs.sh\n")
		proj.Executor.Baseline = ""

		checks := runExecutorDoctor(proj)
		c := findCheck(checks, "`baseline` is unset")
		if c == nil {
			t.Fatalf("expected a baseline finding: %s", formatChecks(checks))
		}
		if c.Level != levelWarn {
			t.Error("a run without a baseline still works (pre-0.25 behaviour), so WARN, not ERROR")
		}
		if failed(checks, false) {
			t.Error("a warning must not fail the command by default")
		}
		// The bound verify command is the strongest evidence of what the
		// baseline should be, so the hint names it instead of staying generic.
		if !strings.Contains(c.Hint, "yarn validate") {
			t.Errorf("hint should point at the configured verify cmd, got %q", c.Hint)
		}
	})

	t.Run("unset with no verify binding still warns, generically", func(t *testing.T) {
		proj, _ := handoffProject(t, "# playbook\nmeasure-element.py, read-rn-logs.sh\n")
		proj.Executor.Baseline = ""
		proj.Executor.Phases = nil

		c := findCheck(runExecutorDoctor(proj), "`baseline` is unset")
		if c == nil {
			t.Fatal("the warning must not depend on a verify binding existing")
		}
		if !strings.Contains(c.Hint, "pm executor init") {
			t.Errorf("with nothing to point at, the hint should name the tool that fills it, got %q", c.Hint)
		}
	})

	t.Run("set says so", func(t *testing.T) {
		proj, _ := handoffProject(t, "# playbook\nmeasure-element.py, read-rn-logs.sh\n")
		if levelOf(t, runExecutorDoctor(proj), "baseline -> yarn validate") != levelOK {
			t.Error("a configured baseline is a passing finding")
		}
	})
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

func TestDoctorAgentSkillsMissingIsWarn(t *testing.T) {
	proj, _ := handoffProject(t, "x")
	proj.ClaudeConfigDir = t.TempDir() // no skills at all

	checks := runExecutorDoctor(proj)
	c := levelOf(t, checks, "skill(s) solo, batch-finish-auto not found under "+proj.ClaudeConfigDir+"/skills")
	if c != levelWarn {
		t.Error("missing agent skills are machine state, not a profile defect: WARN")
	}
	if failed(checks, false) {
		t.Error("a WARN must not fail without --strict")
	}
	if !failed(checks, true) {
		t.Error("--strict must promote it")
	}
	for _, c := range checks {
		if strings.Contains(c.Msg, "not found under") && !strings.Contains(c.Hint, storage.SkillsRepoURL) {
			t.Errorf("the hint must name the skills repository: %q", c.Hint)
		}
	}

	// A project with no executor block still launches solo: the check runs
	// before the early return.
	bare := &storage.Project{Name: "bare", ClaudeConfigDir: proj.ClaudeConfigDir}
	if levelOf(t, runExecutorDoctor(bare), "skill(s) solo, batch-finish-auto not found") != levelWarn {
		t.Error("a project without an executor block must still be told its machine has no skills")
	}
}

func TestDoctorUnknownRuntimeSkillIsError(t *testing.T) {
	proj, _ := handoffProject(t, "x")
	proj.Executor.Handoff.RuntimeSkill = "no-such-skill"

	if levelOf(t, runExecutorDoctor(proj), "not found under .claude/skills") != levelError {
		t.Error("a declared-but-absent runtime skill must be an ERROR")
	}
}

func TestDoctorGlobalRuntimeSkillIsWarn(t *testing.T) {
	proj, _ := handoffProject(t, "x")
	configDir := t.TempDir()
	proj.ClaudeConfigDir = configDir
	writeFile(t, filepath.Join(configDir, "skills", "web-verify", "SKILL.md"), "# web-verify")
	proj.Executor.Handoff.RuntimeSkill = "web-verify"

	checks := runExecutorDoctor(proj)
	if levelOf(t, checks, "resolved in "+configDir+", NOT in the repo") != levelWarn {
		t.Error("a runtime skill found only in the pinned Claude config dir is legitimate (web-verify) but never silent: WARN")
	}
	if failed(checks, false) {
		t.Error("a WARN must not fail without --strict")
	}
	if !failed(checks, true) {
		t.Error("--strict promotes the global-skill WARN, the 0.49.1 concern")
	}
}

func TestDoctorRigSkill(t *testing.T) {
	t.Run("declared and resolved is a passing finding", func(t *testing.T) {
		proj, _ := handoffProject(t, "x")
		if levelOf(t, runExecutorDoctor(proj), "rig skill /start-rig") != levelOK {
			t.Error("a resolved rig skill is a passing finding")
		}
	})

	t.Run("declared but absent is an ERROR", func(t *testing.T) {
		proj, _ := handoffProject(t, "x")
		proj.Executor.Handoff.RigSkill = "no-such-rig"

		if levelOf(t, runExecutorDoctor(proj), `handoff.rig_skill "no-such-rig" not found`) != levelError {
			t.Error("a declared-but-absent rig skill is a binary fact and must be an ERROR")
		}
	})

	t.Run("unset is silence, not a warning", func(t *testing.T) {
		proj, _ := handoffProject(t, "x")
		proj.Executor.Handoff.RigSkill = ""

		if c := findCheck(runExecutorDoctor(proj), "rig"); c != nil {
			t.Errorf("an optional field left unset must produce no finding, got [%s] %s", c.Level.tag(), c.Msg)
		}
	})
}

// A declared worker config dir (pm-cli-119-1) is a filesystem fact doctor can
// check: missing = ERROR (every worker would die on its first call), present
// but never opened (no .claude.json) = WARN, present and initialised = silent.
func TestDoctorWorkerConfigDir(t *testing.T) {
	proj, _ := handoffProject(t, "x")
	missing := filepath.Join(t.TempDir(), "nope")
	proj.Executor.WorkerClaudeConfigDir = missing
	if levelOf(t, runExecutorDoctor(proj), "worker_claude_config_dir does not exist") != levelError {
		t.Error("a missing worker config dir must be an ERROR")
	}

	fresh := t.TempDir()
	proj.Executor.WorkerClaudeConfigDir = fresh
	if levelOf(t, runExecutorDoctor(proj), "has no .claude.json yet") != levelWarn {
		t.Error("an uninitialised worker config dir must be a WARN")
	}

	writeFile(t, filepath.Join(fresh, ".claude.json"), "{}")
	for _, c := range runExecutorDoctor(proj) {
		if strings.Contains(c.Msg, "worker_claude_config_dir") {
			t.Errorf("an initialised worker config dir must be silent, got %+v", c)
		}
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
// playbook never names them, so an acceptance redoes their work by hand.
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

// hermeticGitEnv isolates git's global/system config resolution AND its
// repo-location env vars for the duration of the test, via t.Setenv/
// os.Unsetenv (both affect every subprocess spawned during the test,
// including the ones checkHooksPath itself shells out to).
//
// Without the config isolation, a hooksPath test would read, and could fail
// against, the actual developer machine's ~/.gitconfig - which is exactly
// what motivated this feature (this project's own machine has a global
// core.hooksPath set). Without the GIT_DIR-family unset, a test run from
// inside a git hook (git exports GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE/... into
// hook subprocesses) would have every `git -C <tempdir>` command in this file
// silently operate on the ENCLOSING repo instead of the fixture - the same
// hazard githooks/pre-commit already guards `make check` against for exactly
// this reason. GIT_CONFIG_PARAMETERS/GIT_CONFIG_COUNT go with them: `git -c
// core.hooksPath=... commit` exports its override to every descendant,
// including the pre-commit hook's `make check`, which made all nine hooksPath
// tests resolve to the ENCLOSING repo's hooks dir - config injected by the
// invoker is no more hermetic than config read off ~/.gitconfig.
func hermeticGitEnv(t *testing.T) {
	t.Helper()
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(empty, "xdg-config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(empty, "gitconfig-unused"))
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_PREFIX", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT"} {
		if v, ok := os.LookupEnv(k); ok {
			os.Unsetenv(k)
			t.Cleanup(func() { os.Setenv(k, v) })
		}
	}
}

// writeExecutableHook writes a hook file with the exec bit set - a real hook
// file is executable, and hasHookFile now requires that to tell one apart
// from a stray README or .DS_Store sitting in the configured hooks dir.
func writeExecutableHook(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func symlinkT(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorHooksPathUnsetIsSilent covers case (a): a stock repo whose
// .git/hooks holds only git's own *.sample placeholders and whose config
// never touches core.hooksPath at all. That is the default shape of nearly
// every repo and must never produce a finding.
func TestDoctorHooksPathUnsetIsSilent(t *testing.T) {
	hermeticGitEnv(t)
	dir := t.TempDir()
	gitInitRepo(t, dir)

	checks := checkHooksPath(dir)
	if len(checks) != 0 {
		t.Errorf("no core.hooksPath configured must produce no finding, got: %s", formatChecks(checks))
	}
}

// TestDoctorHooksPathIgnoresInjectedGitConfig: a core.hooksPath handed to git
// on the command line (`git -c core.hooksPath=... commit`) travels to every
// descendant process through GIT_CONFIG_PARAMETERS - a pre-commit hook's
// `make check` included. Observed before hermeticGitEnv stripped it: every
// hooksPath test resolved to the enclosing repo's githooks dir and failed.
func TestDoctorHooksPathIgnoresInjectedGitConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.hooksPath=/definitely/not/here'")
	hermeticGitEnv(t)
	dir := t.TempDir()
	gitInitRepo(t, dir)

	if checks := checkHooksPath(dir); len(checks) != 0 {
		t.Errorf("an injected core.hooksPath must not reach the fixture, got: %s", formatChecks(checks))
	}
}

// TestDoctorHooksPathEmptyValueWarns: core.hooksPath explicitly set to the
// empty string is its own broken state, not "unset" - git resolves it to the
// project root itself (`--git-path hooks` echoes back the invocation dir),
// which without special-casing would make an unrelated executable at the
// repo root (a checked-in `configure` script, `gradlew`) read as a healthy
// hook setup.
func TestDoctorHooksPathEmptyValueWarns(t *testing.T) {
	hermeticGitEnv(t)
	dir := t.TempDir()
	gitInitRepo(t, dir)
	gitT(t, dir, "config", "core.hooksPath", "")

	checks := checkHooksPath(dir)
	if levelOf(t, checks, "configured but empty") != levelWarn {
		t.Error("an explicitly empty core.hooksPath must WARN, not read as unset or as a healthy project-root hooks dir")
	}
	if c := findCheck(checks, "git hooks ->"); c != nil {
		t.Errorf("must not report the project root as a valid hooks dir: [%s] %s", c.Level.tag(), c.Msg)
	}
}

// TestDoctorHooksPathDeadDirWarns covers case (b): core.hooksPath set
// repo-local, pointing at a directory that does not exist.
func TestDoctorHooksPathDeadDirWarns(t *testing.T) {
	hermeticGitEnv(t)
	dir := t.TempDir()
	gitInitRepo(t, dir)
	gitT(t, dir, "config", "core.hooksPath", "nonexistent-hooks")

	checks := checkHooksPath(dir)
	if levelOf(t, checks, "does not exist") != levelWarn {
		t.Error("a configured hooksPath pointing nowhere is a WARN, not silence or an ERROR - the run still works, the hooks are just absent")
	}
	c := findCheck(checks, "does not exist")
	if !strings.Contains(c.Hint, "core.hooksPath") || !strings.Contains(c.Hint, "nonexistent-hooks") {
		t.Errorf("hint must name the configured value, got %q", c.Hint)
	}
	if failed(checks, false) {
		t.Error("a warning must not fail the command by default")
	}
}

// TestDoctorHooksPathWithRealHookIsFine covers case (c): core.hooksPath set
// repo-local, pointing at a directory that holds a real hook file.
func TestDoctorHooksPathWithRealHookIsFine(t *testing.T) {
	hermeticGitEnv(t)
	dir := t.TempDir()
	gitInitRepo(t, dir)
	writeExecutableHook(t, filepath.Join(dir, "myhooks", "pre-commit"))
	gitT(t, dir, "config", "core.hooksPath", "myhooks")

	checks := checkHooksPath(dir)
	if levelOf(t, checks, "git hooks ->") != levelOK {
		t.Errorf("a configured hooksPath with a real hook file must pass, got: %s", formatChecks(checks))
	}
}

// TestDoctorHooksPathEmptyDirWarns: the configured dir exists but holds no
// real hook file (only samples, or nothing) - the same failure mode as a
// missing dir, git silently runs no hooks either way. The sample is written
// EXECUTABLE on purpose: that is the mode `git init` seeds the real ones
// with (0755), so a non-executable sample would pass this test through the
// exec-bit branch and leave the .sample skip itself unexercised.
func TestDoctorHooksPathEmptyDirWarns(t *testing.T) {
	hermeticGitEnv(t)
	dir := t.TempDir()
	gitInitRepo(t, dir)
	writeExecutableHook(t, filepath.Join(dir, "myhooks", "pre-commit.sample"))
	gitT(t, dir, "config", "core.hooksPath", "myhooks")

	if levelOf(t, checkHooksPath(dir), "no hook files") != levelWarn {
		t.Error("a configured hooksPath dir with only *.sample files has no real hooks - must WARN")
	}
}

// TestDoctorHooksPathNonExecutableFileWarns: a hook file that lost its exec
// bit is silently skipped by git, so a dir holding only one must WARN the
// same as an empty dir - not read as healthy just because a file exists.
func TestDoctorHooksPathNonExecutableFileWarns(t *testing.T) {
	hermeticGitEnv(t)
	dir := t.TempDir()
	gitInitRepo(t, dir)
	writeFile(t, filepath.Join(dir, "myhooks", "pre-commit"), "#!/bin/sh\nexit 0\n") // writeFile: 0644, no exec bit
	gitT(t, dir, "config", "core.hooksPath", "myhooks")

	if levelOf(t, checkHooksPath(dir), "no hook files") != levelWarn {
		t.Error("a non-executable file is not a hook git will ever run - must WARN")
	}
}

// TestDoctorHooksPathSymlinkedHooks covers the layout every hook manager
// produces: the configured dir holds symlinks, not files. A symlink's own
// mode is 0777, so judging it unfollowed would report ANY dir of links as
// healthy - including one git runs nothing from.
func TestDoctorHooksPathSymlinkedHooks(t *testing.T) {
	cases := []struct {
		name     string
		link     func(t *testing.T, dir, linkPath string)
		wantMsg  string
		wantWarn bool
	}{
		{
			name: "dangling link",
			link: func(t *testing.T, dir, linkPath string) {
				symlinkT(t, filepath.Join(dir, "gone"), linkPath)
			},
			wantMsg:  "no hook files",
			wantWarn: true,
		},
		{
			name: "link to a non-executable file",
			link: func(t *testing.T, dir, linkPath string) {
				target := filepath.Join(dir, "shared-hook")
				writeFile(t, target, "#!/bin/sh\nexit 0\n") // 0644
				symlinkT(t, target, linkPath)
			},
			wantMsg:  "no hook files",
			wantWarn: true,
		},
		{
			name: "link to an executable file",
			link: func(t *testing.T, dir, linkPath string) {
				target := filepath.Join(dir, "shared-hook")
				writeExecutableHook(t, target)
				symlinkT(t, target, linkPath)
			},
			wantMsg: "git hooks ->",
		},
		{
			name: "link to a directory",
			link: func(t *testing.T, dir, linkPath string) {
				if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
					t.Fatal(err)
				}
				symlinkT(t, filepath.Join(dir, "subdir"), linkPath)
			},
			wantMsg:  "no hook files",
			wantWarn: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hermeticGitEnv(t)
			dir := t.TempDir()
			gitInitRepo(t, dir)
			if err := os.MkdirAll(filepath.Join(dir, "myhooks"), 0o755); err != nil {
				t.Fatal(err)
			}
			tc.link(t, dir, filepath.Join(dir, "myhooks", "pre-commit"))
			gitT(t, dir, "config", "core.hooksPath", "myhooks")

			want := levelOK
			if tc.wantWarn {
				want = levelWarn
			}
			checks := checkHooksPath(dir)
			if got := levelOf(t, checks, tc.wantMsg); got != want {
				t.Errorf("got: %s", formatChecks(checks))
			}
		})
	}
}

// TestDoctorHooksPathNotAGitRepoIsSilent: checkHooksPath must degrade to
// silence, not a crash, when the project path is not a git repo at all.
func TestDoctorHooksPathNotAGitRepoIsSilent(t *testing.T) {
	hermeticGitEnv(t)
	if checks := checkHooksPath(t.TempDir()); len(checks) != 0 {
		t.Errorf("a non-git project path must produce no finding, got: %s", formatChecks(checks))
	}
}

// TestDoctorHooksPathResolvesAgainstProjectSubdir: a project's `path` can
// point at a subdirectory of its repo (a monorepo app dir), not the repo
// root. A relative core.hooksPath must still resolve to the same absolute
// dir git itself would use - not <project path>/<value> taken naively -
// which `git rev-parse --git-path` already guarantees by printing the value
// pre-adjusted for the invocation directory's depth (e.g. "../../myhooks").
func TestDoctorHooksPathResolvesAgainstProjectSubdir(t *testing.T) {
	hermeticGitEnv(t)
	repo := t.TempDir()
	gitInitRepo(t, repo)
	gitT(t, repo, "config", "core.hooksPath", "myhooks")
	sub := filepath.Join(repo, "app", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	checks := checkHooksPath(sub)
	want := "configured git hooks dir does not exist: " + filepath.Join(repo, "myhooks")
	c := findCheck(checks, "does not exist")
	if c == nil || c.Msg != want {
		t.Errorf("relative core.hooksPath must resolve against the repo root reached from the project subdir, got %s, want message %q", formatChecks(checks), want)
	}
}

// TestDoctorHooksPathWiredIntoRunExecutorDoctor confirms the check actually
// runs as part of the full report, not just standalone.
func TestDoctorHooksPathWiredIntoRunExecutorDoctor(t *testing.T) {
	proj, repo := handoffProject(t, "# playbook\nmeasure-element.py, read-rn-logs.sh\n") // hermeticizes itself
	gitInitRepo(t, repo)
	gitT(t, repo, "config", "core.hooksPath", "nonexistent-hooks")

	// "does not exist" alone would also match checkContextRepos/checkHandoff's
	// wording on this same fixture - match the hooks-specific phrasing so a
	// future fixture tweak can't turn this into a false pass for a DIFFERENT
	// check.
	if levelOf(t, runExecutorDoctor(proj), "configured git hooks dir does not exist") != levelWarn {
		t.Error("runExecutorDoctor must include the hooksPath check")
	}
}
