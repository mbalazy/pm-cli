package service

import (
	"errors"
	"fmt"

	"github.com/mbalazy/pm/internal/storage"
)

// ToggleFocusInput names the task to add to / remove from today's focus.
type ToggleFocusInput struct {
	TaskID string `json:"task_id"`
}

// ToggleFocusResult is the plan after the toggle.
type ToggleFocusResult struct {
	TaskID  string   `json:"task_id"`
	Focused bool     `json:"focused"`
	Date    string   `json:"date"`
	TaskIDs []string `json:"task_ids"`
}

// ToggleFocus adds the task to today's focus plan or removes it - the board's
// `t` key, over the same storage.FocusPlan. The task must exist in some
// active project (the id is global, focus.yaml is cross-project), otherwise
// a *ValidationError. A plan from an earlier day is carried over the way the
// board does it on open (`handleStalePlan`): dropped ids and finished tasks
// are cleaned out and the date becomes today, so a toggle never appends to
// yesterday's list. Best-effort on the write, no lock: focus.yaml sits at the
// root, outside any project, and the board writes it lock-free too.
func ToggleFocus(store storage.TaskStore, in ToggleFocusInput) (*ToggleFocusResult, error) {
	if in.TaskID == "" {
		return nil, validation(fmt.Errorf("task_id is required"))
	}
	if _, err := findTaskAnywhere(store, in.TaskID); err != nil {
		return nil, err
	}
	plan, err := storage.ReadFocusPlan(store.RootDir())
	if err != nil {
		return nil, err
	}
	if plan.Date == "" || plan.IsStale() {
		if all, err := store.GetAllTasks(); err == nil {
			plan.Cleanup(all)
		}
		plan.Date = storage.Today()
	}
	plan.Toggle(in.TaskID)
	if err := storage.WriteFocusPlan(store.RootDir(), plan); err != nil {
		return nil, err
	}
	ids := plan.Tasks
	if ids == nil {
		ids = []string{}
	}
	return &ToggleFocusResult{TaskID: in.TaskID, Focused: plan.Contains(in.TaskID), Date: plan.Date, TaskIDs: ids}, nil
}

// findTaskAnywhere resolves an exact task id across the active projects.
// storage.ErrTaskNotFound when no project holds it.
func findTaskAnywhere(store storage.TaskStore, id string) (*storage.Task, error) {
	slugs, err := store.ListActiveProjects()
	if err != nil {
		return nil, err
	}
	for _, slug := range slugs {
		t, err := store.FindTaskExact(slug, id)
		if err == nil {
			return t, nil
		}
		if !errors.Is(err, storage.ErrTaskNotFound) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("task %q: %w", id, storage.ErrTaskNotFound)
}
