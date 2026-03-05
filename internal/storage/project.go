package storage

import (
	"os"

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

func WriteProject(path string, p *Project) error {
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
