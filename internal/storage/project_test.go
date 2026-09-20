package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The worker dir is the slim one (pm-cli-119-1): it wins for workers when set,
// falls back to the project's dir otherwise, and never touches
// ResolveClaudeConfigDir itself - that is what pm finish keeps reading.
func TestResolveWorkerClaudeConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, _ := os.UserHomeDir()

	t.Run("unset -> the project's claude_config_dir", func(t *testing.T) {
		p := &Project{ClaudeConfigDir: "/opt/claude-work"}
		if got := p.ResolveWorkerClaudeConfigDir(); got != "/opt/claude-work" {
			t.Errorf("got %q, want the project dir", got)
		}
	})
	t.Run("unset, no project dir -> default", func(t *testing.T) {
		p := &Project{}
		if got, def := p.ResolveWorkerClaudeConfigDir(), filepath.Join(home, ".claude"); got != def {
			t.Errorf("got %q, want %q", got, def)
		}
	})
	t.Run("set -> the worker dir, tilde expanded, project dir untouched", func(t *testing.T) {
		p := &Project{ClaudeConfigDir: "/opt/claude-work", Executor: &Executor{WorkerClaudeConfigDir: "~/.claude-worker"}}
		if got, want := p.ResolveWorkerClaudeConfigDir(), filepath.Join(home, ".claude-worker"); got != want {
			t.Errorf("worker dir = %q, want %q", got, want)
		}
		if got := p.ResolveClaudeConfigDir(); got != "/opt/claude-work" {
			t.Errorf("the project's own dir must not move: %q", got)
		}
	})
	t.Run("nil project -> default", func(t *testing.T) {
		var p *Project
		if got, def := p.ResolveWorkerClaudeConfigDir(), filepath.Join(home, ".claude"); got != def {
			t.Errorf("got %q, want %q", got, def)
		}
	})
}

func TestResolveClaudeConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "") // ignore any ambient override
	home, _ := os.UserHomeDir()
	def := filepath.Join(home, ".claude")

	t.Run("empty -> default ~/.claude", func(t *testing.T) {
		p := &Project{}
		if got := p.ResolveClaudeConfigDir(); got != def {
			t.Errorf("got %q, want %q", got, def)
		}
	})

	t.Run("nil project -> default", func(t *testing.T) {
		var p *Project
		if got := p.ResolveClaudeConfigDir(); got != def {
			t.Errorf("got %q, want %q", got, def)
		}
	})

	t.Run("explicit absolute path", func(t *testing.T) {
		p := &Project{ClaudeConfigDir: "/opt/claude-work"}
		if got := p.ResolveClaudeConfigDir(); got != "/opt/claude-work" {
			t.Errorf("got %q, want /opt/claude-work", got)
		}
	})

	t.Run("tilde expansion", func(t *testing.T) {
		p := &Project{ClaudeConfigDir: "~/.claude-work"}
		want := filepath.Join(home, ".claude-work")
		if got := p.ResolveClaudeConfigDir(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("CLAUDE_CONFIG_DIR env drives default", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "/env/cfg")
		p := &Project{}
		if got := p.ResolveClaudeConfigDir(); got != "/env/cfg" {
			t.Errorf("got %q, want /env/cfg", got)
		}
	})
}

func TestProjectGetStatuses(t *testing.T) {
	t.Run("custom statuses", func(t *testing.T) {
		p := &Project{Statuses: []string{"Backlog", "Active", "Shipped"}}
		statuses := p.GetStatuses()
		if len(statuses) != 3 {
			t.Fatalf("got %d statuses, want 3", len(statuses))
		}
		// Should be lowercased via ParseStatus
		if statuses[0] != "backlog" {
			t.Errorf("statuses[0] = %q, want %q", statuses[0], "backlog")
		}
	})

	t.Run("empty statuses returns defaults", func(t *testing.T) {
		p := &Project{}
		statuses := p.GetStatuses()
		if len(statuses) != len(DefaultStatuses) {
			t.Fatalf("got %d statuses, want %d", len(statuses), len(DefaultStatuses))
		}
		for i, s := range DefaultStatuses {
			if statuses[i] != s {
				t.Errorf("statuses[%d] = %q, want %q", i, statuses[i], s)
			}
		}
	})

	t.Run("nil project statuses returns defaults", func(t *testing.T) {
		p := &Project{Name: "Test", Statuses: nil}
		statuses := p.GetStatuses()
		if len(statuses) != len(DefaultStatuses) {
			t.Errorf("got %d, want %d", len(statuses), len(DefaultStatuses))
		}
	})
}

func TestReadWriteProjectRoundtrip(t *testing.T) {
	dir := t.TempDir()

	t.Run("full project", func(t *testing.T) {
		path := filepath.Join(dir, "project.yaml")
		orig := &Project{
			Name:     "My Project",
			Prefix:   "mp",
			Path:     "/home/user/myproject",
			Repo:     "github.com/user/myproject",
			Stack:    "Go, React",
			Links:    map[string]string{"jira": "https://jira.example.com/MP"},
			Tags:     []string{"backend", "frontend"},
			Statuses: []string{"todo", "doing", "review", "done"},
			Notes:    "Some notes here",
		}

		if err := WriteProject(path, orig); err != nil {
			t.Fatalf("WriteProject: %v", err)
		}

		got, err := ReadProject(path)
		if err != nil {
			t.Fatalf("ReadProject: %v", err)
		}

		if got.Name != orig.Name {
			t.Errorf("Name: got %q, want %q", got.Name, orig.Name)
		}
		if got.Prefix != orig.Prefix {
			t.Errorf("Prefix: got %q, want %q", got.Prefix, orig.Prefix)
		}
		if got.Path != orig.Path {
			t.Errorf("Path: got %q, want %q", got.Path, orig.Path)
		}
		if got.Stack != orig.Stack {
			t.Errorf("Stack: got %q, want %q", got.Stack, orig.Stack)
		}
		if got.Notes != orig.Notes {
			t.Errorf("Notes: got %q, want %q", got.Notes, orig.Notes)
		}
		if len(got.Links) != len(orig.Links) {
			t.Errorf("Links count: got %d, want %d", len(got.Links), len(orig.Links))
		}
		if len(got.Statuses) != len(orig.Statuses) {
			t.Errorf("Statuses count: got %d, want %d", len(got.Statuses), len(orig.Statuses))
		}
	})

	t.Run("minimal project", func(t *testing.T) {
		path := filepath.Join(dir, "minimal.yaml")
		orig := &Project{Name: "Minimal"}

		WriteProject(path, orig)
		got, err := ReadProject(path)
		if err != nil {
			t.Fatalf("ReadProject: %v", err)
		}
		if got.Name != "Minimal" {
			t.Errorf("Name: got %q, want %q", got.Name, "Minimal")
		}
		if len(got.Statuses) != 0 {
			t.Errorf("Statuses should be empty, got %v", got.Statuses)
		}
	})
}

func TestPhaseSkipBindingSurvivesRoundtrip(t *testing.T) {
	// Regression for pm-cli-55: PhaseBinding.Skip had no MarshalYAML, so a
	// plain struct marshal rendered `pr: false` as `{}` (BindGeneric) on ANY
	// writeProject call - even one only touching an unrelated field like
	// Notes. Simulates that exact sequence: a hand-authored project.yaml with
	// a skip binding, read in, rewritten (as pm_update_project would for a
	// notes-only edit), then read back.
	dir := t.TempDir()
	path := filepath.Join(dir, "project.yaml")
	const initial = `
name: Atlas
executor:
  enabled: true
  phases:
    pr: false
    review: { skill: "/review-pr" }
`
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p, err := ReadProject(path)
	if err != nil {
		t.Fatalf("ReadProject (initial): %v", err)
	}
	if kind := p.GetExecutor().Phase("pr").Kind(); kind != BindSkip {
		t.Fatalf("initial read: pr phase = %v, want skip", kind)
	}

	// Simulate an unrelated field update (e.g. pm_update_project editing notes).
	p.Notes = "updated notes"
	if err := WriteProject(path, p); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	reread, err := ReadProject(path)
	if err != nil {
		t.Fatalf("ReadProject (after write): %v", err)
	}
	e := reread.GetExecutor()
	if kind := e.Phase("pr").Kind(); kind != BindSkip {
		t.Errorf("after roundtrip: pr phase = %v, want skip", kind)
	}
	if kind := e.Phase("review").Kind(); kind != BindSkill {
		t.Errorf("after roundtrip: review phase = %v, want skill", kind)
	}
	if got := e.Phase("review").Skill; got != "/review-pr" {
		t.Errorf("after roundtrip: review skill = %q, want /review-pr", got)
	}

	// A second write (no-op) must keep the skip binding stable, not just
	// survive the first merge by luck.
	if err := WriteProject(path, reread); err != nil {
		t.Fatalf("WriteProject (second): %v", err)
	}
	twice, err := ReadProject(path)
	if err != nil {
		t.Fatalf("ReadProject (twice): %v", err)
	}
	if kind := twice.GetExecutor().Phase("pr").Kind(); kind != BindSkip {
		t.Errorf("after second roundtrip: pr phase = %v, want skip", kind)
	}
}

func TestDefaultStatusesDoNotContainArchived(t *testing.T) {
	for _, s := range DefaultStatuses {
		if s == StatusArchived {
			t.Error("DefaultStatuses must not contain StatusArchived - it is a system-level status")
		}
	}
}

// TestWriteProjectPreservesHandTunedYAML: writeProject must not destroy what a
// struct round-trip cannot represent - comments and unknown keys in a
// hand-tuned project.yaml. `pm executor init` re-writes the whole file to
// refresh two detected fields; before this, every comment and any key pm does
// not know about vanished, contradicting init's own "hand-set fields
// untouched" promise.
func TestWriteProjectPreservesHandTunedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.yaml")
	handTuned := `name: App
# main checkout of the RN app
path: /repos/app
my_custom_key: keep-me
executor:
  enabled: true
  # slot 2 talks to the blue simulator
  env:
    SIM_UDID: ABC-123
  baseline: yarn validate
notes: hand-written notes
`
	if err := os.WriteFile(path, []byte(handTuned), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := ReadProject(path)
	if err != nil {
		t.Fatal(err)
	}

	// The typical `pm executor init` write: refresh baseline, keep the rest.
	p.Executor.Baseline = "yarn verify"
	if err := WriteProject(path, p); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)

	for _, want := range []string{
		"# main checkout of the RN app",
		"# slot 2 talks to the blue simulator",
		"my_custom_key: keep-me",
		"baseline: yarn verify",
		"SIM_UDID: ABC-123",
		"notes: hand-written notes",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("write dropped %q; file now:\n%s", want, text)
		}
	}
	if strings.Contains(text, "yarn validate") {
		t.Errorf("stale baseline survived:\n%s", text)
	}

	// The result must still round-trip through the struct reader.
	got, err := ReadProject(path)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if got.Executor == nil || got.Executor.Baseline != "yarn verify" || got.Executor.Env["SIM_UDID"] != "ABC-123" {
		t.Fatalf("re-read executor: %+v", got.Executor)
	}
}

// TestWriteProjectClearsRemovedKnownFields: a known field the struct no longer
// carries must disappear from the file (it was cleared, not "unknown data").
func TestWriteProjectClearsRemovedKnownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.yaml")
	if err := os.WriteFile(path, []byte("name: App\nnotes: obsolete\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := ReadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	p.Notes = ""
	if err := WriteProject(path, p); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	if strings.Contains(string(out), "obsolete") {
		t.Errorf("cleared field must be dropped, file:\n%s", out)
	}
}

// TestWriteProjectFreshFile: no existing file -> plain marshal, no error.
func TestWriteProjectFreshFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.yaml")
	if err := WriteProject(path, &Project{Name: "New"}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadProject(path)
	if err != nil || got.Name != "New" {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

// TestWriteProjectCorruptExistingFallsBack: an unparseable existing file must
// not block the write - it degrades to the plain marshal.
func TestWriteProjectCorruptExistingFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.yaml")
	if err := os.WriteFile(path, []byte(":: not yaml ["), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteProject(path, &Project{Name: "Recovered"}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadProject(path)
	if err != nil || got.Name != "Recovered" {
		t.Fatalf("got %+v err=%v", got, err)
	}
}
