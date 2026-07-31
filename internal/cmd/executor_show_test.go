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

// A slug-only command must not inherit resolveProjectArg's fatal project load.
// `pm context` and `pm executor stats` are read-only diagnostics that never
// touch the project struct - before the resolver was shared they printed their
// rollup regardless of what project.yaml held, and an unparseable one is
// exactly the situation you run them in.
func TestSlugOnlyCommandsSurviveUnreadableProjectYAML(t *testing.T) {
	newStore := func(t *testing.T) (*storage.Store, string) {
		t.Helper()
		store := &storage.Store{Root: t.TempDir()}
		if err := store.CreateProject("app", &storage.Project{Name: "App"}); err != nil {
			t.Fatalf("create project: %v", err)
		}
		// Valid enough for ListProjects/ResolveProject to see the project,
		// unparseable for ReadProject.
		writeFile(t, store.ProjectYAML("app"), "name: App\nstatuses: [todo, doing\n\tbad: \"unclosed\n")
		if _, err := store.GetProject("app"); err == nil {
			t.Fatal("fixture is not corrupt: GetProject must fail for this test to mean anything")
		}
		return store, "app"
	}

	t.Run("resolveProjectSlugArg resolves it", func(t *testing.T) {
		store, slug := newStore(t)
		got, err := resolveProjectSlugArg(store, []string{slug})
		if err != nil {
			t.Fatalf("slug resolution must not load project.yaml, got: %v", err)
		}
		if got != slug {
			t.Errorf("slug = %q, want %q", got, slug)
		}
	})

	t.Run("resolveProjectArg still fails (the struct really is unusable)", func(t *testing.T) {
		store, slug := newStore(t)
		if _, _, err := resolveProjectArg(store, []string{slug}); err == nil {
			t.Error("a command needing the project struct must still get the load error")
		}
	})

	t.Run("pm context prints its rollup", func(t *testing.T) {
		store, slug := newStore(t)
		addTask(t, store, slug, storage.TaskMeta{ID: "app-1", Title: "A doing task", Status: storage.StatusDoing}, "")
		out, err := execContextCmd(t, store, slug)
		if err != nil {
			t.Fatalf("context must not fail on an unreadable project.yaml, got: %v", err)
		}
		if !strings.Contains(out, "A doing task") {
			t.Errorf("expected the doing task in the rollup, got %q", out)
		}
	})

	t.Run("pm executor stats prints its note", func(t *testing.T) {
		store, slug := newStore(t)
		cmd := newExecutorStatsCmd(store)
		var buf strings.Builder
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{slug})
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		if err := cmd.Execute(); err != nil {
			t.Fatalf("stats must not fail on an unreadable project.yaml, got: %v", err)
		}
		if !strings.Contains(buf.String(), "no executor runs recorded yet") {
			t.Errorf("expected the empty-journal note, got %q", buf.String())
		}
	})
}
