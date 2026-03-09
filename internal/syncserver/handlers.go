package syncserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/frontmatter"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/version"
)

// --- Sync endpoints ---

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	last := s.lastSync
	s.mu.RUnlock()

	var lastStr string
	if !last.IsZero() {
		lastStr = last.Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "ok",
		"version":   version.Version,
		"last_sync": lastStr,
	})
}

// BulkOp is a single operation in a bulk sync request.
type BulkOp struct {
	Op      string `json:"op"`                // "upsert" or "delete"
	Path    string `json:"path"`              // relative path (e.g. "pm-cli/pm-cli-1-title.md")
	Content string `json:"content,omitempty"` // file content (for upsert)
	TS      string `json:"ts"`                // RFC3339 timestamp
}

type bulkResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`          // "ok" or "error"
	Error  string `json:"error,omitempty"`
}

func (s *Server) handleSyncBulk(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ops []BulkOp `json:"ops"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	rootDir := s.store.RootDir()
	results := make([]bulkResult, len(req.Ops))

	for i, op := range req.Ops {
		results[i].Path = op.Path

		fullPath, err := safePath(rootDir, op.Path)
		if err != nil {
			results[i] = bulkResult{Path: op.Path, Status: "error", Error: err.Error()}
			continue
		}

		switch op.Op {
		case "upsert":
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				results[i] = bulkResult{Path: op.Path, Status: "error", Error: err.Error()}
				continue
			}
			if err := os.WriteFile(fullPath, []byte(op.Content), 0644); err != nil {
				results[i] = bulkResult{Path: op.Path, Status: "error", Error: err.Error()}
			} else {
				results[i] = bulkResult{Path: op.Path, Status: "ok"}
			}
		case "delete":
			if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
				results[i] = bulkResult{Path: op.Path, Status: "error", Error: err.Error()}
			} else {
				results[i] = bulkResult{Path: op.Path, Status: "ok"}
			}
		default:
			results[i] = bulkResult{Path: op.Path, Status: "error", Error: "unknown op: " + op.Op}
		}
	}

	now := time.Now().UTC()
	s.mu.Lock()
	s.lastSync = now
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"results":   results,
		"synced_at": now.Format(time.RFC3339),
	})
}

// --- Project endpoints ---

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	slugs, err := s.store.ListProjects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type projectInfo struct {
		Slug     string `json:"slug"`
		Name     string `json:"name"`
		Tasks    int    `json:"tasks"`
		Archived bool   `json:"archived,omitempty"`
	}

	projects := make([]projectInfo, 0, len(slugs))
	for _, slug := range slugs {
		info := projectInfo{Slug: slug, Name: slug}
		if proj, err := s.store.GetProject(slug); err == nil {
			info.Name = proj.Name
			info.Archived = proj.Archived
		}
		if tasks, err := s.store.GetTasks(slug); err == nil {
			info.Tasks = len(tasks)
		}
		projects = append(projects, info)
	}

	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	proj, err := s.store.GetProject(slug)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	data, err := os.ReadFile(s.store.ProjectYAML(slug))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("ETag", etag(data))

	writeJSON(w, http.StatusOK, map[string]any{
		"slug":     slug,
		"name":     proj.Name,
		"prefix":   proj.Prefix,
		"path":     proj.Path,
		"repo":     proj.Repo,
		"stack":    proj.Stack,
		"links":    proj.Links,
		"tags":     proj.Tags,
		"statuses": proj.Statuses,
		"notes":    proj.Notes,
		"archived": proj.Archived,
	})
}

func (s *Server) handlePutProject(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Check ETag for optimistic concurrency
	if ifMatch := r.Header.Get("If-Match"); ifMatch != "" {
		current, err := os.ReadFile(s.store.ProjectYAML(slug))
		if err == nil && etag(current) != ifMatch {
			http.Error(w, "precondition failed", http.StatusPreconditionFailed)
			return
		}
	}

	dir := s.store.ProjectDir(slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(s.store.ProjectYAML(slug), body, 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("ETag", etag(body))
	w.WriteHeader(http.StatusOK)
}

// --- Task endpoints ---

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	tasks, err := s.store.GetTasks(project)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	statusFilter := r.URL.Query().Get("status")

	type taskInfo struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Status  string `json:"status"`
		Updated string `json:"updated"`
		Brief   string `json:"brief,omitempty"`
	}

	result := make([]taskInfo, 0)
	for _, t := range tasks {
		if statusFilter != "" && string(t.Meta.Status) != statusFilter {
			continue
		}
		result = append(result, taskInfo{
			ID:      t.Meta.ID,
			Title:   t.Meta.Title,
			Status:  string(t.Meta.Status),
			Updated: t.Meta.Updated,
			Brief:   t.Meta.Brief,
		})
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	id := r.PathValue("id")

	task, err := s.store.FindTask(project, id)
	if err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	if task.FilePath != "" {
		if data, err := os.ReadFile(task.FilePath); err == nil {
			w.Header().Set("ETag", etag(data))
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":       task.Meta.ID,
		"title":    task.Meta.Title,
		"status":   task.Meta.Status,
		"created":  task.Meta.Created,
		"updated":  task.Meta.Updated,
		"links":    task.Meta.Links,
		"branch":   task.Meta.Branch,
		"tags":     task.Meta.Tags,
		"brief":    task.Meta.Brief,
		"order":    task.Meta.Order,
		"sessions": task.Meta.Sessions,
		"body":     task.Body,
	})
}

func (s *Server) handlePutTask(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	id := r.PathValue("id")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Parse frontmatter to validate and get metadata
	var meta storage.TaskMeta
	if _, err := frontmatter.Parse(bytes.NewReader(body), &meta); err != nil {
		http.Error(w, "invalid task format: "+err.Error(), http.StatusBadRequest)
		return
	}
	if meta.ID != id {
		http.Error(w, fmt.Sprintf("task ID mismatch: URL=%q body=%q", id, meta.ID), http.StatusBadRequest)
		return
	}

	// Find existing task to get file path, or compute new path
	var targetPath string
	existing, err := s.store.FindTask(project, id)
	if err == nil {
		targetPath = existing.FilePath
	} else {
		t := &storage.Task{Meta: meta}
		targetPath = filepath.Join(s.store.ProjectDir(project), t.Filename())
	}

	// Check ETag for optimistic concurrency
	if ifMatch := r.Header.Get("If-Match"); ifMatch != "" {
		current, err := os.ReadFile(targetPath)
		if err == nil && etag(current) != ifMatch {
			http.Error(w, "precondition failed", http.StatusPreconditionFailed)
			return
		}
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(targetPath, body, 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("ETag", etag(body))
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	id := r.PathValue("id")

	task, err := s.store.FindTask(project, id)
	if err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	if err := os.Remove(task.FilePath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func etag(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf(`"%x"`, h[:8])
}

// safePath validates a relative path and returns the full filesystem path.
// Prevents path traversal attacks.
func safePath(rootDir, relPath string) (string, error) {
	clean := filepath.Clean(relPath)
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("absolute path not allowed")
	}
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("path traversal not allowed")
	}
	full := filepath.Join(rootDir, clean)
	if !strings.HasPrefix(full, rootDir) {
		return "", fmt.Errorf("path outside root")
	}
	return full, nil
}
