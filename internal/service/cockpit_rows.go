package service

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// Dismiss / restore on the attention queue and the solo shifts (pm-cli-125,
// pm-cli-136): the cockpit's own state about rows and another tool's notes,
// never a task mutation.

// DismissRow names one attention row the way the queue carries it.
type DismissRow struct {
	Section string `json:"section"`
	Project string `json:"project"`
	TaskID  string `json:"task_id,omitempty"`
	Shift   string `json:"shift,omitempty"`
	Since   string `json:"since,omitempty"`
}

// DismissInput is POST /api/attention/dismiss.
type DismissInput struct {
	Rows []DismissRow `json:"rows"`
}

// DismissResult answers a dismiss.
type DismissResult struct {
	Dismissed int `json:"dismissed"`
}

// RestoreInput is POST /api/attention/restore: every dismissed row of one
// section comes back (optionally only one project's).
type RestoreInput struct {
	Section string `json:"section"`
	Project string `json:"project,omitempty"`
}

// RestoreResult answers a restore.
type RestoreResult struct {
	Restored int `json:"restored"`
}

// DismissRows records rows as dismissed. A row outside the dismissable
// sections, or without a project or an id, is a *ValidationError and
// nothing is written.
func DismissRows(store storage.TaskStore, in DismissInput, now time.Time) (*DismissResult, error) {
	if len(in.Rows) == 0 {
		return nil, validation(fmt.Errorf("rows: at least one row is required"))
	}
	keys := make([]string, 0, len(in.Rows))
	for i, r := range in.Rows {
		if !storage.IsDismissable(r.Section) {
			return nil, validation(fmt.Errorf("rows[%d].section %q is not one of %s", i, r.Section, strings.Join(storage.DismissableSections, ", ")))
		}
		id := r.TaskID
		if r.Shift != "" {
			id = r.Shift
		}
		if r.Project == "" || id == "" {
			return nil, validation(fmt.Errorf("rows[%d]: project and task_id (or shift) are required", i))
		}
		keys = append(keys, storage.DismissKey(r.Section, r.Project, id, r.Since))
	}
	if err := storage.AddDismissed(store.RootDir(), keys, now); err != nil {
		return nil, err
	}
	return &DismissResult{Dismissed: len(keys)}, nil
}

// RestoreRows brings a section's dismissed rows back.
func RestoreRows(store storage.TaskStore, in RestoreInput) (*RestoreResult, error) {
	if !storage.IsDismissable(in.Section) {
		return nil, validation(fmt.Errorf("section %q is not one of %s", in.Section, strings.Join(storage.DismissableSections, ", ")))
	}
	prefix := in.Section + "|"
	if in.Project != "" {
		prefix += in.Project + "|"
	}
	n, err := storage.RemoveDismissed(store.RootDir(), func(k string) bool { return strings.HasPrefix(k, prefix) })
	if err != nil {
		return nil, err
	}
	return &RestoreResult{Restored: n}, nil
}

// SoloResult is GET /api/solo: every shift of every active project, newest first.
type SoloResult struct {
	Shifts []storage.Shift `json:"shifts"`
}

// SoloShifts lists the solo shifts across the active projects.
func SoloShifts(store storage.TaskStore) (*SoloResult, error) {
	slugs, err := store.ListActiveProjects()
	if err != nil {
		return nil, err
	}
	out := &SoloResult{Shifts: []storage.Shift{}}
	for _, slug := range slugs {
		if p, err := store.GetProject(slug); err == nil && p.Archived {
			continue
		}
		out.Shifts = append(out.Shifts, storage.ReadShifts(store.ProjectDir(slug), slug)...)
	}
	sort.SliceStable(out.Shifts, func(i, j int) bool {
		a, b := shiftSortKey(out.Shifts[i]), shiftSortKey(out.Shifts[j])
		return a > b
	})
	return out, nil
}

// shiftSortKey: open shifts first (they are happening now), then the
// closing stamp or the date, newest first.
func shiftSortKey(sh storage.Shift) string {
	if sh.Open {
		return "9" + sh.Date
	}
	if at, ok := storage.ParseStamp(sh.Closed); ok {
		return "1" + at.UTC().Format(time.RFC3339)
	}
	return "1" + sh.Date
}

// SoloReportResult is GET /api/solo/{project}/{shift}/report.
type SoloReportResult struct {
	Shift storage.Shift `json:"shift"`
	// Kind is "report" when the shift left one, else "state" (the shift's
	// own state file, for a shift still open or closed without a report).
	Kind     string `json:"kind"`
	Markdown string `json:"markdown"`
	// Digest is the report split into parts (storage.ParseShiftReport); only
	// for kind "report".
	Digest *storage.ShiftReport `json:"digest,omitempty"`
}

// SoloReport reads one shift's report. The shift id is looked up among the
// project's parsed shifts, never joined into a path, so an id cannot walk
// out of the .shift directory.
func SoloReport(store storage.TaskStore, project, shift string) (*SoloReportResult, error) {
	slug, err := store.ResolveProject(project)
	if err != nil {
		return nil, err
	}
	for _, sh := range storage.ReadShifts(store.ProjectDir(slug), slug) {
		if sh.ID != shift {
			continue
		}
		res := &SoloReportResult{Shift: sh, Kind: "state"}
		path := sh.File
		if sh.Report != "" {
			res.Kind, path = "report", sh.Report
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		res.Markdown = string(data)
		if res.Kind == "report" {
			res.Digest = storage.ParseShiftReport(res.Markdown)
		}
		return res, nil
	}
	return nil, fmt.Errorf("%w: no solo shift %q in %s", storage.ErrTaskNotFound, shift, slug)
}
