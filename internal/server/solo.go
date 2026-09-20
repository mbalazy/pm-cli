package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mbalazy/pm-cli/internal/service"
	"github.com/mbalazy/pm-cli/internal/solo"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// Solo sessions from the cockpit (pm-cli-141): GET /api/solo/plan previews
// the exact `claude --bg` launch of a /solo session (the dialog shows it),
// POST /api/solo runs it, POST /api/solo/{id}/stop ends it with
// `claude stop`. Everything a request does is internal/solo's; this file
// maps the vocabulary onto HTTP. GET /api/solo (cockpit_rows.go) answers
// the shifts the skill wrote AND the launches pm started, so a row exists
// the moment the button is pressed.

// soloResult is GET /api/solo: the service's shifts plus pm's launches.
type soloResult struct {
	*service.SoloResult
	Launches []solo.Launch `json:"launches"`
}

func writeSoloError(w http.ResponseWriter, err error) {
	var ie *solo.InputError
	var se *solo.StartError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, solo.ErrNotFound), errors.Is(err, storage.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &se):
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *handler) soloShifts(w http.ResponseWriter, r *http.Request) {
	res, err := service.SoloShifts(h.store)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	launches, err := h.solo.List()
	if err != nil {
		writeSoloError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, soloResult{SoloResult: res, Launches: launches})
}

// soloInputFromQuery reads the launch form off a query string (the plan is
// a GET so the dialog can re-fetch it as the form changes).
func soloInputFromQuery(r *http.Request) solo.Input {
	q := r.URL.Query()
	flag := func(k string) bool { v := q.Get(k); return v == "1" || v == "true" }
	num := func(k string) int { n, _ := strconv.Atoi(q.Get(k)); return n }
	return solo.Input{
		Project: q.Get("project"), Queue: q.Get("queue"), Runtime: q.Get("runtime"),
		Base: q.Get("base"), Model: q.Get("model"), Push: flag("push"), PR: flag("pr"),
		MaxTasks: num("max_tasks"), MaxHours: num("max_hours"),
	}
}

func (h *handler) soloPlan(w http.ResponseWriter, r *http.Request) {
	p, err := h.solo.Plan(soloInputFromQuery(r))
	if err != nil {
		writeSoloError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *handler) soloStart(w http.ResponseWriter, r *http.Request) {
	var in solo.Input
	if !decodeBody(w, r, &in) {
		return
	}
	p, err := h.solo.Plan(in)
	if err != nil {
		writeSoloError(w, err)
		return
	}
	l, err := h.solo.Start(p)
	if err != nil {
		writeSoloError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, l)
}

func (h *handler) soloLaunch(w http.ResponseWriter, r *http.Request) {
	l, err := h.solo.Get(r.PathValue("id"))
	if err != nil {
		writeSoloError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (h *handler) soloStop(w http.ResponseWriter, r *http.Request) {
	l, err := h.solo.Stop(r.PathValue("id"))
	if err != nil {
		writeSoloError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}
