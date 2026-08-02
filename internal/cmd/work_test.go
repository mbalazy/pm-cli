package cmd

import (
	"os"
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
		data := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"Done.","session_id":"abc-123","num_turns":42,"total_cost_usd":1.25,"structured_output":{"status":"verified","summary":"did the thing","branch":"feat/x","commits":["a1b2c3"],"unresolved":[]}}`)
		res, sid, err := parseClaudeResult(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sid != "abc-123" {
			t.Errorf("session id = %q, want abc-123", sid)
		}
		if res.Status != workerVerified || res.Branch != "feat/x" || res.Summary != "did the thing" {
			t.Errorf("unexpected result: %+v", res)
		}
		if len(res.Commits) != 1 || res.Commits[0] != "a1b2c3" {
			t.Errorf("commits = %v, want [a1b2c3]", res.Commits)
		}
		if res.Turns != 42 || res.CostUSD != 1.25 {
			t.Errorf("envelope stats not lifted: turns=%d cost=%v, want 42/1.25", res.Turns, res.CostUSD)
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
		got := buildWorkerPrompt(task, parent, proj, "atlas", exec, "feat/streaks", proj.Path, true)
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
		got := buildWorkerPrompt(task, parent, proj, "atlas", exec, "feat/streaks", proj.Path, false)
		mustContain(t, got, "pr: skip (epic mode")
		mustContain(t, got, "Mode: epic")
	})

	t.Run("missing AC states none", func(t *testing.T) {
		noAC := &storage.Task{Meta: storage.TaskMeta{ID: "x-1", Title: "T"}, Body: "body"}
		got := buildWorkerPrompt(noAC, nil, proj, "atlas", exec, "feat/t", proj.Path, true)
		mustContain(t, got, "(none stated")
		if strings.Contains(got, "Parent epic spec") {
			t.Error("no parent -> should not render parent spec section")
		}
	})

	t.Run("falls back to AC section in spec body when frontmatter ac empty", func(t *testing.T) {
		bodyAC := &storage.Task{Meta: storage.TaskMeta{ID: "x-2", Title: "T"},
			Body: "<!-- spec:start -->\n## Description\nbuild it\n\n## Acceptance criteria\n- works offline\n- under 2s\n<!-- spec:end -->"}
		got := buildWorkerPrompt(bodyAC, nil, proj, "atlas", exec, "feat/t", proj.Path, true)
		mustContain(t, got, "- works offline")
		mustContain(t, got, "- under 2s")
		if strings.Contains(got, "(none stated") {
			t.Error("AC present in spec body should not render the none-stated fallback")
		}
	})

	t.Run("context repos render sorted and read-only", func(t *testing.T) {
		withRepos := exec
		withRepos.ContextRepos = map[string]string{
			"backend": "../platform.orbit",
			"api":     "~/repos/api-docs",
		}
		got := buildWorkerPrompt(task, nil, proj, "atlas", withRepos, "feat/t", proj.Path, true)
		mustContain(t, got, "## Reference repos (READ-ONLY)")
		mustContain(t, got, "- api: ~/repos/api-docs")
		mustContain(t, got, "- backend: ../platform.orbit")
		mustContain(t, got, "NEVER modify")
		if strings.Index(got, "- api:") > strings.Index(got, "- backend:") {
			t.Error("context repos should render in sorted key order")
		}
	})

	t.Run("no context repos -> no section", func(t *testing.T) {
		got := buildWorkerPrompt(task, nil, proj, "atlas", exec, "feat/t", proj.Path, true)
		if strings.Contains(got, "Reference repos") {
			t.Error("empty context_repos must not render the section")
		}
	})
}

func TestDisplayStatus(t *testing.T) {
	tests := []struct {
		name        string
		status      string
		standalone  bool
		independent bool
		want        string
	}{
		{"standalone ends in a draft PR", workerVerified, true, false, "ready (draft PR)"},
		{"batch sub is pushed, never merged", workerVerified, false, true, "ready (pushed)"},
		{"integration sub really is merged", workerVerified, false, false, "verified (merging into the epic branch)"},
		{"non-green statuses are verbatim", workerBlocked, true, false, "blocked"},
		{"failed is verbatim in batch mode too", workerFailed, false, true, "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := displayStatus(tt.status, tt.standalone, tt.independent); got != tt.want {
				t.Errorf("displayStatus(%q, standalone=%v, independent=%v) = %q, want %q",
					tt.status, tt.standalone, tt.independent, got, tt.want)
			}
		})
	}
}

// The pre-0.34 spelling still arrives from an in-flight worker started before an
// upgrade, so it has to keep resolving to the same verdict as the new one.
func TestNormalizeWorkerStatusFoldsLegacyMerged(t *testing.T) {
	if got := normalizeWorkerStatus("merged"); got != workerVerified {
		t.Errorf("legacy %q must normalize to %q, got %q", "merged", workerVerified, got)
	}
	for _, s := range []string{workerVerified, workerBlocked, workerFailed, "weird"} {
		if got := normalizeWorkerStatus(s); got != s {
			t.Errorf("normalizeWorkerStatus(%q) = %q, want it untouched", s, got)
		}
	}
}

func TestBuildWorkerSystemPrompt(t *testing.T) {
	exec := storage.Executor{FixRounds: 5}
	t.Run("standalone mentions draft PR + generics + contract", func(t *testing.T) {
		got := buildWorkerSystemPrompt(exec, true, false)
		mustContain(t, got, "Review->fix round cap: 5")
		mustContain(t, got, "DRAFT pull request")
		mustContain(t, got, "autonomy envelope")
		mustContain(t, got, "NEVER merge to main")
		mustContain(t, got, "status:")
	})
	t.Run("epic omits pr generic and says no PR", func(t *testing.T) {
		got := buildWorkerSystemPrompt(exec, false, false)
		mustContain(t, got, "do NOT open a pull request")
		if strings.Contains(got, "- pr: Open a DRAFT") {
			t.Error("epic mode should not include the pr generic")
		}
	})
	t.Run("independent adds the best-effort protocol", func(t *testing.T) {
		got := buildWorkerSystemPrompt(exec, false, true)
		mustContain(t, got, "Independent batch mode (BEST-EFFORT)")
		mustContain(t, got, "ASSUMPTION: ")
		mustContain(t, got, "verify: <how>")
		mustContain(t, got, "TODO: ")
		mustContain(t, got, "NEVER stop early")
	})
	t.Run("non-independent omits the best-effort protocol", func(t *testing.T) {
		got := buildWorkerSystemPrompt(exec, false, false)
		if strings.Contains(got, "Independent batch mode") {
			t.Error("integration epic mode must not carry the best-effort protocol")
		}
	})
}

func TestBuildClaudeArgs(t *testing.T) {
	t.Run("curated allowlist by default", func(t *testing.T) {
		args := buildClaudeArgs("p", "sp", "sess-1", "opus", 100, false)
		joined := strings.Join(args, " ")
		mustContain(t, joined, "--permission-mode acceptEdits")
		mustContain(t, joined, "--allowedTools")
		mustContain(t, joined, "--json-schema")
		mustContain(t, joined, "--session-id sess-1")
		if strings.Contains(joined, "--dangerously-skip-permissions") {
			t.Error("default run must not bypass permissions")
		}
	})
	t.Run("yolo bypasses permissions", func(t *testing.T) {
		args := buildClaudeArgs("p", "sp", "sess-1", "opus", 100, true)
		joined := strings.Join(args, " ")
		mustContain(t, joined, "--dangerously-skip-permissions")
		if strings.Contains(joined, "acceptEdits") {
			t.Error("yolo run should not also set acceptEdits")
		}
	})
}

func TestWorkerEnvStripsAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok-secret")
	t.Setenv("PATH", "/usr/bin") // a var that must survive
	env := workerEnv("")
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Error("workerEnv must strip ANTHROPIC_API_KEY (forces API billing instead of subscription)")
		}
		if strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			t.Error("workerEnv must strip ANTHROPIC_AUTH_TOKEN")
		}
	}
	var keptPath bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			keptPath = true
		}
	}
	if !keptPath {
		t.Error("workerEnv must preserve unrelated env vars like PATH")
	}
}

func TestWorkerEnvMarksHeadless(t *testing.T) {
	// The marker must land exactly once REGARDLESS of what the parent env
	// carries: a pm worker running the suite already has PM_HEADLESS=1 in its
	// own environment, and a naive append would leave two entries.
	cases := []struct {
		name      string
		inherited string // PM_HEADLESS value in the parent env ("" = unset)
	}{
		{name: "clean env"},
		{name: "nested worker", inherited: "1"},
		{name: "nested worker, odd value", inherited: "yes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.inherited != "" {
				t.Setenv("PM_HEADLESS", tc.inherited)
			} else {
				t.Setenv("PM_HEADLESS", "") // t.Setenv restores the original after the test
				os.Unsetenv("PM_HEADLESS")
			}
			var got []string
			for _, kv := range workerEnv("") {
				if strings.HasPrefix(kv, "PM_HEADLESS=") {
					got = append(got, kv)
				}
			}
			if len(got) != 1 || got[0] != "PM_HEADLESS=1" {
				t.Errorf("workerEnv must set PM_HEADLESS=1 exactly once (project hooks skip sim/Metro boot on it), got %v", got)
			}
		})
	}
}

func TestWorkerEnvPinsConfigDir(t *testing.T) {
	// Inherit a config dir from the parent env; treat it as the resolved default.
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited/dir")

	// Non-default config dir -> pinned exactly once, replacing the inherited one.
	custom := "/Users/test/.claude-alt"
	var got []string
	for _, kv := range workerEnv(custom) {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			got = append(got, kv)
		}
	}
	if len(got) != 1 || got[0] != "CLAUDE_CONFIG_DIR="+custom {
		t.Errorf("expected exactly one CLAUDE_CONFIG_DIR=%s, got %v", custom, got)
	}

	// Passing the resolved default dir must NOT add an extra pin: the single
	// inherited value passes through untouched (no duplicate).
	def := storage.DefaultClaudeConfigDir() // == "/inherited/dir" here
	count := 0
	for _, kv := range workerEnv(def) {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("default config dir should leave exactly the one inherited CLAUDE_CONFIG_DIR, got %d", count)
	}
}

func TestWorkerBriefAndLog(t *testing.T) {
	res := &workerResult{Status: "blocked", Summary: "made progress", Branch: "feat/x",
		Commits: []string{"a1", "b2"}, Unresolved: []string{"flaky test"}}
	brief := workerBrief(res, "feat/x", true, false)
	mustContain(t, brief, "Worker blocked on feat/x")
	mustContain(t, brief, "flaky test")
	mustContain(t, brief, "a1, b2")

	log := workerLogEntry(res, "feat/x", true, false)
	mustContain(t, log, "status: blocked")
	mustContain(t, log, "- flaky test")

	// A green batch sub must never read as merged: nothing is merged in that
	// mode, the branch is pushed and a human still owes the acceptance pass.
	green := &workerResult{Status: workerVerified, Summary: "done", Branch: "feat/x"}
	batchBrief := workerBrief(green, "feat/x", false, true)
	mustContain(t, batchBrief, "ready (pushed)")
	if strings.Contains(batchBrief, "Worker merged") {
		t.Errorf("batch brief must not claim a merge: %q", batchBrief)
	}
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

// TestWarnInertFlags: --slot and (for `pm work`) --base do nothing without
// --additional. They stay warnings rather than errors so no existing script's
// exit code changes.
func TestWarnInertFlags(t *testing.T) {
	warn := func(additional bool, slotPin int, base string) string {
		var buf strings.Builder
		warnInertFlags(&buf, "pm work", additional, slotPin, base)
		return buf.String()
	}

	if got := warn(false, 2, ""); !strings.Contains(got, "--slot 2 ignored without --additional") {
		t.Errorf("--slot without --additional should warn, got %q", got)
	}
	if got := warn(false, 0, "development"); !strings.Contains(got, "--base development ignored without --additional") {
		t.Errorf("--base without --additional should warn, got %q", got)
	}
	if got := warn(true, 2, "development"); got != "" {
		t.Errorf("with --additional both flags apply - no warning expected, got %q", got)
	}
	if got := warn(false, 0, ""); got != "" {
		t.Errorf("nothing inert, nothing to say - got %q", got)
	}
	// `pm run-epic --base` DOES apply in default mode, so the manager passes an
	// empty base and must stay silent about it.
	if got := warn(false, 0, "  "); got != "" {
		t.Errorf("a blank base is unset, not inert - got %q", got)
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected output to contain %q\n---\n%s", needle, haystack)
	}
}

// TestApplyWorkerResultFreshRead encodes the stale-write hazard: the task copy
// the executor holds was read BEFORE the worker ran; an edit made meanwhile
// from another session (here: a link + a log note) must survive the result
// write.
func TestApplyWorkerResultFreshRead(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	if err := store.CreateProject("app", &storage.Project{Name: "App"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTask("app", &storage.Task{Meta: storage.TaskMeta{ID: "app-1", Title: "T", Status: storage.StatusDoing}}); err != nil {
		t.Fatal(err)
	}

	stale, err := store.FindTask("app", "app-1")
	if err != nil {
		t.Fatal(err)
	}

	// "Another session" edits the task while the worker runs.
	other, _ := store.FindTask("app", "app-1")
	other.Meta.Links = map[string]string{"pr": "https://example.com/pr/1"}
	other.Body = "mid-run note"
	if err := store.WriteTask(other); err != nil {
		t.Fatal(err)
	}

	res := &workerResult{Status: workerVerified, Summary: "done", Branch: "feat/x", Commits: []string{"abc"}}
	if err := applyWorkerResult(os.Stderr, store, stale, "feat/x", "sess-1", res, true, false); err != nil {
		t.Fatal(err)
	}

	final, err := store.FindTask("app", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if final.Meta.Links["pr"] != "https://example.com/pr/1" {
		t.Fatalf("mid-run link clobbered by stale worker write: %v", final.Meta.Links)
	}
	if !strings.Contains(final.Body, "mid-run note") {
		t.Fatalf("mid-run log note clobbered: %q", final.Body)
	}
	if !strings.Contains(final.Body, "Worker run") {
		t.Fatalf("worker log entry missing: %q", final.Body)
	}
	if stale.Meta.Links["pr"] == "" {
		t.Fatal("caller's task copy not refreshed in place")
	}
}
