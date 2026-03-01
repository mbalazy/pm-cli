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
}

type getTaskInput struct {
	Project string `json:"project" jsonschema:"Project slug or prefix"`
	TaskID  string `json:"task_id" jsonschema:"Task ID or search query"`
}

type contextInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project slug or prefix"`
	Cwd     string `json:"cwd,omitempty" jsonschema:"Current working directory for auto-detection"`
}

type addTaskInput struct {
	Project string            `json:"project" jsonschema:"Project slug or prefix"`
	Title   string            `json:"title" jsonschema:"Task title"`
	Status  string            `json:"status,omitempty" jsonschema:"Initial status (default: first project status)"`
	Branch  string            `json:"branch,omitempty" jsonschema:"Git branch name"`
	Tags    []string          `json:"tags,omitempty" jsonschema:"Tags"`
	Links   map[string]string `json:"links,omitempty" jsonschema:"Links as key=url pairs (e.g. azure, pr, slack)"`
	Body    string            `json:"body,omitempty" jsonschema:"Markdown body content"`
	ID      string            `json:"id,omitempty" jsonschema:"Task ID (auto-generated if omitted)"`
	Brief   string            `json:"brief,omitempty" jsonschema:"Short session context summary (overwrites previous)"`
}

type updateTaskInput struct {
	Project    string            `json:"project" jsonschema:"Project slug or prefix"`
	TaskID     string            `json:"task_id" jsonschema:"Task ID or search query"`
	Status     string            `json:"status,omitempty" jsonschema:"New status"`
	Title      string            `json:"title,omitempty" jsonschema:"New title"`
	Branch     string            `json:"branch,omitempty" jsonschema:"Git branch name"`
	Tags       []string          `json:"tags,omitempty" jsonschema:"Replace tags (omit to keep current)"`
	Links      map[string]string `json:"links,omitempty" jsonschema:"Links to merge (existing links are preserved)"`
	BodyAppend string            `json:"body_append,omitempty" jsonschema:"Text to append to body (never replaces existing content)"`
	Brief      string            `json:"brief,omitempty" jsonschema:"Short session context summary (overwrites previous)"`
}

type moveTaskInput struct {
	Project   string `json:"project" jsonschema:"Project slug or prefix"`
	TaskID    string `json:"task_id" jsonschema:"Task ID or search query"`
	NewStatus string `json:"new_status" jsonschema:"Target status"`
}

// --- JSON output helpers ---

type taskSummary struct {
	ID      string            `json:"id"`
	Title   string            `json:"title"`
	Status  string            `json:"status"`
	Project string            `json:"project"`
	Updated string            `json:"updated"`
	Branch  string            `json:"branch,omitempty"`
	Tags    []string          `json:"tags,omitempty"`
	Links   map[string]string `json:"links,omitempty"`
	Brief   string            `json:"brief,omitempty"`
}

type taskDetail struct {
	taskSummary
	Created string `json:"created"`
	Body    string `json:"body,omitempty"`
}

func toSummary(t *storage.Task) taskSummary {
	return taskSummary{
		ID:      t.Meta.ID,
		Title:   t.Meta.Title,
		Status:  string(t.Meta.Status),
		Project: t.Project,
		Updated: t.Meta.Updated,
		Branch:  t.Meta.Branch,
		Tags:    t.Meta.Tags,
		Links:   t.Meta.Links,
		Brief:   t.Meta.Brief,
	}
}

func toDetail(t *storage.Task) taskDetail {
	return taskDetail{
		taskSummary: toSummary(t),
		Created:     t.Meta.Created,
		Body:        t.Body,
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

func registerTools(s *mcp.Server, store *storage.Store) {
	// pm_list_tasks
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pm_list_tasks",
		Description: "List tasks, optionally filtered by project and/or status. Excludes archived unless status=archived.",
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
			filtered = append(filtered, toSummary(t))
		}

		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].Updated > filtered[j].Updated
		})

		r, err := jsonText(filtered)
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
		Description: "Get project context for session start. Auto-detects project from cwd. Returns project info, active tasks, and task counts.",
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
		Description: "Create a new task in a project.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in addTaskInput) (*mcp.CallToolResult, any, error) {
		slug, err := store.ResolveProject(in.Project)
		if err != nil {
			r, _ := toolError(err.Error())
			return r, nil, nil
		}

		id := in.ID
		if id == "" {
			id = store.NextTaskID(slug)
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

		t.Meta.Branch = in.Branch
		t.Meta.Tags = in.Tags
		if len(in.Links) > 0 {
			t.Meta.Links = in.Links
		}
		t.Body = in.Body
		t.Meta.Brief = in.Brief

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
		Description: "Update an existing task. Links merge (never removed). Body appends (never replaces). Tags replace if provided.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateTaskInput) (*mcp.CallToolResult, any, error) {
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

		if in.Status != "" {
			task.Meta.Status = storage.ParseStatus(in.Status)
		}
		if in.Title != "" {
			task.Meta.Title = in.Title
		}
		if in.Branch != "" {
			task.Meta.Branch = in.Branch
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

		// Body: append, never replace
		if in.BodyAppend != "" {
			if task.Body != "" {
				task.Body = task.Body + "\n\n" + in.BodyAppend
			} else {
				task.Body = in.BodyAppend
			}
		}

		// Brief: overwrite (current state, not history)
		// Clear brief when status transitions to done/archived
		if task.Meta.Status == storage.StatusDone || task.Meta.Status == storage.StatusArchived {
			task.Meta.Brief = ""
		} else if in.Brief != "" {
			task.Meta.Brief = in.Brief
		}

		task.Meta.Updated = storage.Today()
		if err := storage.WriteTask(task); err != nil {
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
}

// --- Context helpers ---

func resolveProjectFromCwd(store *storage.Store, cwd string) (string, error) {
	projects, err := store.ListProjects()
	if err != nil {
		return "", err
	}
	for _, slug := range projects {
		proj, err := store.GetProject(slug)
		if err != nil || proj == nil || proj.Path == "" {
			continue
		}
		if strings.HasPrefix(cwd, proj.Path) {
			return slug, nil
		}
	}
	return "", fmt.Errorf("no project found for: %s", cwd)
}

func projectContext(store *storage.Store, slug string) (*mcp.CallToolResult, any, error) {
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
	counts := make(map[string]int)
	var doing []taskDetail
	for _, t := range tasks {
		counts[string(t.Meta.Status)]++
		if t.Meta.Status == storage.StatusDoing {
			doing = append(doing, toDetail(t))
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

	r, err := jsonText(result)
	return r, nil, err
}

func crossProjectContext(store *storage.Store) (*mcp.CallToolResult, any, error) {
	projects, _ := store.ListProjects()

	type projectSummary struct {
		Slug       string         `json:"slug"`
		Name       string         `json:"name"`
		Repo       string         `json:"repo,omitempty"`
		TaskCounts map[string]int `json:"task_counts"`
		DoingTasks []taskSummary  `json:"doing_tasks,omitempty"`
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
		for _, t := range tasks {
			ps.TaskCounts[string(t.Meta.Status)]++
			if t.Meta.Status == storage.StatusDoing {
				ps.DoingTasks = append(ps.DoingTasks, toSummary(t))
			}
		}
		sort.Slice(ps.DoingTasks, func(i, j int) bool {
			return ps.DoingTasks[i].Updated > ps.DoingTasks[j].Updated
		})
		result = append(result, ps)
	}

	r, err := jsonText(result)
	return r, nil, err
}
