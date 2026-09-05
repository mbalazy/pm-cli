package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mbalazy/pm/internal/feed"
	"github.com/mbalazy/pm/internal/storage"
)

// The change feed's HTTP face and its scheduler.
//
// /api/changes reads the CACHE the feed keeps on disk - never the network.
// POST /api/changes/refresh runs the sources now, whatever the clock says
// (a person asked). The scheduler runs them on its own every
// cockpit.refresh.every, but ONLY inside cockpit.refresh.window: outside
// it nothing pm does reaches git or gh unasked. Both paths end in one SSE
// `changes` event, and the client answers by refetching /api/changes and
// /api/attention.
//
// The clock is injected (Options.Clock) so the window rule is tested at
// 06:59 and 07:00 without waiting for either.

// refreshTimeout bounds one whole refresh (every source, every project).
// Each command has its own cap in the feed; this is the outer wall.
const refreshTimeout = 5 * time.Minute

// changeBus fans the "the feed changed" signal out to every open SSE
// stream. A slow or gone subscriber never blocks a publisher: the send is
// non-blocking on a buffered channel, and a client that missed one signal
// refetches on the next.
type changeBus struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func newChangeBus() *changeBus { return &changeBus{subs: map[chan string]struct{}{}} }

func (b *changeBus) subscribe() (<-chan string, func()) {
	ch := make(chan string, 8)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

func (b *changeBus) publish(data string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- data:
		default:
		}
	}
}

// changesEventData is the SSE payload: which sources ran.
func changesEventData(sources []feed.SourceStatus) string {
	names := make([]string, 0, len(sources))
	for _, s := range sources {
		if s.Enabled {
			names = append(names, s.Name)
		}
	}
	data, _ := json.Marshal(map[string]any{"sources": names})
	return string(data)
}

// cockpitConfig loads the cockpit block; the feed's cutoff and window come
// from it on every call so an edited config takes effect without a restart.
func (h *handler) cockpitConfig() (*storage.CockpitConfig, error) {
	cfg, err := h.store.LoadConfig()
	if err != nil {
		return nil, err
	}
	return &cfg.Cockpit, nil
}

// changes answers GET /api/changes from the cache.
func (h *handler) changes(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.cockpitConfig()
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	res, err := h.feed.Read(feed.Cutoff(cfg, h.clock()))
	writeResult(w, res, err)
}

// refresh answers POST /api/changes/refresh: run the sources now.
func (h *handler) refresh(w http.ResponseWriter, r *http.Request) {
	res, err := h.runRefresh(r.Context())
	writeResult(w, res, err)
}

// runRefresh is the one refresh path, shared by the endpoint and the
// scheduler: load the config, fetch since the cutoff, publish the signal.
func (h *handler) runRefresh(ctx context.Context) (*feed.Result, error) {
	cfg, err := h.cockpitConfig()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	now := h.clock()
	res, err := h.feed.Refresh(ctx, h.store, cfg, feed.Cutoff(cfg, now), now)
	if err != nil {
		return nil, err
	}
	h.bus.publish(changesEventData(res.Sources))
	return res, nil
}

// seenRequest is POST /api/changes/seen's optional body.
type seenRequest struct {
	// TS marks everything up to this stamp as seen; empty = now.
	TS string `json:"ts"`
}

type seenResult struct {
	Seen string `json:"seen"`
}

// seen answers POST /api/changes/seen.
func (h *handler) seen(w http.ResponseWriter, r *http.Request) {
	at := h.clock()
	if r.Body != nil && r.ContentLength != 0 {
		var req seenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "body must be JSON {\"ts\": \"<RFC3339>\"}: "+err.Error())
			return
		}
		if strings.TrimSpace(req.TS) != "" {
			ts, ok := storage.ParseStamp(req.TS)
			if !ok {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("ts %q is not RFC3339", req.TS))
				return
			}
			at = ts
		}
	}
	if err := h.feed.MarkSeen(at); err != nil {
		writeResult(w, nil, err)
		return
	}
	h.bus.publish(`{"sources":[]}`)
	writeJSON(w, http.StatusOK, seenResult{Seen: at.Format(time.RFC3339)})
}

// --- scheduler ---

// RunScheduler refreshes the feed every cockpit.refresh.every while the
// clock is inside cockpit.refresh.window, until ctx ends. Blocking; run it
// on its own goroutine. The interval is re-read from the config on every
// tick, so an edit takes effect on the next one; an interval of zero
// disables the automatic refresh entirely (a manual one still works).
func (s *Handler) RunScheduler(ctx context.Context) {
	h := s.h
	for {
		every := defaultRefreshEvery
		if cfg, err := h.cockpitConfig(); err == nil && cfg.Refresh.Every.Duration() >= 0 {
			every = cfg.Refresh.Every.Duration()
		}
		if every == 0 {
			// Disabled: wake up now and then to see whether that changed.
			every = time.Minute
			timer := time.NewTimer(every)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		timer := time.NewTimer(every)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		h.schedulerTick(ctx)
	}
}

const defaultRefreshEvery = 30 * time.Minute

// schedulerTick is one decision: inside the window, refresh; outside, do
// nothing. Returns whether a refresh ran. Errors are reported nowhere but
// the feed's own source statuses - a scheduler has nobody to tell.
func (h *handler) schedulerTick(ctx context.Context) bool {
	cfg, err := h.cockpitConfig()
	if err != nil {
		return false
	}
	if !cfg.Refresh.InWindow(h.clock()) {
		return false
	}
	_, err = h.runRefresh(ctx)
	return err == nil
}
