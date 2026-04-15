package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/frontmatter"
	"gopkg.in/yaml.v3"
)

// ValidateStatus checks if a status is in the allowed list.
func ValidateStatus(s TaskStatus, allowed []TaskStatus) error {
	for _, a := range allowed {
		if a == s {
			return nil
		}
	}
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	return fmt.Errorf("invalid status %q (valid: %s)", s, strings.Join(names, ", "))
}

type TaskStatus string

const (
	StatusTodo     TaskStatus = "todo"
	StatusDoing    TaskStatus = "doing"
	StatusWaiting  TaskStatus = "waiting"
	StatusDone     TaskStatus = "done"
	StatusArchived TaskStatus = "archived"
)

func ParseStatus(s string) TaskStatus {
	return TaskStatus(strings.ToLower(s))
}

type TaskMeta struct {
	ID      string            `yaml:"id"`
	Title   string            `yaml:"title"`
	Status  TaskStatus        `yaml:"status"`
	Created string            `yaml:"created"`
	Updated string            `yaml:"updated"`
	Links   map[string]string `yaml:"links,omitempty"`
	Branch  string            `yaml:"branch,omitempty"`
	Tags    []string          `yaml:"tags,omitempty"`
	Brief    string            `yaml:"brief,omitempty"`
	AC       string            `yaml:"ac,omitempty"`
	Order    int               `yaml:"order,omitempty"`
	Sessions []string          `yaml:"sessions,omitempty"`
}

type Task struct {
	Meta     TaskMeta
	Body     string
	FilePath string
	Project  string
}

func (t *Task) Filename() string {
	slug := Slugify(t.Meta.Title)
	if t.Meta.ID != "" {
		return t.Meta.ID + "-" + slug + ".md"
	}
	return slug + ".md"
}

// Slugify converts a string to a URL/branch-safe slug.
func Slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		if r == ' ' || r == '_' || r == '/' {
			return '-'
		}
		return -1
	}, s)
	// collapse multiple dashes
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func ReadTask(path string) (*Task, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var meta TaskMeta
	body, err := frontmatter.Parse(f, &meta)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return &Task{
		Meta:     meta,
		Body:     strings.TrimSpace(string(body)),
		FilePath: path,
	}, nil
}

// WriteTask is the exported package-level writer, used by tests and setupTestStore.
// Production code should use Store.WriteTask() or Store.AddTask() instead.
func WriteTask(t *Task) error {
	return writeTask(t)
}

func writeTask(t *Task) error {
	metaBytes, err := yaml.Marshal(t.Meta)
	if err != nil {
		return err
	}

	var buf strings.Builder
	buf.WriteString("---\n")
	buf.Write(metaBytes)
	buf.WriteString("---\n")
	if t.Body != "" {
		buf.WriteString("\n")
		buf.WriteString(t.Body)
		buf.WriteString("\n")
	}

	return atomicWriteFile(t.FilePath, []byte(buf.String()), 0644)
}

// atomicWriteFile writes data to a temp file then renames it to path.
// This prevents partial writes on crash.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pm-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// Today returns the current date as YYYY-MM-DD.
func Today() string {
	return time.Now().Format("2006-01-02")
}

func NewTask(id, title, project string) *Task {
	now := Today()
	return &Task{
		Meta: TaskMeta{
			ID:      id,
			Title:   title,
			Status:  StatusTodo,
			Created: now,
			Updated: now,
		},
		Project: project,
	}
}

func ReadTasksFromDir(dir string) ([]*Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var tasks []*Task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "project.yaml" {
			continue
		}
		t, err := ReadTask(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip unreadable files
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}
