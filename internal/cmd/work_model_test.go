package cmd

import (
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

func TestResolveWorkerModelPrecedence(t *testing.T) {
	tests := []struct {
		name                    string
		flagValue               string
		flagSet                 bool
		taskModel, profileModel string
		wantModel, wantSource   string
	}{
		{"nothing set anywhere", "opus", false, "", "", "opus", "built-in default"},
		{"profile beats the flag DEFAULT", "opus", false, "", "sonnet", "sonnet", "executor.model"},
		{"task beats the profile", "opus", false, "haiku", "sonnet", "haiku", "task model:"},
		{"explicit flag beats the task and the profile", "opus", true, "haiku", "sonnet", "opus", "--model"},
		// The flag can pin the default explicitly: "passed" and "left at the
		// default" are different things, which is the whole reason the Set bit exists.
		{"explicit flag equal to the default still wins", "opus", true, "", "sonnet", "opus", "--model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, source := resolveWorkerModel(tt.flagValue, tt.flagSet, tt.taskModel, tt.profileModel)
			if model != tt.wantModel || source != tt.wantSource {
				t.Fatalf("resolveWorkerModel = (%q, %q), want (%q, %q)", model, source, tt.wantModel, tt.wantSource)
			}
		})
	}
}

func TestResolveWorkerEffort(t *testing.T) {
	t.Run("unset everywhere passes no flag", func(t *testing.T) {
		effort, source, err := resolveWorkerEffort("", false, "")
		if err != nil || effort != "" || source != "unset" {
			t.Fatalf("got (%q, %q, %v)", effort, source, err)
		}
	})
	t.Run("profile beats nothing, flag beats profile", func(t *testing.T) {
		effort, source, err := resolveWorkerEffort("", false, "medium")
		if err != nil || effort != "medium" || source != "executor.effort" {
			t.Fatalf("profile: got (%q, %q, %v)", effort, source, err)
		}
		effort, source, err = resolveWorkerEffort("high", true, "medium")
		if err != nil || effort != "high" || source != "--effort" {
			t.Fatalf("flag: got (%q, %q, %v)", effort, source, err)
		}
	})
	t.Run("an invalid level is a plan error naming its source", func(t *testing.T) {
		_, _, err := resolveWorkerEffort("", false, "Medium")
		if err == nil || !strings.Contains(err.Error(), "executor.effort") || !strings.Contains(err.Error(), "Medium") {
			t.Fatalf("profile typo must fail loudly with its source, got %v", err)
		}
		_, _, err = resolveWorkerEffort("turbo", true, "")
		if err == nil || !strings.Contains(err.Error(), "--effort") {
			t.Fatalf("flag typo must fail loudly with its source, got %v", err)
		}
	})
}

// TestPlanWorkResolvesModelAndEffort: the values have to reach the ARGV, and
// the plan has to remember them as set, because retarget/baseline re-render
// the argv later from plan.opts - a second resolution there would be a bug
// waiting for the first project with executor.model.
func TestPlanWorkResolvesModelAndEffort(t *testing.T) {
	store, _ := epicFixture(t, &storage.Executor{
		Enabled: true, StartStatus: "todo", DoneStatus: "merged",
		Model: "sonnet", Effort: "medium",
	})
	task, err := store.FindTask("app", "app-1-1")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := planWork(store, task, "app", workOptions{standalone: true, model: "opus", timeout: defaultWorkerTimeout})
	if err != nil {
		t.Fatalf("planWork: %v", err)
	}
	joined := strings.Join(plan.cmdArgs, " ")
	mustContain(t, joined, "--model sonnet")
	mustContain(t, joined, "--effort medium")
	if !plan.opts.modelSet || !plan.opts.effortSet || plan.opts.model != "sonnet" || plan.opts.effort != "medium" {
		t.Fatalf("plan.opts must carry the resolved pair as set, got %+v", plan.opts)
	}
	if plan.modelSource != "executor.model" || plan.effortSource != "executor.effort" {
		t.Fatalf("sources = %q / %q", plan.modelSource, plan.effortSource)
	}
	// retarget re-renders the argv from plan.opts: the resolved pair survives.
	plan.retarget(plan.workDir, nil)
	joined = strings.Join(plan.cmdArgs, " ")
	mustContain(t, joined, "--model sonnet")
	mustContain(t, joined, "--effort medium")

	// An explicit flag beats the profile; the task's own model beats the flag default.
	plan, err = planWork(store, task, "app", workOptions{standalone: true, model: "opus", modelSet: true, effort: "high", effortSet: true, timeout: defaultWorkerTimeout})
	if err != nil {
		t.Fatalf("planWork with flags: %v", err)
	}
	joined = strings.Join(plan.cmdArgs, " ")
	mustContain(t, joined, "--model opus")
	mustContain(t, joined, "--effort high")

	task.Meta.Model = "haiku"
	plan, err = planWork(store, task, "app", workOptions{standalone: true, model: "opus", timeout: defaultWorkerTimeout})
	if err != nil {
		t.Fatalf("planWork with task model: %v", err)
	}
	mustContain(t, strings.Join(plan.cmdArgs, " "), "--model haiku")
	if plan.modelSource != "task model:" {
		t.Fatalf("task model must be the named source, got %q", plan.modelSource)
	}
}

func TestPlanWorkRejectsInvalidProfileEffort(t *testing.T) {
	store, _ := epicFixture(t, &storage.Executor{
		Enabled: true, StartStatus: "todo", DoneStatus: "merged", Effort: "ultra",
	})
	task, err := store.FindTask("app", "app-1-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planWork(store, task, "app", workOptions{standalone: true, model: "opus", timeout: defaultWorkerTimeout}); err == nil || !strings.Contains(err.Error(), "ultra") {
		t.Fatalf("an invalid executor.effort must fail the plan, got %v", err)
	}
}

// TestPlanEpicResolvesModelAndEffort: the manager resolves the pair ONCE for the
// run; a sub's own `model:` is applied per sub in executeEpic on top of it.
func TestPlanEpicResolvesModelAndEffort(t *testing.T) {
	store, _ := epicFixture(t, &storage.Executor{
		Enabled: true, StartStatus: "todo", DoneStatus: "merged",
		Model: "sonnet", Effort: "medium",
	})
	plan, err := planEpic(store, []string{"app", "app-1"}, epicOptions{model: "opus", timeout: defaultWorkerTimeout})
	if err != nil {
		t.Fatalf("planEpic: %v", err)
	}
	if plan.model != "sonnet" || plan.modelSource != "executor.model" || plan.effort != "medium" {
		t.Fatalf("plan = model %q (%s) effort %q", plan.model, plan.modelSource, plan.effort)
	}
	plan, err = planEpic(store, []string{"app", "app-1"}, epicOptions{model: "opus", modelSet: true, effort: "low", effortSet: true, timeout: defaultWorkerTimeout})
	if err != nil {
		t.Fatalf("planEpic with flags: %v", err)
	}
	if plan.model != "opus" || plan.modelSource != "--model" || plan.effort != "low" {
		t.Fatalf("flags must win: model %q (%s) effort %q", plan.model, plan.modelSource, plan.effort)
	}
}

func TestBuildClaudeArgsToolsAndEffort(t *testing.T) {
	joined := strings.Join(buildClaudeArgs("p", "sp", "s", "opus", "", 10, false, guardOptions{}, "", false, nil), " ")
	mustContain(t, joined, "--tools "+workerTools)
	if strings.Contains(joined, "--effort") {
		t.Error("no effort resolved means no --effort flag - Claude's own default must apply")
	}
	for _, name := range []string{"Bash", "Edit", "Write", "Read", "Grep", "Glob", "Agent", "Task", "Skill"} {
		if !strings.Contains(","+workerTools+",", ","+name+",") {
			t.Errorf("workerTools must keep %s - the inner loop uses it", name)
		}
	}
	joined = strings.Join(buildClaudeArgs("p", "sp", "s", "opus", "medium", 10, true, guardOptions{}, "", false, nil), " ")
	mustContain(t, joined, "--effort medium")
	mustContain(t, joined, "--tools "+workerTools)
}
