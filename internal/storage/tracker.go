package storage

import (
	"sort"
	"strconv"
	"strings"
)

// TrackerChild is a subtask as seen in a parent's rollup.
type TrackerChild struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Order     int    `json:"order"`
	Branch    string `json:"branch,omitempty"`
	BriefLine string `json:"brief_line,omitempty"`
}

// taskIDNum extracts the trailing integer from a task ID (e.g. "atlas-26" -> 26).
// Returns 0 when the ID has no numeric suffix.
func taskIDNum(id string) int {
	if i := strings.LastIndex(id, "-"); i >= 0 {
		if n, err := strconv.Atoi(id[i+1:]); err == nil {
			return n
		}
	}
	return 0
}

// LessByOrder reports whether task a sorts before task b. It is the canonical
// subtask/task ordering shared by the board columns (filteredTasks), the parent
// rollup (BuildTrackers), and the TUI tracker child list (taskChildren) so the
// three never disagree: Order asc (0 = unset sorts first), then numeric ID asc,
// then most-recently-updated first.
func LessByOrder(a, b *Task) bool {
	if a.Meta.Order != b.Meta.Order {
		return a.Meta.Order < b.Meta.Order
	}
	na, nb := taskIDNum(a.Meta.ID), taskIDNum(b.Meta.ID)
	if na != nb {
		return na < nb
	}
	return a.Meta.Updated > b.Meta.Updated
}

// Tracker is a parent task plus a computed rollup of its children.
//
// The rollup is a SUMMARY, so every brief in it is compressed to one line - the
// parent's like the children's. The full brief is one pm_get_task away. This is
// load-bearing for the MCP side: pm_context is the session-start call, and full
// parent briefs alone were a third of a large project's payload (pm-cli-50).
type Tracker struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Status    string         `json:"status"`
	BriefLine string         `json:"brief_line,omitempty"`
	Total     int            `json:"total"`
	Progress  map[string]int `json:"progress"`
	Children  []TrackerChild `json:"children,omitempty"`
	// ChildrenOmitted marks a finished tracker whose child list was collapsed
	// to Progress/Total. See trackerFinished.
	ChildrenOmitted bool `json:"children_omitted,omitempty"`
}

// BriefLine returns the first non-empty line of a brief, stripped of bold
// markers and truncated to ~120 runes - a one-line "where it's at" for rollups.
func BriefLine(brief string) string {
	for _, ln := range strings.Split(brief, "\n") {
		ln = strings.TrimSpace(strings.ReplaceAll(ln, "**", ""))
		if ln == "" {
			continue
		}
		if r := []rune(ln); len(r) > 120 {
			return string(r[:120]) + "…"
		}
		return ln
	}
	return ""
}

// terminalStatus reports whether a task is closed for good. "merged" is a
// per-project status (the epic lifecycle todo -> doing -> merged -> done), so
// it is matched by name here the same way the board's progress badge does.
func terminalStatus(s TaskStatus) bool {
	switch s {
	case StatusDone, StatusArchived, "merged":
		return true
	}
	return false
}

// trackerFinished reports whether a tracker is done being watched: the parent
// itself is closed AND every child reached a terminal status.
func trackerFinished(parent *Task, kids []*Task) bool {
	if !terminalStatus(parent.Meta.Status) {
		return false
	}
	for _, k := range kids {
		if !terminalStatus(k.Meta.Status) {
			return false
		}
	}
	return true
}

// BuildTrackers groups tasks by parent. A task is a "tracker" iff at least one
// other task names it as parent AND that parent task actually exists among
// tasks and is not archived - only such parents emit a tracker block. Returns
// the rollup for every active tracker plus the set of task IDs that should be
// suppressed from flat doing lists (every child of an emitted tracker, plus
// every tracker itself - they belong under the tracker view). A child whose
// parent is archived or missing (an orphan) is never suppressed, so it falls
// back to the flat list like an ordinary task instead of disappearing.
func BuildTrackers(tasks []*Task) ([]Tracker, map[string]bool) {
	byID := make(map[string]*Task, len(tasks))
	for _, t := range tasks {
		byID[t.Meta.ID] = t
	}

	childrenByParent := make(map[string][]*Task)
	for _, t := range tasks {
		if p := t.Meta.Parent; p != "" {
			if parent, ok := byID[p]; ok && parent.Meta.Status != StatusArchived {
				childrenByParent[p] = append(childrenByParent[p], t)
			}
		}
	}

	suppressed := make(map[string]bool)
	for parentID, kids := range childrenByParent {
		suppressed[parentID] = true
		for _, k := range kids {
			suppressed[k.Meta.ID] = true
		}
	}

	var trackers []Tracker
	for _, t := range tasks {
		kids, ok := childrenByParent[t.Meta.ID]
		if !ok {
			continue
		}

		sort.Slice(kids, func(i, j int) bool { return LessByOrder(kids[i], kids[j]) })
		progress := make(map[string]int)
		children := make([]TrackerChild, 0, len(kids))
		for _, k := range kids {
			progress[string(k.Meta.Status)]++
			children = append(children, TrackerChild{
				ID:        k.Meta.ID,
				Title:     k.Meta.Title,
				Status:    string(k.Meta.Status),
				Order:     k.Meta.Order,
				Branch:    k.Meta.Branch,
				BriefLine: BriefLine(k.Meta.Brief),
			})
		}

		tr := Tracker{
			ID:        t.Meta.ID,
			Title:     t.Meta.Title,
			Status:    string(t.Meta.Status),
			BriefLine: BriefLine(t.Meta.Brief),
			Total:     len(kids),
			Progress:  progress,
			Children:  children,
		}
		// A finished tracker has nothing left to look at, so its children
		// collapse to the progress counts. Session context is re-read at the
		// start of every session while finished epics accumulate forever, which
		// makes their child lists the one part of the rollup that grows without
		// bound. Anything still open (e.g. a waiting child under a closed
		// parent) keeps the full list.
		if trackerFinished(t, kids) {
			tr.Children = nil
			tr.ChildrenOmitted = true
		}
		trackers = append(trackers, tr)
	}
	sort.Slice(trackers, func(i, j int) bool { return trackers[i].ID < trackers[j].ID })
	return trackers, suppressed
}
