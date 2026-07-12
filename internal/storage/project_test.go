package storage

import (
	"os"
	"path/filepath"
	"testing"
)

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
		p := &Project{ClaudeConfigDir: "/opt/claude-alt"}
		if got := p.ResolveClaudeConfigDir(); got != "/opt/claude-alt" {
			t.Errorf("got %q, want /opt/claude-alt", got)
		}
	})

	t.Run("tilde expansion", func(t *testing.T) {
		p := &Project{ClaudeConfigDir: "~/.claude-alt"}
		want := filepath.Join(home, ".claude-alt")
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

func TestDefaultStatusesDoNotContainArchived(t *testing.T) {
	for _, s := range DefaultStatuses {
		if s == StatusArchived {
			t.Error("DefaultStatuses must not contain StatusArchived - it is a system-level status")
		}
	}
}
