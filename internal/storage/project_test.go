package storage

import (
	"path/filepath"
	"testing"
)

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
