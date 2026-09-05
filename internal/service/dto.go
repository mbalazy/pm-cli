package service

import (
	"fmt"
	"os"

	"github.com/mbalazy/pm/internal/storage"
)

// The read-side DTOs. Their json tags are the agent-facing contract every
// front end (MCP, HTTP) returns verbatim - a renamed key here is a broken
// client somewhere. Where a shape used to be a map[string]any, the struct's
// FIELD ORDER is the map's alphabetical key order, so the bytes on the wire
// did not change when the map became a type.

// DefaultListLimit caps unfiltered ListTasks output: without it a full
// cross-project listing once dumped 74k chars into the calling session's
// context. Newest-first + an explicit total/shown footer keeps the tool
// useful while bounding the damage.
const DefaultListLimit = 50

// MaxListLimit is a hard ceiling on the `limit` param. Without it, the note
// telling a caller to "raise limit" when truncated is an invitation to pass
// something huge and get exactly the context dump DefaultListLimit exists to
// prevent.
const MaxListLimit = 200

// ContextBodyLimit caps each doing task's body in Context output: a project
// with a dozen doing tasks carrying full Spec/Log bodies dumps 100k+ chars
// into the calling session otherwise. The brief stays complete (it is the
// designed cold-start vehicle); the full body is one GetTask away.
const ContextBodyLimit = 2000

// TaskSummary is the listing shape of a task.
type TaskSummary struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Status     string            `json:"status"`
	Project    string            `json:"project"`
	Updated    string            `json:"updated"`
	Branch     string            `json:"branch,omitempty"`
	Parent     string            `json:"parent,omitempty"`
	Order      int               `json:"order,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Links      map[string]string `json:"links,omitempty"`
	Brief      string            `json:"brief,omitempty"`
	AC         string            `json:"ac,omitempty"`
	WaitingFor string            `json:"waiting_for,omitempty"`
	// StatusChanged is empty on every task written before the field existed -
	// pm never migrates task files. Empty means UNKNOWN, not "just now": don't
	// fall back to updated, which moves on any edit.
	StatusChanged string `json:"status_changed,omitempty"`
	SessionCount  int    `json:"session_count"`
}

// ListTasksResult wraps ListTasks output with a size-budget footer: total
// vs shown makes truncation explicit instead of silently dropping tasks.
type ListTasksResult struct {
	Tasks []TaskSummary `json:"tasks"`
	Total int           `json:"total"`
	Shown int           `json:"shown"`
	Note  string        `json:"note,omitempty"`
}

// TaskDetail is the full shape of a task (GetTask, mutation results).
type TaskDetail struct {
	TaskSummary
	Created    string   `json:"created"`
	Body       string   `json:"body,omitempty"`
	Sessions   []string `json:"sessions,omitempty"`
	DependsOn  []string `json:"depends_on,omitempty"`
	Mode       string   `json:"mode,omitempty"`
	Model      string   `json:"model,omitempty"`
	EpicMode   string   `json:"epic_mode,omitempty"`
	FinishMode string   `json:"finish_mode,omitempty"`
}

// ToSummary converts a task to its listing shape.
func ToSummary(t *storage.Task) TaskSummary {
	return TaskSummary{
		ID:            t.Meta.ID,
		Title:         t.Meta.Title,
		Status:        string(t.Meta.Status),
		Project:       t.Project,
		Updated:       t.Meta.Updated,
		Branch:        t.Meta.Branch,
		Parent:        t.Meta.Parent,
		Order:         t.Meta.Order,
		Tags:          t.Meta.Tags,
		Links:         t.Meta.Links,
		Brief:         t.Meta.Brief,
		AC:            t.Meta.AC,
		WaitingFor:    t.Meta.WaitingFor,
		StatusChanged: t.Meta.StatusChanged,
		SessionCount:  len(t.Meta.Sessions),
	}
}

// ToDetail converts a task to its full shape.
func ToDetail(t *storage.Task) TaskDetail {
	return TaskDetail{
		TaskSummary: ToSummary(t),
		Created:     t.Meta.Created,
		Body:        t.Body,
		Sessions:    t.Meta.Sessions,
		DependsOn:   t.Meta.DependsOn,
		Mode:        t.Meta.Mode,
		Model:       t.Meta.Model,
		EpicMode:    t.Meta.EpicMode,
		FinishMode:  t.Meta.FinishMode,
	}
}

// TruncateBody cuts s to max runes, appending a pointer to GetTask when it
// did cut.
func TruncateBody(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n… [body truncated - use pm_get_task for the full body]"
}

// FocusTaskSummaries expands today's focus plan into one-line-brief
// summaries of its still-open tasks; nil when there is no plan for today.
func FocusTaskSummaries(store storage.TaskStore) []TaskSummary {
	fp, err := storage.ReadFocusPlan(store.RootDir())
	if err != nil {
		// Best-effort supplementary section: never fail the whole context
		// call over a corrupt focus.yaml, but don't swallow it either.
		fmt.Fprintf(os.Stderr, "pm: skipping unreadable focus plan: %v\n", err)
		return nil
	}
	if fp.Date != storage.Today() || len(fp.Tasks) == 0 {
		return nil
	}
	allTasks, _ := store.GetAllTasks()
	lookup := make(map[string]*storage.Task, len(allTasks))
	for _, t := range allTasks {
		lookup[t.Meta.ID] = t
	}
	var result []TaskSummary
	for _, id := range fp.Tasks {
		if t, ok := lookup[id]; ok && t.Meta.Status != storage.StatusDone && t.Meta.Status != storage.StatusArchived {
			s := ToSummary(t)
			// A listing, so the same one-line rule as ListTasks: the focus
			// plan points at tasks, it is not the place to read them.
			s.Brief = storage.BriefLine(s.Brief)
			result = append(result, s)
		}
	}
	return result
}

// ProjectMeta is the project header of a project-scoped Context.
type ProjectMeta struct {
	Slug     string            `json:"slug"`
	Name     string            `json:"name"`
	Repo     string            `json:"repo,omitempty"`
	Stack    string            `json:"stack,omitempty"`
	Notes    string            `json:"notes,omitempty"`
	Links    map[string]string `json:"links,omitempty"`
	Statuses []string          `json:"statuses"`
}

// ProjectContextResult is the project-scoped Context shape. Field order =
// the alphabetical key order of the map it replaced.
type ProjectContextResult struct {
	DoingTasks      []TaskDetail           `json:"doing_tasks"`
	ExecutorProfile string                 `json:"executor_profile,omitempty"`
	FocusTasks      []TaskSummary          `json:"focus_tasks,omitempty"`
	Journals        []storage.JournalCount `json:"journals,omitempty"`
	JournalsNote    string                 `json:"journals_note,omitempty"`
	Project         ProjectMeta            `json:"project"`
	TaskCounts      map[string]int         `json:"task_counts"`
	Trackers        []storage.Tracker      `json:"trackers,omitempty"`
}

// ProjectSummary is one project's row in the cross-project Context.
type ProjectSummary struct {
	Slug       string            `json:"slug"`
	Name       string            `json:"name"`
	Repo       string            `json:"repo,omitempty"`
	TaskCounts map[string]int    `json:"task_counts"`
	DoingTasks []TaskSummary     `json:"doing_tasks,omitempty"`
	Trackers   []storage.Tracker `json:"trackers,omitempty"`
}

// CrossProjectContextResult is the cross-project Context shape. Field order
// = the alphabetical key order of the map it replaced.
type CrossProjectContextResult struct {
	FocusTasks []TaskSummary    `json:"focus_tasks,omitempty"`
	Note       string           `json:"note,omitempty"`
	Projects   []ProjectSummary `json:"projects"`
}

// ProjectInfo is one project's row in ListProjects.
type ProjectInfo struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Stack    string `json:"stack,omitempty"`
	Archived bool   `json:"archived,omitempty"`
	// Group is the cockpit group slug (project.yaml `group`, else the project's
	// own slug - never empty); GroupName is its display name from the global
	// config's cockpit.groups (else the slug).
	Group      string         `json:"group"`
	GroupName  string         `json:"group_name"`
	TaskCounts map[string]int `json:"task_counts"`
}

// ListGroupsResult is ListGroups' shape: the cockpit's project groups,
// derived from the active projects' `group` fields.
type ListGroupsResult struct {
	Groups []storage.ProjectGroup `json:"groups"`
}

// ListProjectsResult wraps the array so a broken project can be skipped
// with a visible warning instead of silently going missing - the same
// shape as ListTasksResult.Note, applied here for the same reason.
type ListProjectsResult struct {
	Projects []ProjectInfo `json:"projects"`
	Note     string        `json:"note,omitempty"`
}

// ProjectResult is the shape CreateProject and UpdateProject return.
type ProjectResult struct {
	Slug     string            `json:"slug"`
	Name     string            `json:"name"`
	Path     string            `json:"path,omitempty"`
	Repo     string            `json:"repo,omitempty"`
	Stack    string            `json:"stack,omitempty"`
	Notes    string            `json:"notes,omitempty"`
	Prefix   string            `json:"prefix,omitempty"`
	Links    map[string]string `json:"links,omitempty"`
	Tags     []string          `json:"tags,omitempty"`
	Statuses []string          `json:"statuses,omitempty"`
	Archived bool              `json:"archived,omitempty"`
	Group    string            `json:"group,omitempty"`
}
