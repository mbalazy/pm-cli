package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/mbalazy/pm/internal/service"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every tool's argument set and result shape lives in internal/service (the
// MCP schemas are generated from those very structs, so the parameter
// descriptions stay one copy shared with every other front end); the aliases
// below keep this file's handlers reading the same as before, and let the
// tests keep their names. Handlers do three things: decode, call the service,
// encode - a handler that does a fourth is the bug the service layer exists
// to prevent.
type (
	listTasksInput     = service.ListTasksInput
	getTaskInput       = service.GetTaskInput
	contextInput       = service.ContextInput
	addTaskInput       = service.AddTaskInput
	updateTaskInput    = service.UpdateTaskInput
	moveTaskInput      = service.MoveTaskInput
	createProjectInput = service.CreateProjectInput
	deleteTaskInput    = service.DeleteTaskInput
	updateProjectInput = service.UpdateProjectInput

	taskSummary = service.TaskSummary
	taskDetail  = service.TaskDetail
)

const (
	defaultListLimit = service.DefaultListLimit
	maxListLimit     = service.MaxListLimit
	contextBodyLimit = service.ContextBodyLimit
)

func toSummary(t *storage.Task) taskSummary { return service.ToSummary(t) }
func toDetail(t *storage.Task) taskDetail   { return service.ToDetail(t) }

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

// respond is the one encode step: a service error becomes a tool error (the
// message is the whole of what the agent sees), a result becomes JSON text.
func respond(v any, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		r, _ := toolError(err.Error())
		return r, nil, nil
	}
	r, jerr := jsonText(v)
	return r, nil, jerr
}

// --- Tool registration ---

func registerTools(s *mcp.Server, store storage.TaskStore) {
	// pm_list_tasks
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_list_tasks",
		Description: "List tasks, optionally filtered by project and/or status. Excludes archived unless status=archived. Returns at most `limit` newest tasks (default 50) with total/shown counters; briefs are compressed to one line - use pm_get_task for full detail.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listTasksInput) (*mcp.CallToolResult, any, error) {
		return respond(service.ListTasks(store, in))
	})

	// pm_get_task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_get_task",
		Description: "Get full task details including body content.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getTaskInput) (*mcp.CallToolResult, any, error) {
		return respond(service.GetTask(store, in))
	})

	// pm_context
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_context",
		Description: "Get project context for session start. Auto-detects project from cwd. Returns project info, active tasks, and task counts. Doing tasks carry their full brief; briefs in the tracker rollup and focus list are compressed to one line and finished trackers omit their children - use pm_get_task for full detail. The `attention` block is the cockpit's queue of what needs the human (failed/crashed runs, acceptances with open visual claims, runs landed without acceptance, waiting tasks with their age and a no_reason alarm, stuck projects) - the same picture the user's home screen shows.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in contextInput) (*mcp.CallToolResult, any, error) {
		return respond(service.Context(store, in))
	})

	// pm_list_projects
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_list_projects",
		Description: "List all projects with task counts, as {projects, note}. A project whose project.yaml or task dir fails to read is skipped and named in `note` instead of silently vanishing from `projects`.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		return respond(service.ListProjects(store))
	})

	// pm_add_task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_add_task",
		Description: "Create a new task in a project. Body must contain ONLY verified facts from the conversation - never include speculative implementation details, architecture suggestions, or technical approaches that were not explicitly discussed. The body splits into a Spec zone (current-truth, editable - pass via spec) and a Log zone (append-only history - pass via body).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in addTaskInput) (*mcp.CallToolResult, any, error) {
		t, err := service.AddTask(store, in)
		if err != nil {
			return respond(nil, err)
		}
		return respond(toDetail(t), nil)
	})

	// pm_update_task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_update_task",
		Description: "Update an existing task. Links merge (never removed). body_append appends to the Log zone (never replaces); spec rewrites the current-truth Spec block in place. Tags replace if provided. Branch/parent/brief/ac/waiting_for are tri-state: omit to keep current, pass an empty string to clear. status_changed is stamped automatically whenever status actually changes. Title is required-non-empty - an empty/omitted value leaves it untouched.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateTaskInput) (*mcp.CallToolResult, any, error) {
		task, err := service.UpdateTask(store, in)
		if err != nil {
			return respond(nil, err)
		}
		return respond(toDetail(task), nil)
	})

	// pm_move_task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_move_task",
		Description: "Move a task to a different status.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in moveTaskInput) (*mcp.CallToolResult, any, error) {
		return respond(service.MoveTask(store, in))
	})

	// pm_create_project
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_create_project",
		Description: "Create a new project. Creates project directory and project.yaml.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in createProjectInput) (*mcp.CallToolResult, any, error) {
		return respond(service.CreateProject(store, in))
	})

	// pm_delete_task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_delete_task",
		Description: "Permanently delete a task file. Use for removing junk, test tasks, or duplicates. Cannot be undone. Requires the exact full task ID - no fuzzy title match.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deleteTaskInput) (*mcp.CallToolResult, any, error) {
		res, err := service.DeleteTask(store, in)
		if err != nil {
			return respond(nil, err)
		}
		// The wire shape predates the service layer: a string map with a
		// literal "true", kept byte-for-byte so no client sees a change.
		return respond(map[string]string{
			"id":      res.ID,
			"title":   res.Title,
			"deleted": "true",
		}, nil)
	})

	// pm_update_project
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_update_project",
		Description: "Update project metadata. Links merge (never removed). Tags and statuses replace if provided. Scalar fields overwrite if non-empty. Rejects a statuses replacement that would drop a status current tasks sit on (they'd vanish from every board column) - move or close those tasks first.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateProjectInput) (*mcp.CallToolResult, any, error) {
		return respond(service.UpdateProject(store, in))
	})

	registerJournalTools(s, store)
	registerTimelineTools(s, store)
}

// --- Context helpers kept under their old names for the tests ---

func resolveProjectFromCwd(store storage.TaskStore, cwd string) (string, error) {
	return storage.ResolveProjectFromCwd(store, cwd)
}

func crossProjectContext(store storage.TaskStore, note string) (*mcp.CallToolResult, any, error) {
	return respond(service.CrossProjectContext(store, note))
}
