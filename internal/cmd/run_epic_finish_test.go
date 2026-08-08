package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// fakePM installs a script standing in for the pm binary the chain spawns, and
// points finishChainExecutable at it. The script's own stdout/stderr is what
// chainFinish redirects into the acceptance log, so a test can read back both
// the argv the chain used and the fact that the redirection happened.
func fakePM(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pm-fake")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0755); err != nil {
		t.Fatal(err)
	}
	orig := finishChainExecutable
	finishChainExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { finishChainExecutable = orig })
	return path
}

// waitForFile polls until path is non-empty, and returns its contents. The
// chained acceptance is DETACHED - nothing waits for it - so a test that read
// the log once would be reading a file the child has not reached yet.
func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nothing was written to %s within the deadline", path)
	return ""
}

// TestResolveAutoFinish pins the flag/field precedence. The case that carries
// the design is `--then-finish=false over a tracker's auto`: the flag overrides
// in BOTH directions, so a run launched by hand can suppress a chain the
// tracker asks for - otherwise there would be no way to run an epic without its
// odbiór short of editing the task.
func TestResolveAutoFinish(t *testing.T) {
	cases := []struct {
		name       string
		opts       epicOptions
		finishMode string
		want       bool
	}{
		{"unset flag, no field", epicOptions{}, "", false},
		{"unset flag, field off", epicOptions{}, storage.FinishModeOff, false},
		{"unset flag, field auto", epicOptions{}, storage.FinishModeAuto, true},
		{"--then-finish over no field", epicOptions{thenFinish: true, thenFinishSet: true}, "", true},
		{"--then-finish over field off", epicOptions{thenFinish: true, thenFinishSet: true}, storage.FinishModeOff, true},
		{"--then-finish=false over field auto", epicOptions{thenFinish: false, thenFinishSet: true}, storage.FinishModeAuto, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveAutoFinish(tc.opts, tc.finishMode); got != tc.want {
				t.Errorf("resolveAutoFinish(%+v, %q) = %v, want %v", tc.opts, tc.finishMode, got, tc.want)
			}
		})
	}
}

// TestChainFinishSpawnsDetachedAcceptance covers the spawn itself: the real
// exec path, the real detach, the real log redirection. The argv assertions are
// the load-bearing ones - a chain that ran `pm finish` with the wrong project,
// or let the acceptance touch the simulator, would still "work" in the sense of
// starting a process.
func TestChainFinishSpawnsDetachedAcceptance(t *testing.T) {
	store, slug := tempStore(t)
	stateDir := store.ProjectDir(slug)
	workDir := t.TempDir()
	fakePM(t, `echo "argv: $@"; echo "cwd: $(pwd)"`)

	var errOut strings.Builder
	if !chainFinish(&errOut, stateDir, slug, "proj-1", workDir) {
		t.Fatalf("chainFinish must report a started acceptance, stderr:\n%s", errOut.String())
	}

	logPath := storage.FinishRunLogPath(stateDir, "proj-1")
	log := waitForFile(t, logPath)
	if !strings.Contains(log, "argv: finish proj-1 --project "+slug+" --no-sim") {
		t.Errorf("argv is wrong (project must be explicit, sim must be off):\n%s", log)
	}
	// The acceptance runs in the repo the manager ran in - in a claimed
	// worktree slot that is NOT the path in project.yaml, which is why workDir
	// is passed through rather than re-resolved.
	// Either spelling of the same directory: on macOS the temp dir is reached
	// through a symlink, and `pwd` in the child reports the logical path.
	resolvedWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "cwd: "+workDir+"\n") && !strings.Contains(log, "cwd: "+resolvedWorkDir+"\n") {
		t.Errorf("the acceptance must run in the manager's work dir (%s):\n%s", workDir, log)
	}
	if !strings.Contains(errOut.String(), "chained the odbiór of proj-1") {
		t.Errorf("the chain must announce itself on stderr:\n%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), logPath) {
		t.Errorf("stderr must name the acceptance log so it can be followed:\n%s", errOut.String())
	}
}

// TestChainFinishSkipsWhenClaimHeld: a live acceptance claim is the NORMAL
// state (a `batch-finish-auto` session started before the run ended and is
// accepting subs as they land), so the chain stands down with one line and no
// error - and, crucially, spawns nothing. A second acceptance of one run is the
// exact collision the claim exists to prevent.
func TestChainFinishSkipsWhenClaimHeld(t *testing.T) {
	store, slug := tempStore(t)
	stateDir := store.ProjectDir(slug)
	if _, err := storage.AcquireFinishClaim(stateDir, "proj-1", "sess-other"); err != nil {
		t.Fatalf("seed the claim: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "spawned")
	fakePM(t, "touch "+marker)

	var errOut strings.Builder
	if chainFinish(&errOut, stateDir, slug, "proj-1", t.TempDir()) {
		t.Error("chainFinish must not start an acceptance while the claim is held")
	}
	if !strings.Contains(errOut.String(), "already claimed by") {
		t.Errorf("the skip must say WHY, got:\n%s", errOut.String())
	}
	// Give a wrongly-spawned child a moment to exist before declaring it absent.
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a held claim must spawn nothing at all")
	}
}

// TestChainFinishSurvivesAFailedSpawn is the best-effort contract: the run has
// already succeeded, so nothing about the chain may turn into an error. Both
// failure shapes are covered - the binary cannot be resolved, and it can be
// resolved but will not start.
func TestChainFinishSurvivesAFailedSpawn(t *testing.T) {
	store, slug := tempStore(t)
	stateDir := store.ProjectDir(slug)

	t.Run("unresolvable pm binary", func(t *testing.T) {
		orig := finishChainExecutable
		finishChainExecutable = func() (string, error) { return "", os.ErrNotExist }
		t.Cleanup(func() { finishChainExecutable = orig })

		var errOut strings.Builder
		if chainFinish(&errOut, stateDir, slug, "proj-1", t.TempDir()) {
			t.Error("chainFinish must report no acceptance was started")
		}
		if !strings.Contains(errOut.String(), "cannot resolve the pm binary") {
			t.Errorf("stderr must name the failure:\n%s", errOut.String())
		}
	})

	t.Run("binary that will not start", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "not-a-binary")
		orig := finishChainExecutable
		finishChainExecutable = func() (string, error) { return missing, nil }
		t.Cleanup(func() { finishChainExecutable = orig })

		var errOut strings.Builder
		if chainFinish(&errOut, stateDir, slug, "proj-1", t.TempDir()) {
			t.Error("chainFinish must report no acceptance was started")
		}
		out := errOut.String()
		if !strings.Contains(out, "could not start the odbiór of proj-1") {
			t.Errorf("stderr must name the failure:\n%s", out)
		}
		// The message has to leave the human a way forward, since nothing else
		// will retry the acceptance.
		if !strings.Contains(out, "pm finish proj-1") {
			t.Errorf("stderr must name the manual fallback:\n%s", out)
		}
	})
}

// setTrackerFinishMode rewrites the epicRunFixture tracker's finish_mode.
func setTrackerFinishMode(t *testing.T, store *storage.Store, mode string) {
	t.Helper()
	tracker, err := store.FindTask("app", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	tracker.Meta.FinishMode = mode
	if err := store.WriteTask(tracker); err != nil {
		t.Fatal(err)
	}
}

// TestExecuteEpicChainsTheOdbior drives the whole manager loop and asserts the
// chain fires (or does not) off the tracker's finish_mode - the end-to-end path
// the feature exists for: launch a batch at night, find it accepted.
func TestExecuteEpicChainsTheOdbior(t *testing.T) {
	t.Run("finish_mode auto chains after the run", func(t *testing.T) {
		fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\necho '"+envelope("merged", "all green")+"'")
		store, _ := epicRunFixture(t)
		setTrackerFinishMode(t, store, storage.FinishModeAuto)
		fakePM(t, `echo "argv: $@"`)

		_, stderr := runEpic(t, store, epicOptions{noPR: true})

		log := waitForFile(t, storage.FinishRunLogPath(store.ProjectDir("app"), "app-1"))
		if !strings.Contains(log, "argv: finish app-1 --project app --no-sim") {
			t.Errorf("the chained acceptance got the wrong argv:\n%s", log)
		}
		if !strings.Contains(stderr, "chained the odbiór of app-1") {
			t.Errorf("the run must announce the chain:\n%s", stderr)
		}
	})

	t.Run("finish_mode off chains nothing", func(t *testing.T) {
		fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\necho '"+envelope("merged", "all green")+"'")
		store, _ := epicRunFixture(t)
		setTrackerFinishMode(t, store, storage.FinishModeOff)
		marker := filepath.Join(t.TempDir(), "spawned")
		fakePM(t, "touch "+marker)

		_, stderr := runEpic(t, store, epicOptions{noPR: true})

		time.Sleep(200 * time.Millisecond)
		if _, err := os.Stat(marker); err == nil {
			t.Error("finish_mode off must not start an acceptance")
		}
		if strings.Contains(stderr, "odbiór") {
			t.Errorf("a run that chains nothing should not mention the odbiór at all:\n%s", stderr)
		}
	})

	t.Run("--then-finish chains a tracker that never asked", func(t *testing.T) {
		fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\necho '"+envelope("merged", "all green")+"'")
		store, _ := epicRunFixture(t)
		fakePM(t, `echo "argv: $@"`)

		_, stderr := runEpic(t, store, epicOptions{noPR: true, thenFinish: true, thenFinishSet: true})

		log := waitForFile(t, storage.FinishRunLogPath(store.ProjectDir("app"), "app-1"))
		if !strings.Contains(log, "argv: finish app-1") {
			t.Errorf("--then-finish must chain the acceptance:\n%s", log)
		}
		if !strings.Contains(stderr, "chained the odbiór of app-1") {
			t.Errorf("the run must announce the chain:\n%s", stderr)
		}
	})
}

// TestPlanEpicRejectsAnInvalidFinishMode: writeTask refuses the value, so the
// only way one reaches a run is a hand-edited task file - and a run that
// silently ignored an unreadable gate would be the worst of both.
func TestPlanEpicRejectsAnInvalidFinishMode(t *testing.T) {
	store, _ := epicRunFixture(t)
	tracker, err := store.FindTask("app", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	// Straight past the store's own validation, as a hand edit would be.
	raw, err := os.ReadFile(tracker.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "status: doing", "status: doing\nfinish_mode: AUTO", 1)
	if err := os.WriteFile(tracker.FilePath, []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}

	_, err = planEpic(store, []string{"app", "app-1"}, epicOptions{})
	if err == nil || !strings.Contains(err.Error(), "invalid finish_mode") {
		t.Fatalf("planEpic err = %v, want an invalid finish_mode refusal", err)
	}
}

// TestDescribeAutoFinishNamesTheSource: --dry-run has to answer "will an odbiór
// start", and a plan that said only YES/NO would leave the reader guessing
// which of the two inputs to change.
func TestDescribeAutoFinishNamesTheSource(t *testing.T) {
	plan := &epicPlan{tracker: &storage.Task{Meta: storage.TaskMeta{ID: "proj-1"}}}

	plan.autoFinish = false
	if got := describeAutoFinish(plan, epicOptions{}); !strings.Contains(got, "NO") ||
		!strings.Contains(got, "unset") || !strings.Contains(got, "pm finish proj-1") {
		t.Errorf("off-by-default line = %q", got)
	}

	plan.tracker.Meta.FinishMode = storage.FinishModeAuto
	plan.autoFinish = true
	if got := describeAutoFinish(plan, epicOptions{}); !strings.Contains(got, "YES") ||
		!strings.Contains(got, "tracker finish_mode") {
		t.Errorf("tracker-driven line = %q", got)
	}

	plan.autoFinish = false
	if got := describeAutoFinish(plan, epicOptions{thenFinishSet: true}); !strings.Contains(got, "NO") ||
		!strings.Contains(got, "--then-finish") {
		t.Errorf("flag-suppressed line must credit the flag, not the tracker: %q", got)
	}
}
