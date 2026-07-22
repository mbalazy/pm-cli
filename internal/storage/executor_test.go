package storage

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExecutorEnvSlice(t *testing.T) {
	if got := (Executor{}).EnvSlice(); got != nil {
		t.Fatalf("nil env should yield nil slice, got %v", got)
	}
	env := map[string]string{
		"SIM_UDID":              "2CE9",
		"ADDITIONAL_METRO_PORT": "8090",
	}
	// Sorted by key for determinism.
	want := []string{"ADDITIONAL_METRO_PORT=8090", "SIM_UDID=2CE9"}
	if got := (Executor{Env: env}).EnvSlice(); !reflect.DeepEqual(got, want) {
		t.Fatalf("EnvSlice = %v, want %v", got, want)
	}
}

func TestGetExecutorMissingBlock(t *testing.T) {
	// A project with no executor block resolves to all-generic defaults.
	p := &Project{Name: "Bare"}

	if p.HasExecutor() {
		t.Fatal("HasExecutor() = true, want false for project with no block")
	}

	e := p.GetExecutor()
	if !e.Enabled {
		t.Error("Enabled = false, want true by default")
	}
	if e.AdditionalWorktree {
		t.Error("AdditionalWorktree = true, want false by default")
	}
	if e.StartStatus != "todo" || e.WipStatus != "doing" || e.DoneStatus != "merged" {
		t.Errorf("statuses = %q/%q/%q, want todo/doing/merged", e.StartStatus, e.WipStatus, e.DoneStatus)
	}
	if e.FixRounds != 3 {
		t.Errorf("FixRounds = %d, want 3", e.FixRounds)
	}
	if e.Gate.PR != GateHuman || e.Gate.Merge != GateHuman {
		t.Errorf("Gate = %q/%q, want human/human", e.Gate.PR, e.Gate.Merge)
	}
	// Every phase resolves to generic when no block exists.
	for _, name := range ExecutorPhases {
		if k := e.Phase(name).Kind(); k != BindGeneric {
			t.Errorf("phase %q = %v, want generic", name, k)
		}
	}
}

func TestExecutorParseFullBlock(t *testing.T) {
	const data = `
name: Atlas
executor:
  enabled: true
  additional_worktree: true
  start_status: todo
  wip_status: doing
  done_status: merged
  fix_rounds: 5
  gate:
    pr: human
    merge: auto
  phases:
    implement: { skill: "/implement" }
    test:      { skill: "/test" }
    review:    { skill: "/review-pr" }
    verify:    { cmd: "cd apps/expo && yarn jest && yarn tsc --noEmit" }
    pr:        { skill: "/pr" }
  notes: |
    - gh: switch account before push
    - branch: feat/<slug>
`
	var p Project
	if err := yaml.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !p.HasExecutor() {
		t.Fatal("HasExecutor() = false, want true")
	}
	e := p.GetExecutor()

	if !e.Enabled || !e.AdditionalWorktree {
		t.Errorf("Enabled/AdditionalWorktree = %v/%v, want true/true", e.Enabled, e.AdditionalWorktree)
	}
	if e.FixRounds != 5 {
		t.Errorf("FixRounds = %d, want 5", e.FixRounds)
	}
	if e.Gate.PR != GateHuman || e.Gate.Merge != GateAuto {
		t.Errorf("Gate = %q/%q, want human/auto", e.Gate.PR, e.Gate.Merge)
	}
	if e.Notes == "" {
		t.Error("Notes empty, want populated")
	}

	tests := []struct {
		phase string
		kind  BindKind
		skill string
		cmd   string
	}{
		{PhaseImplement, BindSkill, "/implement", ""},
		{PhaseTest, BindSkill, "/test", ""},
		{PhaseReview, BindSkill, "/review-pr", ""},
		{PhaseVerify, BindCmd, "", "cd apps/expo && yarn jest && yarn tsc --noEmit"},
		{PhasePR, BindSkill, "/pr", ""},
	}
	for _, tt := range tests {
		t.Run(tt.phase, func(t *testing.T) {
			b := e.Phase(tt.phase)
			if b.Kind() != tt.kind {
				t.Errorf("Kind() = %v, want %v", b.Kind(), tt.kind)
			}
			if b.Skill != tt.skill {
				t.Errorf("Skill = %q, want %q", b.Skill, tt.skill)
			}
			if b.Cmd != tt.cmd {
				t.Errorf("Cmd = %q, want %q", b.Cmd, tt.cmd)
			}
		})
	}
}

func TestExecutorContextRepos(t *testing.T) {
	const data = `
name: LE
executor:
  enabled: true
  context_repos:
    backend: ../platform.orbit
    infra: ~/repos/infra
`
	var p Project
	if err := yaml.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	e := p.GetExecutor()
	if got := e.ContextRepos["backend"]; got != "../platform.orbit" {
		t.Errorf("ContextRepos[backend] = %q, want ../platform.orbit", got)
	}
	if got := e.ContextRepos["infra"]; got != "~/repos/infra" {
		t.Errorf("ContextRepos[infra] = %q, want ~/repos/infra", got)
	}

	var bare Project
	if err := yaml.Unmarshal([]byte("name: X\nexecutor:\n  enabled: true\n"), &bare); err != nil {
		t.Fatalf("Unmarshal bare: %v", err)
	}
	if len(bare.GetExecutor().ContextRepos) != 0 {
		t.Error("missing context_repos should resolve to an empty map")
	}
}

func TestPhaseBindingStates(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		wantKind BindKind
	}{
		{"skill", `{ skill: "/implement" }`, BindSkill},
		{"cmd", `{ cmd: "make test" }`, BindCmd},
		{"empty mapping", `{}`, BindGeneric},
		{"skip via false", `false`, BindSkip},
		{"explicit generic via true", `true`, BindGeneric},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b PhaseBinding
			if err := yaml.Unmarshal([]byte(tt.yaml), &b); err != nil {
				t.Fatalf("Unmarshal(%q): %v", tt.yaml, err)
			}
			if b.Kind() != tt.wantKind {
				t.Errorf("Kind() = %v, want %v", b.Kind(), tt.wantKind)
			}
		})
	}

	t.Run("invalid scalar errors", func(t *testing.T) {
		var b PhaseBinding
		if err := yaml.Unmarshal([]byte(`"oops"`), &b); err == nil {
			t.Error("expected error for non-bool scalar, got nil")
		}
	})
}

func TestExecutorDefaultsOverlay(t *testing.T) {
	t.Run("worktree defaults false when unset", func(t *testing.T) {
		const data = `
executor:
  enabled: true
  phases:
    implement: { skill: "/implement" }
`
		var p Project
		if err := yaml.Unmarshal([]byte(data), &p); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		e := p.GetExecutor()
		if e.AdditionalWorktree {
			t.Error("AdditionalWorktree = true, want false when unset")
		}
		if e.FixRounds != 3 {
			t.Errorf("FixRounds = %d, want default 3", e.FixRounds)
		}
		if e.Gate.PR != GateHuman || e.Gate.Merge != GateHuman {
			t.Errorf("Gate = %q/%q, want default human/human", e.Gate.PR, e.Gate.Merge)
		}
		if e.DoneStatus != "merged" {
			t.Errorf("DoneStatus = %q, want default merged", e.DoneStatus)
		}
	})

	t.Run("partial gate keeps default for omitted side", func(t *testing.T) {
		const data = `
executor:
  gate:
    pr: auto
`
		var p Project
		if err := yaml.Unmarshal([]byte(data), &p); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		e := p.GetExecutor()
		if e.Gate.PR != GateAuto {
			t.Errorf("Gate.PR = %q, want auto", e.Gate.PR)
		}
		if e.Gate.Merge != GateHuman {
			t.Errorf("Gate.Merge = %q, want default human", e.Gate.Merge)
		}
	})

	t.Run("missing phases resolve to generic, present do not", func(t *testing.T) {
		const data = `
executor:
  phases:
    implement: { skill: "/implement" }
    review: false
`
		var p Project
		if err := yaml.Unmarshal([]byte(data), &p); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		e := p.GetExecutor()
		if got := e.Phase(PhaseImplement).Kind(); got != BindSkill {
			t.Errorf("implement = %v, want skill", got)
		}
		if got := e.Phase(PhaseReview).Kind(); got != BindSkip {
			t.Errorf("review = %v, want skip", got)
		}
		// test/verify/pr unspecified -> generic
		for _, name := range []string{PhaseTest, PhaseVerify, PhasePR} {
			if got := e.Phase(name).Kind(); got != BindGeneric {
				t.Errorf("phase %q = %v, want generic", name, got)
			}
		}
	})
}
