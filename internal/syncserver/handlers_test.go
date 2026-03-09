package syncserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

const testToken = "test-secret-token"

func setupTestServer(t *testing.T) (*httptest.Server, *storage.Store) {
	t.Helper()

	dir := t.TempDir()
	store := &storage.Store{Root: dir}

	// Create a project with a task
	alphaDir := filepath.Join(dir, "alpha")
	os.MkdirAll(alphaDir, 0755)
	storage.WriteProject(filepath.Join(alphaDir, "project.yaml"), &storage.Project{
		Name:   "Alpha",
		Prefix: "a",
		Stack:  "Go",
	})
	storage.WriteTask(&storage.Task{
		Meta: storage.TaskMeta{
			ID: "a-1", Title: "First task",
			Status: storage.StatusTodo, Created: "2025-01-01", Updated: "2025-01-01",
			Brief: "test brief",
		},
		FilePath: filepath.Join(alphaDir, "a-1-first-task.md"),
	})

	srv := New(store, testToken)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, store
}

func authReq(method, url string, body io.Reader) *http.Request {
	req, _ := http.NewRequest(method, url, body)
	req.Header.Set("Authorization", "Bearer "+testToken)
	return req
}

func doJSON(t *testing.T, ts *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req := authReq(method, ts.URL+path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return data
}

// --- Auth tests ---

func TestAuth_MissingToken(t *testing.T) {
	ts, _ := setupTestServer(t)
	resp, err := http.Get(ts.URL + "/sync/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuth_InvalidToken(t *testing.T) {
	ts, _ := setupTestServer(t)
	req, _ := http.NewRequest("GET", ts.URL+"/sync/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuth_ValidToken(t *testing.T) {
	ts, _ := setupTestServer(t)
	resp := doJSON(t, ts, "GET", "/sync/status", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// --- Sync endpoints ---

func TestSyncStatus(t *testing.T) {
	ts, _ := setupTestServer(t)
	resp := doJSON(t, ts, "GET", "/sync/status", nil)
	body := readBody(t, resp)

	var result map[string]string
	json.Unmarshal(body, &result)

	if result["status"] != "ok" {
		t.Errorf("status = %q, want ok", result["status"])
	}
	if result["last_sync"] != "" {
		t.Errorf("last_sync should be empty before first sync, got %q", result["last_sync"])
	}
}

func TestSyncBulk_Upsert(t *testing.T) {
	ts, store := setupTestServer(t)

	taskContent := "---\nid: a-2\ntitle: Bulk task\nstatus: todo\ncreated: \"2025-01-01\"\nupdated: \"2025-01-01\"\n---\n\nBulk task body\n"

	resp := doJSON(t, ts, "POST", "/sync/bulk", map[string]any{
		"ops": []map[string]any{
			{"op": "upsert", "path": "alpha/a-2-bulk-task.md", "content": taskContent, "ts": "2025-01-01T00:00:00Z"},
		},
	})
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", resp.StatusCode, body)
	}

	var result struct {
		Results  []bulkResult `json:"results"`
		SyncedAt string       `json:"synced_at"`
	}
	json.Unmarshal(body, &result)

	if len(result.Results) != 1 || result.Results[0].Status != "ok" {
		t.Errorf("results = %+v, want 1 ok result", result.Results)
	}
	if result.SyncedAt == "" {
		t.Error("synced_at should be set")
	}

	// Verify file was written
	task, err := store.FindTask("alpha", "a-2")
	if err != nil {
		t.Fatalf("task not found after bulk upsert: %v", err)
	}
	if task.Meta.Title != "Bulk task" {
		t.Errorf("title = %q, want Bulk task", task.Meta.Title)
	}
}

func TestSyncBulk_Delete(t *testing.T) {
	ts, store := setupTestServer(t)

	// Verify task exists first
	if _, err := store.FindTask("alpha", "a-1"); err != nil {
		t.Fatalf("setup: task should exist: %v", err)
	}

	resp := doJSON(t, ts, "POST", "/sync/bulk", map[string]any{
		"ops": []map[string]any{
			{"op": "delete", "path": "alpha/a-1-first-task.md", "ts": "2025-01-01T00:00:00Z"},
		},
	})
	readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Verify file was deleted
	if _, err := store.FindTask("alpha", "a-1"); err == nil {
		t.Error("task should be deleted after bulk delete")
	}
}

func TestSyncBulk_PathTraversal(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp := doJSON(t, ts, "POST", "/sync/bulk", map[string]any{
		"ops": []map[string]any{
			{"op": "upsert", "path": "../../../etc/passwd", "content": "hacked", "ts": "2025-01-01T00:00:00Z"},
		},
	})
	body := readBody(t, resp)

	var result struct {
		Results []bulkResult `json:"results"`
	}
	json.Unmarshal(body, &result)

	if len(result.Results) != 1 || result.Results[0].Status != "error" {
		t.Errorf("path traversal should fail, got: %+v", result.Results)
	}
}

func TestSyncBulk_UpdatesSyncTime(t *testing.T) {
	ts, _ := setupTestServer(t)

	// First sync
	doJSON(t, ts, "POST", "/sync/bulk", map[string]any{
		"ops": []map[string]any{},
	}).Body.Close()

	// Check status
	resp := doJSON(t, ts, "GET", "/sync/status", nil)
	body := readBody(t, resp)

	var result map[string]string
	json.Unmarshal(body, &result)

	if result["last_sync"] == "" {
		t.Error("last_sync should be set after bulk sync")
	}
}

// --- Project endpoints ---

func TestListProjects(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp := doJSON(t, ts, "GET", "/projects", nil)
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var projects []struct {
		Slug  string `json:"slug"`
		Name  string `json:"name"`
		Tasks int    `json:"tasks"`
	}
	json.Unmarshal(body, &projects)

	if len(projects) != 1 || projects[0].Slug != "alpha" {
		t.Errorf("projects = %+v, want [alpha]", projects)
	}
	if projects[0].Tasks != 1 {
		t.Errorf("tasks = %d, want 1", projects[0].Tasks)
	}
}

func TestGetProject(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp := doJSON(t, ts, "GET", "/projects/alpha", nil)
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var proj map[string]any
	json.Unmarshal(body, &proj)

	if proj["name"] != "Alpha" {
		t.Errorf("name = %v, want Alpha", proj["name"])
	}
	if proj["stack"] != "Go" {
		t.Errorf("stack = %v, want Go", proj["stack"])
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("ETag header should be set")
	}
}

func TestGetProject_NotFound(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp := doJSON(t, ts, "GET", "/projects/nonexistent", nil)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPutProject(t *testing.T) {
	ts, store := setupTestServer(t)

	yamlContent := "name: Beta\nstack: Rust\n"
	req := authReq("PUT", ts.URL+"/projects/beta", bytes.NewReader([]byte(yamlContent)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("ETag should be set")
	}

	proj, err := store.GetProject("beta")
	if err != nil {
		t.Fatalf("project not found: %v", err)
	}
	if proj.Name != "Beta" {
		t.Errorf("name = %q, want Beta", proj.Name)
	}
}

// --- Task endpoints ---

func TestListTasks(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp := doJSON(t, ts, "GET", "/tasks/alpha", nil)
	body := readBody(t, resp)

	var tasks []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	json.Unmarshal(body, &tasks)

	if len(tasks) != 1 || tasks[0].ID != "a-1" {
		t.Errorf("tasks = %+v, want [a-1]", tasks)
	}
}

func TestListTasks_StatusFilter(t *testing.T) {
	ts, _ := setupTestServer(t)

	// Filter by "doing" - should return empty
	resp := doJSON(t, ts, "GET", "/tasks/alpha?status=doing", nil)
	body := readBody(t, resp)

	var tasks []any
	json.Unmarshal(body, &tasks)

	if len(tasks) != 0 {
		t.Errorf("tasks with status=doing = %d, want 0", len(tasks))
	}

	// Filter by "todo" - should return 1
	resp = doJSON(t, ts, "GET", "/tasks/alpha?status=todo", nil)
	body = readBody(t, resp)

	json.Unmarshal(body, &tasks)
	if len(tasks) != 1 {
		t.Errorf("tasks with status=todo = %d, want 1", len(tasks))
	}
}

func TestGetTask(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp := doJSON(t, ts, "GET", "/tasks/alpha/a-1", nil)
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var task map[string]any
	json.Unmarshal(body, &task)

	if task["id"] != "a-1" {
		t.Errorf("id = %v, want a-1", task["id"])
	}
	if task["title"] != "First task" {
		t.Errorf("title = %v, want First task", task["title"])
	}
	if task["brief"] != "test brief" {
		t.Errorf("brief = %v, want test brief", task["brief"])
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("ETag should be set")
	}
}

func TestGetTask_NotFound(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp := doJSON(t, ts, "GET", "/tasks/alpha/a-999", nil)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPutTask(t *testing.T) {
	ts, store := setupTestServer(t)

	taskContent := "---\nid: a-3\ntitle: New REST task\nstatus: doing\ncreated: \"2025-02-01\"\nupdated: \"2025-02-01\"\n---\n\nTask body here\n"
	req := authReq("PUT", ts.URL+"/tasks/alpha/a-3", bytes.NewReader([]byte(taskContent)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	task, err := store.FindTask("alpha", "a-3")
	if err != nil {
		t.Fatalf("task not found: %v", err)
	}
	if task.Meta.Title != "New REST task" {
		t.Errorf("title = %q, want New REST task", task.Meta.Title)
	}
	if task.Meta.Status != storage.StatusDoing {
		t.Errorf("status = %q, want doing", task.Meta.Status)
	}
}

func TestPutTask_IDMismatch(t *testing.T) {
	ts, _ := setupTestServer(t)

	taskContent := "---\nid: a-999\ntitle: Wrong ID\nstatus: todo\ncreated: \"2025-01-01\"\nupdated: \"2025-01-01\"\n---\n"
	req := authReq("PUT", ts.URL+"/tasks/alpha/a-1", bytes.NewReader([]byte(taskContent)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for ID mismatch", resp.StatusCode)
	}
}

func TestPutTask_ETagConflict(t *testing.T) {
	ts, _ := setupTestServer(t)

	taskContent := "---\nid: a-1\ntitle: Updated task\nstatus: todo\ncreated: \"2025-01-01\"\nupdated: \"2025-01-02\"\n---\n"
	req := authReq("PUT", ts.URL+"/tasks/alpha/a-1", bytes.NewReader([]byte(taskContent)))
	req.Header.Set("If-Match", `"stale-etag-value"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("status = %d, want 412 for stale ETag", resp.StatusCode)
	}
}

func TestDeleteTask(t *testing.T) {
	ts, store := setupTestServer(t)

	resp := doJSON(t, ts, "DELETE", "/tasks/alpha/a-1", nil)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}

	if _, err := store.FindTask("alpha", "a-1"); err == nil {
		t.Error("task should be deleted")
	}
}

func TestDeleteTask_NotFound(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp := doJSON(t, ts, "DELETE", "/tasks/alpha/a-999", nil)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
