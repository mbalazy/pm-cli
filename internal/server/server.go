// Package server is `pm serve`: the cockpit's HTTP face over the same
// internal/service functions the MCP server calls, plus an SSE change feed
// and the embedded React bundle. Read-only by design - every mutation still
// goes through MCP or the CLI - and unauthenticated, because it binds to
// localhost (remote access is Tailscale's job, not this package's).
//
// stdlib net/http only (Go 1.22+ method+pattern routing); no framework.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/service"
	"github.com/mbalazy/pm/internal/storage"
)

// dist holds the built SPA. `all:` is load-bearing: without it go:embed skips
// files beginning with a dot, and the versioned directory holds ONLY the
// .gitkeep - an embed of nothing fails the build. The real bundle lands here
// via `make web` and is gitignored.
//
//go:embed all:dist
var dist embed.FS

// RemoteRunsFunc fetches run rows from the remote runners in the global
// config (`pm config show`) - the ssh round-trip `pm runs` does. Injected by
// the command layer because that code lives in internal/cmd, which imports
// this package; nil = no remotes reachable from here.
type RemoteRunsFunc func(ctx context.Context, store storage.TaskStore) ([]storage.RunRow, error)

// Options tunes a Handler. The zero value is production: embedded bundle,
// 2 s change polling (the board's tick), 15 s pings, no remote runs.
type Options struct {
	// Static overrides the embedded SPA (tests hand in an fstest.MapFS).
	// Its root is the dist dir itself (index.html at the top).
	Static fs.FS
	// RemoteRuns serves `?remote=1` on /api/runs. Nil = local rows only.
	RemoteRuns RemoteRunsFunc
	// PollInterval is how often the SSE feed re-reads the data dir.
	PollInterval time.Duration
	// PingInterval is how often an idle SSE stream sends `event: ping`.
	PingInterval time.Duration
}

const (
	defaultPollInterval = 2 * time.Second
	defaultPingInterval = 15 * time.Second
)

// NewHandler routes /api/* to the JSON endpoints, /api/events to the SSE
// feed and everything else to the SPA (or the placeholder page when the
// bundle has not been built).
func NewHandler(store storage.TaskStore, opts Options) http.Handler {
	if opts.Static == nil {
		sub, err := fs.Sub(dist, "dist")
		if err != nil {
			panic("pm serve: embedded dist: " + err.Error())
		}
		opts.Static = sub
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = defaultPingInterval
	}
	h := &handler{store: store, opts: opts}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", h.projects)
	mux.HandleFunc("GET /api/tasks", h.tasks)
	mux.HandleFunc("GET /api/tasks/{project}/{id}", h.task)
	mux.HandleFunc("GET /api/context", h.context)
	mux.HandleFunc("GET /api/runs", h.runs)
	mux.HandleFunc("GET /api/focus", h.focus)
	mux.HandleFunc("GET /api/events", h.events)
	// Anything else under /api/ is unknown, never the SPA: a typo'd endpoint
	// answering with index.html would read as "the server is fine, the data
	// is empty". Method-qualified so a POST to a real endpoint still gets
	// the mux's 405 instead of matching this catch-all.
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint: "+r.URL.Path)
	})
	// GET-only, like every route here: a POST anywhere is answered 405 by the
	// mux itself - this server mutates nothing and says so.
	mux.Handle("GET /", h.static())
	return mux
}

type handler struct {
	store storage.TaskStore
	opts  Options
}

// --- JSON plumbing ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeResult maps a service error onto the HTTP vocabulary: a caller's
// mistake is 400, a thing that is not there is 404, everything else is
// pm's own problem and 500. v is never encoded on the error path, so a
// typed-nil pointer arriving with an error is harmless.
func writeResult(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func statusFor(err error) int {
	var ve *service.ValidationError
	switch {
	case errors.As(err, &ve):
		return http.StatusBadRequest
	case errors.Is(err, storage.ErrTaskNotFound), errors.Is(err, storage.ErrProjectNotFound):
		return http.StatusNotFound
	case errors.Is(err, storage.ErrAmbiguousTask):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// --- Endpoints ---

// apiProject is /api/projects' row: ListProjects' row plus what a board
// needs to draw columns and badges (statuses, landing statuses, path, tags).
type apiProject struct {
	service.ProjectInfo
	Path            string            `json:"path,omitempty"`
	Repo            string            `json:"repo,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	Links           map[string]string `json:"links,omitempty"`
	Statuses        []string          `json:"statuses"`
	LandingStatuses []string          `json:"landing_statuses"`
}

type apiProjectsResult struct {
	Projects []apiProject `json:"projects"`
	Note     string       `json:"note,omitempty"`
}

func (h *handler) projects(w http.ResponseWriter, r *http.Request) {
	res, err := service.ListProjects(h.store)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	out := apiProjectsResult{Projects: []apiProject{}, Note: res.Note}
	for _, pi := range res.Projects {
		p := apiProject{ProjectInfo: pi, Statuses: []string{}, LandingStatuses: []string{}}
		// ListProjects already read the yaml once and skipped the unreadable;
		// a project listed there reads again here - the second read failing
		// leaves the row with the counts it has.
		if proj, err := h.store.GetProject(pi.Slug); err == nil {
			p.Path, p.Repo, p.Tags, p.Links = proj.Path, proj.Repo, proj.Tags, proj.Links
		}
		for _, s := range h.store.GetProjectStatuses(pi.Slug) {
			p.Statuses = append(p.Statuses, string(s))
		}
		for _, s := range h.store.GetLandingStatuses(pi.Slug) {
			p.LandingStatuses = append(p.LandingStatuses, string(s))
		}
		out.Projects = append(out.Projects, p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) tasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := service.ListTasksInput{Project: q.Get("project"), Status: q.Get("status")}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit must be an integer, got "+strconv.Quote(raw))
			return
		}
		in.Limit = n
	}
	res, err := service.ListTasks(h.store, in)
	writeResult(w, res, err)
}

func (h *handler) task(w http.ResponseWriter, r *http.Request) {
	res, err := service.GetTask(h.store, service.GetTaskInput{
		Project: r.PathValue("project"),
		TaskID:  r.PathValue("id"),
	})
	writeResult(w, res, err)
}

func (h *handler) context(w http.ResponseWriter, r *http.Request) {
	res, err := service.Context(h.store, service.ContextInput{Project: r.URL.Query().Get("project")})
	writeResult(w, res, err)
}

// runsResult is the same {rows} object `pm runs --json` prints - the one
// shape the board and the remote fetch already parse.
type runsResult struct {
	Rows []storage.RunRow `json:"rows"`
}

func (h *handler) runs(w http.ResponseWriter, r *http.Request) {
	rows, err := storage.LocalRunRows(h.store, nil)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	// Remote runners cost an ssh round-trip each, so they are fetched only on
	// an explicit ask - never on the default read a dashboard polls.
	if r.URL.Query().Get("remote") == "1" && h.opts.RemoteRuns != nil {
		remote, err := h.opts.RemoteRuns(r.Context(), h.store)
		if err != nil {
			writeResult(w, nil, err)
			return
		}
		rows = append(rows, remote...)
	}
	storage.SortRunRows(rows)
	if rows == nil {
		rows = []storage.RunRow{}
	}
	writeJSON(w, http.StatusOK, runsResult{Rows: rows})
}

// focusResult is today's focus plan with its tasks expanded the way
// pm_context lists them; Tasks is empty when the plan is not today's.
type focusResult struct {
	Date    string                `json:"date,omitempty"`
	TaskIDs []string              `json:"task_ids"`
	Tasks   []service.TaskSummary `json:"tasks"`
}

func (h *handler) focus(w http.ResponseWriter, r *http.Request) {
	fp, err := storage.ReadFocusPlan(h.store.RootDir())
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	out := focusResult{Date: fp.Date, TaskIDs: fp.Tasks, Tasks: service.FocusTaskSummaries(h.store)}
	if out.TaskIDs == nil {
		out.TaskIDs = []string{}
	}
	if out.Tasks == nil {
		out.Tasks = []service.TaskSummary{}
	}
	writeJSON(w, http.StatusOK, out)
}

// --- SPA ---

const placeholderPage = `<!doctype html>
<meta charset="utf-8">
<title>pm serve</title>
<style>body{font:15px/1.5 system-ui,sans-serif;max-width:40em;margin:4em auto;padding:0 1em;color:#333}code{background:#eee;padding:.1em .3em;border-radius:3px}</style>
<h1>pm serve</h1>
<p>The server is up, but the front end is not built into this binary.</p>
<p>Run <code>make web</code> and rebuild, or use the JSON API directly: <code>/api/projects</code>, <code>/api/tasks</code>, <code>/api/context</code>, <code>/api/runs</code>, <code>/api/focus</code>, <code>/api/events</code>.</p>
`

// static serves the bundle with client-side routing: a path that names a
// file gets the file, anything else gets index.html - and when there is no
// index.html at all, a placeholder that says so (200, not 404: the server
// is fine, the bundle is missing, and a 404 would read as the former).
func (h *handler) static() http.Handler {
	files := http.FileServerFS(h.opts.Static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(h.opts.Static, name); err == nil && !st.IsDir() {
				// Vite hashes asset names, so a file under assets/ never
				// changes content under the same name - cache it hard.
				// Everything else (favicon, manifest) is cheap to refetch.
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(h.opts.Static, "index.html")
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(placeholderPage))
			return
		}
		// The entry point is what changes between builds and what a client
		// must never hold on to - it names the hashed assets.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(index)
	})
}
