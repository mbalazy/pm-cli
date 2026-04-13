package board

import (
	"os"
	"testing"
)

func TestTUIConfig(t *testing.T) {
	t.Run("load missing file returns empty", func(t *testing.T) {
		dir := t.TempDir()
		cfg := loadTUIConfig(dir)
		if len(cfg.HiddenProjects) != 0 {
			t.Errorf("expected empty hidden projects, got %v", cfg.HiddenProjects)
		}
	})

	t.Run("save and load roundtrip", func(t *testing.T) {
		dir := t.TempDir()
		err := saveTUIConfig(dir, tuiConfig{
			HiddenProjects: []string{"alpha", "beta"},
			ProjectOrder:   []string{"gamma", "alpha", "beta"},
		})
		if err != nil {
			t.Fatal(err)
		}
		cfg := loadTUIConfig(dir)
		if len(cfg.HiddenProjects) != 2 {
			t.Fatalf("expected 2 hidden projects, got %d", len(cfg.HiddenProjects))
		}
		if cfg.HiddenProjects[0] != "alpha" || cfg.HiddenProjects[1] != "beta" {
			t.Errorf("unexpected hidden projects: %v", cfg.HiddenProjects)
		}
		if len(cfg.ProjectOrder) != 3 {
			t.Fatalf("expected 3 project order, got %d", len(cfg.ProjectOrder))
		}
		if cfg.ProjectOrder[0] != "gamma" {
			t.Errorf("first in order should be gamma, got %q", cfg.ProjectOrder[0])
		}
	})

	t.Run("save empty clears list", func(t *testing.T) {
		dir := t.TempDir()
		saveTUIConfig(dir, tuiConfig{HiddenProjects: []string{"x"}})
		saveTUIConfig(dir, tuiConfig{})
		cfg := loadTUIConfig(dir)
		if len(cfg.HiddenProjects) != 0 {
			t.Errorf("expected empty after save, got %v", cfg.HiddenProjects)
		}
	})

	t.Run("invalid yaml returns empty", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(tuiConfigPath(dir), []byte("{{invalid"), 0644)
		cfg := loadTUIConfig(dir)
		if len(cfg.HiddenProjects) != 0 {
			t.Errorf("expected empty on invalid yaml, got %v", cfg.HiddenProjects)
		}
	})
}
