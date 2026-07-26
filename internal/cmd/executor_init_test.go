package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func containsNote(notes []string, want string) bool {
	for _, n := range notes {
		if n == want {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPhaseForName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"implement", storage.PhaseImplement},
		{"review-pr", storage.PhaseReview}, // review before pr
		{"review", storage.PhaseReview},
		{"test", storage.PhaseTest},
		{"pr", storage.PhasePR},
		{"create-pr", storage.PhasePR},
		{"humanizer", ""},
		{"tui-patterns", ""},
	}
	for _, tt := range tests {
		if got := phaseForName(tt.name); got != tt.want {
			t.Errorf("phaseForName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestDetectVerifyCmd(t *testing.T) {
	t.Run("go project", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "module x\n")
		if got := detectVerifyCmd(dir); got != "go test ./... && go vet ./..." {
			t.Errorf("got %q", got)
		}
	})

	t.Run("node yarn project composes scripts", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "yarn.lock"), "")
		writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"test":"jest","typecheck":"tsc --noEmit","build":"x"}}`)
		got := detectVerifyCmd(dir)
		if got != "yarn test && yarn typecheck" {
			t.Errorf("got %q, want 'yarn test && yarn typecheck'", got)
		}
	})

	t.Run("node prefers aggregate script", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"test":"jest","verify":"run-all"}}`)
		if got := detectVerifyCmd(dir); got != "npm run verify" {
			t.Errorf("got %q, want 'npm run verify'", got)
		}
	})

	t.Run("unknown stack returns empty", func(t *testing.T) {
		if got := detectVerifyCmd(t.TempDir()); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestDraftExecutor(t *testing.T) {
	t.Run("bare project -> all generic, no phases", func(t *testing.T) {
		e, _ := draftExecutor(t.TempDir())
		if !e.Enabled || e.AdditionalWorktree {
			t.Errorf("enabled/additional_worktree = %v/%v, want true/false", e.Enabled, e.AdditionalWorktree)
		}
		if e.FixRounds != 3 || e.DoneStatus != "merged" {
			t.Errorf("defaults wrong: fix_rounds=%d done=%q", e.FixRounds, e.DoneStatus)
		}
		if len(e.Phases) != 0 {
			t.Errorf("bare project should have no phase bindings, got %v", e.Phases)
		}
		// every phase resolves to generic
		for _, p := range storage.ExecutorPhases {
			if e.Phase(p).Kind() != storage.BindGeneric {
				t.Errorf("phase %q not generic", p)
			}
		}
	})

	t.Run("skills + go stack -> skill + cmd bindings", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "module x\n")
		for _, s := range []string{"implement", "review-pr", "test", "pr", "humanizer"} {
			writeFile(t, filepath.Join(dir, ".claude", "skills", s, "SKILL.md"), "---\nname: "+s+"\ndescription: does "+s+"\n---\n")
		}
		e, notes := draftExecutor(dir)

		if got := e.Phase(storage.PhaseImplement); got.Kind() != storage.BindSkill || got.Skill != "/implement" {
			t.Errorf("implement = %+v", got)
		}
		if got := e.Phase(storage.PhaseReview); got.Skill != "/review-pr" {
			t.Errorf("review = %+v, want /review-pr", got)
		}
		if got := e.Phase(storage.PhaseTest); got.Skill != "/test" {
			t.Errorf("test = %+v", got)
		}
		if got := e.Phase(storage.PhasePR); got.Skill != "/pr" {
			t.Errorf("pr = %+v", got)
		}
		if got := e.Phase(storage.PhaseVerify); got.Kind() != storage.BindCmd || got.Cmd != "go test ./... && go vet ./..." {
			t.Errorf("verify = %+v", got)
		}
		if e.Baseline != "go test ./... && go vet ./..." {
			t.Errorf("baseline = %q, want same as verify cmd", e.Baseline)
		}
		// humanizer is not a phase -> must not appear
		for _, b := range e.Phases {
			if b.Skill == "/humanizer" {
				t.Error("humanizer should not bind to any phase")
			}
		}
		if len(notes) == 0 {
			t.Error("expected detection notes")
		}
		if !containsNote(notes, "baseline -> cmd: go test ./... && go vet ./...") {
			t.Errorf("expected baseline detection note, got %v", notes)
		}
	})

	t.Run("bare project -> no baseline detected, no guessing", func(t *testing.T) {
		e, notes := draftExecutor(t.TempDir())
		if e.Baseline != "" {
			t.Errorf("baseline = %q, want empty when no verify command detected", e.Baseline)
		}
		if !containsNote(notes, "baseline -> unset (no stack verify command detected)") {
			t.Errorf("expected baseline-unset note, got %v", notes)
		}
	})

	t.Run("commands also detected", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".claude", "commands", "implement.md"), "---\ndescription: impl\n---\n")
		e, _ := draftExecutor(dir)
		if got := e.Phase(storage.PhaseImplement); got.Skill != "/implement" {
			t.Errorf("implement command not detected: %+v", got)
		}
	})
}

func TestMarshalExecutorBlock(t *testing.T) {
	e, _ := draftExecutor(t.TempDir())
	out := marshalExecutorBlock(e)
	mustContain(t, out, "executor:")
	mustContain(t, out, "enabled: true")
	mustContain(t, out, "fix_rounds: 3")
}

func TestMergeExecutor(t *testing.T) {
	t.Run("no existing block -> draft written as-is", func(t *testing.T) {
		draft, _ := draftExecutor(t.TempDir())
		result, notes := mergeExecutor(nil, draft)
		if result != draft {
			t.Errorf("expected draft to pass through unchanged, got a different pointer/value")
		}
		if len(notes) != 0 {
			t.Errorf("expected no preserved-field notes with no existing block, got %v", notes)
		}
	})

	t.Run("existing hand-set fields survive, phases/baseline refresh", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "module x\n")

		existing := &storage.Executor{
			Enabled:            false,
			AdditionalWorktree: true,
			WorktreePath:       "../proj-additional",
			Worktrees: []storage.WorktreeSlot{
				{Path: "../proj-slot-1", Env: map[string]string{"SIM_UDID": "abc"}},
			},
			BaseBranch:   "development",
			Env:          map[string]string{"SIM_UDID": "abc"},
			SeedExclude:  []string{"node_modules"},
			Prepare:      "yarn install --frozen-lockfile",
			Baseline:     "stale-hand-tuned-check",
			ContextRepos: map[string]string{"backend": "../platform"},
			StartStatus:  "ready",
			WipStatus:    "in-progress",
			DoneStatus:   "shipped",
			FixRounds:    5,
			Gate:         storage.Gate{PR: storage.GateAuto, Merge: storage.GateHuman},
			Phases: map[string]storage.PhaseBinding{
				storage.PhaseReview: {Skill: "/hand-picked-review"},
			},
			Notes: "hand-written notes, do not clobber",
		}

		draft, _ := draftExecutor(dir)
		result, notes := mergeExecutor(existing, draft)

		// detected fields refreshed from the draft
		if result.Baseline != "go test ./... && go vet ./..." {
			t.Errorf("baseline not refreshed: %q", result.Baseline)
		}
		if result.Phase(storage.PhaseVerify).Cmd != "go test ./... && go vet ./..." {
			t.Errorf("phases not refreshed: %+v", result.Phases)
		}
		if _, ok := result.Phases[storage.PhaseReview]; ok {
			t.Errorf("stale hand-picked phase binding should be replaced by the fresh draft, got %+v", result.Phases)
		}

		// hand-set fields preserved untouched
		if result.WorktreePath != "../proj-additional" {
			t.Errorf("worktree_path clobbered: %q", result.WorktreePath)
		}
		if len(result.Worktrees) != 1 || result.Worktrees[0].Path != "../proj-slot-1" {
			t.Errorf("worktrees clobbered: %+v", result.Worktrees)
		}
		if result.BaseBranch != "development" {
			t.Errorf("base_branch clobbered: %q", result.BaseBranch)
		}
		if result.Env["SIM_UDID"] != "abc" {
			t.Errorf("env clobbered: %+v", result.Env)
		}
		if len(result.SeedExclude) != 1 || result.SeedExclude[0] != "node_modules" {
			t.Errorf("seed_exclude clobbered: %+v", result.SeedExclude)
		}
		if result.Prepare != "yarn install --frozen-lockfile" {
			t.Errorf("prepare clobbered: %q", result.Prepare)
		}
		if result.ContextRepos["backend"] != "../platform" {
			t.Errorf("context_repos clobbered: %+v", result.ContextRepos)
		}
		if result.FixRounds != 5 {
			t.Errorf("fix_rounds clobbered: %d", result.FixRounds)
		}
		if result.Gate.PR != storage.GateAuto {
			t.Errorf("gate clobbered: %+v", result.Gate)
		}
		if result.Notes != "hand-written notes, do not clobber" {
			t.Errorf("notes clobbered: %q", result.Notes)
		}
		if !result.AdditionalWorktree {
			t.Error("additional_worktree clobbered")
		}
		if result.Enabled {
			t.Error("enabled clobbered")
		}
		if result.StartStatus != "ready" || result.WipStatus != "in-progress" || result.DoneStatus != "shipped" {
			t.Errorf("status fields clobbered: start=%q wip=%q done=%q", result.StartStatus, result.WipStatus, result.DoneStatus)
		}

		// output says which fields were preserved
		for _, want := range []string{
			"preserved existing enabled",
			"preserved existing additional_worktree",
			"preserved existing worktree_path",
			"preserved existing worktrees",
			"preserved existing base_branch",
			"preserved existing env",
			"preserved existing seed_exclude",
			"preserved existing prepare",
			"preserved existing context_repos",
			"preserved existing start_status",
			"preserved existing wip_status",
			"preserved existing done_status",
			"preserved existing fix_rounds",
			"preserved existing gate",
			"preserved existing notes",
		} {
			if !containsNote(notes, want) {
				t.Errorf("expected note %q, got %v", want, notes)
			}
		}
	})
}

// TestExecutorInitCmdPreservesExistingConfig runs the real `pm executor init`
// command (not just the mergeExecutor helper) against a project that already
// has a hand-tuned executor block, so a regression in the RunE wiring (e.g.
// reverting to `proj.Executor = draft`) fails this test even if mergeExecutor
// itself stays correct.
func TestExecutorInitCmdPreservesExistingConfig(t *testing.T) {
	store, slug := tempStore(t)
	proj, _ := store.GetProject(slug)
	writeFile(t, filepath.Join(proj.Path, "go.mod"), "module x\n")

	proj.Executor = &storage.Executor{
		Enabled:     true,
		Baseline:    "stale-hand-tuned-check",
		BaseBranch:  "development",
		Env:         map[string]string{"SIM_UDID": "abc"},
		Prepare:     "yarn install --frozen-lockfile",
		FixRounds:   7,
		StartStatus: "todo",
		WipStatus:   "doing",
		DoneStatus:  "merged",
	}
	if err := store.UpdateProject(slug, proj); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	cmd := newExecutorInitCmd(store)
	cmd.SetArgs([]string{slug})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("executor init: %v", err)
	}

	reloaded, err := store.GetProject(slug)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	e := reloaded.GetExecutor()
	if e.BaseBranch != "development" {
		t.Errorf("base_branch clobbered: %q", e.BaseBranch)
	}
	if e.Env["SIM_UDID"] != "abc" {
		t.Errorf("env clobbered: %+v", e.Env)
	}
	if e.Prepare != "yarn install --frozen-lockfile" {
		t.Errorf("prepare clobbered: %q", e.Prepare)
	}
	if e.FixRounds != 7 {
		t.Errorf("fix_rounds clobbered: %d", e.FixRounds)
	}
	if e.Baseline != "go test ./... && go vet ./..." {
		t.Errorf("baseline not refreshed: %q", e.Baseline)
	}
	if e.Phase(storage.PhaseVerify).Cmd != "go test ./... && go vet ./..." {
		t.Errorf("phases not refreshed: %+v", e.Phases)
	}
}

func TestExecutorInitWritesIdempotently(t *testing.T) {
	store, slug := tempStore(t)
	proj, _ := store.GetProject(slug)
	writeFile(t, filepath.Join(proj.Path, "go.mod"), "module x\n")

	// first draft + persist
	draft, _ := draftExecutor(proj.Path)
	proj.Executor = draft
	if err := store.UpdateProject(slug, proj); err != nil {
		t.Fatalf("update: %v", err)
	}

	reloaded, err := store.GetProject(slug)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.HasExecutor() {
		t.Fatal("executor block not persisted")
	}
	if reloaded.GetExecutor().Phase(storage.PhaseVerify).Cmd != "go test ./... && go vet ./..." {
		t.Error("verify cmd not persisted")
	}
	// other config preserved (name/path not clobbered)
	if reloaded.Name != "Proj" || reloaded.Path == "" {
		t.Errorf("project config clobbered: name=%q path=%q", reloaded.Name, reloaded.Path)
	}
}
