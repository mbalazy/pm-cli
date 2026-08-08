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
		{"long title cut at word boundary", "enable analytics tracking for the seven day streak retention flow now", "enable-analytics-tracking-for-the-seven-day-streak"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Slugify(tt.in)
			if got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.in, got, tt.want)
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

func TestValidateMode(t *testing.T) {
	for _, ok := range []string{"", "auto", "manual"} {
		if err := ValidateMode(ok); err != nil {
			t.Errorf("ValidateMode(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"Manual", "AUTO", "sometimes", " manual"} {
		if err := ValidateMode(bad); err == nil {
			t.Errorf("ValidateMode(%q) = nil, want error (case-sensitive lowercase only)", bad)
		}
	}
}

func TestValidateEpicMode(t *testing.T) {
	for _, ok := range []string{"", EpicModeIndependent} {
		if err := ValidateEpicMode(ok); err != nil {
			t.Errorf("ValidateEpicMode(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"Independent", "INDEPENDENT", "integration", "batch", " independent"} {
		if err := ValidateEpicMode(bad); err == nil {
			t.Errorf("ValidateEpicMode(%q) = nil, want error (case-sensitive lowercase only)", bad)
		}
	}
}

func TestValidateFinishMode(t *testing.T) {
	// "" is a third spelling of "off" and MUST stay legal: every tracker
	// written before the field existed carries it.
	for _, ok := range []string{"", FinishModeAuto, FinishModeOff} {
		if err := ValidateFinishMode(ok); err != nil {
			t.Errorf("ValidateFinishMode(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"Auto", "AUTO", "Off", "on", "true", "finish", " auto", "auto "} {
		if err := ValidateFinishMode(bad); err == nil {
			t.Errorf("ValidateFinishMode(%q) = nil, want error (case-sensitive lowercase only)", bad)
		}
	}
}

// TestWriteTaskRejectsInvalidFinishMode is TestWriteTaskRejectsInvalidEpicMode's
// sibling, and exists for the same reason: the gate has to sit in writeTask so
// EVERY writer is covered, and a rejected write must leave no file behind - a
// 0-byte task file would make the retry fail as "already exists" (pm-cli-41).
func TestWriteTaskRejectsInvalidFinishMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t-1-tracker.md")

	newTask := func(finishMode string) *Task {
		return &Task{
			Meta:     TaskMeta{ID: "t-1", Title: "Tracker", Status: StatusTodo, FinishMode: finishMode},
			FilePath: path,
			Project:  "test",
		}
	}

	if err := WriteTask(newTask("Auto")); err == nil {
		t.Fatal("expected write to reject invalid finish_mode")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("rejected write must not create the file")
	}

	for _, ok := range []string{"", FinishModeOff, FinishModeAuto} {
		if err := WriteTask(newTask(ok)); err != nil {
			t.Errorf("WriteTask with finish_mode %q: %v", ok, err)
		}
	}
	reloaded, err := ReadTask(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Meta.FinishMode != FinishModeAuto {
		t.Errorf("finish_mode = %q, want %q", reloaded.Meta.FinishMode, FinishModeAuto)
	}

	// A rejected write over an EXISTING task must leave the good one on disk:
	// writeTask validates before it marshals anything, so the previous content
	// stands.
	if err := WriteTask(newTask("nightly")); err == nil {
		t.Fatal("expected write to reject invalid finish_mode")
	}
	again, err := ReadTask(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Meta.FinishMode != FinishModeAuto {
		t.Errorf("a rejected write clobbered the stored task: finish_mode = %q", again.Meta.FinishMode)
	}
}

// TestWriteTaskRejectsInvalidEpicMode pins the storage-level gate: epic_mode is
// checked in writeTask exactly like mode, so every writer (MCP, CLI, TUI,
// executor) is covered - not just the MCP handlers. Without it a hand-edited
// `epic_mode: INDEPENDENT` would survive every later pm rewrite and only
// surface when `pm run-epic` refuses to start.
func TestWriteTaskRejectsInvalidEpicMode(t *testing.T) {
	dir := t.TempDir()

	newTask := func(epicMode string) *Task {
		return &Task{
			Meta:     TaskMeta{ID: "t-1", Title: "Tracker", Status: StatusTodo, EpicMode: epicMode},
			FilePath: filepath.Join(dir, "t-1-tracker.md"),
			Project:  "test",
		}
	}

	if err := WriteTask(newTask("INDEPENDENT")); err == nil {
		t.Fatal("expected write to reject invalid epic_mode")
	}
	if _, err := os.Stat(filepath.Join(dir, "t-1-tracker.md")); !os.IsNotExist(err) {
		t.Error("rejected write must not create the file")
	}

	for _, ok := range []string{"", EpicModeIndependent} {
		if err := WriteTask(newTask(ok)); err != nil {
			t.Errorf("WriteTask with epic_mode %q: %v", ok, err)
		}
	}
	reloaded, err := ReadTask(filepath.Join(dir, "t-1-tracker.md"))
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Meta.EpicMode != EpicModeIndependent {
		t.Errorf("epic_mode = %q, want %q", reloaded.Meta.EpicMode, EpicModeIndependent)
	}
}

func TestValidateSlug(t *testing.T) {
	for _, ok := range []string{"test", "app-orbit", "mobile-claude-toolkit", "app.orbit", "a", "a1", "full_project", "pm-cli"} {
		if err := ValidateSlug(ok); err != nil {
			t.Errorf("ValidateSlug(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		"", "../foo", "..", ".", "foo/bar", "/etc/passwd", "-foo", ".foo",
		"Foo", "FOO", "foo bar", "foo/../bar", "~foo",
	} {
		if err := ValidateSlug(bad); err == nil {
			t.Errorf("ValidateSlug(%q) = nil, want error", bad)
		}
	}
}

// TestValidateTaskID: the ID is a path component (Filename renders
// "<id>-<title>.md"), so traversal must be impossible - while every ID shape
// that exists in real data keeps working (legacy numeric "30422", imported
// ticket keys "ACME-253", hierarchical sub ids "pm-cli-72-3").
func TestValidateTaskID(t *testing.T) {
	for _, ok := range []string{
		"pm-cli-72-3", "30422", "ACME-253", "a", "1", "app.orbit-1",
		"full_project-2", "orbit2-16",
	} {
		if err := ValidateTaskID(ok); err != nil {
			t.Errorf("ValidateTaskID(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		"", "..", ".", "../foo", "../../escaped", "foo/bar", "/etc/passwd",
		"foo/../bar", ".hidden", "-foo", "foo bar", "~foo", "a\\b",
	} {
		if err := ValidateTaskID(bad); err == nil {
			t.Errorf("ValidateTaskID(%q) = nil, want error", bad)
		}
	}
}

// TestValidateProjectPrefix: the prefix is the ID source for every auto-minted
// task, so it has to pass the ID bar - otherwise a project accepts creation
// and then rejects every single `pm add` on it.
func TestValidateProjectPrefix(t *testing.T) {
	for _, ok := range []string{"", "pm-cli", "best", "rc", "a1", "app.le"} {
		if err := ValidateProjectPrefix(ok); err != nil {
			t.Errorf("ValidateProjectPrefix(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"My Proj", "../x", "sub/dir", ".hidden", "-lead"} {
		if err := ValidateProjectPrefix(bad); err == nil {
			t.Errorf("ValidateProjectPrefix(%q) = nil, want error", bad)
		}
	}
}

// TestCreateProjectRejectsUnsafeSlug pins the check at the STORAGE layer, not
// only in `pm projects add`: pm_create_project's handler pre-check would
// otherwise mask a missing guard here, leaving every future caller (a new
// command, an importer) able to MkdirAll its way out of the pm root.
func TestCreateProjectRejectsUnsafeSlug(t *testing.T) {
	root := t.TempDir()
	s := &Store{Root: filepath.Join(root, "pm")}
	if err := os.MkdirAll(s.Root, 0755); err != nil {
		t.Fatal(err)
	}

	for _, slug := range []string{"../outside", "MyProj", "sub/dir", "..", ""} {
		err := s.CreateProject(slug, &Project{Name: "X"})
		if err == nil {
			t.Fatalf("CreateProject(%q) returned nil", slug)
		}
		if !strings.Contains(err.Error(), "invalid slug") {
			t.Errorf("slug %q: want a ValidateSlug error, got: %v", slug, err)
		}
	}
	// Nothing was created inside the pm root...
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected creates left %d entr(ies) in the pm root: %v", len(entries), entries[0].Name())
	}
	// ...nor next to it, where "../outside" would have landed.
	entries, err = os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "pm" {
		t.Fatalf("rejected create escaped the pm root: %v", entries)
	}
}

// TestMutateProjectRejectsNewUnsafePrefix: CreateProject guards the prefix on
// creation, but the partial-edit path must not be able to introduce one later
// (the same asymmetry argument the slug check makes). A pre-existing bad
// prefix stays editable - refusing there would make the project unfixable.
func TestMutateProjectRejectsNewUnsafePrefix(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	if err := s.CreateProject("app", &Project{Name: "App", Prefix: "ap"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.MutateProject("app", func(p *Project) error {
		p.Prefix = "My Proj"
		return nil
	}); err == nil {
		t.Fatal("MutateProject accepted an unsafe prefix")
	}
	proj, err := s.GetProject("app")
	if err != nil {
		t.Fatal(err)
	}
	if proj.Prefix != "ap" {
		t.Fatalf("rejected mutation still wrote the prefix: %q", proj.Prefix)
	}

	// A project that ALREADY carries a bad prefix stays editable elsewhere.
	dir := s.ProjectDir("legacy")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteProject(s.ProjectYAML("legacy"), &Project{Name: "Legacy", Prefix: "My Proj"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MutateProject("legacy", func(p *Project) error {
		p.Notes = "still editable"
		return nil
	}); err != nil {
		t.Fatalf("an untouched legacy prefix must not block other edits: %v", err)
	}
}

// TestCreateProjectRejectsUnsafePrefix: rejected at creation, so the lockout
// (project exists, no task can ever be added to it) cannot be created at all.
func TestCreateProjectRejectsUnsafePrefix(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	err := s.CreateProject("acme", &Project{Name: "Acme", Prefix: "Acme Corp"})
	if err == nil {
		t.Fatal("an unsafe prefix was accepted at create")
	}
	if !strings.Contains(err.Error(), "invalid prefix") {
		t.Errorf("want an invalid-prefix error, got: %v", err)
	}
	if _, statErr := os.Stat(s.ProjectYAML("acme")); !os.IsNotExist(statErr) {
		t.Errorf("rejected create still wrote project.yaml (stat err = %v)", statErr)
	}

	// The valid case is untouched, including an empty prefix (slug is used).
	if err := s.CreateProject("acme", &Project{Name: "Acme", Prefix: "ac"}); err != nil {
		t.Fatalf("valid prefix rejected: %v", err)
	}
	if err := s.CreateProject("plain", &Project{Name: "Plain"}); err != nil {
		t.Fatalf("empty prefix rejected: %v", err)
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
					ID:       "t-2",
					Title:    "Full Task",
					Status:   StatusDoing,
					Created:  "2025-01-01",
					Updated:  "2025-06-15",
					Links:    map[string]string{"pr": "https://github.com/pr/1", "jira": "SCRUM-42"},
					Branch:   "feat/auth",
					Tags:     []string{"backend", "urgent"},
					Brief:    "Working on auth flow",
					EpicMode: EpicModeIndependent,
					Model:    "sonnet",
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
			if got.Meta.EpicMode != tt.task.Meta.EpicMode {
				t.Errorf("EpicMode: got %q, want %q", got.Meta.EpicMode, tt.task.Meta.EpicMode)
			}
			if got.Meta.Model != tt.task.Meta.Model {
				t.Errorf("Model: got %q, want %q", got.Meta.Model, tt.task.Meta.Model)
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
		// No frontmatter at all: a stray README/note in the data dir must not
		// become a phantom task with an empty ID.
		os.WriteFile(filepath.Join(dir, "bad.md"), []byte("no frontmatter here"), 0644)
		// Broken YAML inside the frontmatter block: a hand-edit gone wrong.
		os.WriteFile(filepath.Join(dir, "broken.md"), []byte("---\nid: [unclosed\n---\nbody"), 0644)
		tasks, err := ReadTasksFromDir(dir)
		if err != nil {
			t.Fatalf("partial errors must not fail the whole dir: %v", err)
		}
		if len(tasks) != 2 {
			t.Errorf("got %d tasks, want 2 (bad.md and broken.md skipped, valid ones kept)", len(tasks))
		}
		for _, tk := range tasks {
			if tk.Meta.ID == "" {
				t.Errorf("phantom task with empty ID leaked in: %+v", tk.Meta)
			}
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
