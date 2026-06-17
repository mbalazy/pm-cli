package storage

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FocusPlan holds a cross-project ordered list of task IDs the user wants to focus on.
type FocusPlan struct {
	Date  string   `yaml:"date"`
	Tasks []string `yaml:"tasks,omitempty"`
}

func focusPlanPath(rootDir string) string {
	return filepath.Join(rootDir, "focus.yaml")
}

func ReadFocusPlan(rootDir string) FocusPlan {
	path := focusPlanPath(rootDir)
	data, err := os.ReadFile(path)
	if err != nil {
		// Migration: try legacy daily.yaml
		data, err = os.ReadFile(filepath.Join(rootDir, "daily.yaml"))
		if err != nil {
			return FocusPlan{}
		}
	}
	var fp FocusPlan
	if err := yaml.Unmarshal(data, &fp); err != nil {
		return FocusPlan{}
	}
	return fp
}

func WriteFocusPlan(rootDir string, plan FocusPlan) error {
	data, err := yaml.Marshal(&plan)
	if err != nil {
		return err
	}
	path := focusPlanPath(rootDir)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}
	// Migration: remove legacy daily.yaml after successful write
	legacy := filepath.Join(rootDir, "daily.yaml")
	if _, err := os.Stat(legacy); err == nil {
		os.Remove(legacy)
	}
	return nil
}

func (fp *FocusPlan) Contains(id string) bool {
	for _, t := range fp.Tasks {
		if t == id {
			return true
		}
	}
	return false
}

func (fp *FocusPlan) Toggle(id string) {
	if fp.Contains(id) {
		fp.Remove(id)
	} else {
		fp.Tasks = append(fp.Tasks, id)
	}
}

func (fp *FocusPlan) Remove(id string) {
	for i, t := range fp.Tasks {
		if t == id {
			fp.Tasks = append(fp.Tasks[:i], fp.Tasks[i+1:]...)
			return
		}
	}
}

func (fp *FocusPlan) Swap(i, j int) {
	if i < 0 || j < 0 || i >= len(fp.Tasks) || j >= len(fp.Tasks) {
		return
	}
	fp.Tasks[i], fp.Tasks[j] = fp.Tasks[j], fp.Tasks[i]
}

func (fp *FocusPlan) IsStale() bool {
	return fp.Date != "" && fp.Date != Today()
}

// Cleanup removes task IDs that are not found in allTasks or have status done/archived.
// Returns true if any tasks were removed.
func (fp *FocusPlan) Cleanup(allTasks []*Task) bool {
	lookup := make(map[string]*Task, len(allTasks))
	for _, t := range allTasks {
		lookup[t.Meta.ID] = t
	}
	var kept []string
	for _, id := range fp.Tasks {
		t, ok := lookup[id]
		if !ok {
			continue
		}
		if t.Meta.Status == StatusDone || t.Meta.Status == StatusArchived {
			continue
		}
		kept = append(kept, id)
	}
	changed := len(kept) != len(fp.Tasks)
	fp.Tasks = kept
	return changed
}
