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
