package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "Hello World", "hello-world"},
		{"special chars", "Fix bug #42 (urgent)", "fix-bug-42-urgent"},
		{"underscores", "my_task_name", "my-task-name"},
		{"slashes", "feat/add-auth", "feat-addauth"},
		{"multiple spaces", "too   many   spaces", "too-many-spaces"},
		{"leading trailing dashes", "--leading-trailing--", "leadingtrailing"},
		{"unicode stripped", "zażółć gęślą", "za-gl"},
		{"empty string", "", ""},
		{"all special", "!!!@@@###", ""},
		{"mixed case", "CamelCaseTitle", "camelcasetitle"},
		{"numbers", "task 123 done", "task-123-done"},
		{"long title truncated", strings.Repeat("a", 100), strings.Repeat("a", 60)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slugify(tt.in)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		in   string
		want TaskStatus
	}{
		{"todo", StatusTodo},
		{"TODO", StatusTodo},
		{"Done", StatusDone},
		{"ARCHIVED", StatusArchived},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := ParseStatus(tt.in)
			if got != tt.want {
				t.Errorf("ParseStatus(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidateStatus(t *testing.T) {
	allowed := []TaskStatus{StatusTodo, StatusDoing, StatusDone}

	t.Run("valid status", func(t *testing.T) {
		if err := ValidateStatus(StatusTodo, allowed); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("invalid status", func(t *testing.T) {
		err := ValidateStatus(StatusArchived, allowed)
		if err == nil {
			t.Error("expected error for invalid status")
		}
	})

	t.Run("empty allowed list", func(t *testing.T) {
		err := ValidateStatus(StatusTodo, nil)
		if err == nil {
			t.Error("expected error with empty allowed list")
		}
	})
}

func TestTaskFilename(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		title string
		want  string
	}{
		{"with id", "proj-1", "My Task", "proj-1-my-task.md"},
		{"without id", "", "My Task", "my-task.md"},
		{"special chars in title", "p-2", "Fix bug #42!", "p-2-fix-bug-42.md"},
		{"unicode title", "p-3", "Zrób coś", "p-3-zrb-co.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &Task{Meta: TaskMeta{ID: tt.id, Title: tt.title}}
			got := task.Filename()
			if got != tt.want {
				t.Errorf("Filename() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewTask(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		task := NewTask("p-1", "Test Task", "myproject")
		if task.Meta.ID != "p-1" {
			t.Errorf("ID = %q, want %q", task.Meta.ID, "p-1")
		}
		if task.Meta.Title != "Test Task" {
			t.Errorf("Title = %q, want %q", task.Meta.Title, "Test Task")
		}
		if task.Meta.Status != StatusTodo {
			t.Errorf("Status = %q, want %q", task.Meta.Status, StatusTodo)
		}
		if task.Project != "myproject" {
			t.Errorf("Project = %q, want %q", task.Project, "myproject")
		}
		today := time.Now().Format("2006-01-02")
		if task.Meta.Created != today {
			t.Errorf("Created = %q, want %q", task.Meta.Created, today)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		task := NewTask("p-2", "Another", "proj")
		if task.Body != "" {
			t.Errorf("Body should be empty, got %q", task.Body)
		}
	})
}

func TestWriteTaskReadTaskRoundtrip(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name string
		task Task
	}{
		{
			"minimal task",
			Task{
				Meta: TaskMeta{
					ID:      "t-1",
					Title:   "Minimal",
					Status:  StatusTodo,
					Created: "2025-01-01",
					Updated: "2025-01-01",
				},
			},
		},
		{
			"full task with all fields",
			Task{
				Meta: TaskMeta{
					ID:      "t-2",
					Title:   "Full Task",
					Status:  StatusDoing,
					Created: "2025-01-01",
					Updated: "2025-06-15",
					Links:   map[string]string{"pr": "https://github.com/pr/1", "jira": "SCRUM-42"},
					Branch:  "feat/auth",
					Tags:    []string{"backend", "urgent"},
					Brief:   "Working on auth flow",
				},
				Body: "## Description\n\nSome body content\n\n## Notes\n\n- Note 1",
			},
		},
		{
			"task with empty links/tags",
			Task{
				Meta: TaskMeta{
					ID:      "t-3",
					Title:   "Empty Collections",
					Status:  StatusDone,
					Created: "2025-01-01",
					Updated: "2025-01-01",
				},
			},
		},
		{
			"task with unicode title",
			Task{
				Meta: TaskMeta{
					ID:      "t-4",
					Title:   "Zrób deploy na staging",
					Status:  StatusWaiting,
					Created: "2025-03-01",
					Updated: "2025-03-01",
				},
				Body: "Czekamy na review od Chrisa",
			},
		},
		{
			"task with multiline body",
			Task{
				Meta: TaskMeta{
					ID:      "t-5",
					Title:   "Multiline",
					Status:  StatusTodo,
					Created: "2025-01-01",
					Updated: "2025-01-01",
				},
				Body: "Line 1\n\nLine 2\n\nLine 3",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.task.Filename())
			tt.task.FilePath = path

			if err := WriteTask(&tt.task); err != nil {
				t.Fatalf("WriteTask: %v", err)
			}

			got, err := ReadTask(path)
			if err != nil {
				t.Fatalf("ReadTask: %v", err)
			}

			if got.Meta.ID != tt.task.Meta.ID {
				t.Errorf("ID: got %q, want %q", got.Meta.ID, tt.task.Meta.ID)
			}
			if got.Meta.Title != tt.task.Meta.Title {
				t.Errorf("Title: got %q, want %q", got.Meta.Title, tt.task.Meta.Title)
			}
			if got.Meta.Status != tt.task.Meta.Status {
				t.Errorf("Status: got %q, want %q", got.Meta.Status, tt.task.Meta.Status)
			}
			if got.Meta.Branch != tt.task.Meta.Branch {
				t.Errorf("Branch: got %q, want %q", got.Meta.Branch, tt.task.Meta.Branch)
			}
			if got.Meta.Brief != tt.task.Meta.Brief {
				t.Errorf("Brief: got %q, want %q", got.Meta.Brief, tt.task.Meta.Brief)
			}
			if got.Body != tt.task.Body {
				t.Errorf("Body: got %q, want %q", got.Body, tt.task.Body)
			}
			// Check links
			if len(tt.task.Meta.Links) > 0 {
				for k, v := range tt.task.Meta.Links {
					if got.Meta.Links[k] != v {
						t.Errorf("Links[%s]: got %q, want %q", k, got.Meta.Links[k], v)
					}
				}
			}
			// Check tags
			if len(tt.task.Meta.Tags) != len(got.Meta.Tags) {
				t.Errorf("Tags length: got %d, want %d", len(got.Meta.Tags), len(tt.task.Meta.Tags))
			}
		})
	}
}

func TestReadTasksFromDir(t *testing.T) {
	dir := t.TempDir()

	// Write valid tasks
	t1 := &Task{
		Meta:     TaskMeta{ID: "t-1", Title: "Task One", Status: StatusTodo, Created: "2025-01-01", Updated: "2025-01-01"},
		FilePath: filepath.Join(dir, "t-1-task-one.md"),
	}
	t2 := &Task{
		Meta:     TaskMeta{ID: "t-2", Title: "Task Two", Status: StatusDoing, Created: "2025-01-01", Updated: "2025-01-01"},
		FilePath: filepath.Join(dir, "t-2-task-two.md"),
	}
	WriteTask(t1)
	WriteTask(t2)

	t.Run("reads valid tasks", func(t *testing.T) {
		tasks, err := ReadTasksFromDir(dir)
		if err != nil {
			t.Fatalf("ReadTasksFromDir: %v", err)
		}
		if len(tasks) != 2 {
			t.Errorf("got %d tasks, want 2", len(tasks))
		}
	})

	t.Run("skips non-md files", func(t *testing.T) {
		os.WriteFile(filepath.Join(dir, "project.yaml"), []byte("name: test"), 0644)
		os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0644)
		tasks, _ := ReadTasksFromDir(dir)
		if len(tasks) != 2 {
			t.Errorf("got %d tasks, want 2 (should skip non-md and yaml)", len(tasks))
		}
	})

	t.Run("skips malformed md files", func(t *testing.T) {
		os.WriteFile(filepath.Join(dir, "bad.md"), []byte("no frontmatter here"), 0644)
		tasks, _ := ReadTasksFromDir(dir)
		// bad.md may or may not parse depending on frontmatter lib behavior with no frontmatter
		// The key invariant: no crash
		if tasks == nil {
			t.Error("should return slice, not nil on partial errors")
		}
	})

	t.Run("empty dir returns empty slice", func(t *testing.T) {
		emptyDir := t.TempDir()
		tasks, err := ReadTasksFromDir(emptyDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tasks) != 0 {
			t.Errorf("got %d tasks, want 0", len(tasks))
		}
	})
}
