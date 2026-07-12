package storage

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var DefaultStatuses = []TaskStatus{StatusTodo, StatusDoing, StatusWaiting, StatusDone}

type Project struct {
	Name     string            `yaml:"name"`
	Prefix   string            `yaml:"prefix,omitempty"`
	Path     string            `yaml:"path,omitempty"`
	Repo     string            `yaml:"repo,omitempty"`
	Stack    string            `yaml:"stack,omitempty"`
	Links    map[string]string `yaml:"links,omitempty"`
	Tags     []string          `yaml:"tags,omitempty"`
	Statuses []string          `yaml:"statuses,omitempty"`
	Notes    string            `yaml:"notes,omitempty"`
	Archived bool              `yaml:"archived,omitempty"`
	Executor *Executor         `yaml:"executor,omitempty"`
	// ClaudeConfigDir overrides the Claude Code config dir for this project
	// (the dir CLAUDE_CONFIG_DIR points at - holds projects/, credentials, MCP).
	// Empty = the default ~/.claude. Set it when a project runs claude under a
	// separate account/config (e.g. a company Team account in ~/.claude-alt).
	// "~" is expanded. See ResolveClaudeConfigDir.
	ClaudeConfigDir string `yaml:"claude_config_dir,omitempty"`
}

// DefaultClaudeConfigDir returns the default Claude Code config dir (~/.claude),
// honoring the CLAUDE_CONFIG_DIR env var if set.
func DefaultClaudeConfigDir() string {
	if env := os.Getenv("CLAUDE_CONFIG_DIR"); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// ResolveClaudeConfigDir returns the absolute Claude config dir for this project:
// the explicit claude_config_dir (with "~" expanded) if set, else the default.
func (p *Project) ResolveClaudeConfigDir() string {
	if p == nil || p.ClaudeConfigDir == "" {
		return DefaultClaudeConfigDir()
	}
	dir := p.ClaudeConfigDir
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	return dir
}

func (p *Project) GetStatuses() []TaskStatus {
	if len(p.Statuses) == 0 {
		out := make([]TaskStatus, len(DefaultStatuses))
		copy(out, DefaultStatuses)
		return out
	}
	out := make([]TaskStatus, len(p.Statuses))
	for i, s := range p.Statuses {
		out[i] = ParseStatus(s)
	}
	return out
}

func ReadProject(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Project
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// WriteProject is the exported package-level writer, used by tests and setupTestStore.
// Production code should use Store.CreateProject() or Store.UpdateProject() instead.
func WriteProject(path string, p *Project) error {
	return writeProject(path, p)
}

func writeProject(path string, p *Project) error {
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
