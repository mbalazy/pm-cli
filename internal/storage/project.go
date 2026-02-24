package storage

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Project struct {
	Name string            `yaml:"name"`
	Path string            `yaml:"path,omitempty"`
	Repo string            `yaml:"repo,omitempty"`
	Links map[string]string `yaml:"links,omitempty"`
	Tags  []string          `yaml:"tags,omitempty"`
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
