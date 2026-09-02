package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The SSE feed says WHAT CHANGED, never what it changed to: `event: tasks`
// or `event: runs` with `{"project": slug}`, and the client refetches the
// endpoint it cares about. Polling, not fsnotify (decision: zero new
// dependencies, and the board already lives on a 2 s tick), and the poll
// reads only directory metadata - names, sizes, mtimes - never a file's
// content, so a hundred projects cost a hundred ReadDirs per tick.

// snapshot is one project's change fingerprint: the task files' shape and
// the executor dir's shape, each folded into a string that differs iff
// something a client would refetch for differs.
type snapshot struct {
	tasks string
	runs  string
}

func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported by this connection")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	emit := func(event, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	last := h.snapshots()
	poll := time.NewTicker(h.opts.PollInterval)
	defer poll.Stop()
	ping := time.NewTicker(h.opts.PingInterval)
	defer ping.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			emit("ping", "{}")
		case <-poll.C:
			now := h.snapshots()
			// Sorted so two clients see the same order and a test can
			// count on it; a project that appeared or vanished shows up as
			// a tasks change for it.
			slugs := make([]string, 0, len(now))
			for slug := range now {
				slugs = append(slugs, slug)
			}
			sort.Strings(slugs)
			for _, slug := range slugs {
				prev, had := last[slug]
				if !had || prev.tasks != now[slug].tasks {
					emit("tasks", fmt.Sprintf(`{"project":%q}`, slug))
				}
				if !had || prev.runs != now[slug].runs {
					emit("runs", fmt.Sprintf(`{"project":%q}`, slug))
				}
			}
			for slug := range last {
				if _, still := now[slug]; !still {
					emit("tasks", fmt.Sprintf(`{"project":%q}`, slug))
				}
			}
			last = now
		}
	}
}

// snapshots fingerprints every active project. A project whose dir cannot
// be listed is simply absent this tick (and present again when it can be),
// which the loop reports as a change - the honest answer.
func (h *handler) snapshots() map[string]snapshot {
	out := map[string]snapshot{}
	slugs, err := h.store.ListActiveProjects()
	if err != nil {
		return out
	}
	for _, slug := range slugs {
		dir := h.store.ProjectDir(slug)
		out[slug] = snapshot{
			tasks: dirShape(dir, func(name string) bool {
				return strings.HasSuffix(name, ".md") || name == "project.yaml"
			}),
			runs: dirShape(filepath.Join(dir, ".executor"), func(name string) bool {
				// Run-states, acceptance states and claims all live here;
				// the journal is append-only history nobody polls for.
				return strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".claim")
			}),
		}
	}
	return out
}

// dirShape folds the names, sizes and mtimes of the matching entries of dir
// into one string. A missing dir is the empty shape, so a project without an
// .executor dir yet is not an error - and its first run is a change.
func dirShape(dir string, keep func(name string) bool) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !keep(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d;", e.Name(), info.Size(), info.ModTime().UnixNano())
	}
	return b.String()
}
