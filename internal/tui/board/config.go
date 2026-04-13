package board

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type tuiConfig struct {
	HiddenProjects []string `yaml:"hidden_projects,omitempty"`
	ProjectOrder   []string `yaml:"project_order,omitempty"`
}

func tuiConfigPath(rootDir string) string {
	return filepath.Join(rootDir, "tui-config.yaml")
}

func loadTUIConfig(rootDir string) tuiConfig {
	data, err := os.ReadFile(tuiConfigPath(rootDir))
	if err != nil {
		return tuiConfig{}
	}
	var cfg tuiConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return tuiConfig{}
	}
	return cfg
}

func saveTUIConfig(rootDir string, cfg tuiConfig) error {
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(tuiConfigPath(rootDir), data, 0644)
}
