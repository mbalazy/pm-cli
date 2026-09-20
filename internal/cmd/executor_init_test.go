package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
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

	// The real app.orbit shape: an aggregate called `validate`, no
	// verify/check/ci, and a HYPHENATED type-check. Before this was fixed,
	// detection fell through to `yarn test && yarn lint` - a command the
	// project's own playbook calls wrong (jest is not part of its verification)
	// and one that silently dropped type checking because pm only looked for
	// `typecheck`. That string becomes the executor baseline, so it decides
	// whether workers can tell new failures from old ones.
	t.Run("validate counts as an aggregate", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{
			"validate":"npm run type-check && npm run lint && npm run format:check",
			"test":"jest","lint":"eslint .","type-check":"tsc --noEmit"}}`)
		writeFile(t, filepath.Join(dir, "yarn.lock"), "")
		if got := detectVerifyCmd(dir); got != "yarn validate" {
			t.Errorf("got %q, want yarn validate", got)
		}
	})

	t.Run("hyphenated type-check is the same step as typecheck", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "package.json"),
			`{"scripts":{"test":"jest","lint":"eslint .","type-check":"tsc --noEmit"}}`)
		writeFile(t, filepath.Join(dir, "yarn.lock"), "")
		want := "yarn test && yarn lint && yarn type-check"
		if got := detectVerifyCmd(dir); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("one spelling per step, never both", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "package.json"),
			`{"scripts":{"typecheck":"tsc","type-check":"tsc --noEmit","tsc":"tsc"}}`)
		writeFile(t, filepath.Join(dir, "yarn.lock"), "")
		if got := detectVerifyCmd(dir); got != "yarn typecheck" {
			t.Errorf("got %q, want just yarn typecheck (first spelling wins)", got)
		}
	})

	t.Run("verify still beats validate", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "package.json"),
			`{"scripts":{"verify":"make all","validate":"npm run lint"}}`)
		writeFile(t, filepath.Join(dir, "yarn.lock"), "")
		if got := detectVerifyCmd(dir); got != "yarn verify" {
			t.Errorf("got %q, want yarn verify", got)
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
		if e.FixRounds != 2 || e.DoneStatus != "merged" {
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
	mustContain(t, out, "fix_rounds: 2")
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

// racingStore lets a test land a project.yaml edit in the window between
// `pm executor init`'s scan and its write - the window the MutateProject
// re-merge exists for.
type racingStore struct {
	storage.TaskStore
	during func()
	fired  bool
}

func (r *racingStore) MutateProject(slug string, fn func(*storage.Project) error) (*storage.Project, error) {
	if !r.fired {
		r.fired = true
		r.during()
	}
	return r.TaskStore.MutateProject(slug, fn)
}

// TestExecutorInitMergesOverTheCurrentFile: init reads the project, then scans
// the repo (slow), then writes. The merge MUST be redone against the executor
// block as it is at write time - merging the draft into the block read before
// the scan and assigning that wholesale silently reverts an edit made during
// it, on the one field this command owns. Every other executor_init test is
// single-goroutine, so nothing else can catch a stale merge base.
func TestExecutorInitMergesOverTheCurrentFile(t *testing.T) {
	base, slug := tempStore(t)
	proj, _ := base.GetProject(slug)
	writeFile(t, filepath.Join(proj.Path, "go.mod"), "module x\n")

	proj.Executor = &storage.Executor{Enabled: true, BaseBranch: "development", Prepare: "old-prepare"}
	if err := base.UpdateProject(slug, proj); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	store := &racingStore{TaskStore: base, during: func() {
		if _, err := base.MutateProject(slug, func(p *storage.Project) error {
			p.Executor.BaseBranch = "release"
			p.Executor.Prepare = "new-prepare"
			return nil
		}); err != nil {
			t.Errorf("racing edit: %v", err)
		}
	}}

	cmd := newExecutorInitCmd(store)
	cmd.SetArgs([]string{slug})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("executor init: %v", err)
	}

	reloaded, err := base.GetProject(slug)
	if err != nil {
		t.Fatal(err)
	}
	e := reloaded.GetExecutor()
	if e.BaseBranch != "release" || e.Prepare != "new-prepare" {
		t.Fatalf("merge used the stale executor block: base_branch=%q prepare=%q", e.BaseBranch, e.Prepare)
	}
	// The detected half must still be refreshed from the scan.
	if e.Baseline != "go test ./... && go vet ./..." {
		t.Fatalf("baseline not refreshed: %q", e.Baseline)
	}
}

func TestRuntimeSkillTier(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"simulator-verify", 0},
		{"sim", 0},
		{"sim-ui", 0},
		{"iossimulator", 0}, // long keyword still matches inside a segment
		{"device-check", 1},
		{"android-emulator", 1},
		{"browser-drive", 2},
		{"e2e", 2},
		{"verify", 3},
		{"verify-ui", 3},
		// The trap: "sim" is a substring of these, and a substring rule would
		// bind an unrelated skill as the project's runtime driver.
		{"simplify", -1},
		{"similar-things", -1},
		{"simple", -1},
		{"figma-pp", -1},
		{"humanizer", -1},
	}
	for _, tt := range tests {
		if got := runtimeSkillTier(tt.name); got != tt.want {
			t.Errorf("runtimeSkillTier(%q) = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestDetectRuntimeSkill(t *testing.T) {
	skill := func(t *testing.T, dir, name string, scripts ...string) {
		writeFile(t, filepath.Join(dir, ".claude", "skills", name, "SKILL.md"), "# "+name+"\n")
		for _, s := range scripts {
			writeFile(t, filepath.Join(dir, ".claude", "skills", name, "scripts", s), "#!/bin/sh\n")
		}
	}

	t.Run("no candidates -> empty with a note saying so", func(t *testing.T) {
		dir := t.TempDir()
		skill(t, dir, "implement-feature")
		skill(t, dir, "simplify")
		got, note := detectRuntimeSkill(dir)
		if got != "" {
			t.Errorf("got %q, want empty (simplify must not read as a simulator skill)", got)
		}
		if !strings.Contains(note, "none detected") {
			t.Errorf("note = %q", note)
		}
	})

	t.Run("stronger tier wins over weaker", func(t *testing.T) {
		dir := t.TempDir()
		skill(t, dir, "verify-things")
		skill(t, dir, "simulator-verify")
		if got, _ := detectRuntimeSkill(dir); got != "simulator-verify" {
			t.Errorf("got %q, want simulator-verify", got)
		}
	})

	t.Run("within a tier, the one shipping scripts wins", func(t *testing.T) {
		dir := t.TempDir()
		// "a-sim" sorts first, so only the scripts/ tie-break can flip this.
		skill(t, dir, "a-sim")
		skill(t, dir, "z-simulator", "sim-ui.sh")
		got, note := detectRuntimeSkill(dir)
		if got != "z-simulator" {
			t.Errorf("got %q, want z-simulator (it ships scripts/)", got)
		}
		if !strings.Contains(note, "ships scripts/") {
			t.Errorf("note should mention the scripts dir, got %q", note)
		}
	})

	t.Run("a name already claimed by a phase is never a runtime skill", func(t *testing.T) {
		dir := t.TempDir()
		// Contains "test" -> phaseForName binds it to the test phase, so it must
		// not ALSO be proposed as the runtime driver.
		skill(t, dir, "test-on-device")
		if got, _ := detectRuntimeSkill(dir); got != "" {
			t.Errorf("got %q, want empty - the name belongs to the test phase", got)
		}
	})

	t.Run("falls back to commands when no skill matches", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".claude", "commands", "simulator.md"), "drive the sim\n")
		if got, _ := detectRuntimeSkill(dir); got != "simulator" {
			t.Errorf("got %q, want simulator", got)
		}
	})

	t.Run("a skill beats a command even on a weaker tier", func(t *testing.T) {
		dir := t.TempDir()
		skill(t, dir, "verify-screen")
		writeFile(t, filepath.Join(dir, ".claude", "commands", "simulator.md"), "drive the sim\n")
		if got, _ := detectRuntimeSkill(dir); got != "verify-screen" {
			t.Errorf("got %q, want verify-screen (only a skill can carry scripts/)", got)
		}
	})
}

// TestSkillNamesFollowsSymlinks guards the distribution mechanism, not just the
// helper: skills shared between repos are installed as symlinks into one
// checkout, and os.ReadDir reports a symlinked directory as a NON-directory. An
// IsDir() gate here therefore made every linked-in skill invisible to both
// phase detection and runtime-skill detection - found by running the real
// installer against a synthetic project and watching runtime_skill come back
// unset even though the skill was right there.
func TestSkillNamesFollowsSymlinks(t *testing.T) {
	real := t.TempDir()
	writeFile(t, filepath.Join(real, "simulator-verify", "SKILL.md"), "# sim\n")
	writeFile(t, filepath.Join(real, "simulator-verify", "scripts", "sim-ui.sh"), "#!/bin/sh\n")

	proj := t.TempDir()
	skills := filepath.Join(proj, ".claude", "skills")
	if err := os.MkdirAll(skills, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, "simulator-verify"), filepath.Join(skills, "simulator-verify")); err != nil {
		t.Fatal(err)
	}

	if got := skillNames(skills); len(got) != 1 || got[0] != "simulator-verify" {
		t.Fatalf("skillNames = %v, want [simulator-verify] - a linked-in skill must be found", got)
	}
	name, note := detectRuntimeSkill(proj)
	if name != "simulator-verify" {
		t.Errorf("detectRuntimeSkill = %q, want simulator-verify", name)
	}
	if !strings.Contains(note, "ships scripts/") {
		t.Errorf("scripts/ behind the link should be seen too, note = %q", note)
	}
}

func TestDraftExecutorHandoff(t *testing.T) {
	t.Run("playbook always proposed, runtime skill only when found", func(t *testing.T) {
		dir := t.TempDir()
		e, notes := draftExecutor(dir)
		if e.Handoff.Playbook != defaultPlaybookPath {
			t.Errorf("playbook = %q, want %q", e.Handoff.Playbook, defaultPlaybookPath)
		}
		if e.Handoff.RuntimeSkill != "" {
			t.Errorf("runtime_skill = %q, want empty on a bare project", e.Handoff.RuntimeSkill)
		}
		if !containsNote(notes, "handoff.playbook -> "+defaultPlaybookPath) {
			t.Errorf("notes = %v", notes)
		}
	})

	t.Run("detected runtime skill lands in the block", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".claude", "skills", "simulator-verify", "SKILL.md"), "x\n")
		e, _ := draftExecutor(dir)
		if e.Handoff.RuntimeSkill != "simulator-verify" {
			t.Errorf("runtime_skill = %q", e.Handoff.RuntimeSkill)
		}
	})
}

func TestMergeExecutorHandoff(t *testing.T) {
	t.Run("filled in when the existing block is empty", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".claude", "skills", "simulator-verify", "SKILL.md"), "x\n")
		draft, _ := draftExecutor(dir)

		result, notes := mergeExecutor(&storage.Executor{Prepare: "yarn install"}, draft)
		if result.Handoff.RuntimeSkill != "simulator-verify" {
			t.Errorf("runtime_skill = %q, want the detected one", result.Handoff.RuntimeSkill)
		}
		if !containsNote(notes, "handoff -> filled in (the block was empty)") {
			t.Errorf("notes = %v", notes)
		}
	})

	t.Run("a hand-set handoff is never overwritten", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".claude", "skills", "simulator-verify", "SKILL.md"), "x\n")
		draft, _ := draftExecutor(dir)

		existing := &storage.Executor{Handoff: storage.Handoff{
			Playbook:     "docs/my-own-playbook.md",
			RuntimeSkill: "hand-picked-runtime",
		}}
		result, notes := mergeExecutor(existing, draft)
		if result.Handoff.Playbook != "docs/my-own-playbook.md" {
			t.Errorf("playbook clobbered: %q", result.Handoff.Playbook)
		}
		if result.Handoff.RuntimeSkill != "hand-picked-runtime" {
			t.Errorf("runtime_skill clobbered: %q", result.Handoff.RuntimeSkill)
		}
		if !containsNote(notes, "preserved existing handoff") {
			t.Errorf("notes = %v", notes)
		}
	})
}

func TestPlaybookSkeleton(t *testing.T) {
	e := &storage.Executor{
		Baseline:     "yarn validate",
		ContextRepos: map[string]string{"backend": "/repos/platform"},
	}
	h := storage.ResolvedHandoff{
		Declared:     true,
		RuntimeSkill: "simulator-verify",
		SkillPath:    "/repo/.claude/skills/simulator-verify/SKILL.md",
		Scripts:      []string{"measure-element.py", "sim-ui.sh"},
	}
	out := playbookSkeleton("myproj", e, h)

	t.Run("derived facts are filled in", func(t *testing.T) {
		for _, want := range []string{
			"myproj",
			"simulator-verify",
			"/repo/.claude/skills/simulator-verify/SKILL.md",
			"yarn validate",
			"/repos/platform",
			"pm executor show myproj",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("skeleton missing derived fact %q", want)
			}
		}
	})

	t.Run("every script gets its own what-for slot", func(t *testing.T) {
		// The pm-cli-49 lesson: naming the scripts is not documenting them.
		// Each one must carry an explicit, unfilled slot for its purpose.
		for _, s := range h.Scripts {
			if !strings.Contains(out, "`"+s+"`") {
				t.Errorf("skeleton never names %q", s)
			}
		}
		if got := strings.Count(out, playbookTODO+" what it is FOR"); got != len(h.Scripts) {
			t.Errorf("got %d what-for slots, want one per script (%d)", got, len(h.Scripts))
		}
		if !strings.Contains(out, playbookTODO+" WHEN to reach for it") {
			t.Error("skeleton must ask when to reach for a tool, not just what it is")
		}
	})

	t.Run("no fabricated purpose text", func(t *testing.T) {
		// A generated guess about what a script does is worse than a blank,
		// because nobody goes back to check it.
		if strings.Contains(out, "measures the") || strings.Contains(out, "drives the simulator (tap") {
			t.Error("skeleton invented a purpose for a script")
		}
	})

	t.Run("slot count is reported honestly", func(t *testing.T) {
		if n := strings.Count(out, playbookTODO); n < 10 {
			t.Errorf("expected the scaffold to leave many slots, got %d", n)
		}
	})

	t.Run("bare project still gets a usable scaffold", func(t *testing.T) {
		bare := playbookSkeleton("bare", &storage.Executor{}, storage.ResolvedHandoff{})
		for _, want := range []string{
			"no runtime skill is declared",
			"executor.baseline",
			"## PR conventions",
		} {
			if !strings.Contains(bare, want) {
				t.Errorf("bare scaffold missing %q", want)
			}
		}
	})
}

func TestPlanPlaybook(t *testing.T) {
	t.Run("scaffolds when absent", func(t *testing.T) {
		dir := t.TempDir()
		e := &storage.Executor{Handoff: storage.Handoff{Playbook: defaultPlaybookPath}}
		plan := planPlaybook(dir, "p", e)
		if plan == nil || plan.Exists || plan.Content == "" {
			t.Fatalf("plan = %+v, want a scaffold", plan)
		}
		if err := plan.write(); err != nil {
			t.Fatalf("write: %v", err)
		}
		if !fileExists(filepath.Join(dir, defaultPlaybookPath)) {
			t.Error("playbook not written")
		}
	})

	t.Run("an existing playbook is left alone", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, defaultPlaybookPath)
		writeFile(t, path, "# hand-written, do not touch\n")

		e := &storage.Executor{Handoff: storage.Handoff{Playbook: defaultPlaybookPath}}
		plan := planPlaybook(dir, "p", e)
		if plan == nil || !plan.Exists {
			t.Fatalf("plan = %+v, want Exists", plan)
		}
		if plan.Content != "" {
			t.Error("must not prepare content for an existing playbook")
		}
		if err := plan.write(); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "# hand-written, do not touch\n" {
			t.Errorf("existing playbook was rewritten: %q", got)
		}
	})

	t.Run("no playbook declared -> no plan", func(t *testing.T) {
		if plan := planPlaybook(t.TempDir(), "p", &storage.Executor{}); plan != nil {
			t.Errorf("plan = %+v, want nil", plan)
		}
	})
}

// TestExecutorInitThenDoctorIsClean is the acceptance criterion for pm-cli-51:
// a fresh project set up by `pm executor init` alone must not make
// `pm executor doctor` report a single ERROR. Warnings are expected and
// correct - the scaffold's slots are still empty, and that is exactly what the
// warning level is for.
func TestExecutorInitThenDoctorIsClean(t *testing.T) {
	store, slug := tempStore(t)
	proj, _ := store.GetProject(slug)
	writeFile(t, filepath.Join(proj.Path, "go.mod"), "module x\n")
	writeFile(t, filepath.Join(proj.Path, ".claude", "skills", "simulator-verify", "SKILL.md"), "# sim\n")
	writeFile(t, filepath.Join(proj.Path, ".claude", "skills", "simulator-verify", "scripts", "sim-ui.sh"), "#!/bin/sh\n")

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
	if e.Handoff.RuntimeSkill != "simulator-verify" {
		t.Errorf("runtime_skill = %q, want simulator-verify", e.Handoff.RuntimeSkill)
	}
	if e.Handoff.Playbook != defaultPlaybookPath {
		t.Errorf("playbook = %q", e.Handoff.Playbook)
	}

	playbook := filepath.Join(proj.Path, defaultPlaybookPath)
	body, err := os.ReadFile(playbook)
	if err != nil {
		t.Fatalf("init did not scaffold the playbook: %v", err)
	}
	// The derived inventory has to be there, or doctor's mention check would be
	// warning about scripts the scaffold was supposed to introduce.
	if !strings.Contains(string(body), "sim-ui.sh") {
		t.Error("scaffold does not name the runtime skill's script")
	}

	checks := runExecutorDoctor(reloaded)
	for _, c := range checks {
		if c.Level == levelError {
			t.Errorf("doctor reported an ERROR after a bare init: %s", c.Msg)
		}
	}
	if failed(checks, false) {
		t.Error("doctor should exit 0 after a bare init")
	}
	// ...but it must still say the prose is missing.
	if !failed(checks, true) {
		t.Error("doctor --strict should flag the unfilled scaffold")
	}
	var sawTODO bool
	for _, c := range checks {
		if strings.Contains(c.Msg, playbookTODO) {
			sawTODO = true
		}
	}
	if !sawTODO {
		t.Error("doctor must warn about the scaffold's unfilled slots")
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
