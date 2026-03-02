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
	home, _ := os.UserHomeDir()
	return &Store{Root: filepath.Join(home, ".claude", "pm")}
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

// GetAllStatuses returns the union of statuses across all projects, preserving order.
// Default statuses come first, then any unique custom statuses.
func (s *Store) GetAllStatuses() []TaskStatus {
	projects, err := s.ListProjects()
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
	return WriteProject(s.ProjectYAML(slug), p)
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

// GetAllTasks returns tasks from all projects.
func (s *Store) GetAllTasks() ([]*Task, error) {
	projects, err := s.ListProjects()
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

// MoveTask changes a task's status, updates the timestamp, and clears brief on done/archived.
// This is the single source of truth for status transition logic.
func (s *Store) MoveTask(t *Task, newStatus TaskStatus) error {
	t.Meta.Status = newStatus
	t.Meta.Updated = Today()
	if newStatus == StatusDone || newStatus == StatusArchived {
		if t.Meta.Brief != "" {
			sep := "\n\n"
			if t.Body == "" {
				sep = ""
			}
			briefNote := fmt.Sprintf("%s---\n**Session brief (archived %s):**\n%s", sep, Today(), t.Meta.Brief)
			t.Body += briefNote
		}
		t.Meta.Brief = ""
	}
	return WriteTask(t)
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

	// check for duplicate filename
	if _, err := os.Stat(t.FilePath); err == nil {
		return fmt.Errorf("task file already exists: %s", t.FilePath)
	}

	return WriteTask(t)
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
