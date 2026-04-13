package storage

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DailyPlan holds a cross-project ordered list of task IDs planned for a given day.
type DailyPlan struct {
	Date  string   `yaml:"date"`
	Tasks []string `yaml:"tasks,omitempty"`
}

func dailyPlanPath(rootDir string) string {
	return filepath.Join(rootDir, "daily.yaml")
}

func ReadDailyPlan(rootDir string) DailyPlan {
	data, err := os.ReadFile(dailyPlanPath(rootDir))
	if err != nil {
		return DailyPlan{}
	}
	var dp DailyPlan
	if err := yaml.Unmarshal(data, &dp); err != nil {
		return DailyPlan{}
	}
	return dp
}

func WriteDailyPlan(rootDir string, plan DailyPlan) error {
	data, err := yaml.Marshal(&plan)
	if err != nil {
		return err
	}
	return os.WriteFile(dailyPlanPath(rootDir), data, 0644)
}

func (dp *DailyPlan) Contains(id string) bool {
	for _, t := range dp.Tasks {
		if t == id {
			return true
		}
	}
	return false
}

func (dp *DailyPlan) Toggle(id string) {
	if dp.Contains(id) {
		dp.Remove(id)
	} else {
		dp.Tasks = append(dp.Tasks, id)
	}
}

func (dp *DailyPlan) Remove(id string) {
	for i, t := range dp.Tasks {
		if t == id {
			dp.Tasks = append(dp.Tasks[:i], dp.Tasks[i+1:]...)
			return
		}
	}
}

func (dp *DailyPlan) Swap(i, j int) {
	if i < 0 || j < 0 || i >= len(dp.Tasks) || j >= len(dp.Tasks) {
		return
	}
	dp.Tasks[i], dp.Tasks[j] = dp.Tasks[j], dp.Tasks[i]
}

func (dp *DailyPlan) IsStale() bool {
	return dp.Date != "" && dp.Date != Today()
}

// Cleanup removes task IDs that are not found in allTasks or have status done/archived.
// Returns true if any tasks were removed.
func (dp *DailyPlan) Cleanup(allTasks []*Task) bool {
	lookup := make(map[string]*Task, len(allTasks))
	for _, t := range allTasks {
		lookup[t.Meta.ID] = t
	}
	var kept []string
	for _, id := range dp.Tasks {
		t, ok := lookup[id]
		if !ok {
			continue
		}
		if t.Meta.Status == StatusDone || t.Meta.Status == StatusArchived {
			continue
		}
		kept = append(kept, id)
	}
	changed := len(kept) != len(dp.Tasks)
	dp.Tasks = kept
	return changed
}
