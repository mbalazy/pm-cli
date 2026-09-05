package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/mbalazy/pm/internal/feed"
	"github.com/mbalazy/pm/internal/report"
	"github.com/mbalazy/pm/internal/storage"
)

// The LLM report over the feed (pm-cli-118-20). GET /api/report is the
// period's report off disk (or its absence, or "writing"); POST /api/report
// writes one NOW - only when cockpit.sources.report is on, else 409 and
// claude is never started; POST /api/report/dismiss {id} hides a
// suggestion. A report is also written on its own ONCE per period, after
// the first successful feed refresh of that period, when the switch is on.
// Either way the write runs in the background and ends in the SSE
// `report` event; a client refetches /api/report on it.

// reportState is GET /api/report's shape.
type reportState struct {
	// Enabled is cockpit.sources.report.
	Enabled bool   `json:"enabled"`
	Period  string `json:"period"`
	Cutoff  string `json:"cutoff"`
	// State: off (disabled, nothing stored), none (enabled, not written
	// yet), writing, done, error.
	State  string         `json:"state"`
	Model  string         `json:"model"`
	Report *report.Report `json:"report,omitempty"`
}

type reportControl struct {
	mu      sync.Mutex
	writing map[string]bool
}

// reportContext is what one report needs from the server: config, cutoff,
// period, the store paths.
func (h *handler) reportContext() (*storage.CockpitConfig, time.Time, string, error) {
	cfg, err := h.cockpitConfig()
	if err != nil {
		return nil, time.Time{}, "", err
	}
	cutoff := feed.Cutoff(cfg, h.clock())
	return cfg, cutoff, report.PeriodKey(cutoff), nil
}

func (h *handler) reportStore() report.Store { return report.Store{Root: h.store.RootDir()} }

// getReport answers GET /api/report.
func (h *handler) getReport(w http.ResponseWriter, r *http.Request) {
	cfg, cutoff, period, err := h.reportContext()
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	st := reportState{Enabled: cfg.SourceEnabled("report"), Period: period, Cutoff: cutoff.Format(time.RFC3339), Model: cfg.Report.Model}
	rep, err := h.reportStore().Read(period)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	h.reports.mu.Lock()
	writing := h.reports.writing[period]
	h.reports.mu.Unlock()
	switch {
	case writing:
		st.State = "writing"
	case rep != nil && rep.Error != "":
		st.State, st.Report = "error", rep
	case rep != nil:
		st.State, st.Report = "done", rep
	case !st.Enabled:
		st.State = "off"
	default:
		st.State = "none"
	}
	writeJSON(w, http.StatusOK, st)
}

// writeReport answers POST /api/report: start writing this period's report
// now. 409 when the switch is off (claude is never run for a disabled
// report - the one hard rule of this feature) and when a write is already
// in flight. Answers at once with state "writing"; the SSE `report` event
// says when it is done.
func (h *handler) writeReport(w http.ResponseWriter, r *http.Request) {
	cfg, cutoff, period, err := h.reportContext()
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	if !cfg.SourceEnabled("report") {
		writeError(w, http.StatusConflict, "the report is off (Settings › Sources › report) - nothing was started")
		return
	}
	if !h.startReport(cfg, cutoff, period) {
		writeError(w, http.StatusConflict, "a report for "+period+" is already being written")
		return
	}
	writeJSON(w, http.StatusAccepted, reportState{Enabled: true, Period: period, Cutoff: cutoff.Format(time.RFC3339), State: "writing", Model: cfg.Report.Model})
}

// startReport launches the background write unless one is in flight for
// the period. Returns whether it started.
func (h *handler) startReport(cfg *storage.CockpitConfig, cutoff time.Time, period string) bool {
	h.reports.mu.Lock()
	if h.reports.writing[period] {
		h.reports.mu.Unlock()
		return false
	}
	h.reports.writing[period] = true
	h.reports.mu.Unlock()
	model := cfg.Report.Model
	lang := cfg.Report.Language
	go func() {
		defer func() {
			h.reports.mu.Lock()
			delete(h.reports.writing, period)
			h.reports.mu.Unlock()
			h.bus.publish("report", `{"period":`+quote(period)+`}`)
		}()
		in := report.Input{Cutoff: cutoff, Now: h.clock(), Language: lang}
		if ch, err := h.feed.Read(cutoff); err == nil {
			in.Events = ch.Events
		}
		if a, err := storage.BuildAttention(h.store, cfg, storage.AttentionOptions{Now: h.clock()}); err == nil {
			in.Attention = a
		}
		if groups, err := h.store.ProjectGroups(); err == nil {
			in.Groups = groups
		}
		// A failure is stored by Write itself (as the period's error), so
		// nothing to do with the error here - GET shows it.
		_, _ = h.opts.Report.Write(context.Background(), h.reportStore(), in, model)
	}()
	return true
}

// autoReport is the once-per-period rule, called after a successful feed
// refresh: with the switch on and no report (done or failed) for the
// period yet, write one. Never runs when the switch is off.
func (h *handler) autoReport(cfg *storage.CockpitConfig, cutoff time.Time) {
	if cfg == nil || !cfg.SourceEnabled("report") {
		return
	}
	period := report.PeriodKey(cutoff)
	if rep, err := h.reportStore().Read(period); err != nil || rep != nil {
		return
	}
	h.startReport(cfg, cutoff, period)
}

// dismissRequest is POST /api/report/dismiss's body.
type dismissRequest struct {
	ID string `json:"id"`
}

// dismissSuggestion answers POST /api/report/dismiss {id}.
func (h *handler) dismissSuggestion(w http.ResponseWriter, r *http.Request) {
	var req dismissRequest
	if !decodeBody(w, r, &req) {
		return
	}
	_, _, period, err := h.reportContext()
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	rep, err := h.reportStore().Dismiss(period, req.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.bus.publish("report", `{"period":`+quote(period)+`}`)
	writeJSON(w, http.StatusOK, rep)
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
