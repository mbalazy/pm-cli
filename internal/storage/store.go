package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Store struct {
	Root string // ~/.claude/pm
}

func NewStore() *Store {
	if dir := os.Getenv("PM_DATA_DIR"); dir != "" {
		return &Store{Root: dir}
	}
	home, _ := os.UserHomeDir()
	return &Store{Root: filepath.Join(home, ".claude", "pm")}
}

func (s *Store) RootDir() string {
	return s.Root
}

func (s *Store) Init() error {
	return os.MkdirAll(s.Root, 0755)
}

func (s *Store) ProjectDir(slug string) string {
	return filepath.Join(s.Root, slug)
}

func (s *Store) ProjectYAML(slug string) string {
	return filepath.Join(s.Root, slug, "project.yaml")
}

// ListProjects returns all project directory slugs that contain a project.yaml.
func (s *Store) ListProjects() ([]string, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var projects []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(s.ProjectYAML(e.Name())); err == nil {
			projects = append(projects, e.Name())
		}
	}
	sort.Strings(projects)
	return projects, nil
}

// ListActiveProjects returns project slugs excluding archived ones.
func (s *Store) ListActiveProjects() ([]string, error) {
	all, err := s.ListProjects()
	if err != nil {
		return nil, err
	}
	var active []string
	for _, slug := range all {
		proj, err := s.GetProject(slug)
		if err != nil || !proj.Archived {
			active = append(active, slug)
		}
	}
	return active, nil
}

func (s *Store) GetProject(slug string) (*Project, error) {
	return ReadProject(s.ProjectYAML(slug))
}

// GetProjectStatuses returns the configured statuses for a project, or defaults.
func (s *Store) GetProjectStatuses(slug string) []TaskStatus {
	p, err := s.GetProject(slug)
	if err != nil {
		return DefaultStatuses
	}
	return p.GetStatuses()
}

// GetAllStatuses returns the union of statuses across all active (non-archived) projects, preserving order.
// Default statuses come first, then any unique custom statuses.
func (s *Store) GetAllStatuses() []TaskStatus {
	projects, err := s.ListActiveProjects()
	if err != nil {
		return DefaultStatuses
	}

	seen := make(map[TaskStatus]bool)
	var result []TaskStatus
	// seed with defaults
	for _, s := range DefaultStatuses {
		seen[s] = true
		result = append(result, s)
	}
	for _, slug := range projects {
		for _, st := range s.GetProjectStatuses(slug) {
			if !seen[st] {
				seen[st] = true
				result = append(result, st)
			}
		}
	}
	return result
}

func (s *Store) CreateProject(slug string, p *Project) error {
	dir := s.ProjectDir(slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return writeProject(s.ProjectYAML(slug), p)
}

func (s *Store) GetTasks(projectSlug string) ([]*Task, error) {
	dir := s.ProjectDir(projectSlug)
	tasks, err := ReadTasksFromDir(dir)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		t.Project = projectSlug
	}
	return tasks, nil
}

// GetAllTasks returns tasks from all active (non-archived) projects.
func (s *Store) GetAllTasks() ([]*Task, error) {
	projects, err := s.ListActiveProjects()
	if err != nil {
		return nil, err
	}

	var all []*Task
	for _, p := range projects {
		tasks, err := s.GetTasks(p)
		if err != nil {
			continue
		}
		all = append(all, tasks...)
	}
	return all, nil
}

// MoveTask changes a task's status and updates the timestamp.
// This is the single source of truth for status transition logic.
// Brief is preserved on all status transitions (useful for summaries/reverts).
//
// The caller's copy may be MINUTES old (a board loaded at the last reload, an
// epic sub read at run start) and a move rewrites the whole file - so the task
// is re-read FRESH under the project lock and only then moved, refreshing the
// caller's copy in place. Callers must NOT hold the project lock themselves
// (a second flock in the same process deadlocks); release before calling.
func (s *Store) MoveTask(t *Task, newStatus TaskStatus) error {
	if release, err := s.LockProject(t.Project); err == nil {
		defer release()
	}
	if fresh, err := s.FindTask(t.Project, t.Meta.ID); err == nil {
		*t = *fresh
	}
	t.Meta.Status = newStatus
	t.Meta.Updated = Today()
	return writeTask(t)
}

// WriteTask persists a task to disk. Use for in-place updates (reorder, undo, field changes).
// For new tasks, use AddTask. For status changes, use MoveTask.
func (s *Store) WriteTask(t *Task) error {
	return writeTask(t)
}

// UpdateProject persists project metadata to disk.
func (s *Store) UpdateProject(slug string, p *Project) error {
	return writeProject(s.ProjectYAML(slug), p)
}

func (s *Store) DeleteTask(t *Task) error {
	if t.FilePath == "" {
		return fmt.Errorf("task has no file path")
	}
	return os.Remove(t.FilePath)
}

func (s *Store) AddTask(projectSlug string, t *Task) error {
	t.Project = projectSlug
	t.FilePath = filepath.Join(s.ProjectDir(projectSlug), t.Filename())

	// Validate status against project's allowed statuses
	allowed := s.GetProjectStatuses(projectSlug)
	if err := ValidateStatus(t.Meta.Status, allowed); err != nil {
		return err
	}

	// O_EXCL: atomic create-or-fail, no race between stat and write
	f, err := os.OpenFile(t.FilePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("task file already exists: %s", t.FilePath)
		}
		return err
	}
	f.Close()

	// File claimed; now write content atomically (temp+rename)
	return writeTask(t)
}

// ProjectPrefix returns the task ID prefix for a project.
// Uses project.yaml prefix field if set, otherwise falls back to slug.
func (s *Store) ProjectPrefix(slug string) string {
	proj, err := s.GetProject(slug)
	if err == nil && proj.Prefix != "" {
		return proj.Prefix
	}
	return slug
}

// NextTaskID returns the next sequential ID for a project (e.g. "orbit2-3").
func (s *Store) NextTaskID(slug string) string {
	prefix := s.ProjectPrefix(slug)
	tasks, err := s.GetTasks(slug)
	if err != nil {
		return prefix + "-1"
	}

	maxN := 0
	for _, t := range tasks {
		if strings.HasPrefix(t.Meta.ID, prefix+"-") {
			numStr := strings.TrimPrefix(t.Meta.ID, prefix+"-")
			if n, err := strconv.Atoi(numStr); err == nil && n > maxN {
				maxN = n
			}
		}
	}
	return fmt.Sprintf("%s-%d", prefix, maxN+1)
}

// NextChildID returns the next sequential subtask ID under a parent
// (e.g. parent "atlas-39" -> "atlas-39-3"). Only direct children count -
// IDs whose remainder after the parent prefix is a plain integer.
func (s *Store) NextChildID(slug, parentID string) string {
	tasks, err := s.GetTasks(slug)
	if err != nil {
		return parentID + "-1"
	}
	prefix := parentID + "-"
	maxN := 0
	for _, t := range tasks {
		if !strings.HasPrefix(t.Meta.ID, prefix) {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(t.Meta.ID, prefix)); err == nil && n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("%s-%d", parentID, maxN+1)
}

// ResolveProject finds a project slug by prefix match.
func (s *Store) ResolveProject(input string) (string, error) {
	input = strings.ToLower(input)
	projects, err := s.ListProjects()
	if err != nil {
		return "", err
	}

	var matches []string
	for _, p := range projects {
		if strings.HasPrefix(p, input) || p == input {
			matches = append(matches, p)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("project not found: %q", input)
	case 1:
		return matches[0], nil
	default:
		if matches[0] == input {
			return matches[0], nil
		}
		return "", fmt.Errorf("ambiguous project %q: matches %s", input, strings.Join(matches, ", "))
	}
}

// FindTask finds a task by ID or title prefix within a project.
func (s *Store) FindTask(projectSlug, query string) (*Task, error) {
	tasks, err := s.GetTasks(projectSlug)
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(query)

	// exact ID match first
	for _, t := range tasks {
		if strings.ToLower(t.Meta.ID) == query {
			return t, nil
		}
	}

	// prefix match on ID
	var matches []*Task
	for _, t := range tasks {
		if strings.HasPrefix(strings.ToLower(t.Meta.ID), query) {
			matches = append(matches, t)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}

	// title substring
	for _, t := range tasks {
		if strings.Contains(strings.ToLower(t.Meta.Title), query) {
			matches = append(matches, t)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous task %q: %d matches", query, len(matches))
	}

	return nil, fmt.Errorf("task not found: %q", query)
}
