package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Input structs (auto-generate JSON schema via struct tags) ---

type listTasksInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project slug or prefix (omit for all projects)"`
	Status  string `json:"status,omitempty" jsonschema:"Filter by status (e.g. todo, doing, done, archived)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Max tasks to return, newest first (default 50). The result reports total vs shown; narrow with project/status or raise limit to see more."`
}

// defaultListLimit caps unfiltered pm_list_tasks output: without it a full
// cross-project listing once dumped 74k chars into the calling session's
// context. Newest-first + an explicit total/shown footer keeps the tool
// useful while bounding the damage.
const defaultListLimit = 50

type getTaskInput struct {
	Project string `json:"project" jsonschema:"Project slug or prefix"`
	TaskID  string `json:"task_id" jsonschema:"Task ID or search query"`
}

type contextInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project slug or prefix"`
	Cwd     string `json:"cwd,omitempty" jsonschema:"Current working directory for auto-detection"`
}

type addTaskInput struct {
	Project   string            `json:"project" jsonschema:"Project slug or prefix"`
	Title     string            `json:"title" jsonschema:"Task title"`
	Status    string            `json:"status,omitempty" jsonschema:"Initial status (default: first project status)"`
	Branch    string            `json:"branch,omitempty" jsonschema:"Git branch name"`
	Parent    string            `json:"parent,omitempty" jsonschema:"Parent task ID for subtasks (e.g. atlas-39). Makes this a child of a tracker task."`
	Order     int               `json:"order,omitempty" jsonschema:"Sort order within a column / parent rollup (lower runs first; convention: 10, 20, 30...). 0 = unset (sorts before ordered siblings, then by ID). Used by pm run-epic for sub execution order."`
	DependsOn []string          `json:"depends_on,omitempty" jsonschema:"Sub IDs this subtask depends on (e.g. atlas-39-2). pm run-epic skips this sub (no worker spawned) until every listed dep is merged/done, then a re-run picks it up. Empty = runs by Order."`
	Mode      string            `json:"mode,omitempty" jsonschema:"Execution mode for a subtask under pm run-epic. 'auto' (default, empty) runs autonomously via a headless worker. 'manual' marks it human-only: pm run-epic skips it entirely (no worker spawned, status untouched) as a PERMANENT gate on every run until you do the work and move it to the done status yourself. Use for subs needing interactive/visual work (simulator verification, design/visual checks)."`
	Model     string            `json:"model,omitempty" jsonschema:"Worker model override for this sub under pm run-epic / pm work (claude alias or full name, e.g. 'sonnet'). Empty = inherit the run-level model. Put trivial subs (copy/color/one-prop tweaks) on a cheaper model; leave investigation subs on the default."`
	EpicMode  string            `json:"epic_mode,omitempty" jsonschema:"How pm run-epic drives this PARENT tracker's subs. Empty (default) = integration mode: subs branch off and merge back into a shared epic/<tracker> branch, ending in one epic PR. 'independent' = batch mode for UNRELATED tasks: each sub gets its own branch off the base, is pushed when it carries commits, and nothing is merged (no integration branch, no epic PR). Meaningful on a tracker only."`
	Tags      []string          `json:"tags,omitempty" jsonschema:"Tags"`
	Links     map[string]string `json:"links,omitempty" jsonschema:"Links as key=url pairs (e.g. azure, pr, slack)"`
	Body      string            `json:"body,omitempty" jsonschema:"Markdown body content. This is the append-only Log zone (session history)."`
	Spec      string            `json:"spec,omitempty" jsonschema:"Initial Spec block (current-truth zone): what we're building, current decisions, still-open questions. Wrapped in spec markers at the top of the body; the body field becomes the append-only Log below it."`
	ID        string            `json:"id,omitempty" jsonschema:"Task ID (auto-generated if omitted; when parent is set, auto-numbers as <parent>-<n>)"`
	Brief     string            `json:"brief,omitempty" jsonschema:"Short session context summary (overwrites previous)"`
	AC        string            `json:"ac,omitempty" jsonschema:"Acceptance criteria (overwrites previous)"`
	Sessions  []string          `json:"sessions,omitempty" jsonschema:"Claude session IDs to attach"`
}

type updateTaskInput struct {
	Project    string            `json:"project" jsonschema:"Project slug or prefix"`
	TaskID     string            `json:"task_id" jsonschema:"Task ID or search query"`
	Status     string            `json:"status,omitempty" jsonschema:"New status"`
	Title      string            `json:"title,omitempty" jsonschema:"New title"`
	Branch     string            `json:"branch,omitempty" jsonschema:"Git branch name"`
	Parent     string            `json:"parent,omitempty" jsonschema:"Parent task ID for subtasks (e.g. atlas-39). Set to make this a child of a tracker task."`
	Order      *int              `json:"order,omitempty" jsonschema:"Set sort order within a column / parent rollup (lower runs first; convention: 10, 20, 30...). Omit to keep current; pass 0 to clear. Changes only the order - the rest of the task is untouched."`
	DependsOn  []string          `json:"depends_on,omitempty" jsonschema:"Replace the sub's depends_on list (sub IDs that must be merged/done before pm run-epic runs this sub). Omit to keep current; pass an empty array to clear."`
	Mode       *string           `json:"mode,omitempty" jsonschema:"Set the execution mode for pm run-epic. 'auto' runs the sub autonomously via a headless worker; 'manual' makes pm run-epic skip this sub (no worker spawned, status untouched) as a permanent gate until you do the work and move it to the done status yourself. Omit to keep current."`
	Model      *string           `json:"model,omitempty" jsonschema:"Set the worker model override for pm run-epic / pm work (claude alias or full name, e.g. 'sonnet'). Empty string clears it (inherit run-level model). Omit to keep current."`
	EpicMode   *string           `json:"epic_mode,omitempty" jsonschema:"Set how pm run-epic drives this PARENT tracker's subs. 'independent' = batch mode for UNRELATED tasks (each sub on its own branch off the base, pushed, nothing merged, no epic PR). Empty string clears it back to integration mode (shared epic/<tracker> branch, one epic PR). Omit to keep current."`
	Tags       []string          `json:"tags,omitempty" jsonschema:"Replace tags (omit to keep current)"`
	Links      map[string]string `json:"links,omitempty" jsonschema:"Links to merge (existing links are preserved)"`
	BodyAppend string            `json:"body_append,omitempty" jsonschema:"Append to the Log zone of the body (append-only session history; never replaces existing content)"`
	Spec       string            `json:"spec,omitempty" jsonschema:"Replace the current-truth Spec block (between <!-- spec:start --> / <!-- spec:end --> markers): what we're building, current decisions, still-open questions. Editable in place; created at the top of the body if absent. When a decision changes, rewrite the Spec to read as current truth AND append a one-line pointer to the Log via body_append (e.g. 'Q3 resolved -> see Spec'). Nothing is lost; the Spec never rots."`
	Brief      string            `json:"brief,omitempty" jsonschema:"Short session context summary (overwrites previous)"`
	AC         string            `json:"ac,omitempty" jsonschema:"Acceptance criteria (overwrites previous)"`
	Sessions   []string          `json:"sessions,omitempty" jsonschema:"Claude session IDs to append (never removes existing)"`
}

type moveTaskInput struct {
	Project   string `json:"project" jsonschema:"Project slug or prefix"`
	TaskID    string `json:"task_id" jsonschema:"Task ID or search query"`
	NewStatus string `json:"new_status" jsonschema:"Target status"`
}

type createProjectInput struct {
	Slug     string            `json:"slug" jsonschema:"Project slug (directory name, lowercase, no spaces)"`
	Name     string            `json:"name,omitempty" jsonschema:"Project display name (defaults to slug)"`
	Path     string            `json:"path,omitempty" jsonschema:"Local filesystem path"`
	Repo     string            `json:"repo,omitempty" jsonschema:"Repository URL"`
	Stack    string            `json:"stack,omitempty" jsonschema:"Tech stack description"`
	Notes    string            `json:"notes,omitempty" jsonschema:"Project notes"`
	Prefix   string            `json:"prefix,omitempty" jsonschema:"Task ID prefix (defaults to slug)"`
	Links    map[string]string `json:"links,omitempty" jsonschema:"Links as key=url pairs"`
	Tags     []string          `json:"tags,omitempty" jsonschema:"Tags"`
	Statuses []string          `json:"statuses,omitempty" jsonschema:"Custom statuses (default: todo, doing, waiting, done)"`
}

type deleteTaskInput struct {
	Project string `json:"project" jsonschema:"Project slug or prefix"`
	TaskID  string `json:"task_id" jsonschema:"Task ID or search query"`
}
type updateProjectInput struct {
	Project  string            `json:"project" jsonschema:"Project slug or prefix"`
	Name     string            `json:"name,omitempty" jsonschema:"Project display name"`
	Path     string            `json:"path,omitempty" jsonschema:"Local filesystem path"`
	Repo     string            `json:"repo,omitempty" jsonschema:"Repository URL"`
	Stack    string            `json:"stack,omitempty" jsonschema:"Tech stack description"`
	Notes    string            `json:"notes,omitempty" jsonschema:"Project notes"`
	Prefix   string            `json:"prefix,omitempty" jsonschema:"Task ID prefix"`
	Links    map[string]string `json:"links,omitempty" jsonschema:"Links to merge (existing links are preserved)"`
	Tags     []string          `json:"tags,omitempty" jsonschema:"Replace tags (omit to keep current)"`
	Statuses []string          `json:"statuses,omitempty" jsonschema:"Replace statuses (omit to keep current)"`
	Archived *bool             `json:"archived,omitempty" jsonschema:"Archive or unarchive the project"`
}

// --- JSON output helpers ---

type taskSummary struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Status       string            `json:"status"`
	Project      string            `json:"project"`
	Updated      string            `json:"updated"`
	Branch       string            `json:"branch,omitempty"`
	Parent       string            `json:"parent,omitempty"`
	Order        int               `json:"order,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	Links        map[string]string `json:"links,omitempty"`
	Brief        string            `json:"brief,omitempty"`
	AC           string            `json:"ac,omitempty"`
	SessionCount int               `json:"session_count"`
}

// listTasksResult wraps pm_list_tasks output with a size-budget footer: total
// vs shown makes truncation explicit instead of silently dropping tasks.
type listTasksResult struct {
	Tasks []taskSummary `json:"tasks"`
	Total int           `json:"total"`
	Shown int           `json:"shown"`
	Note  string        `json:"note,omitempty"`
}

type taskDetail struct {
	taskSummary
	Created   string   `json:"created"`
	Body      string   `json:"body,omitempty"`
	Sessions  []string `json:"sessions,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Mode      string   `json:"mode,omitempty"`
	Model     string   `json:"model,omitempty"`
	EpicMode  string   `json:"epic_mode,omitempty"`
}

func toSummary(t *storage.Task) taskSummary {
	return taskSummary{
		ID:           t.Meta.ID,
		Title:        t.Meta.Title,
		Status:       string(t.Meta.Status),
		Project:      t.Project,
		Updated:      t.Meta.Updated,
		Branch:       t.Meta.Branch,
		Parent:       t.Meta.Parent,
		Order:        t.Meta.Order,
		Tags:         t.Meta.Tags,
		Links:        t.Meta.Links,
		Brief:        t.Meta.Brief,
		AC:           t.Meta.AC,
		SessionCount: len(t.Meta.Sessions),
	}
}

func focusTaskSummaries(store storage.TaskStore) []taskSummary {
	fp := storage.ReadFocusPlan(store.RootDir())
	if fp.Date != storage.Today() || len(fp.Tasks) == 0 {
		return nil
	}
	allTasks, _ := store.GetAllTasks()
	lookup := make(map[string]*storage.Task, len(allTasks))
	for _, t := range allTasks {
		lookup[t.Meta.ID] = t
	}
	var result []taskSummary
	for _, id := range fp.Tasks {
		if t, ok := lookup[id]; ok && t.Meta.Status != storage.StatusDone && t.Meta.Status != storage.StatusArchived {
			s := toSummary(t)
			// A listing, so the same one-line rule as pm_list_tasks: the focus
			// plan points at tasks, it is not the place to read them.
			s.Brief = storage.BriefLine(s.Brief)
			result = append(result, s)
		}
	}
	return result
}

// contextBodyLimit caps each doing task's body in pm_context output: a
// project with a dozen doing tasks carrying full Spec/Log bodies dumps
// 100k+ chars into the calling session otherwise. The brief stays complete
// (it is the designed cold-start vehicle); the full body is one pm_get_task
// away.
const contextBodyLimit = 2000

func truncateBody(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n… [body truncated - use pm_get_task for the full body]"
}

func toDetail(t *storage.Task) taskDetail {
	return taskDetail{
		taskSummary: toSummary(t),
		Created:     t.Meta.Created,
		Body:        t.Body,
		Sessions:    t.Meta.Sessions,
		DependsOn:   t.Meta.DependsOn,
		Mode:        t.Meta.Mode,
		Model:       t.Meta.Model,
		EpicMode:    t.Meta.EpicMode,
	}
}

func jsonText(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}, nil
}

func toolError(msg string) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}, nil
}

// --- Tool registration ---

func registerTools(s *mcp.Server, store storage.TaskStore) {
	// pm_list_tasks
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_list_tasks",
		Description: "List tasks, optionally filtered by project and/or status. Excludes archived unless status=archived. Returns at most `limit` newest tasks (default 50) with total/shown counters; briefs are compressed to one line - use pm_get_task for full detail.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listTasksInput) (*mcp.CallToolResult, any, error) {
		var tasks []*storage.Task
		var err error

		if in.Project != "" {
			slug, e := store.ResolveProject(in.Project)
			if e != nil {
				r, _ := toolError(e.Error())
				return r, nil, nil
			}
			tasks, err = store.GetTasks(slug)
		} else {
			tasks, err = store.GetAllTasks()
		}
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		var filtered []taskSummary
		for _, t := range tasks {
			if in.Status != "" {
				if string(t.Meta.Status) != strings.ToLower(in.Status) {
					continue
				}
			} else if t.Meta.Status == storage.StatusArchived {
				continue
			}
			s := toSummary(t)
			// One-line brief in listings; pm_get_task returns the full brief.
			s.Brief = storage.BriefLine(s.Brief)
			filtered = append(filtered, s)
		}

		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].Updated > filtered[j].Updated
		})

		limit := in.Limit
		if limit <= 0 {
			limit = defaultListLimit
		}
		total := len(filtered)
		if total > limit {
			filtered = filtered[:limit]
		}
		result := listTasksResult{Tasks: filtered, Total: total, Shown: len(filtered)}
		if result.Shown < total {
			result.Note = fmt.Sprintf("%d of %d tasks shown (newest first) - narrow with project/status or raise limit", result.Shown, total)
		}

		r, err := jsonText(result)
		return r, nil, err
	})

	// pm_get_task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_get_task",
		Description: "Get full task details including body content.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getTaskInput) (*mcp.CallToolResult, any, error) {
		slug, err := store.ResolveProject(in.Project)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}
		task, err := store.FindTask(slug, in.TaskID)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}
		r, err := jsonText(toDetail(task))
		return r, nil, err
	})

	// pm_context
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_context",
		Description: "Get project context for session start. Auto-detects project from cwd. Returns project info, active tasks, and task counts. Doing tasks carry their full brief; briefs in the tracker rollup and focus list are compressed to one line and finished trackers omit their children - use pm_get_task for full detail.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in contextInput) (*mcp.CallToolResult, any, error) {
		// Resolve project
		projectSlug := ""
		if in.Project != "" {
			slug, err := store.ResolveProject(in.Project)
			if err != nil {
				r, _ := toolError(err.Error())
				return r, nil, nil
			}
			projectSlug = slug
		} else if in.Cwd != "" {
			slug, _ := resolveProjectFromCwd(store, in.Cwd)
			projectSlug = slug
		}

		if projectSlug != "" {
			return projectContext(store, projectSlug)
		}
		return crossProjectContext(store)
	})

	// pm_list_projects
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_list_projects",
		Description: "List all projects with task counts.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		projects, err := store.ListProjects()
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		type projectInfo struct {
			Slug       string         `json:"slug"`
			Name       string         `json:"name"`
			Stack      string         `json:"stack,omitempty"`
			Archived   bool           `json:"archived,omitempty"`
			TaskCounts map[string]int `json:"task_counts"`
		}

		var result []projectInfo
		for _, slug := range projects {
			proj, _ := store.GetProject(slug)
			pi := projectInfo{
				Slug:       slug,
				TaskCounts: make(map[string]int),
			}
			if proj != nil {
				pi.Name = proj.Name
				pi.Stack = proj.Stack
				pi.Archived = proj.Archived
			}
			tasks, _ := store.GetTasks(slug)
			for _, t := range tasks {
				pi.TaskCounts[string(t.Meta.Status)]++
			}
			result = append(result, pi)
		}

		r, err := jsonText(result)
		return r, nil, err
	})

	// pm_add_task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_add_task",
		Description: "Create a new task in a project. Body must contain ONLY verified facts from the conversation - never include speculative implementation details, architecture suggestions, or technical approaches that were not explicitly discussed. The body splits into a Spec zone (current-truth, editable - pass via spec) and a Log zone (append-only history - pass via body).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in addTaskInput) (*mcp.CallToolResult, any, error) {
		slug, err := store.ResolveProject(in.Project)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}
		// Serialize the read-modify-write against other pm processes (parallel
		// CC sessions, executor managers) so concurrent updates never drop each
		// other; best-effort - a lock failure degrades to today's behaviour.
		if release, lockErr := store.LockProject(slug); lockErr == nil {
			defer release()
		}

		id := in.ID
		if id == "" {
			if in.Parent != "" {
				id = store.NextChildID(slug, in.Parent)
			} else {
				id = store.NextTaskID(slug)
			}
		}

		t := storage.NewTask(id, in.Title, slug)

		if in.Status != "" {
			t.Meta.Status = storage.ParseStatus(in.Status)
		} else {
			statuses := store.GetProjectStatuses(slug)
			if len(statuses) > 0 {
				t.Meta.Status = statuses[0]
			}
		}

		// Validate status (AddTask also validates, but fail early with a clear MCP error)
		statuses := store.GetProjectStatuses(slug)
		if err := storage.ValidateStatus(t.Meta.Status, statuses); err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		// Validate mode (writeTask also validates, but fail early with a clear MCP error)
		if err := storage.ValidateMode(in.Mode); err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		// Validate epic_mode. writeTask validates too, but this pre-check is
		// REQUIRED, not just a nicer error: AddTask claims the file with O_EXCL
		// before writeTask runs, so a validation failure down there would leave a
		// 0-byte task file behind and make the retry fail as "already exists".
		if err := storage.ValidateEpicMode(in.EpicMode); err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		t.Meta.Branch = in.Branch
		t.Meta.Parent = in.Parent
		t.Meta.Order = in.Order
		t.Meta.DependsOn = in.DependsOn
		t.Meta.Mode = in.Mode
		t.Meta.Model = in.Model
		t.Meta.EpicMode = in.EpicMode
		t.Meta.Tags = in.Tags
		if len(in.Links) > 0 {
			t.Meta.Links = in.Links
		}
		t.Body = in.Body
		if in.Spec != "" {
			t.Body = storage.ApplySpec(t.Body, in.Spec)
		}
		t.Meta.Brief = in.Brief
		t.Meta.AC = in.AC
		t.Meta.Sessions = in.Sessions

		if err := store.AddTask(slug, t); err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		r, err := jsonText(toDetail(t))
		return r, nil, err
	})

	// pm_update_task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_update_task",
		Description: "Update an existing task. Links merge (never removed). body_append appends to the Log zone (never replaces); spec rewrites the current-truth Spec block in place. Brief and ac overwrite. Tags replace if provided.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateTaskInput) (*mcp.CallToolResult, any, error) {
		slug, err := store.ResolveProject(in.Project)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}
		// Serialize the read-modify-write against other pm processes (parallel
		// CC sessions, executor managers) so concurrent updates never drop each
		// other; best-effort - a lock failure degrades to today's behaviour.
		if release, lockErr := store.LockProject(slug); lockErr == nil {
			defer release()
		}
		task, err := store.FindTask(slug, in.TaskID)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		if in.Status != "" {
			// Same rule as MoveTask: an unlisted status renders on no board column
			// (the task vanishes), so reject it here; archived is system-level and
			// always legal.
			st := storage.ParseStatus(in.Status)
			if st != storage.StatusArchived {
				if err := storage.ValidateStatus(st, store.GetProjectStatuses(slug)); err != nil {
					r, _ := toolError(err.Error())
					return r, nil, nil
				}
			}
			task.Meta.Status = st
		}
		if in.Title != "" {
			task.Meta.Title = in.Title
		}
		if in.Branch != "" {
			task.Meta.Branch = in.Branch
		}
		if in.Parent != "" {
			task.Meta.Parent = in.Parent
		}
		if in.Order != nil {
			task.Meta.Order = *in.Order
		}
		if in.DependsOn != nil {
			task.Meta.DependsOn = in.DependsOn
		}
		if in.Mode != nil {
			if err := storage.ValidateMode(*in.Mode); err != nil {
				r, _ := toolError(err.Error())
				return r, nil, nil
			}
			task.Meta.Mode = *in.Mode
		}
		if in.Model != nil {
			task.Meta.Model = *in.Model
		}
		if in.EpicMode != nil {
			if err := storage.ValidateEpicMode(*in.EpicMode); err != nil {
				r, _ := toolError(err.Error())
				return r, nil, nil
			}
			task.Meta.EpicMode = *in.EpicMode
		}
		if in.Tags != nil {
			task.Meta.Tags = in.Tags
		}

		// Links: merge, never remove
		if len(in.Links) > 0 {
			if task.Meta.Links == nil {
				task.Meta.Links = make(map[string]string)
			}
			for k, v := range in.Links {
				task.Meta.Links[k] = v
			}
		}

		// Spec: replace the current-truth Spec block in place (the Log zone,
		// everything outside the markers, is left untouched).
		if in.Spec != "" {
			task.Body = storage.ApplySpec(task.Body, in.Spec)
		}

		// Body: append to the Log, never replace
		if in.BodyAppend != "" {
			if task.Body != "" {
				task.Body = task.Body + "\n\n" + in.BodyAppend
			} else {
				task.Body = in.BodyAppend
			}
		}

		// Brief: overwrite (current state, not history)
		if in.Brief != "" {
			task.Meta.Brief = in.Brief
		}

		// AC: overwrite (acceptance criteria, not history)
		if in.AC != "" {
			task.Meta.AC = in.AC
		}

		// Sessions: append, never remove
		if len(in.Sessions) > 0 {
			task.Meta.Sessions = append(task.Meta.Sessions, in.Sessions...)
		}

		task.Meta.Updated = storage.Today()
		if err := store.WriteTask(task); err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		r, err := jsonText(toDetail(task))
		return r, nil, err
	})

	// pm_move_task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_move_task",
		Description: "Move a task to a different status.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in moveTaskInput) (*mcp.CallToolResult, any, error) {
		slug, err := store.ResolveProject(in.Project)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}
		// No handler-level lock here: MoveTask itself locks the project and
		// re-reads the task fresh (a second flock in-process would deadlock).
		task, err := store.FindTask(slug, in.TaskID)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		oldStatus := string(task.Meta.Status)
		newStatus := storage.ParseStatus(in.NewStatus)
		if err := store.MoveTask(task, newStatus); err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		result := map[string]string{
			"id":         task.Meta.ID,
			"old_status": oldStatus,
			"new_status": string(task.Meta.Status),
		}
		r, err := jsonText(result)
		return r, nil, err
	})

	// pm_create_project
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_create_project",
		Description: "Create a new project. Creates project directory and project.yaml.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in createProjectInput) (*mcp.CallToolResult, any, error) {
		if in.Slug == "" {
			r, _ := toolError("slug is required")
			return r, nil, nil
		}

		// Check if project already exists
		existing, _ := store.ListProjects()
		for _, s := range existing {
			if s == in.Slug {
				r, _ := toolError(fmt.Sprintf("project %q already exists", in.Slug))
				return r, nil, nil
			}
		}

		name := in.Slug
		if in.Name != "" {
			name = in.Name
		}

		p := &storage.Project{
			Name:     name,
			Prefix:   in.Prefix,
			Path:     in.Path,
			Repo:     in.Repo,
			Stack:    in.Stack,
			Notes:    in.Notes,
			Links:    in.Links,
			Tags:     in.Tags,
			Statuses: in.Statuses,
		}

		if err := store.CreateProject(in.Slug, p); err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		type projectResult struct {
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
		}

		r, err := jsonText(projectResult{
			Slug:     in.Slug,
			Name:     p.Name,
			Path:     p.Path,
			Repo:     p.Repo,
			Stack:    p.Stack,
			Notes:    p.Notes,
			Prefix:   p.Prefix,
			Links:    p.Links,
			Tags:     p.Tags,
			Statuses: p.Statuses,
		})
		return r, nil, err
	})

	// pm_delete_task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_delete_task",
		Description: "Permanently delete a task file. Use for removing junk, test tasks, or duplicates. Cannot be undone.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deleteTaskInput) (*mcp.CallToolResult, any, error) {
		slug, err := store.ResolveProject(in.Project)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}
		// Serialize the read-modify-write against other pm processes (parallel
		// CC sessions, executor managers) so concurrent updates never drop each
		// other; best-effort - a lock failure degrades to today's behaviour.
		if release, lockErr := store.LockProject(slug); lockErr == nil {
			defer release()
		}
		task, err := store.FindTask(slug, in.TaskID)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		taskID := task.Meta.ID
		taskTitle := task.Meta.Title
		if err := store.DeleteTask(task); err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		result := map[string]string{
			"id":      taskID,
			"title":   taskTitle,
			"deleted": "true",
		}
		r, err := jsonText(result)
		return r, nil, err
	})
	// pm_update_project
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_update_project",
		Description: "Update project metadata. Links merge (never removed). Tags and statuses replace if provided. Scalar fields overwrite if non-empty.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateProjectInput) (*mcp.CallToolResult, any, error) {
		slug, err := store.ResolveProject(in.Project)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}
		proj, err := store.GetProject(slug)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		if in.Name != "" {
			proj.Name = in.Name
		}
		if in.Path != "" {
			proj.Path = in.Path
		}
		if in.Repo != "" {
			proj.Repo = in.Repo
		}
		if in.Stack != "" {
			proj.Stack = in.Stack
		}
		if in.Notes != "" {
			proj.Notes = in.Notes
		}
		if in.Prefix != "" {
			proj.Prefix = in.Prefix
		}

		// Links: merge, never remove
		if len(in.Links) > 0 {
			if proj.Links == nil {
				proj.Links = make(map[string]string)
			}
			for k, v := range in.Links {
				proj.Links[k] = v
			}
		}

		// Tags: replace if provided
		if in.Tags != nil {
			proj.Tags = in.Tags
		}

		// Statuses: replace if provided
		if in.Statuses != nil {
			proj.Statuses = in.Statuses
		}

		// Archived: set if provided
		if in.Archived != nil {
			proj.Archived = *in.Archived
		}

		if err := store.UpdateProject(slug, proj); err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		type projectResult struct {
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
		}

		r, err := jsonText(projectResult{
			Slug:     slug,
			Name:     proj.Name,
			Path:     proj.Path,
			Repo:     proj.Repo,
			Stack:    proj.Stack,
			Notes:    proj.Notes,
			Prefix:   proj.Prefix,
			Links:    proj.Links,
			Tags:     proj.Tags,
			Statuses: proj.Statuses,
			Archived: proj.Archived,
		})
		return r, nil, err
	})
}

// --- Context helpers ---

func resolveProjectFromCwd(store storage.TaskStore, cwd string) (string, error) {
	return storage.ResolveProjectFromCwd(store, cwd)
}

func projectContext(store storage.TaskStore, slug string) (*mcp.CallToolResult, any, error) {
	proj, _ := store.GetProject(slug)

	type projectMeta struct {
		Slug     string            `json:"slug"`
		Name     string            `json:"name"`
		Repo     string            `json:"repo,omitempty"`
		Stack    string            `json:"stack,omitempty"`
		Notes    string            `json:"notes,omitempty"`
		Links    map[string]string `json:"links,omitempty"`
		Statuses []string          `json:"statuses"`
	}

	pm := projectMeta{Slug: slug}
	statuses := storage.DefaultStatuses
	if proj != nil {
		pm.Name = proj.Name
		pm.Repo = proj.Repo
		pm.Stack = proj.Stack
		pm.Notes = proj.Notes
		pm.Links = proj.Links
		statuses = proj.GetStatuses()
	}
	for _, s := range statuses {
		pm.Statuses = append(pm.Statuses, string(s))
	}

	tasks, _ := store.GetTasks(slug)
	trackers, suppressed := storage.BuildTrackers(tasks)
	counts := make(map[string]int)
	var doing []taskDetail
	for _, t := range tasks {
		counts[string(t.Meta.Status)]++
		if t.Meta.Status == storage.StatusDoing && !suppressed[t.Meta.ID] {
			d := toDetail(t)
			d.Body = truncateBody(d.Body, contextBodyLimit)
			doing = append(doing, d)
		}
	}

	sort.Slice(doing, func(i, j int) bool {
		return doing[i].Updated > doing[j].Updated
	})

	result := map[string]any{
		"project":     pm,
		"doing_tasks": doing,
		"task_counts": counts,
	}

	if len(trackers) > 0 {
		result["trackers"] = trackers
	}

	// A POINTER, not the profile. pm_context is the session-start call, so its
	// output sits in every session's window - the executor/handoff profile is
	// only worth its size to the session that actually runs or accepts work,
	// and `pm executor show` charges it to that session alone.
	if proj != nil && proj.HasExecutor() {
		result["executor_profile"] = "run `pm executor show " + slug +
			"` for phase bindings, worktree slot runtimes (ports/device ids), context repos and the handoff playbook"
	}

	if focus := focusTaskSummaries(store); len(focus) > 0 {
		result["focus_tasks"] = focus
	}

	r, err := jsonText(result)
	return r, nil, err
}

func crossProjectContext(store storage.TaskStore) (*mcp.CallToolResult, any, error) {
	projects, _ := store.ListActiveProjects()

	type projectSummary struct {
		Slug       string            `json:"slug"`
		Name       string            `json:"name"`
		Repo       string            `json:"repo,omitempty"`
		TaskCounts map[string]int    `json:"task_counts"`
		DoingTasks []taskSummary     `json:"doing_tasks,omitempty"`
		Trackers   []storage.Tracker `json:"trackers,omitempty"`
	}

	var result []projectSummary
	for _, slug := range projects {
		proj, _ := store.GetProject(slug)
		ps := projectSummary{
			Slug:       slug,
			TaskCounts: make(map[string]int),
		}
		if proj != nil {
			ps.Name = proj.Name
			ps.Repo = proj.Repo
		}
		tasks, _ := store.GetTasks(slug)
		trackers, suppressed := storage.BuildTrackers(tasks)
		ps.Trackers = trackers
		for _, t := range tasks {
			ps.TaskCounts[string(t.Meta.Status)]++
			if t.Meta.Status == storage.StatusDoing && !suppressed[t.Meta.ID] {
				s := toSummary(t)
				// Cross-project view = a scan across every active project, so
				// briefs compress like any other listing. The project-scoped
				// branch keeps them whole (see projectContext).
				s.Brief = storage.BriefLine(s.Brief)
				ps.DoingTasks = append(ps.DoingTasks, s)
			}
		}
		sort.Slice(ps.DoingTasks, func(i, j int) bool {
			return ps.DoingTasks[i].Updated > ps.DoingTasks[j].Updated
		})
		result = append(result, ps)
	}

	output := map[string]any{
		"projects": result,
	}
	if focus := focusTaskSummaries(store); len(focus) > 0 {
		output["focus_tasks"] = focus
	}

	r, err := jsonText(output)
	return r, nil, err
}
