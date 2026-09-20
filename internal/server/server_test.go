package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mbalazy/pm-cli/internal/feed"
	"github.com/mbalazy/pm-cli/internal/report"
	"github.com/mbalazy/pm-cli/internal/runctl"
	"github.com/mbalazy/pm-cli/internal/service"
	"github.com/mbalazy/pm-cli/internal/storage"
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
		Name: "Test Project", Prefix: "t", Path: "/home/user/test", Tags: []string{"go"}, Notes: "where we left off",
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
	wantKeys(t, p, "slug", "name", "path", "tags", "statuses", "landing_statuses", "task_counts", "group", "group_name")
	if p["slug"] != "test" || p["path"] != "/home/user/test" {
		t.Fatalf("row = %v", p)
	}
	// No group declared and no config: the project is its own group, and
	// archived is omitted (false) rather than emitted.
	if p["group"] != "test" || p["group_name"] != "test" {
		t.Fatalf("group = %v / %v", p["group"], p["group_name"])
	}
	if _, has := p["archived"]; has {
		t.Fatalf("archived must be omitted when false: %v", p)
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

// /api/projects carries the group per row and /api/groups the groups
// themselves; an archived project drops out of the groups but stays a row.
func TestProjectsGroups(t *testing.T) {
	store := newTestStore(t)
	for _, slug := range []string{"acme-api", "acme-zap"} {
		dir := filepath.Join(store.Root, slug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := storage.WriteProject(filepath.Join(dir, "project.yaml"), &storage.Project{Name: slug, Group: "acme", Archived: slug == "acme-zap"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  groups:\n    acme: {name: Acme}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newServer(t, store, Options{})

	m := getJSON(t, srv.URL+"/api/projects", 200)
	rows := map[string]map[string]any{}
	for _, row := range m["projects"].([]any) {
		r := row.(map[string]any)
		rows[r["slug"].(string)] = r
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %v", rows)
	}
	if r := rows["acme-api"]; r["group"] != "acme" || r["group_name"] != "Acme" {
		t.Fatalf("acme-api = %v", r)
	}
	if r := rows["acme-zap"]; r["archived"] != true || r["group"] != "acme" {
		t.Fatalf("acme-zap = %v", r)
	}

	g := getJSON(t, srv.URL+"/api/groups", 200)
	groups := g["groups"].([]any)
	if len(groups) != 2 {
		t.Fatalf("groups = %v", groups)
	}
	acme := groups[0].(map[string]any)
	if acme["slug"] != "acme" || acme["name"] != "Acme" {
		t.Fatalf("acme = %v", acme)
	}
	if members := acme["projects"].([]any); len(members) != 1 || members[0] != "acme-api" {
		t.Fatalf("acme members = %v (archived acme-zap must be out)", members)
	}
	if test := groups[1].(map[string]any); test["slug"] != "test" || test["name"] != "test" {
		t.Fatalf("test = %v", test)
	}

	// A broken config is a 500 with the file named - never a sidebar of
	// slug-named groups pretending that is the setting.
	if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  sections:\n    typo: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := getJSON(t, srv.URL+"/api/groups", 500)
	if !strings.Contains(e["error"].(string), "typo") {
		t.Fatalf("error = %v", e)
	}
}

func TestConfig(t *testing.T) {
	store := newTestStore(t)
	srv := newServer(t, store, Options{})

	// No file: the defaults, resolved, so the SPA never has to know them.
	m := getJSON(t, srv.URL+"/api/config", 200)
	c := m["cockpit"].(map[string]any)
	wantKeys(t, c, "groups", "doing_idle_days", "waiting_highlight_days", "stuck_project_days",
		"cutoff_hour", "refresh", "sections", "sources", "sidebar")
	sb := c["sidebar"].(map[string]any)
	if sb["variant"] != "columns" || sb["show_repos"] != true || sb["sort"] != "worst" || sb["width"] != float64(0) {
		t.Fatalf("sidebar = %v", sb)
	}
	if c["refresh"].(map[string]any)["every_seconds"] != float64(1800) {
		t.Fatalf("refresh = %v", c["refresh"])
	}

	if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  sidebar: {variant: plain, width: 240}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = getJSON(t, srv.URL+"/api/config", 200)
	sb = m["cockpit"].(map[string]any)["sidebar"].(map[string]any)
	if sb["variant"] != "plain" || sb["width"] != float64(240) {
		t.Fatalf("sidebar = %v", sb)
	}

	// A broken file is a loud 500, never a sidebar drawn on defaults.
	if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  sidebar: {variant: bogus}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := getJSON(t, srv.URL+"/api/config", 500)
	if !strings.Contains(e["error"].(string), "bogus") {
		t.Fatalf("error = %v", e)
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

	t.Run("embedded default: the bundle when make web has run, else the placeholder", func(t *testing.T) {
		// A checkout holds only dist/.gitkeep; a tree after `make web` holds
		// the real index.html. Both are legitimate builds of this package, so
		// the test asserts whichever one it is running in.
		_, built := fs.Stat(dist, "dist/index.html")
		want := "make web"
		if built == nil {
			want = `<div id="root">`
		}
		srv := newServer(t, newTestStore(t), Options{})
		if status, body, _ := get(t, srv.URL+"/"); status != 200 || !strings.Contains(body, want) {
			t.Fatalf("%d %q (want %q)", status, body, want)
		}
	})

	t.Run("bundle: files served, unknown routes fall back to index", func(t *testing.T) {
		static := fstest.MapFS{
			"index.html":           {Data: []byte("<html>app</html>")},
			"assets/app-abc.js":    {Data: []byte("console.log(1)")},
			"favicon.svg":          {Data: []byte("<svg/>")},
			"manifest.webmanifest": {Data: []byte("{}")},
			"assets/sub/x.css":     {Data: []byte("a{}")},
			"assets/only-a-dir/":   {Mode: os.ModeDir},
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
		// The PWA manifest's type is not in Go's built-in table; sniffed, it
		// would go out as text/plain and the browser would not install the app.
		status, _, hdr = get(t, srv.URL+"/manifest.webmanifest")
		if status != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "application/manifest+json") {
			t.Fatalf("manifest: %d content-type=%q", status, hdr.Get("Content-Type"))
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

// /api/attention is the queue (storage.Attention) scoped by ?project= or
// ?group=; a bad project is 404 and a broken config 500.
func TestAttention(t *testing.T) {
	store := newTestStore(t)
	projDir := filepath.Join(store.Root, "test")
	w := &storage.Task{
		Meta:     storage.TaskMeta{ID: "t-3", Title: "Waits", Status: storage.StatusWaiting, Created: "2025-01-01", Updated: "2025-01-01T10:00:00+01:00"},
		FilePath: filepath.Join(projDir, "t-3.md"), Project: "test",
	}
	if err := storage.WriteTask(w); err != nil {
		t.Fatal(err)
	}
	srv := newServer(t, store, Options{})

	m := getJSON(t, srv.URL+"/api/attention", 200)
	wantKeys(t, m, "generated", "wip", "sections", "groups")
	sections := m["sections"].([]any)
	if len(sections) != 8 {
		t.Fatalf("sections = %d, want the eight defaults", len(sections))
	}
	var waiting map[string]any
	for _, s := range sections {
		sec := s.(map[string]any)
		if sec["name"] == "waiting" {
			waiting = sec
		}
	}
	rows := waiting["rows"].([]any)
	if waiting["total"] != float64(1) || len(rows) != 1 {
		t.Fatalf("waiting = %v", waiting)
	}
	row := rows[0].(map[string]any)
	wantKeys(t, row, "section", "severity", "project", "group", "task_id", "title", "reason", "age_seconds", "actions")
	if row["age_seconds"] != nil || row["group"] != "test" {
		t.Fatalf("row = %v (no status_changed = null age)", row)
	}
	if flags := row["flags"].([]any); len(flags) != 1 || flags[0] != "no_reason" {
		t.Fatalf("flags = %v", flags)
	}
	groups := m["groups"].([]any)
	if len(groups) != 1 || groups[0].(map[string]any)["waiting"] != float64(1) {
		t.Fatalf("groups = %v", groups)
	}

	scoped := getJSON(t, srv.URL+"/api/attention?group=other", 200)
	for _, s := range scoped["sections"].([]any) {
		if sec := s.(map[string]any); sec["total"] != float64(0) {
			t.Fatalf("group scope leaked: %v", sec)
		}
	}
	getJSON(t, srv.URL+"/api/attention?project=nosuch", 404)
	if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  sidebar:\n    variant: huge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	getJSON(t, srv.URL+"/api/attention", 500)
}

// --- change feed ---

// fakeFeedSource is a scripted feed source for the endpoint tests.
type fakeFeedSource struct {
	name   string
	events []feed.Event
	err    error
	calls  int
}

func (s *fakeFeedSource) Name() string { return s.name }
func (s *fakeFeedSource) Fetch(ctx context.Context, from, to time.Time, projects []feed.Project) ([]feed.Event, error) {
	s.calls++
	return s.events, s.err
}

// postJSON POSTs as the cockpit does - with the client header.
func postJSON(t *testing.T, url, body string, want int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ClientHeader, ClientValue)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("POST %s = %d, want %d: %s", url, resp.StatusCode, want, b)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not JSON: %v: %s", err, b)
	}
	return m
}

// Every mutation is the service function the MCP tool runs, behind one
// header check; the tests cover each endpoint's success, its 400/404, the
// 403 without the header, and the round-trips through storage.
func TestMutations(t *testing.T) {
	store := newTestStore(t)
	srv := newServer(t, store, Options{})

	t.Run("no client header is a 403 before the body is read", func(t *testing.T) {
		for _, path := range []string{"/api/tasks/test/t-1", "/api/focus/toggle", "/api/projects/test", "/api/changes/seen", "/api/changes/refresh"} {
			resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(b), ClientHeader) {
				t.Fatalf("POST %s without header = %d %s", path, resp.StatusCode, b)
			}
		}
		if task, _ := store.FindTaskExact("test", "t-1"); task.Meta.Status != storage.StatusDoing {
			t.Fatal("a refused POST must change nothing")
		}
	})

	t.Run("update task: status + waiting_for, tri-state clear, Log append", func(t *testing.T) {
		m := postJSON(t, srv.URL+"/api/tasks/test/t-1", `{"status":"waiting","waiting_for":"review","body_append":"a note"}`, 200)
		if m["status"] != "waiting" || m["waiting_for"] != "review" || m["id"] != "t-1" {
			t.Fatalf("result = %v", m)
		}
		if m["status_changed"] == "" || m["status_changed"] == nil {
			t.Fatalf("a real status change is stamped: %v", m)
		}
		task, err := store.FindTaskExact("test", "t-1")
		if err != nil {
			t.Fatal(err)
		}
		if task.Meta.Status != storage.StatusWaiting || task.Meta.WaitingFor != "review" || !strings.Contains(task.Body, "a note") {
			t.Fatalf("task = %+v / %q", task.Meta, task.Body)
		}
		// Tri-state: an empty string clears, an omitted field keeps.
		m = postJSON(t, srv.URL+"/api/tasks/test/t-1", `{"waiting_for":"","brief":"where we are"}`, 200)
		if _, has := m["waiting_for"]; has {
			t.Fatalf("waiting_for must be cleared: %v", m)
		}
		if m["brief"] != "where we are" || m["status"] != "waiting" {
			t.Fatalf("result = %v", m)
		}
	})

	t.Run("update task: bad status 400, unknown field 400, missing task 404, path wins over body", func(t *testing.T) {
		m := postJSON(t, srv.URL+"/api/tasks/test/t-1", `{"status":"bogus"}`, 400)
		if !strings.Contains(m["error"].(string), "bogus") {
			t.Fatalf("error = %v", m)
		}
		m = postJSON(t, srv.URL+"/api/tasks/test/t-1", `{"stauts":"todo"}`, 400)
		if !strings.Contains(m["error"].(string), "stauts") {
			t.Fatalf("error = %v", m)
		}
		postJSON(t, srv.URL+"/api/tasks/test/t-999", `{"status":"todo"}`, 404)
		postJSON(t, srv.URL+"/api/tasks/nope/t-1", `{"status":"todo"}`, 404)
		m = postJSON(t, srv.URL+"/api/tasks/test/t-2", `{"task_id":"t-1","project":"other","brief":"b2"}`, 200)
		if m["id"] != "t-2" || m["project"] != "test" {
			t.Fatalf("the path must name the task, not the body: %v", m)
		}
	})

	t.Run("focus toggle round-trips through focus.yaml", func(t *testing.T) {
		m := postJSON(t, srv.URL+"/api/focus/toggle", `{"task_id":"t-2"}`, 200)
		if m["focused"] != true || m["date"] != storage.Today() {
			t.Fatalf("result = %v", m)
		}
		fp, err := storage.ReadFocusPlan(store.RootDir())
		if err != nil {
			t.Fatal(err)
		}
		if len(fp.Tasks) != 1 || fp.Tasks[0] != "t-2" {
			t.Fatalf("plan = %v", fp)
		}
		f := getJSON(t, srv.URL+"/api/focus", 200)
		if len(f["tasks"].([]any)) != 1 {
			t.Fatalf("GET /api/focus = %v", f)
		}
		m = postJSON(t, srv.URL+"/api/focus/toggle", `{"task_id":"t-2"}`, 200)
		if m["focused"] != false || len(m["task_ids"].([]any)) != 0 {
			t.Fatalf("result = %v", m)
		}
		postJSON(t, srv.URL+"/api/focus/toggle", `{"task_id":"t-999"}`, 404)
		postJSON(t, srv.URL+"/api/focus/toggle", `{}`, 400)
	})

	t.Run("project notes", func(t *testing.T) {
		m := postJSON(t, srv.URL+"/api/projects/test", `{"notes":"cycle 15: batches accepted"}`, 200)
		if m["notes"] != "cycle 15: batches accepted" {
			t.Fatalf("result = %v", m)
		}
		p, err := store.GetProject("test")
		if err != nil {
			t.Fatal(err)
		}
		if p.Notes != "cycle 15: batches accepted" {
			t.Fatalf("project = %+v", p)
		}
		postJSON(t, srv.URL+"/api/projects/nope", `{"notes":"x"}`, 404)
		postJSON(t, srv.URL+"/api/projects/test", `{"prefix":"bad prefix"}`, 400)
	})

	t.Run("HTTP and the service (what MCP runs) leave identical tasks", func(t *testing.T) {
		viaHTTP, viaService := newTestStore(t), newTestStore(t)
		srv2 := newServer(t, viaHTTP, Options{})
		postJSON(t, srv2.URL+"/api/tasks/test/t-1", `{"status":"waiting","waiting_for":"client","brief":"b","body_append":"note","links":{"pr":"https://x/1"}}`, 200)
		if _, err := service.UpdateTask(viaService, service.UpdateTaskInput{
			Project: "test", TaskID: "t-1", Status: "waiting", WaitingFor: ptr("client"), Brief: ptr("b"),
			BodyAppend: "note", Links: map[string]string{"pr": "https://x/1"},
		}); err != nil {
			t.Fatal(err)
		}
		a, _ := viaHTTP.FindTaskExact("test", "t-1")
		b, _ := viaService.FindTaskExact("test", "t-1")
		// Stamps are wall-clock; everything else must match byte for byte.
		a.Meta.Updated, b.Meta.Updated = "", ""
		a.Meta.StatusChanged, b.Meta.StatusChanged = "", ""
		a.FilePath, b.FilePath = "", ""
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Fatalf("HTTP:\n%s\nservice:\n%s", ja, jb)
		}
	})
}

func ptr(s string) *string { return &s }

// /api/changes reads the cache; POST /api/changes/refresh runs the sources
// and publishes an SSE `changes` event; POST /api/changes/seen moves the
// mark and every event carries its seen flag.
func TestChangesEndpoints(t *testing.T) {
	store := newTestStore(t)
	// A fixed clock at 15:00, inside the default window; the cutoff is
	// yesterday 18:00.
	now := time.Date(2026, 9, 9, 15, 0, 0, 0, time.Local)
	src := &fakeFeedSource{name: "pm", events: []feed.Event{
		{ID: "e-new", TS: now.Add(-time.Hour).Format(time.RFC3339), Project: "test", Group: "test", Title: "fresh", Detail: "moved"},
		{ID: "e-old", TS: now.Add(-30 * time.Hour).Format(time.RFC3339), Project: "test", Group: "test", Title: "before cutoff"},
	}}
	f := feed.New(store.Root, []feed.Source{src})
	srv := newServer(t, store, Options{Feed: f, Clock: func() time.Time { return now }, PollInterval: 20 * time.Millisecond, PingInterval: time.Minute})
	events, cancel := sseClient(t, srv.URL+"/api/events")
	defer cancel()

	// Nothing cached yet: an empty feed, no sources, the cutoff stated.
	m := getJSON(t, srv.URL+"/api/changes", 200)
	wantKeys(t, m, "cutoff", "events", "unseen", "sources")
	if len(m["events"].([]any)) != 0 || m["cutoff"] != time.Date(2026, 9, 8, 18, 0, 0, 0, time.Local).Format(time.RFC3339) {
		t.Fatalf("empty feed = %v", m)
	}

	r := postJSON(t, srv.URL+"/api/changes/refresh", "", 200)
	if r["added"] != float64(1) || src.calls != 1 {
		t.Fatalf("refresh = %v (calls %d)", r, src.calls)
	}
	waitFor(t, events, `changes {"sources":["pm"]}`)

	m = getJSON(t, srv.URL+"/api/changes", 200)
	evs := m["events"].([]any)
	if len(evs) != 1 || evs[0].(map[string]any)["id"] != "e-new" || evs[0].(map[string]any)["seen"] != false || m["unseen"] != float64(1) {
		t.Fatalf("after refresh = %v", m)
	}
	sources := m["sources"].([]any)
	if len(sources) != 1 || sources[0].(map[string]any)["name"] != "pm" || sources[0].(map[string]any)["enabled"] != true {
		t.Fatalf("sources = %v", sources)
	}

	postJSON(t, srv.URL+"/api/changes/seen", "", 200)
	waitFor(t, events, `changes {"sources":[]}`)
	m = getJSON(t, srv.URL+"/api/changes", 200)
	if m["unseen"] != float64(0) || m["events"].([]any)[0].(map[string]any)["seen"] != true {
		t.Fatalf("after seen = %v", m)
	}
	// An explicit older mark leaves the newer event unseen again.
	postJSON(t, srv.URL+"/api/changes/seen", `{"ts":"`+now.Add(-2*time.Hour).Format(time.RFC3339)+`"}`, 200)
	m = getJSON(t, srv.URL+"/api/changes", 200)
	if m["unseen"] != float64(1) {
		t.Fatalf("after older mark = %v", m)
	}
	postJSON(t, srv.URL+"/api/changes/seen", `{"ts":"yesterday"}`, 400)

	// The attention queue embeds the digest off the cache: count + rows.
	a := getJSON(t, srv.URL+"/api/attention", 200)
	for _, s := range a["sections"].([]any) {
		sec := s.(map[string]any)
		if sec["name"] != "changes" {
			continue
		}
		if sec["total"] != float64(1) || len(sec["rows"].([]any)) != 1 || sec["note"] != nil {
			t.Fatalf("changes section = %v", sec)
		}
		row := sec["rows"].([]any)[0].(map[string]any)
		if row["title"] != "fresh" || row["reason"] != "pm: moved" {
			t.Fatalf("changes row = %v", row)
		}
	}

	// A GET on a POST-only route falls to the /api/ catch-all (a JSON 404,
	// never the SPA); a broken config is a 500.
	if st, body, _ := get(t, srv.URL+"/api/changes/refresh"); st != 404 || !strings.Contains(body, "no such endpoint") {
		t.Fatalf("GET refresh = %d %s", st, body)
	}
	if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  cutoff_hour: 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	getJSON(t, srv.URL+"/api/changes", 500)
}

// The scheduler refreshes inside the window and never outside it.
func TestSchedulerHonoursTheWindow(t *testing.T) {
	run := func(t *testing.T, hour int) int {
		t.Helper()
		store := newTestStore(t)
		if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  refresh:\n    every: 10ms\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		src := &fakeFeedSource{name: "pm"}
		clock := func() time.Time { return time.Date(2026, 9, 9, hour, 30, 0, 0, time.Local) }
		h := NewHandler(store, Options{Feed: feed.New(store.Root, []feed.Source{src}), Clock: clock})
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		h.RunScheduler(ctx)
		return src.calls
	}
	if n := run(t, 12); n == 0 {
		t.Error("inside the window the scheduler never refreshed")
	}
	if n := run(t, 22); n != 0 {
		t.Errorf("outside the window the scheduler refreshed %d times", n)
	}
	// The first refresh runs at start, not one interval in: a restart must
	// not leave the cache stale for a whole interval.
	{
		store := newTestStore(t)
		if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  refresh:\n    every: 1h\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		src := &fakeFeedSource{name: "pm"}
		clock := func() time.Time { return time.Date(2026, 9, 9, 12, 30, 0, 0, time.Local) }
		h := NewHandler(store, Options{Feed: feed.New(store.Root, []feed.Source{src}), Clock: clock})
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		h.RunScheduler(ctx)
		cancel()
		if src.calls != 1 {
			t.Errorf("start-up refresh: calls = %d, want 1", src.calls)
		}
	}
	// A manual refresh works outside the window regardless.
	store := newTestStore(t)
	src := &fakeFeedSource{name: "pm"}
	srv := newServer(t, store, Options{Feed: feed.New(store.Root, []feed.Source{src}), Clock: func() time.Time { return time.Date(2026, 9, 9, 22, 30, 0, 0, time.Local) }})
	postJSON(t, srv.URL+"/api/changes/refresh", "", 200)
	if src.calls != 1 {
		t.Errorf("manual refresh outside the window: calls = %d", src.calls)
	}
}

// The settings screen's writes (pm-cli-118-18): the cockpit block through
// POST /api/settings, the per-project fields through POST /api/projects.
func TestSettings(t *testing.T) {
	store := newTestStore(t)
	srv := newServer(t, store, Options{PollInterval: 20 * time.Millisecond, PingInterval: time.Minute})
	if err := os.WriteFile(store.ConfigPath(), []byte("# mine\ncockpit:\n  # owl\n  cutoff_hour: 20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	events, cancel := sseClient(t, srv.URL+"/api/events")
	defer cancel()

	t.Run("no header 403, unknown field 400, bad value 400 with the field named, nothing written", func(t *testing.T) {
		resp, err := http.Post(srv.URL+"/api/settings", "application/json", strings.NewReader(`{"cutoff_hour":9}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("no header = %d", resp.StatusCode)
		}
		m := postJSON(t, srv.URL+"/api/settings", `{"cutof_hour":9}`, 400)
		if !strings.Contains(m["error"].(string), "cutof_hour") {
			t.Fatalf("error = %v", m)
		}
		m = postJSON(t, srv.URL+"/api/settings", `{"refresh":{"every_seconds":30}}`, 400)
		if !strings.Contains(m["error"].(string), "every_seconds") {
			t.Fatalf("error = %v", m)
		}
		raw, _ := os.ReadFile(store.ConfigPath())
		if string(raw) != "# mine\ncockpit:\n  # owl\n  cutoff_hour: 20\n" {
			t.Fatalf("a rejected patch must write nothing:\n%s", raw)
		}
	})

	t.Run("a patch is written with the comments kept, answered resolved, visible on the next GET, announced on SSE", func(t *testing.T) {
		m := postJSON(t, srv.URL+"/api/settings", `{"sidebar":{"variant":"plain"},"sections":{"recent":true},"groups":[{"slug":"acme","name":"Acme","order":1}],"git":{"all_branches":true}}`, 200)
		c := m["cockpit"].(map[string]any)
		if c["cutoff_hour"] != float64(20) || c["sidebar"].(map[string]any)["variant"] != "plain" {
			t.Fatalf("result = %v", c)
		}
		groups := c["groups"].([]any)
		if len(groups) != 1 || groups[0].(map[string]any)["name"] != "Acme" || groups[0].(map[string]any)["order"] != float64(1) {
			t.Fatalf("groups = %v", groups)
		}
		if c["git"].(map[string]any)["all_branches"] != true {
			t.Fatalf("git = %v", c["git"])
		}
		waitFor(t, events, "settings {}")
		raw, _ := os.ReadFile(store.ConfigPath())
		for _, want := range []string{"# mine", "# owl", "cutoff_hour: 20", "variant: plain", "recent: true", "name: Acme"} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("config.yaml lacks %q:\n%s", want, raw)
			}
		}
		g := getJSON(t, srv.URL+"/api/config", 200)
		if g["cockpit"].(map[string]any)["sidebar"].(map[string]any)["variant"] != "plain" {
			t.Fatalf("the next GET must see the write: %v", g)
		}
	})

	t.Run("project settings: group, archived and slack through POST /api/projects, comments kept", func(t *testing.T) {
		path := filepath.Join(store.Root, "test", "project.yaml")
		raw, _ := os.ReadFile(path)
		if err := os.WriteFile(path, append([]byte("# project comment\n"), raw...), 0o644); err != nil {
			t.Fatal(err)
		}
		m := postJSON(t, srv.URL+"/api/projects/test", `{"group":"acme","slack":{"workspace":"acme","channels":[" #acme-api-dev ",""]}}`, 200)
		if m["group"] != "acme" {
			t.Fatalf("result = %v", m)
		}
		slack := m["slack"].(map[string]any)
		if slack["workspace"] != "acme" || len(slack["channels"].([]any)) != 1 || slack["channels"].([]any)[0] != "#acme-api-dev" {
			t.Fatalf("slack = %v", slack)
		}
		raw, _ = os.ReadFile(path)
		for _, want := range []string{"# project comment", "group: acme", "workspace: acme", "- '#acme-api-dev'"} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("project.yaml lacks %q:\n%s", want, raw)
			}
		}
		p := getJSON(t, srv.URL+"/api/projects", 200)
		row := p["projects"].([]any)[0].(map[string]any)
		if row["group"] != "acme" || row["slack"].(map[string]any)["workspace"] != "acme" {
			t.Fatalf("projects row = %v", row)
		}
		// An empty mapping removes the key; archiving puts the project to sleep.
		m = postJSON(t, srv.URL+"/api/projects/test", `{"slack":{"workspace":"","channels":[]},"archived":true}`, 200)
		if _, has := m["slack"]; has || m["archived"] != true {
			t.Fatalf("result = %v", m)
		}
		raw, _ = os.ReadFile(path)
		if strings.Contains(string(raw), "slack:") || !strings.Contains(string(raw), "archived: true") {
			t.Fatalf("project.yaml = \n%s", raw)
		}
		postJSON(t, srv.URL+"/api/projects/test", `{"group":"Bad Group"}`, 400)
		postJSON(t, srv.URL+"/api/projects/nope", `{"archived":false}`, 404)
	})
}

// Run control over HTTP (pm-cli-118-21): every action against a fake `pm`
// so no request ever starts a worker; the conflict states are 409.
func TestRunControl(t *testing.T) {
	store := newTestStore(t)
	// The fixture's project path is not a directory; point it at a temp
	// checkout so a spawn has somewhere to run.
	repo := t.TempDir()
	if _, err := store.MutateProject("test", func(p *storage.Project) error { p.Path = repo; return nil }); err != nil {
		t.Fatal(err)
	}
	fakeDir := t.TempDir()
	out := filepath.Join(fakeDir, "argv.txt")
	exe := filepath.Join(fakeDir, "pm")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$FAKE_PM_OUT\"\nprintf 'source=%s\\n' \"$PM_LAUNCH_SOURCE\" >> \"$FAKE_PM_OUT\"\nsleep 30\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PM_OUT", out)
	rc := runctl.New(store, runctl.Options{Exe: exe, Session: "cockpit-testhost", After: func(time.Duration, func()) {}})
	srv := newServer(t, store, Options{RunControl: rc})

	t.Run("plan: argv preview, never --sim, unknown action 400, missing task 404", func(t *testing.T) {
		m := getJSON(t, srv.URL+"/api/runs/test/t-2/plan?action=resume_run&yolo=1&additional=1", 200)
		argv := m["argv"].([]any)
		if len(argv) != 4 || argv[0] != "run-epic" || argv[3] != "--yolo" || m["additional_avail"] != false {
			t.Fatalf("plan = %v", m)
		}
		m = getJSON(t, srv.URL+"/api/runs/test/t-2/plan?action=rerun_finish", 200)
		joined := ""
		for _, a := range m["argv"].([]any) {
			joined += a.(string) + " "
		}
		if !strings.Contains(joined, "--no-sim") || strings.Contains(joined, "--sim ") {
			t.Fatalf("finish argv = %q", joined)
		}
		m = getJSON(t, srv.URL+"/api/runs/test/t-2/plan?action=claim", 200)
		if m["session"] != "cockpit-testhost" {
			t.Fatalf("claim plan = %v", m)
		}
		getJSON(t, srv.URL+"/api/runs/test/t-2/plan?action=dance", 400)
		getJSON(t, srv.URL+"/api/runs/test/t-9/plan?action=kill", 404)
	})

	t.Run("POST needs the header; resume_run spawns the fake with the cockpit source; kill stops it; a second kill is 409", func(t *testing.T) {
		resp, err := http.Post(srv.URL+"/api/runs/test/t-2/resume_run", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("no header = %d", resp.StatusCode)
		}
		m := postJSON(t, srv.URL+"/api/runs/test/t-2/resume_run", `{"yolo":true}`, 200)
		pid := int(m["pid"].(float64))
		t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		if pid <= 0 || m["kind"] != "run-epic" {
			t.Fatalf("started = %v", m)
		}
		deadline := time.Now().Add(5 * time.Second)
		var seen string
		for time.Now().Before(deadline) {
			if b, err := os.ReadFile(out); err == nil && strings.Contains(string(b), "source=") {
				seen = string(b)
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !strings.HasPrefix(seen, "run-epic test t-2 --yolo\n") || !strings.Contains(seen, "source=cockpit") {
			t.Fatalf("fake pm saw %q", seen)
		}
		// The run is on /api/runs at once (the seed).
		r := getJSON(t, srv.URL+"/api/runs", 200)
		live := false
		for _, row := range r["rows"].([]any) {
			if row.(map[string]any)["tracker"] == "t-2" && row.(map[string]any)["run_live"] == true {
				live = true
			}
		}
		if !live {
			t.Fatalf("runs = %v", r)
		}
		k := postJSON(t, srv.URL+"/api/runs/test/t-2/kill", "", 200)
		if int(k["pid"].(float64)) != pid || k["parked"] != "t-2" {
			t.Fatalf("kill = %v", k)
		}
		for time.Now().Before(time.Now().Add(5*time.Second)) && storage.ProcessAlive(pid) {
			time.Sleep(10 * time.Millisecond)
		}
		e := postJSON(t, srv.URL+"/api/runs/test/t-2/kill", "", 409)
		if !strings.Contains(e["error"].(string), "nothing was signalled") {
			t.Fatalf("second kill = %v", e)
		}
		postJSON(t, srv.URL+"/api/runs/test/t-2/dance", "", 400)
		postJSON(t, srv.URL+"/api/runs/test/t-2/resume_run", `{"yolo":"yes"}`, 400)
	})

	t.Run("claim, a foreign claim is 409 naming the holder, release", func(t *testing.T) {
		// The killed run above outranks a live claim in needs_me (a failed
		// run is rank a, a claim rank f); drop its state so the claim row shows.
		if err := os.Remove(storage.ExecutorRunPath(store.ProjectDir("test"), "t-2")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  show_executor: true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		m := postJSON(t, srv.URL+"/api/runs/test/t-2/claim", "", 200)
		if m["claim"].(map[string]any)["session"] != "cockpit-testhost" {
			t.Fatalf("claim = %v", m)
		}
		// The attention queue now offers release_claim on the row.
		a := getJSON(t, srv.URL+"/api/attention", 200)
		found := false
		for _, s := range a["sections"].([]any) {
			for _, row := range s.(map[string]any)["rows"].([]any) {
				rm := row.(map[string]any)
				if rm["task_id"] == "t-2" && rm["section"] == "needs_me" {
					for _, act := range rm["actions"].([]any) {
						if act == "release_claim" {
							found = true
						}
					}
				}
			}
		}
		if !found {
			t.Fatalf("the live-claim row must carry release_claim: %v", a)
		}
		rel := postJSON(t, srv.URL+"/api/runs/test/t-2/release_claim", "", 200)
		if rel["released"] != true {
			t.Fatalf("release = %v", rel)
		}
		if _, err := storage.AcquireFinishClaim(store.ProjectDir("test"), "t-2", "somebody"); err != nil {
			t.Fatal(err)
		}
		e := postJSON(t, srv.URL+"/api/runs/test/t-2/claim", "", 409)
		if !strings.Contains(e["error"].(string), "somebody") {
			t.Fatalf("busy = %v", e)
		}
		postJSON(t, srv.URL+"/api/runs/test/t-2/release_claim", "", 409)
	})
}

// The LLM report (pm-cli-118-20) against a fake `claude`: never run while
// the switch is off, written once per period after a refresh when on,
// written now on POST, its failure stored, a suggestion dismissed.
func TestReport(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 9, 9, 15, 0, 0, 0, time.Local)
	fakeDir := t.TempDir()
	calls := filepath.Join(fakeDir, "calls.log")
	exe := filepath.Join(fakeDir, "claude")
	env := `{"type":"result","subtype":"success","is_error":false,"result":"**test**: t-1 waits.\n\n- SUGGEST t-1 back_to_todo: the block lifted\n","session_id":"s","usage":{"input_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":20}}`
	prompts := filepath.Join(fakeDir, "prompts.log")
	script := "#!/bin/sh\necho CALL >> \"$FAKE_CLAUDE_CALLS\"\necho \"$*\" >> \"$FAKE_CLAUDE_PROMPTS\"\nif [ -f \"$FAKE_CLAUDE_FAIL\" ]; then echo boom >&2; exit 1; fi\nprintf '%s' '" + env + "'\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_CALLS", calls)
	t.Setenv("FAKE_CLAUDE_PROMPTS", prompts)
	failFlag := filepath.Join(fakeDir, "fail")
	t.Setenv("FAKE_CLAUDE_FAIL", failFlag)
	countCalls := func() int {
		b, _ := os.ReadFile(calls)
		return strings.Count(string(b), "CALL")
	}
	src := &fakeFeedSource{name: "pm", events: []feed.Event{{ID: "e1", TS: now.Add(-time.Hour).Format(time.RFC3339), Project: "test", Group: "test", TaskID: "t-1", Title: "Test Task", Detail: "moved"}}}
	f := feed.New(store.Root, []feed.Source{src})
	srv := newServer(t, store, Options{Feed: f, Clock: func() time.Time { return now }, Report: &report.Writer{Exe: exe}, PollInterval: 20 * time.Millisecond, PingInterval: time.Minute})
	events, cancel := sseClient(t, srv.URL+"/api/events")
	defer cancel()

	t.Run("off: GET says off, POST is 409, a refresh writes nothing - claude never ran", func(t *testing.T) {
		m := getJSON(t, srv.URL+"/api/report", 200)
		if m["enabled"] != false || m["state"] != "off" || m["period"] != "2026-09-08T18" {
			t.Fatalf("report = %v", m)
		}
		e := postJSON(t, srv.URL+"/api/report", "", 409)
		if !strings.Contains(e["error"].(string), "off") {
			t.Fatalf("error = %v", e)
		}
		postJSON(t, srv.URL+"/api/changes/refresh", "", 200)
		waitFor(t, events, `changes {"sources":["pm"]}`)
		time.Sleep(50 * time.Millisecond)
		if n := countCalls(); n != 0 {
			t.Fatalf("claude ran %d time(s) while the report was off", n)
		}
		if m := getJSON(t, srv.URL+"/api/report", 200); m["state"] != "off" {
			t.Fatalf("after refresh = %v", m)
		}
	})

	t.Run("on: the next refresh writes the period's report once; GET carries text, suggestions, tokens", func(t *testing.T) {
		if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  sources:\n    report: true\n  report:\n    model: haiku\n    language: en\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if m := getJSON(t, srv.URL+"/api/report", 200); m["state"] != "none" || m["enabled"] != true {
			t.Fatalf("enabled, nothing yet = %v", m)
		}
		postJSON(t, srv.URL+"/api/changes/refresh", "", 200)
		waitFor(t, events, `report {"period":"2026-09-08T18"}`)
		if n := countCalls(); n != 1 {
			t.Fatalf("claude calls = %d, want 1", n)
		}
		m := getJSON(t, srv.URL+"/api/report", 200)
		if m["state"] != "done" {
			t.Fatalf("report = %v", m)
		}
		rep := m["report"].(map[string]any)
		if !strings.HasPrefix(rep["text"].(string), "**test**") || rep["model"] != "haiku" {
			t.Fatalf("report = %v", rep)
		}
		if rep["tokens"].(map[string]any)["cache_read"] != float64(100) {
			t.Fatalf("tokens = %v", rep["tokens"])
		}
		sugg := rep["suggestions"].([]any)
		if len(sugg) != 1 || sugg[0].(map[string]any)["project"] != "test" || sugg[0].(map[string]any)["action"] != "back_to_todo" {
			t.Fatalf("suggestions = %v", sugg)
		}
		// A second refresh in the same period writes nothing more.
		postJSON(t, srv.URL+"/api/changes/refresh", "", 200)
		waitFor(t, events, `changes {"sources":["pm"]}`)
		time.Sleep(50 * time.Millisecond)
		if n := countCalls(); n != 1 {
			t.Fatalf("claude calls after the second refresh = %d, want 1", n)
		}
		// The prompt carried the event and the language.
		b, _ := os.ReadFile(prompts)
		if !strings.Contains(string(b), "t-1: Test Task - moved") || !strings.Contains(string(b), `language with code "en"`) || !strings.Contains(string(b), "--model haiku") {
			t.Fatalf("prompt = %s", b)
		}
		// ...and the task's status as of the write, beside the history.
		if !strings.Contains(string(b), "- task test t-1 ") {
			t.Fatalf("prompt lacks the task's current state: %s", b)
		}
		// Dismiss: recorded and announced; an unknown id is 400.
		id := sugg[0].(map[string]any)["id"].(string)
		d := postJSON(t, srv.URL+"/api/report/dismiss", `{"id":"`+id+`"}`, 200)
		if len(d["dismissed"].([]any)) != 1 {
			t.Fatalf("dismiss = %v", d)
		}
		waitFor(t, events, `report {"period":"2026-09-08T18"}`)
		postJSON(t, srv.URL+"/api/report/dismiss", `{"id":"nope"}`, 400)
	})

	t.Run("POST writes now even though the period has a report; a failing claude is stored as the error state", func(t *testing.T) {
		if err := os.WriteFile(failFlag, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		// An event only a NEW refresh can bring: "write now" must describe
		// the present, not the cache of the last tick.
		src.events = append(src.events, feed.Event{ID: "e2", TS: now.Add(-time.Minute).Format(time.RFC3339), Project: "test", Group: "test", Title: "Fresh since the last tick", Detail: "merged"})
		m := postJSON(t, srv.URL+"/api/report", "", 202)
		if m["state"] != "writing" {
			t.Fatalf("accepted = %v", m)
		}
		waitFor(t, events, `report {"period":"2026-09-08T18"}`)
		m = getJSON(t, srv.URL+"/api/report", 200)
		if m["state"] != "error" || !strings.Contains(m["report"].(map[string]any)["error"].(string), "boom") {
			t.Fatalf("after failure = %v", m)
		}
		if n := countCalls(); n != 2 {
			t.Fatalf("claude calls = %d, want 2", n)
		}
		if b, _ := os.ReadFile(prompts); !strings.Contains(string(b), "Fresh since the last tick") {
			t.Fatalf("write now did not refresh the feed first: %s", b)
		}
		// GET on the POST-only route is the catch-all 404; no header is 403.
		if st, _, _ := get(t, srv.URL+"/api/report/dismiss"); st != 404 {
			t.Fatalf("GET dismiss = %d", st)
		}
		resp, _ := http.Post(srv.URL+"/api/report", "application/json", nil)
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatalf("no header = %d", resp.StatusCode)
		}
	})
}

// TestStopStreamsEndsHandlers: StopStreams (registered with the server's
// Shutdown by pm serve) ends an open /api/events stream at once, so
// Shutdown does not wait out its deadline with a cockpit tab open.
func TestStopStreamsEndsHandlers(t *testing.T) {
	store := newTestStore(t)
	h := NewHandler(store, Options{PollInterval: 20 * time.Millisecond, PingInterval: 50 * time.Millisecond})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	events, cancel := sseClient(t, srv.URL+"/api/events")
	defer cancel()
	waitFor(t, events, "ping {}")
	started := time.Now()
	h.StopStreams()
	h.StopStreams() // idempotent
	select {
	case _, ok := <-events:
		for ok {
			_, ok = <-events
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close after StopStreams")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("stream took %v to close", time.Since(started))
	}
	// A stream opened after the stop ends immediately too.
	later, cancel2 := sseClient(t, srv.URL+"/api/events")
	defer cancel2()
	select {
	case _, ok := <-later:
		for ok {
			_, ok = <-later
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a stream opened after StopStreams did not end")
	}
}

// TestReportWaitsForACleanRefresh: a refresh in which an enabled source
// failed (gh unauthenticated, the laptop offline) is not the period's
// "first successful refresh" - the report is not written off an empty feed
// and is written by the next clean refresh instead.
func TestReportWaitsForACleanRefresh(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 9, 9, 15, 0, 0, 0, time.Local)
	fakeDir := t.TempDir()
	calls := filepath.Join(fakeDir, "calls.log")
	exe := filepath.Join(fakeDir, "claude")
	env := `{"type":"result","subtype":"success","is_error":false,"result":"quiet day","session_id":"s","usage":{"input_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":1,"output_tokens":2}}`
	script := "#!/bin/sh\necho CALL >> \"$FAKE_CLAUDE_CALLS2\"\nprintf '%s' '" + env + "'\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_CALLS2", calls)
	countCalls := func() int {
		b, _ := os.ReadFile(calls)
		return strings.Count(string(b), "CALL")
	}
	if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  sources:\n    report: true\n  report:\n    model: haiku\n    language: en\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &fakeFeedSource{name: "pm", err: errors.New("gh: not logged in")}
	f := feed.New(store.Root, []feed.Source{src})
	srv := newServer(t, store, Options{Feed: f, Clock: func() time.Time { return now }, Report: &report.Writer{Exe: exe}, PollInterval: 20 * time.Millisecond, PingInterval: time.Minute})
	events, cancel := sseClient(t, srv.URL+"/api/events")
	defer cancel()

	postJSON(t, srv.URL+"/api/changes/refresh", "", 200)
	waitFor(t, events, `changes {"sources":["pm"]}`)
	time.Sleep(100 * time.Millisecond)
	if n := countCalls(); n != 0 {
		t.Fatalf("claude ran %d time(s) after a refresh whose source failed", n)
	}
	if m := getJSON(t, srv.URL+"/api/report", 200); m["state"] != "none" {
		t.Fatalf("after the failed refresh = %v", m)
	}
	// The next clean refresh writes it.
	src.err = nil
	postJSON(t, srv.URL+"/api/changes/refresh", "", 200)
	waitFor(t, events, `report {"period":"2026-09-08T18"}`)
	if n := countCalls(); n != 1 {
		t.Fatalf("claude calls = %d, want 1", n)
	}
	if m := getJSON(t, srv.URL+"/api/report", 200); m["state"] != "done" {
		t.Fatalf("after the clean refresh = %v", m)
	}
}
