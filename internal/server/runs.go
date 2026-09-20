package server

import (
	"errors"
	"net/http"

	"github.com/mbalazy/pm-cli/internal/runctl"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// Run control over HTTP (pm-cli-118-21): the attention row's run actions -
// claim, release_claim, rerun_finish, resume_run, kill - as
// POST /api/runs/{project}/{id}/{action}, each behind the client header and
// a confirmation dialog on the other end whose preview comes from
// GET /api/runs/{project}/{id}/plan. Everything a request does is
// internal/runctl's (the board's rules, re-stated once for a non-TUI
// caller); this file only maps the vocabulary onto HTTP.
//
// The two conflict states are 409: somebody else holds the claim (the body
// names the holder), and a kill whose run-state's pid is gone or recycled
// (nothing was signalled - runctl says so instead of pretending).

// runPlan answers GET /api/runs/{project}/{id}/plan?action=&yolo=1&additional=1
// with what the action would do: argv, cwd, log, warnings, the claim
// session, the kill target. Reads local files only.
func (h *handler) runPlan(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	action, ok := runctl.ParseAction(q.Get("action"))
	if !ok {
		writeError(w, http.StatusBadRequest, "action must be one of claim, release_claim, rerun_finish, resume_run, kill; got "+q.Get("action"))
		return
	}
	flags := runctl.Flags{Yolo: q.Get("yolo") == "1" || q.Get("yolo") == "true", Additional: q.Get("additional") == "1" || q.Get("additional") == "true"}
	p, err := h.runs.Plan(r.PathValue("project"), r.PathValue("id"), action, flags)
	if err != nil {
		writeError(w, runStatusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// runAction answers POST /api/runs/{project}/{id}/{action} with a JSON body
// of runctl.Flags (optional). A spawn answers runctl.Started, a claim or a
// release runctl.ClaimResult, a kill runctl.KillResult.
func (h *handler) runAction(w http.ResponseWriter, r *http.Request) {
	action, ok := runctl.ParseAction(r.PathValue("action"))
	if !ok {
		writeError(w, http.StatusBadRequest, "no such run action: "+r.PathValue("action"))
		return
	}
	var flags runctl.Flags
	if r.ContentLength != 0 {
		if !decodeBody(w, r, &flags) {
			return
		}
	}
	project, id := r.PathValue("project"), r.PathValue("id")
	var (
		res any
		err error
	)
	switch action {
	case runctl.ActionClaim:
		res, err = h.runs.Claim(project, id)
	case runctl.ActionReleaseClaim:
		res, err = h.runs.Release(project, id)
	case runctl.ActionKill:
		res, err = h.runs.Kill(project, id)
	default: // rerun_finish, resume_run
		var p *runctl.Plan
		p, err = h.runs.Plan(project, id, action, flags)
		if err == nil {
			res, err = h.runs.Spawn(p)
		}
	}
	if err != nil {
		writeError(w, runStatusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// runStatusFor maps runctl's errors: a missing project/task 404, a
// caller's mistake 400, a claim held by somebody else or a run that is
// already gone 409, anything else 500.
func runStatusFor(err error) int {
	var (
		notFound *runctl.NotFoundError
		input    *runctl.InputError
		busy     *runctl.BusyError
		stale    *storage.StaleRunError
		noRun    *runctl.NoRunError
	)
	switch {
	case errors.As(err, &notFound):
		return http.StatusNotFound
	case errors.As(err, &input):
		return http.StatusBadRequest
	case errors.As(err, &busy), errors.As(err, &stale), errors.As(err, &noRun):
		return http.StatusConflict
	}
	return statusFor(err)
}
