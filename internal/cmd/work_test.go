package cmd

import (
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func TestResolveWorkBranch(t *testing.T) {
	t.Run("explicit branch", func(t *testing.T) {
		task := &storage.Task{Meta: storage.TaskMeta{Branch: "feat/custom", Title: "Some Title"}}
		if got := resolveWorkBranch(task); got != "feat/custom" {
			t.Errorf("got %q, want feat/custom", got)
		}
	})
	t.Run("derived from title", func(t *testing.T) {
		task := &storage.Task{Meta: storage.TaskMeta{Title: "Add CSV Export"}}
		if got := resolveWorkBranch(task); got != "feat/add-csv-export" {
			t.Errorf("got %q, want feat/add-csv-export", got)
		}
	})
}

func TestParseClaudeResult(t *testing.T) {
	t.Run("success with structured output", func(t *testing.T) {
		data := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"Done.","session_id":"abc-123","structured_output":{"status":"merged","summary":"did the thing","branch":"feat/x","commits":["a1b2c3"],"unresolved":[]}}`)
		res, sid, err := parseClaudeResult(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sid != "abc-123" {
			t.Errorf("session id = %q, want abc-123", sid)
		}
		if res.Status != "merged" || res.Branch != "feat/x" || res.Summary != "did the thing" {
			t.Errorf("unexpected result: %+v", res)
		}
		if len(res.Commits) != 1 || res.Commits[0] != "a1b2c3" {
			t.Errorf("commits = %v, want [a1b2c3]", res.Commits)
		}
	})

	t.Run("missing structured output errors but returns session id", func(t *testing.T) {
		data := []byte(`{"type":"result","is_error":true,"result":"hit max turns","session_id":"sess-9"}`)
		_, sid, err := parseClaudeResult(data)
		if err == nil {
			t.Fatal("expected error for missing structured_output, got nil")
		}
		if sid != "sess-9" {
			t.Errorf("session id = %q, want sess-9", sid)
		}
		if !strings.Contains(err.Error(), "hit max turns") {
			t.Errorf("error should surface the result text, got %v", err)
		}
	})

	t.Run("invalid json errors", func(t *testing.T) {
		if _, _, err := parseClaudeResult([]byte("not json")); err == nil {
			t.Error("expected error for invalid json")
		}
	})
}

func TestPhaseDirective(t *testing.T) {
	tests := []struct {
		name    string
		binding storage.PhaseBinding
		want    string
	}{
		{"skill", storage.PhaseBinding{Skill: "/implement"}, "run the project skill `/implement`"},
		{"cmd", storage.PhaseBinding{Cmd: "make test"}, "run shell verbatim: `make test`"},
		{"skip", storage.PhaseBinding{Skip: true}, "skip this phase"},
		{"generic", storage.PhaseBinding{}, "use the built-in generic for this phase (see system prompt)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := phaseDirective(storage.PhaseImplement, tt.binding); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildWorkerPrompt(t *testing.T) {
	proj := &storage.Project{Name: "Atlas", Path: "/repo/atlas", Stack: "RN"}
	parent := &storage.Task{Meta: storage.TaskMeta{ID: "atlas-64", Title: "Epic"},
		Body: "<!-- spec:start -->parent decisions and seams<!-- spec:end -->\nlog noise"}
	task := &storage.Task{Meta: storage.TaskMeta{
		ID: "atlas-64-3", Title: "Streaks", Parent: "atlas-64", AC: "must work offline",
	}, Body: "<!-- spec:start -->child current truth<!-- spec:end -->\nold log"}

	exec := storage.Executor{FixRounds: 3, Phases: map[string]storage.PhaseBinding{
		storage.PhaseImplement: {Skill: "/implement"},
		storage.PhaseVerify:    {Cmd: "yarn jest"},
	}}

	t.Run("standalone includes parent spec, AC, child spec, bindings", func(t *testing.T) {
		got := buildWorkerPrompt(task, parent, proj, "atlas", exec, "feat/streaks", true)
		mustContain(t, got, "#atlas-64-3 Streaks")
		mustContain(t, got, "must work offline")
		mustContain(t, got, "child current truth")
		mustContain(t, got, "parent decisions and seams")
		mustContain(t, got, "run the project skill `/implement`")
		mustContain(t, got, "run shell verbatim: `yarn jest`")
		mustContain(t, got, "Mode: standalone")
		// log noise outside the spec markers must not leak in
		if strings.Contains(got, "old log") || strings.Contains(got, "log noise") {
			t.Error("Log zone leaked into the worker prompt; only the Spec should be sent")
		}
	})

	t.Run("epic mode skips pr phase", func(t *testing.T) {
		got := buildWorkerPrompt(task, parent, proj, "atlas", exec, "feat/streaks", false)
		mustContain(t, got, "pr: skip (epic mode")
		mustContain(t, got, "Mode: epic")
	})

	t.Run("missing AC states none", func(t *testing.T) {
		noAC := &storage.Task{Meta: storage.TaskMeta{ID: "x-1", Title: "T"}, Body: "body"}
		got := buildWorkerPrompt(noAC, nil, proj, "atlas", exec, "feat/t", true)
		mustContain(t, got, "(none stated")
		if strings.Contains(got, "Parent epic spec") {
			t.Error("no parent -> should not render parent spec section")
		}
	})
}

func TestBuildWorkerSystemPrompt(t *testing.T) {
	exec := storage.Executor{FixRounds: 5}
	t.Run("standalone mentions draft PR + generics + contract", func(t *testing.T) {
		got := buildWorkerSystemPrompt(exec, true)
		mustContain(t, got, "Review->fix round cap: 5")
		mustContain(t, got, "DRAFT pull request")
		mustContain(t, got, "autonomy envelope")
		mustContain(t, got, "NEVER merge to main")
		mustContain(t, got, "status:")
	})
	t.Run("epic omits pr generic and says no PR", func(t *testing.T) {
		got := buildWorkerSystemPrompt(exec, false)
		mustContain(t, got, "do NOT open a pull request")
		if strings.Contains(got, "- pr: Open a DRAFT") {
			t.Error("epic mode should not include the pr generic")
		}
	})
}

func TestBuildClaudeArgs(t *testing.T) {
	t.Run("curated allowlist by default", func(t *testing.T) {
		args := buildClaudeArgs("p", "sp", "opus", 100, false)
		joined := strings.Join(args, " ")
		mustContain(t, joined, "--permission-mode acceptEdits")
		mustContain(t, joined, "--allowedTools")
		mustContain(t, joined, "--json-schema")
		if strings.Contains(joined, "--dangerously-skip-permissions") {
			t.Error("default run must not bypass permissions")
		}
	})
	t.Run("yolo bypasses permissions", func(t *testing.T) {
		args := buildClaudeArgs("p", "sp", "opus", 100, true)
		joined := strings.Join(args, " ")
		mustContain(t, joined, "--dangerously-skip-permissions")
		if strings.Contains(joined, "acceptEdits") {
			t.Error("yolo run should not also set acceptEdits")
		}
	})
}

func TestWorkerBriefAndLog(t *testing.T) {
	res := &workerResult{Status: "blocked", Summary: "made progress", Branch: "feat/x",
		Commits: []string{"a1", "b2"}, Unresolved: []string{"flaky test"}}
	brief := workerBrief(res, "feat/x", true)
	mustContain(t, brief, "Worker blocked on feat/x")
	mustContain(t, brief, "flaky test")
	mustContain(t, brief, "a1, b2")

	log := workerLogEntry(res, "feat/x", true)
	mustContain(t, log, "status: blocked")
	mustContain(t, log, "- flaky test")
}

func TestAppendLog(t *testing.T) {
	t.Run("empty body", func(t *testing.T) {
		if got := appendLog("", "entry"); got != "entry" {
			t.Errorf("got %q, want entry", got)
		}
	})
	t.Run("appends with separator", func(t *testing.T) {
		got := appendLog("existing", "new")
		if got != "existing\n\nnew" {
			t.Errorf("got %q", got)
		}
	})
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected output to contain %q\n---\n%s", needle, haystack)
	}
}
