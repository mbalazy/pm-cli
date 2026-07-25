package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func TestBaselineSection(t *testing.T) {
	t.Run("green baseline", func(t *testing.T) {
		got := baselineSection("yarn validate", 0, "")
		mustContain(t, got, "## Verification baseline")
		mustContain(t, got, "GREEN")
		mustContain(t, got, "yarn validate")
		if strings.Contains(got, "```") {
			t.Error("green baseline should not render an output block")
		}
	})
	t.Run("red baseline carries output and rules", func(t *testing.T) {
		got := baselineSection("yarn validate", 42, "src/old.ts:1 no-undef")
		mustContain(t, got, "exited 42")
		mustContain(t, got, "PRE-EXISTING")
		mustContain(t, got, "MUST NOT count against your verify verdict")
		mustContain(t, got, "src/old.ts:1 no-undef")
	})
}

func TestCaptureBaseline(t *testing.T) {
	dir := t.TempDir()

	t.Run("green command", func(t *testing.T) {
		got := captureBaseline(dir, "true")
		mustContain(t, got, "GREEN")
	})
	t.Run("red command captures combined output", func(t *testing.T) {
		got := captureBaseline(dir, "echo out-line; echo err-line >&2; exit 3")
		mustContain(t, got, "exited 3")
		mustContain(t, got, "out-line")
		mustContain(t, got, "err-line")
	})
	t.Run("long output keeps the tail", func(t *testing.T) {
		got := captureBaseline(dir, `i=0; while [ $i -lt 600 ]; do echo "line-$i 0123456789"; i=$((i+1)); done; exit 1`)
		mustContain(t, got, "truncated")
		mustContain(t, got, "line-599")
		if strings.Contains(got, "\"line-0 ") {
			t.Error("head of over-cap output should be dropped, tail kept")
		}
	})
	t.Run("command that cannot run degrades to no baseline", func(t *testing.T) {
		if got := captureBaseline(filepath.Join(dir, "no-such-dir"), "true"); got != "" {
			t.Errorf("expected empty section when the command cannot run, got %q", got)
		}
	})
}

// baselineFixture mirrors executorFixture but with executor.baseline configured.
func baselineFixture(t *testing.T, baselineCmd string) (*storage.Store, *storage.Task) {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")

	if err := store.CreateProject("app", &storage.Project{
		Name: "App", Path: repo,
		Executor: &storage.Executor{Enabled: true, Baseline: baselineCmd},
	}); err != nil {
		t.Fatal(err)
	}
	task := &storage.Task{Meta: storage.TaskMeta{ID: "app-1", Title: "Fix thing", Status: storage.StatusDoing}, Project: "app"}
	if err := store.AddTask("app", task); err != nil {
		t.Fatal(err)
	}
	task, err := store.FindTask("app", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	return store, task
}

func TestPlanWorkBaselineSplit(t *testing.T) {
	store, task := baselineFixture(t, "yarn validate")

	t.Run("standalone captures its own baseline", func(t *testing.T) {
		plan, err := planWork(store, task, "app", workOptions{standalone: true, model: "opus", maxTurns: 10})
		if err != nil {
			t.Fatal(err)
		}
		if plan.baselineCmd != "yarn validate" {
			t.Errorf("baselineCmd = %q, want the executor.baseline", plan.baselineCmd)
		}
	})
	t.Run("epic sub gets the manager's capture baked into the prompt", func(t *testing.T) {
		opts := workOptions{standalone: false, model: "opus", maxTurns: 10, baseline: "## Verification baseline\nMANAGER-CAPTURED"}
		plan, err := planWork(store, task, "app", opts)
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, plan.prompt, "MANAGER-CAPTURED")
		if plan.baselineCmd != "" {
			t.Errorf("epic sub must not re-capture (baselineCmd = %q)", plan.baselineCmd)
		}
		// The prompt embedded in the argv must carry the section too.
		mustContain(t, strings.Join(plan.cmdArgs, " "), "MANAGER-CAPTURED")
	})
}

func TestExecuteWorkInjectsCapturedBaseline(t *testing.T) {
	store, task := baselineFixture(t, "echo lint-error-old; exit 2")

	promptFile := filepath.Join(t.TempDir(), "prompt.txt")
	fakeClaude(t, `printf '%s' "$2" > `+promptFile+`
echo '`+envelope("merged", "done")+`'`)

	opts := workOptions{standalone: true, model: "opus", maxTurns: 10, timeout: 30 * 1e9}
	plan, err := planWork(store, task, "app", opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeWork(store, task, plan, opts); err != nil {
		t.Fatalf("executeWork: %v", err)
	}

	prompt, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("fake worker never received a prompt: %v", err)
	}
	mustContain(t, string(prompt), "## Verification baseline")
	mustContain(t, string(prompt), "exited 2")
	mustContain(t, string(prompt), "lint-error-old")
}

func TestExecuteWorkJournalsBaselineUsage(t *testing.T) {
	store, task := baselineFixture(t, "true")
	fakeClaude(t, "echo '"+envelope("merged", "done")+"'")

	opts := workOptions{standalone: true, model: "opus", maxTurns: 10, timeout: 30 * 1e9}
	plan, err := planWork(store, task, "app", opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeWork(store, task, plan, opts); err != nil {
		t.Fatalf("executeWork: %v", err)
	}

	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	if len(entries) != 2 || entries[0].Baseline != "true" || entries[1].Baseline != "true" {
		t.Fatalf("configured + captured executor.baseline must be recorded on both journal entries: %+v", entries)
	}
}

func TestSystemPromptBaselineRules(t *testing.T) {
	exec := storage.Executor{FixRounds: 3}
	t.Run("verify gate is baseline-aware in every mode", func(t *testing.T) {
		got := buildWorkerSystemPrompt(exec, true, false)
		mustContain(t, got, "Verification baseline")
		mustContain(t, got, "PRE-EXISTING")
	})
	t.Run("independent verdict never demotes on pre-existing breakage", func(t *testing.T) {
		got := buildWorkerSystemPrompt(exec, false, true)
		mustContain(t, got, "zero NEW failures")
		mustContain(t, got, "PRE-EXISTING breakage never demotes the verdict")
	})
}
