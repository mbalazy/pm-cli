package service

import (
	"fmt"
	"sort"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// ListTasksInput is the argument set of pm_list_tasks.
type ListTasksInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project slug or prefix (omit for all projects)"`
	Status  string `json:"status,omitempty" jsonschema:"Filter by status (e.g. todo, doing, done, archived)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Max tasks to return, newest first (default 50, hard cap 200). The result reports total vs shown; raise limit (up to 200) or narrow with project/status to see more of a truncated result."`
}

// GetTaskInput is the argument set of pm_get_task.
type GetTaskInput struct {
	Project string `json:"project" jsonschema:"Project slug or prefix"`
	TaskID  string `json:"task_id" jsonschema:"Task ID or search query"`
}

// ListTasks lists tasks newest first under the output budget (DefaultListLimit,
// MaxListLimit), briefs compressed to one line. Archived tasks are excluded
// unless status=archived; a status outside the allowed set (the project's,
// or cross-project the union of every project's) is a *ValidationError.
func ListTasks(store storage.TaskStore, in ListTasksInput) (*ListTasksResult, error) {
	var tasks []*storage.Task
	var err error
	var allowedStatuses []storage.TaskStatus

	if in.Project != "" {
		slug, e := store.ResolveProject(in.Project)
		if e != nil {
			return nil, e
		}
		allowedStatuses = store.GetProjectStatuses(slug)
		tasks, err = store.GetTasks(slug)
	} else {
		// Cross-project: validate against the UNION of every project's
		// statuses + archived, matching how the board's ALL view counts them.
		allowedStatuses = store.GetAllStatuses()
		tasks, err = store.GetAllTasks()
	}
	if err != nil {
		return nil, err
	}

	var status storage.TaskStatus
	if in.Status != "" {
		// A typo'd status used to silently render an empty list; reject it
		// with the allowed set instead, like UpdateTask/MoveTask.
		status = storage.ParseStatus(in.Status)
		if status != storage.StatusArchived {
			if err := validation(storage.ValidateStatus(status, allowedStatuses)); err != nil {
				return nil, err
			}
		}
	}

	filtered := []TaskSummary{}
	for _, t := range tasks {
		if in.Status != "" {
			if t.Meta.Status != status {
				continue
			}
		} else if t.Meta.Status == storage.StatusArchived {
			continue
		}
		s := ToSummary(t)
		// One-line brief in listings; GetTask returns the full brief.
		s.Brief = storage.BriefLine(s.Brief)
		filtered = append(filtered, s)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Updated > filtered[j].Updated
	})

	limit := in.Limit
	capped := false
	if limit <= 0 {
		limit = DefaultListLimit
	} else if limit > MaxListLimit {
		limit = MaxListLimit
		capped = true
	}
	total := len(filtered)
	if total > limit {
		filtered = filtered[:limit]
	}
	result := &ListTasksResult{Tasks: filtered, Total: total, Shown: len(filtered)}
	switch {
	case capped && result.Shown < total:
		result.Note = fmt.Sprintf("%d of %d tasks shown (newest first) - limit capped at %d, narrow with project/status", result.Shown, total, MaxListLimit)
	case capped:
		result.Note = fmt.Sprintf("all %d tasks shown; limit capped at %d", total, MaxListLimit)
	case result.Shown < total:
		result.Note = fmt.Sprintf("%d of %d tasks shown (newest first) - narrow with project/status or raise limit", result.Shown, total)
	}
	return result, nil
}

// GetTask resolves in.TaskID fuzzily (exact ID, ID prefix, title substring)
// and returns the full task; unbounded on purpose.
func GetTask(store storage.TaskStore, in GetTaskInput) (*TaskDetail, error) {
	slug, err := store.ResolveProject(in.Project)
	if err != nil {
		return nil, err
	}
	task, err := store.FindTask(slug, in.TaskID)
	if err != nil {
		return nil, err
	}
	d := ToDetail(task)
	return &d, nil
}
