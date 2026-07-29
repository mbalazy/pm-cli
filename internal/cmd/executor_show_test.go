package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// handoffProject builds a project whose repo dir carries a playbook and a
// runtime skill with scripts - the orbit shape this feature was built
// for, minus the client specifics.
func handoffProject(t *testing.T, playbook string) (*storage.Project, string) {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".claude", "evidence-playbook.md"), playbook)
	skill := filepath.Join(repo, ".claude", "skills", "simulator-verify")
	writeFile(t, filepath.Join(skill, "SKILL.md"), "# simulator-verify")
	writeFile(t, filepath.Join(skill, "scripts", "measure-element.py"), "")
	writeFile(t, filepath.Join(skill, "scripts", "read-rn-logs.sh"), "")

	proj := &storage.Project{
		Name: "Demo",
		Path: repo,
		Executor: &storage.Executor{
			Enabled:    true,
			BaseBranch: "development",
			Baseline:   "yarn validate",
			Worktrees: []storage.WorktreeSlot{
				{Path: "../demo-additional", Env: map[string]string{"SIM_UDID": "2CE98C80-9633", "PORT": "8090"}},
				{Path: "../demo-additional-2", Env: map[string]string{"SIM_UDID": "C0F4EFDF-BE65", "PORT": "8091"}},
			},
			ContextRepos: map[string]string{"backend": repo},
			Phases:       map[string]storage.PhaseBinding{storage.PhaseVerify: {Cmd: "yarn validate"}},
			Handoff: storage.Handoff{
				Playbook:     ".claude/evidence-playbook.md",
				RuntimeSkill: "simulator-verify",
			},
			StartStatus: "todo", WipStatus: "doing", DoneStatus: "merged", FixRounds: 3,
		},
	}
	return proj, repo
}

func TestRenderExecutorProfile(t *testing.T) {
	proj, repo := handoffProject(t, "# playbook\nuses read-rn-logs.sh\n")
	out := renderExecutorProfile("demo", proj)

	for _, want := range []string{
		"# executor profile: demo",
		repo,
		// phases: the one bound, and a generic
		"verify     cmd:   yarn validate",
		"implement  generic (built-in)",
		// resolved slots with merged env - the whole point: the odbiór reads
		// runtime identifiers from here instead of a doc that drifts
		"slot 1",
		"SIM_UDID=2CE98C80-9633",
		"PORT=8090",
		"slot 2",
		"SIM_UDID=C0F4EFDF-BE65",
		"Reference repos (READ-ONLY)",
		"backend",
		"## Handoff (odbiór)",
		"evidence-playbook.md  [ok]",
		"runtime skill: /simulator-verify",
		"measure-element.py",
		"read-rn-logs.sh",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("profile missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderExecutorProfileNoBlock(t *testing.T) {
	proj := &storage.Project{Name: "Bare", Path: t.TempDir()}
	out := renderExecutorProfile("bare", proj)
	if !strings.Contains(out, "No `executor` block") {
		t.Errorf("expected the no-block notice, got:\n%s", out)
	}
	if strings.Contains(out, "## Phases") {
		t.Error("a project with no block should not print a phase table")
	}
}

func TestRenderExecutorProfileNoHandoff(t *testing.T) {
	proj := &storage.Project{
		Name:     "Demo",
		Path:     t.TempDir(),
		Executor: &storage.Executor{Enabled: true, FixRounds: 3},
	}
	out := renderExecutorProfile("demo", proj)
	if !strings.Contains(out, "no handoff block") {
		t.Errorf("expected the missing-handoff notice, got:\n%s", out)
	}
	if !strings.Contains(out, "## Worktree slots\n  (none") {
		t.Errorf("expected the no-slots notice, got:\n%s", out)
	}
}

func TestRenderExecutorProfileMissingPlaybook(t *testing.T) {
	proj := &storage.Project{
		Name: "Demo",
		Path: t.TempDir(),
		Executor: &storage.Executor{
			Enabled: true, FixRounds: 3,
			Handoff: storage.Handoff{Playbook: ".claude/gone.md", RuntimeSkill: "nope"},
		},
	}
	out := renderExecutorProfile("demo", proj)
	if !strings.Contains(out, "[MISSING]") {
		t.Errorf("expected the playbook flagged MISSING, got:\n%s", out)
	}
	if !strings.Contains(out, "NOT FOUND") {
		t.Errorf("expected the runtime skill flagged NOT FOUND, got:\n%s", out)
	}
}

func TestDescribeBinding(t *testing.T) {
	tests := []struct {
		name string
		pb   storage.PhaseBinding
		want string
	}{
		{"generic", storage.PhaseBinding{}, "generic (built-in)"},
		{"skill", storage.PhaseBinding{Skill: "/foo"}, "skill: /foo"},
		{"cmd", storage.PhaseBinding{Cmd: "make x"}, "cmd:   make x"},
		{"skip", storage.PhaseBinding{Skip: true}, "skip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeBinding(tt.pb); got != tt.want {
				t.Errorf("describeBinding() = %q, want %q", got, tt.want)
			}
		})
	}
}
