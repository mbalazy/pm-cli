package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func TestRunRig(t *testing.T) {
	dir := t.TempDir()
	var log bytes.Buffer

	t.Run("exit 0 = up, output kept", func(t *testing.T) {
		v := runRig(&log, dir, "echo RIG OK marker seen", nil)
		if !v.Up || v.Reason != "" || v.journalWord() != rigWordUp {
			t.Fatalf("verdict = %+v", v)
		}
		if !strings.Contains(v.Output, "RIG OK marker seen") {
			t.Errorf("output not kept: %q", v.Output)
		}
		if !strings.Contains(v.String(), "RIG UP") {
			t.Errorf("String() = %q", v.String())
		}
	})
	t.Run("non-zero exit = dead with the code and the output tail", func(t *testing.T) {
		v := runRig(&log, dir, "echo no marker after 30s >&2; exit 3", nil)
		if v.Up || v.Reason != "exited 3" || v.journalWord() != rigWordDead {
			t.Fatalf("verdict = %+v", v)
		}
		if !strings.Contains(v.Output, "no marker after 30s") {
			t.Errorf("stderr must be part of the kept output: %q", v.Output)
		}
		if !strings.Contains(v.String(), "RIG DEAD (exited 3)") {
			t.Errorf("String() = %q", v.String())
		}
	})
	t.Run("the slot env reaches the command", func(t *testing.T) {
		v := runRig(&log, dir, `test "$SIM_UDID" = "EBC3" && test "$ADDITIONAL_METRO_PORT" = "8090"`, []string{"SIM_UDID=EBC3", "ADDITIONAL_METRO_PORT=8090"})
		if !v.Up {
			t.Fatalf("slot env not injected: %+v", v)
		}
		if v := runRig(&log, dir, `test "$SIM_UDID" = "EBC3"`, nil); v.Up {
			t.Fatal("without env the same check must be dead - the test would otherwise prove nothing")
		}
	})
	t.Run("runs in the given dir", func(t *testing.T) {
		os.WriteFile(filepath.Join(dir, "here.txt"), []byte("x"), 0644)
		if v := runRig(&log, dir, "test -f here.txt", nil); !v.Up {
			t.Fatalf("cwd must be the slot dir: %+v", v)
		}
	})
	t.Run("timeout = dead, never an error", func(t *testing.T) {
		old := rigTimeout
		rigTimeout = 200 * time.Millisecond
		defer func() { rigTimeout = old }()
		v := runRig(&log, dir, "sleep 5", nil)
		if v.Up || !strings.HasPrefix(v.Reason, "timed out after") {
			t.Fatalf("verdict = %+v", v)
		}
	})
	t.Run("a command that cannot start = dead with the cause", func(t *testing.T) {
		v := runRig(&log, filepath.Join(dir, "no-such-dir"), "true", nil)
		if v.Up || !strings.HasPrefix(v.Reason, "could not run:") {
			t.Fatalf("verdict = %+v", v)
		}
	})
	t.Run("long output keeps the tail", func(t *testing.T) {
		v := runRig(&log, dir, `i=0; while [ $i -lt 400 ]; do echo "line-$i 0123456789012345"; i=$((i+1)); done; echo LAST; exit 1`, nil)
		if !strings.HasSuffix(v.Output, "LAST") || !strings.Contains(v.Output, "truncated") || strings.Contains(v.Output, "line-0 ") {
			t.Errorf("tail not kept / head not cut: %.120q ... %.40q", v.Output, v.Output[len(v.Output)-40:])
		}
	})
	if !strings.Contains(log.String(), "rig check in") {
		t.Errorf("every rig run must be logged: %q", log.String())
	}
}

func TestRigSection(t *testing.T) {
	up := rigSection(rigVerdict{Command: "rig-check.sh", Up: true, Output: "marker seen after 4s"}, nil, nil)
	mustContain(t, up, "## Runtime rig")
	mustContain(t, up, "`rig-check.sh` exited 0 - the rig is UP")
	mustContain(t, up, "may drive it as bound")
	mustContain(t, up, "never restart, rebuild or re-point the runtime yourself")
	mustContain(t, up, "marker seen after 4s")

	dead := rigSection(rigVerdict{Command: "rig-check.sh", Reason: "exited 1", Output: "RIG DEAD: no marker"}, nil, nil)
	mustContain(t, dead, "`rig-check.sh` exited 1 - the rig is DEAD")
	mustContain(t, dead, "SKIP the `runtime` phase entirely")
	mustContain(t, dead, "do not boot, build, install or launch anything")
	mustContain(t, dead, "`TODO: runtime verification not run - rig DEAD: exited 1`")
	mustContain(t, dead, "the gate is verify, not the rig")
	mustContain(t, dead, "RIG DEAD: no marker")
	if strings.Contains(dead, "may drive") {
		t.Error("a dead rig must not license driving the runtime")
	}
}

func TestRuntimeSubs(t *testing.T) {
	done := storage.TaskStatus("merged")
	subs := []*storage.Task{
		{Meta: storage.TaskMeta{ID: "a", Status: storage.StatusTodo, Runtime: storage.RuntimeOn}},
		{Meta: storage.TaskMeta{ID: "b", Status: storage.StatusTodo}},
		{Meta: storage.TaskMeta{ID: "c", Status: storage.StatusTodo, Runtime: storage.RuntimeOff}},
		{Meta: storage.TaskMeta{ID: "d", Status: done, Runtime: storage.RuntimeOn}},
		{Meta: storage.TaskMeta{ID: "e", Status: storage.StatusDone, Runtime: storage.RuntimeOn}},
		{Meta: storage.TaskMeta{ID: "f", Status: storage.StatusTodo, Runtime: storage.RuntimeOn, Mode: "manual"}},
		{Meta: storage.TaskMeta{ID: "g", Status: storage.StatusWaiting, Runtime: storage.RuntimeOn}},
	}
	got := runtimeSubs(subs, done)
	var ids []string
	for _, s := range got {
		ids = append(ids, s.Meta.ID)
	}
	// g is not ready, but a re-run may recover it - the rig question is "could
	// any sub of this run drive the runtime", so only landed/manual are out.
	if want := "a g"; strings.Join(ids, " ") != want {
		t.Errorf("runtimeSubs = %v, want %s", ids, want)
	}
	if runtimeSubs(subs[1:3], done) != nil {
		t.Error("no runtime-enabled sub -> nil (the run must not touch the rig)")
	}
}

// rigFixture: a project with one worktree slot, executor.rig set, and a task
// whose runtime field the caller picks.
func rigFixture(t *testing.T, runtime string) (*storage.Store, *storage.Task) {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	repo := t.TempDir()
	gitInitRepo(t, repo)
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")
	proj := &storage.Project{Name: "App", Path: repo, Executor: &storage.Executor{
		Enabled:   true,
		Worktrees: []storage.WorktreeSlot{{Path: "../app-additional", Env: map[string]string{"SIM_UDID": "EBC3"}}},
		Prepare:   "yarn install",
		Rig:       "rig-check.sh --udid $SIM_UDID",
	}}
	if err := store.CreateProject("app", proj); err != nil {
		t.Fatal(err)
	}
	task := &storage.Task{Meta: storage.TaskMeta{ID: "app-1", Title: "Visual fix", Status: storage.StatusTodo, Runtime: runtime}}
	if err := store.AddTask("app", task); err != nil {
		t.Fatal(err)
	}
	return store, task
}

func TestPlanWorkRigGating(t *testing.T) {
	t.Run("standalone --additional with runtime: on runs the rig", func(t *testing.T) {
		store, task := rigFixture(t, storage.RuntimeOn)
		plan, err := planWork(store, task, "app", workOptions{standalone: true, additional: true})
		if err != nil {
			t.Fatal(err)
		}
		if plan.rigCmd != "rig-check.sh --udid $SIM_UDID" || plan.prepare != "yarn install" {
			t.Errorf("rigCmd = %q prepare = %q", plan.rigCmd, plan.prepare)
		}
	})
	t.Run("runtime off pays nothing: no rig, prepare unchanged", func(t *testing.T) {
		for _, off := range []string{"", storage.RuntimeOff} {
			store, task := rigFixture(t, off)
			plan, err := planWork(store, task, "app", workOptions{standalone: true, additional: true})
			if err != nil {
				t.Fatal(err)
			}
			if plan.rigCmd != "" {
				t.Errorf("runtime %q: rigCmd = %q, want none", off, plan.rigCmd)
			}
			if plan.prepare != "yarn install" {
				t.Errorf("runtime %q must not affect prepare: %q", off, plan.prepare)
			}
		}
	})
	t.Run("main checkout never runs the rig (the user's own simulator)", func(t *testing.T) {
		store, task := rigFixture(t, storage.RuntimeOn)
		plan, err := planWork(store, task, "app", workOptions{standalone: true})
		if err != nil {
			t.Fatal(err)
		}
		if plan.rigCmd != "" {
			t.Errorf("rigCmd = %q on the main checkout", plan.rigCmd)
		}
	})
	t.Run("epic sub: the manager's verdict is baked in for runtime: on only", func(t *testing.T) {
		section := rigSection(rigVerdict{Command: "rig-check.sh", Up: true}, nil, nil)
		for _, tc := range []struct {
			runtime string
			want    bool
		}{{storage.RuntimeOn, true}, {"", false}, {storage.RuntimeOff, false}} {
			store, task := rigFixture(t, tc.runtime)
			opts := workOptions{standalone: false, additional: true, slotDir: "/mgr/claimed", rig: section}
			plan, err := planWork(store, task, "app", opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(plan.prompt, "## Runtime rig"); got != tc.want {
				t.Errorf("runtime %q: rig section in prompt = %v, want %v", tc.runtime, got, tc.want)
			}
			if plan.rigCmd != "" {
				t.Errorf("an epic sub must never run its own rig check (rigCmd = %q)", plan.rigCmd)
			}
		}
	})
}

func TestPrintEpicPlanRuntimeMarker(t *testing.T) {
	tracker := &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Epic"}}
	subs := []*storage.Task{
		{Meta: storage.TaskMeta{ID: "p-1-1", Status: storage.StatusTodo, Order: 10, Runtime: storage.RuntimeOn}},
		{Meta: storage.TaskMeta{ID: "p-1-2", Status: storage.StatusTodo, Order: 20}},
	}
	out := captureStdout(t, func() {
		printEpicPlan(os.Stdout, tracker, "epic/p-1", "main", storage.StatusTodo, storage.TaskStatus("merged"), subs, false, "/repo", false, nil)
	})
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "p-1-1") && !strings.Contains(line, "runtime=on"):
			t.Errorf("runtime sub not marked: %q", line)
		case strings.Contains(line, "p-1-2") && strings.Contains(line, "runtime=on"):
			t.Errorf("runtime-off sub marked: %q", line)
		}
	}
}
