package server

import (
	"net/http"

	"github.com/mbalazy/pm-cli/internal/service"
)

// Dismiss/restore on the attention queue and the solo shifts (pm-cli-125,
// pm-cli-136) - thin: decode, call the service, answer.

func (h *handler) dismissRows(w http.ResponseWriter, r *http.Request) {
	var in service.DismissInput
	if !decodeBody(w, r, &in) {
		return
	}
	res, err := service.DismissRows(h.store, in, h.clock())
	writeResult(w, res, err)
}

func (h *handler) restoreRows(w http.ResponseWriter, r *http.Request) {
	var in service.RestoreInput
	if !decodeBody(w, r, &in) {
		return
	}
	res, err := service.RestoreRows(h.store, in)
	writeResult(w, res, err)
}

func (h *handler) soloReport(w http.ResponseWriter, r *http.Request) {
	res, err := service.SoloReport(h.store, r.PathValue("project"), r.PathValue("shift"))
	writeResult(w, res, err)
}
