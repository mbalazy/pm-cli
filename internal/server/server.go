// Package server is `pm serve`: the cockpit's HTTP face over the same
// internal/service functions the MCP server calls, plus an SSE change feed,
// the change feed's endpoints and scheduler (changes.go), and the embedded
// React bundle. Mutations (task edits, focus, project notes and settings,
// the cockpit block of config.yaml) are the service's functions behind the
// client header (pm-cli-118-16/-18); unauthenticated, because it binds to
// localhost (remote access is Tailscale's job, not this package's).
//
// stdlib net/http only (Go 1.22+ method+pattern routing); no framework.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mbalazy/pm/internal/feed"
	"github.com/mbalazy/pm/internal/report"
	"github.com/mbalazy/pm/internal/review"
	"github.com/mbalazy/pm/internal/runctl"
	"github.com/mbalazy/pm/internal/service"
	"github.com/mbalazy/pm/internal/solo"
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
	// Feed is the change feed; nil = feed.New over the store's root with the
	// default sources (git/gh through feed.DefaultRunner). The command layer
	// hands in one whose runner is the executor's process-group runner.
	Feed *feed.Feed
	// Clock is the scheduler's and the cutoff's clock; nil = time.Now.
	// Injected so the refresh window is testable at any hour.
	Clock func() time.Time
	// RunControl performs the run actions (pm-cli-118-21); nil = runctl.New
	// over the store with the serving binary. Tests hand in one whose pm
	// is a fake script, so no request ever starts a real worker.
	RunControl *runctl.Controller
	// Report writes the LLM report (pm-cli-118-20); nil = `claude` on PATH.
	// Tests hand in one whose Exe is a fake script - and the test that the
	// report never runs while switched off records that no script ran.
	Report *report.Writer
	// Review runs PR code reviews; nil = review.New over the store with
	// `claude` on PATH. Tests hand in one whose Exe is a fake script.
	Review *review.Controller
	// Solo starts /solo sessions as Claude Code background sessions
	// (pm-cli-141); nil = solo.New over the store with `claude` on PATH.
	// Tests hand in one whose Exe is a fake script.
	Solo *solo.Controller
}

const (
	defaultPollInterval = 2 * time.Second
	defaultPingInterval = 15 * time.Second
)

func init() {
	// Go's built-in table has no entry for the PWA manifest and would sniff
	// it as text/plain; browsers want the manifest type to install the app.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// Handler is the server: an http.Handler plus the feed scheduler's entry
// point (RunScheduler), which needs the same feed and clock the endpoints
// use.
type Handler struct {
	mux *http.ServeMux
	h   *handler
}

// ServeHTTP makes Handler an http.Handler.
func (s *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// StopStreams ends every open /api/events stream (and refuses none: a
// stream opened afterwards ends at once). Register it with
// http.Server.RegisterOnShutdown so Shutdown completes as soon as the
// ordinary requests are done instead of waiting out its deadline with a
// cockpit tab open. Idempotent.
func (s *Handler) StopStreams() { s.h.stopOnce.Do(func() { close(s.h.done) }) }

// NewHandler routes /api/* to the JSON endpoints, /api/events to the SSE
// feed and everything else to the SPA (or the placeholder page when the
// bundle has not been built).
func NewHandler(store storage.TaskStore, opts Options) *Handler {
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
	if opts.Feed == nil {
		opts.Feed = feed.New(store.RootDir(), nil)
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.RunControl == nil {
		opts.RunControl = runctl.New(store, runctl.Options{})
	}
	if opts.Report == nil {
		opts.Report = &report.Writer{}
	}
	if opts.Review == nil {
		opts.Review = review.New(store)
	}
	if opts.Solo == nil {
		opts.Solo = solo.New(store)
	}
	h := &handler{store: store, opts: opts, feed: opts.Feed, clock: opts.Clock, bus: newChangeBus(), runs: opts.RunControl, reviews: opts.Review, solo: opts.Solo,
		reports: &reportControl{writing: map[string]bool{}}, done: make(chan struct{})}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", h.projects)
	mux.HandleFunc("GET /api/groups", h.groups)
	mux.HandleFunc("GET /api/config", h.config)
	mux.HandleFunc("GET /api/tasks", h.tasks)
	mux.HandleFunc("GET /api/tasks/{project}/{id}", h.task)
	mux.HandleFunc("GET /api/context", h.context)
	mux.HandleFunc("GET /api/runs", h.runRows)
	mux.HandleFunc("GET /api/runs/{project}/{id}/plan", h.runPlan)
	mux.HandleFunc("GET /api/focus", h.focus)
	mux.HandleFunc("GET /api/attention", h.attention)
	mux.HandleFunc("GET /api/changes", h.changes)
	mux.HandleFunc("GET /api/report", h.getReport)
	mux.HandleFunc("GET /api/reviews", h.listReviews)
	mux.HandleFunc("GET /api/reviews/{id}", h.getReview)
	mux.HandleFunc("GET /api/solo", h.soloShifts)
	mux.HandleFunc("GET /api/solo/plan", h.soloPlan)
	mux.HandleFunc("GET /api/solo/{id}", h.soloLaunch)
	mux.HandleFunc("GET /api/solo/{project}/{shift}/report", h.soloReport)
	// Mutations. Every POST under /api/ needs the client header (see
	// requireClient); GETs stay open.
	mux.HandleFunc("POST /api/changes/refresh", requireClient(h.refresh))
	mux.HandleFunc("POST /api/changes/seen", requireClient(h.seen))
	mux.HandleFunc("POST /api/tasks/{project}/{id}", requireClient(h.updateTask))
	mux.HandleFunc("POST /api/focus/toggle", requireClient(h.toggleFocus))
	mux.HandleFunc("POST /api/projects/{slug}", requireClient(h.updateProject))
	mux.HandleFunc("POST /api/settings", requireClient(h.updateSettings))
	mux.HandleFunc("POST /api/runs/{project}/{id}/{action}", requireClient(h.runAction))
	mux.HandleFunc("POST /api/report", requireClient(h.writeReport))
	mux.HandleFunc("POST /api/report/dismiss", requireClient(h.dismissSuggestion))
	mux.HandleFunc("POST /api/reviews", requireClient(h.startReview))
	mux.HandleFunc("POST /api/reviews/{id}/cancel", requireClient(h.cancelReview))
	mux.HandleFunc("POST /api/reviews/{id}/approve", requireClient(h.approveReview))
	mux.HandleFunc("POST /api/solo", requireClient(h.soloStart))
	mux.HandleFunc("POST /api/solo/{id}/stop", requireClient(h.soloStop))
	mux.HandleFunc("POST /api/attention/dismiss", requireClient(h.dismissRows))
	mux.HandleFunc("POST /api/attention/restore", requireClient(h.restoreRows))
	mux.HandleFunc("GET /api/events", h.events)
	// Anything else under /api/ is unknown, never the SPA: a typo'd endpoint
	// answering with index.html would read as "the server is fine, the data
	// is empty". Method-qualified so a POST to a real endpoint still gets
	// the mux's 405 instead of matching this catch-all.
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint: "+r.URL.Path)
	})
	// GET-only, like every data route here: a POST anywhere else is answered
	// 405 by the mux itself - pm's data is not mutated over HTTP. The two
	// POSTs above touch the feed's own cache, nothing of pm's.
	mux.Handle("GET /", h.static())
	return &Handler{mux: mux, h: h}
}

type handler struct {
	store storage.TaskStore
	opts  Options
	feed  *feed.Feed
	clock func() time.Time
	bus   *changeBus
	runs  *runctl.Controller
	// reviews runs the PR code reviews.
	reviews *review.Controller
	solo    *solo.Controller
	// reports tracks the report writes in flight (one per period).
	reports *reportControl
	// done is closed by StopStreams: every SSE handler selects on it and
	// returns, which is what lets http.Server.Shutdown finish - Shutdown
	// waits for active connections and never cancels a request's context,
	// so a stream would otherwise hold it until the timeout.
	done     chan struct{}
	stopOnce sync.Once
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

// ClientHeader is the header every POST must carry, with ClientValue as its
// value. There is no auth (localhost, Tailscale for the phone - pm-cli-118-5),
// and this is not auth either: it is the one thing a cross-site form post or
// a stray `curl` cannot send by accident, so a mutation only ever comes from
// the cockpit itself. GETs are unaffected.
const (
	ClientHeader = "X-PM-Client"
	ClientValue  = "cockpit"
)

// requireClient refuses a POST without the client header with a 403 naming
// the header, before the handler reads a byte of the body.
func requireClient(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(ClientHeader) != ClientValue {
			writeError(w, http.StatusForbidden, fmt.Sprintf("mutations need the %s: %s header", ClientHeader, ClientValue))
			return
		}
		next(w, r)
	}
}

// decodeBody decodes a JSON body into v, refusing unknown fields - a typo'd
// field name would otherwise be a silent no-op the caller reads as "saved".
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "body must be a JSON object: "+err.Error())
		return false
	}
	return true
}

// --- Endpoints ---

// apiProject is /api/projects' row: ListProjects' row plus what a board
// needs to draw columns and badges (statuses, landing statuses, path, tags).
type apiProject struct {
	service.ProjectInfo
	Path            string            `json:"path,omitempty"`
	Repo            string            `json:"repo,omitempty"`
	Notes           string            `json:"notes,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	Links           map[string]string `json:"links,omitempty"`
	Statuses        []string          `json:"statuses"`
	LandingStatuses []string          `json:"landing_statuses"`
	// Slack is the project's Slack mapping (project.yaml `slack`), edited
	// on the settings screen; absent when the project has none.
	Slack *service.SlackMapping `json:"slack,omitempty"`
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
			p.Path, p.Repo, p.Notes, p.Tags, p.Links = proj.Path, proj.Repo, proj.Notes, proj.Tags, proj.Links
			p.Slack = service.SlackOf(proj)
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

// groups is the cockpit's sidebar input: the project groups derived from the
// active projects' `group` fields, named by the global config.
func (h *handler) groups(w http.ResponseWriter, r *http.Request) {
	res, err := service.ListGroups(h.store)
	writeResult(w, res, err)
}

// config is the resolved cockpit block of config.yaml - the SPA shapes its
// sidebar and sections from it, and the settings screen edits it through
// updateSettings below. Read on every call, so a write (from the screen or
// by hand) is visible on the next request without a restart.
func (h *handler) config(w http.ResponseWriter, r *http.Request) {
	res, err := service.Config(h.store)
	writeResult(w, res, err)
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

func (h *handler) runRows(w http.ResponseWriter, r *http.Request) {
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

// attention is the home screen's queue: the whole storage.Attention, scoped
// by ?project= or ?group=. Computed on every call - it reads only local
// files, and the SSE feed tells the client when to ask again.
func (h *handler) attention(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := service.AttentionAt(h.store, service.AttentionInput{Project: q.Get("project"), Group: q.Get("group")}, h.clock())
	writeResult(w, res, err)
}

// --- Mutations (pm-cli-118-16) ---
//
// Each one is a thin call into internal/service - the SAME function the MCP
// tool runs, so tri-state fields, link merging, status validation, the Spec/
// Log body zones and the project lock are the service's and never re-stated
// here. The body decodes straight into the service's input struct: there is
// no second copy of the parameter list to drift.

// updateTask is pm_update_task over HTTP: POST /api/tasks/{project}/{id}
// with a JSON body of service.UpdateTaskInput fields (project and task_id
// come from the path and override the body).
func (h *handler) updateTask(w http.ResponseWriter, r *http.Request) {
	var in service.UpdateTaskInput
	if !decodeBody(w, r, &in) {
		return
	}
	in.Project = r.PathValue("project")
	in.TaskID = r.PathValue("id")
	task, err := service.UpdateTask(h.store, in)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, service.ToDetail(task))
}

// toggleFocus is the board's `t`: POST /api/focus/toggle {"task_id": ...}.
func (h *handler) toggleFocus(w http.ResponseWriter, r *http.Request) {
	var in service.ToggleFocusInput
	if !decodeBody(w, r, &in) {
		return
	}
	res, err := service.ToggleFocus(h.store, in)
	writeResult(w, res, err)
}

// updateProject is pm_update_project over HTTP: POST /api/projects/{slug}
// with service.UpdateProjectInput fields (the slug comes from the path). The
// cockpit sends `notes` ("where we left off", v1 = hand-written) and, from
// the settings screen, `group`, `archived` and `slack` - the per-project
// settings, which live in project.yaml and not in the global block.
func (h *handler) updateProject(w http.ResponseWriter, r *http.Request) {
	var in service.UpdateProjectInput
	if !decodeBody(w, r, &in) {
		return
	}
	in.Project = r.PathValue("slug")
	res, err := service.UpdateProject(h.store, in)
	writeResult(w, res, err)
}

// updateSettings is the settings screen's write: POST /api/settings with a
// service.UpdateSettingsInput PATCH of the cockpit block (only what changed;
// absent = keep). config.yaml is written back with its comments and unknown
// keys kept, the resolved block is answered, and every open SSE stream gets
// `event: settings` so other tabs re-read /api/config and the queue. No
// lock: the file is edited by one person, and the scheduler only reads it.
func (h *handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var in service.UpdateSettingsInput
	if !decodeBody(w, r, &in) {
		return
	}
	res, err := service.UpdateSettings(h.store, in)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	h.bus.publish("settings", "{}")
	writeJSON(w, http.StatusOK, res)
}

// --- SPA ---

const placeholderPage = `<!doctype html>
<meta charset="utf-8">
<title>pm serve</title>
<style>body{font:15px/1.5 system-ui,sans-serif;max-width:40em;margin:4em auto;padding:0 1em;color:#333}code{background:#eee;padding:.1em .3em;border-radius:3px}</style>
<h1>pm serve</h1>
<p>The server is up, but the front end is not built into this binary.</p>
<p>Run <code>make web</code> and rebuild, or use the JSON API directly: <code>/api/projects</code>, <code>/api/groups</code>, <code>/api/tasks</code>, <code>/api/context</code>, <code>/api/runs</code>, <code>/api/focus</code>, <code>/api/attention</code>, <code>/api/changes</code>, <code>/api/config</code>, <code>/api/events</code>.</p>
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
