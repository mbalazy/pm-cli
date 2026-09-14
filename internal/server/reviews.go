package server

import (
	"errors"
	"net/http"

	"github.com/mbalazy/pm/internal/review"
)

// PR code reviews from the cockpit: POST a GitHub PR URL, pm runs
// `claude -p "/review <url>"` in the matching project's checkout (see
// internal/review) and serves the report.

func writeReviewError(w http.ResponseWriter, err error) {
	var ie *review.InputError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, review.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *handler) listReviews(w http.ResponseWriter, r *http.Request) {
	rs, err := h.reviews.List()
	if err != nil {
		writeReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviews": rs})
}

func (h *handler) getReview(w http.ResponseWriter, r *http.Request) {
	rv, err := h.reviews.Get(r.PathValue("id"))
	if err != nil {
		writeReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rv)
}

func (h *handler) startReview(w http.ResponseWriter, r *http.Request) {
	// input = a PR URL, a Slack link or a sentence; url is the older name.
	var in struct {
		Input string `json:"input"`
		URL   string `json:"url"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Input == "" {
		in.Input = in.URL
	}
	rv, err := h.reviews.Start(r.Context(), in.Input)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, rv)
}

// approveReview approves the review's PR on GitHub (an approve, no comment)
// and reacts ✅ on the Slack message the request came from.
func (h *handler) approveReview(w http.ResponseWriter, r *http.Request) {
	rv, err := h.reviews.Approve(r.Context(), r.PathValue("id"))
	if err != nil {
		writeReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rv)
}

func (h *handler) cancelReview(w http.ResponseWriter, r *http.Request) {
	rv, err := h.reviews.Cancel(r.PathValue("id"))
	if err != nil {
		writeReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rv)
}
