package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// newTestStore mirrors the MCP e2e fixture: a temp root with project "test"
// (prefix t, path /home/user/test) holding t-1 on doing, plus a tracker
// t-2 with one child so /api/runs has a row.
func newTestStore(t *testing.T) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	store := &storage.Store{Root: dir}
	projDir := filepath.Join(dir, "test")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteProject(filepath.Join(projDir, "project.yaml"), &storage.Project{
		Name: "Test Project", Prefix: "t", Path: "/home/user/test", Tags: []string{"go"},
	}); err != nil {
		t.Fatal(err)
	}
	mk := func(id, title, parent string, st storage.TaskStatus) {
		task := &storage.Task{
			Meta:     storage.TaskMeta{ID: id, Title: title, Status: st, Parent: parent, Created: "2025-01-01", Updated: "2025-01-01T10:00:00+01:00", Brief: "line1\nline2"},
			Body:     "## Description\n\nBody of " + id,
			FilePath: filepath.Join(projDir, id+".md"),
			Project:  "test",
		}
		if err := storage.WriteTask(task); err != nil {
			t.Fatal(err)
		}
	}
	mk("t-1", "Test Task", "", storage.StatusDoing)
	mk("t-2", "Tracker", "", storage.StatusTodo)
	mk("t-2-1", "Child", "t-2", storage.StatusTodo)
	return store
}

func newServer(t *testing.T, store *storage.Store, opts Options) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewHandler(store, opts))
	t.Cleanup(srv.Close)
	return srv
}

// get returns status, body and the response headers.
func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func getJSON(t *testing.T, url string, want int) map[string]any {
	t.Helper()
	status, body, hdr := get(t, url)
	if status != want {
		t.Fatalf("GET %s = %d, want %d: %s", url, status, want, body)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("not JSON: %v: %s", err, body)
	}
	return m
}

func wantKeys(t *testing.T, m map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing key %q in %v", k, m)
		}
	}
}

func TestProjects(t *testing.T) {
	srv := newServer(t, newTestStore(t), Options{})
	m := getJSON(t, srv.URL+"/api/projects", 200)
	projects := m["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("projects = %v", projects)
	}
	p := projects[0].(map[string]any)
	wantKeys(t, p, "slug", "name", "path", "tags", "statuses", "landing_statuses", "task_counts")
	if p["slug"] != "test" || p["path"] != "/home/user/test" {
		t.Fatalf("row = %v", p)
	}
	if st := p["statuses"].([]any); len(st) != 4 || st[0] != "todo" {
		t.Fatalf("statuses = %v", st)
	}
	if ls := p["landing_statuses"].([]any); len(ls) == 0 {
		t.Fatalf("landing_statuses = %v", ls)
	}
	if counts := p["task_counts"].(map[string]any); counts["doing"] != float64(1) || counts["todo"] != float64(2) {
		t.Fatalf("task_counts = %v", counts)
	}
}

func TestTasks(t *testing.T) {
	srv := newServer(t, newTestStore(t), Options{})

	t.Run("list has the MCP shape", func(t *testing.T) {
		m := getJSON(t, srv.URL+"/api/tasks?project=test", 200)
		wantKeys(t, m, "tasks", "total", "shown")
		if m["total"] != float64(3) {
			t.Fatalf("total = %v", m["total"])
		}
		first := m["tasks"].([]any)[0].(map[string]any)
		if strings.Contains(first["brief"].(string), "\n") {
			t.Fatalf("listing brief must be one line: %q", first["brief"])
		}
		m = getJSON(t, srv.URL+"/api/tasks?limit=1", 200)
		if m["shown"] != float64(1) || m["total"] != float64(3) || m["note"] == nil {
			t.Fatalf("budget: %v", m)
		}
	})

	t.Run("caller mistakes are 400, missing things 404, both as {error}", func(t *testing.T) {
		m := getJSON(t, srv.URL+"/api/tasks?project=test&status=shipped", 400)
		if !strings.Contains(m["error"].(string), `invalid status "shipped"`) {
			t.Fatalf("error = %v", m["error"])
		}
		m = getJSON(t, srv.URL+"/api/tasks?limit=abc", 400)
		if !strings.Contains(m["error"].(string), "limit must be an integer") {
			t.Fatalf("error = %v", m["error"])
		}
		m = getJSON(t, srv.URL+"/api/tasks?project=nope", 404)
		if !strings.Contains(m["error"].(string), `project not found: "nope"`) {
			t.Fatalf("error = %v", m["error"])
		}
	})

	t.Run("get one, fuzzy id, 404 on miss", func(t *testing.T) {
		m := getJSON(t, srv.URL+"/api/tasks/test/t-1", 200)
		if m["id"] != "t-1" || m["brief"] != "line1\nline2" || m["body"] == nil {
			t.Fatalf("detail = %v", m)
		}
		if m := getJSON(t, srv.URL+"/api/tasks/test/Test%20Task", 200); m["id"] != "t-1" {
			t.Fatalf("fuzzy = %v", m)
		}
		getJSON(t, srv.URL+"/api/tasks/test/t-99", 404)
		getJSON(t, srv.URL+"/api/tasks/nope/t-1", 404)
		// prefix "t-2" hits t-2 and t-2-1: ambiguous is the caller's problem
		getJSON(t, srv.URL+"/api/tasks/test/t-", 400)
	})
}

func TestContext(t *testing.T) {
	srv := newServer(t, newTestStore(t), Options{})
	m := getJSON(t, srv.URL+"/api/context?project=test", 200)
	wantKeys(t, m, "project", "doing_tasks", "task_counts", "trackers")
	doing := m["doing_tasks"].([]any)
	if len(doing) != 1 || doing[0].(map[string]any)["brief"] != "line1\nline2" {
		t.Fatalf("doing = %v", doing)
	}
	m = getJSON(t, srv.URL+"/api/context", 200)
	wantKeys(t, m, "projects")
	getJSON(t, srv.URL+"/api/context?project=nope", 404)
}

func TestRuns(t *testing.T) {
	store := newTestStore(t)
	remoteCalls := 0
	srv := newServer(t, store, Options{
		RemoteRuns: func(ctx context.Context, s storage.TaskStore) ([]storage.RunRow, error) {
			remoteCalls++
			return []storage.RunRow{{Remote: "runner", Project: "atlas", Tracker: "atlas-1", Title: "remote tracker"}}, nil
		},
	})
	m := getJSON(t, srv.URL+"/api/runs", 200)
	rows := m["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["tracker"] != "t-2" {
		t.Fatalf("rows = %v", rows)
	}
	if remoteCalls != 0 {
		t.Fatal("remote runners must never be contacted without ?remote=1")
	}
	m = getJSON(t, srv.URL+"/api/runs?remote=1", 200)
	if rows := m["rows"].([]any); len(rows) != 2 || remoteCalls != 1 {
		t.Fatalf("rows = %v, remote calls = %d", rows, remoteCalls)
	}

	// no hook = local rows only, still 200
	srv2 := newServer(t, store, Options{})
	if m := getJSON(t, srv2.URL+"/api/runs?remote=1", 200); len(m["rows"].([]any)) != 1 {
		t.Fatalf("rows = %v", m["rows"])
	}
	// no trackers at all = an empty array, not null
	empty := &storage.Store{Root: t.TempDir()}
	srv3 := newServer(t, empty, Options{})
	if _, body, _ := get(t, srv3.URL+"/api/runs"); strings.TrimSpace(body) != `{"rows":[]}` {
		t.Fatalf("body = %s", body)
	}
}

func TestFocus(t *testing.T) {
	store := newTestStore(t)
	srv := newServer(t, store, Options{})
	m := getJSON(t, srv.URL+"/api/focus", 200)
	if len(m["task_ids"].([]any)) != 0 || len(m["tasks"].([]any)) != 0 {
		t.Fatalf("empty plan: %v", m)
	}
	if err := storage.WriteFocusPlan(store.RootDir(), storage.FocusPlan{Date: storage.Today(), Tasks: []string{"t-1", "t-2"}}); err != nil {
		t.Fatal(err)
	}
	m = getJSON(t, srv.URL+"/api/focus", 200)
	if m["date"] != storage.Today() || len(m["task_ids"].([]any)) != 2 || len(m["tasks"].([]any)) != 2 {
		t.Fatalf("plan: %v", m)
	}
	// a stale plan keeps its ids but expands no tasks (same gate as pm_context)
	if err := storage.WriteFocusPlan(store.RootDir(), storage.FocusPlan{Date: "2020-01-01", Tasks: []string{"t-1"}}); err != nil {
		t.Fatal(err)
	}
	m = getJSON(t, srv.URL+"/api/focus", 200)
	if len(m["task_ids"].([]any)) != 1 || len(m["tasks"].([]any)) != 0 {
		t.Fatalf("stale plan: %v", m)
	}
}

func TestAPIMisses(t *testing.T) {
	srv := newServer(t, newTestStore(t), Options{})
	m := getJSON(t, srv.URL+"/api/nope", 404)
	if !strings.Contains(m["error"].(string), "no such endpoint") {
		t.Fatalf("error = %v", m["error"])
	}
	resp, err := http.Post(srv.URL+"/api/tasks", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", resp.StatusCode)
	}
}

func TestStatic(t *testing.T) {
	t.Run("no bundle: placeholder on every non-api path, 200", func(t *testing.T) {
		srv := newServer(t, newTestStore(t), Options{Static: fstest.MapFS{}})
		for _, path := range []string{"/", "/some/route", "/index.html"} {
			status, body, hdr := get(t, srv.URL+path)
			if status != 200 || !strings.Contains(body, "make web") {
				t.Fatalf("%s = %d %q", path, status, body)
			}
			if !strings.HasPrefix(hdr.Get("Content-Type"), "text/html") {
				t.Fatalf("%s content-type = %q", path, hdr.Get("Content-Type"))
			}
		}
	})

	t.Run("embedded default is the placeholder in a test build", func(t *testing.T) {
		srv := newServer(t, newTestStore(t), Options{})
		if status, body, _ := get(t, srv.URL+"/"); status != 200 || !strings.Contains(body, "make web") {
			t.Fatalf("%d %q", status, body)
		}
	})

	t.Run("bundle: files served, unknown routes fall back to index", func(t *testing.T) {
		static := fstest.MapFS{
			"index.html":         {Data: []byte("<html>app</html>")},
			"assets/app-abc.js":  {Data: []byte("console.log(1)")},
			"favicon.svg":        {Data: []byte("<svg/>")},
			"assets/sub/x.css":   {Data: []byte("a{}")},
			"assets/only-a-dir/": {Mode: os.ModeDir},
		}
		srv := newServer(t, newTestStore(t), Options{Static: static})

		status, body, hdr := get(t, srv.URL+"/assets/app-abc.js")
		if status != 200 || body != "console.log(1)" || !strings.Contains(hdr.Get("Cache-Control"), "immutable") {
			t.Fatalf("asset: %d %q cache=%q", status, body, hdr.Get("Cache-Control"))
		}
		status, body, hdr = get(t, srv.URL+"/favicon.svg")
		if status != 200 || body != "<svg/>" || hdr.Get("Cache-Control") != "no-cache" {
			t.Fatalf("favicon: %d %q cache=%q", status, body, hdr.Get("Cache-Control"))
		}
		for _, path := range []string{"/", "/index.html", "/tasks/test/t-1", "/assets/only-a-dir"} {
			status, body, hdr := get(t, srv.URL+path)
			if status != 200 || body != "<html>app</html>" || hdr.Get("Cache-Control") != "no-store" {
				t.Fatalf("%s: %d %q cache=%q", path, status, body, hdr.Get("Cache-Control"))
			}
		}
		// the API is never shadowed by the bundle
		getJSON(t, srv.URL+"/api/projects", 200)
	})
}

// sseClient opens /api/events and hands back a channel of "event data"
// lines plus the cancel that ends the stream.
func sseClient(t *testing.T, url string) (<-chan string, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		cancel()
		t.Fatalf("events: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	events := make(chan string, 64)
	go func() {
		defer resp.Body.Close()
		defer close(events)
		sc := bufio.NewScanner(resp.Body)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				events <- event + " " + strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return events, cancel
}

func waitFor(t *testing.T, events <-chan string, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("stream closed before %q", want)
			}
			if ev == want {
				return
			}
		case <-deadline:
			t.Fatalf("no %q within 5s", want)
		}
	}
}

func TestEvents(t *testing.T) {
	store := newTestStore(t)
	srv := newServer(t, store, Options{PollInterval: 20 * time.Millisecond, PingInterval: 150 * time.Millisecond})
	events, cancel := sseClient(t, srv.URL+"/api/events")
	defer cancel()

	// idle stream pings
	waitFor(t, events, "ping {}")

	// a task file change is a tasks event for that project
	task, err := store.FindTaskExact("test", "t-1")
	if err != nil {
		t.Fatal(err)
	}
	task.Body += "\n\nedited"
	if err := storage.WriteTask(task); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, `tasks {"project":"test"}`)

	// a run-state appearing is a runs event
	execDir := filepath.Join(store.ProjectDir("test"), ".executor")
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execDir, "t-2.json"), []byte(`{"status":"running"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, `runs {"project":"test"}`)

	// cancelling the request ends the handler: the stream closes, and the
	// server can shut down (Close blocks on in-flight handlers).
	cancel()
	select {
	case _, ok := <-events:
		for ok {
			_, ok = <-events
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close after cancel")
	}
	done := make(chan struct{})
	go func() { srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("server.Close hung: the events handler did not return")
	}
}
