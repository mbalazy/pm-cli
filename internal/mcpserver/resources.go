package mcpserver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerResources(s *mcp.Server, store storage.TaskStore) {
	// pm://projects — static: all projects with task counts
	s.AddResource(
		&mcp.Resource{
			URI:         "pm://projects",
			Name:        "All Projects",
			Description: "All projects with task counts",
			MIMEType:    "application/json",
		},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			projects, err := store.ListProjects()
			if err != nil {
				return nil, err
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

			data, _ := json.Marshal(result)
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      req.Params.URI,
					Text:     string(data),
					MIMEType: "application/json",
				}},
			}, nil
		},
	)

	// pm://tasks/{project}/{status} — template: tasks filtered by project + status
	s.AddResourceTemplate(
		&mcp.ResourceTemplate{
			URITemplate: "pm://tasks/{project}/{status}",
			Name:        "Tasks by project and status",
			Description: "List tasks filtered by project slug and status",
			MIMEType:    "application/json",
		},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			uri := req.Params.URI
			// Parse: pm://tasks/{project}/{status}
			parts := strings.SplitN(strings.TrimPrefix(uri, "pm://tasks/"), "/", 2)
			if len(parts) != 2 {
				return nil, mcp.ResourceNotFoundError(uri)
			}
			projectSlug, status := parts[0], parts[1]

			slug, err := store.ResolveProject(projectSlug)
			if err != nil {
				return nil, mcp.ResourceNotFoundError(uri)
			}

			tasks, err := store.GetTasks(slug)
			if err != nil {
				return nil, err
			}

			var filtered []taskSummary
			for _, t := range tasks {
				if string(t.Meta.Status) == status {
					filtered = append(filtered, toSummary(t))
				}
			}

			data, _ := json.Marshal(filtered)
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      uri,
					Text:     string(data),
					MIMEType: "application/json",
				}},
			}, nil
		},
	)

	// pm://project/{slug} — template: project metadata
	s.AddResourceTemplate(
		&mcp.ResourceTemplate{
			URITemplate: "pm://project/{slug}",
			Name:        "Project metadata",
			Description: "Project configuration from project.yaml",
			MIMEType:    "application/json",
		},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			uri := req.Params.URI
			slug := strings.TrimPrefix(uri, "pm://project/")
			if slug == "" {
				return nil, mcp.ResourceNotFoundError(uri)
			}

			resolved, err := store.ResolveProject(slug)
			if err != nil {
				return nil, mcp.ResourceNotFoundError(uri)
			}

			proj, err := store.GetProject(resolved)
			if err != nil {
				return nil, mcp.ResourceNotFoundError(uri)
			}

			data, _ := json.Marshal(proj)
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      uri,
					Text:     string(data),
					MIMEType: "application/json",
				}},
			}, nil
		},
	)
}
