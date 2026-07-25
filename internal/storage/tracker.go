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
type Tracker struct {
	ID       string         `json:"id"`
	Title    string         `json:"title"`
	Status   string         `json:"status"`
	Brief    string         `json:"brief,omitempty"`
	Total    int            `json:"total"`
	Progress map[string]int `json:"progress"`
	Children []TrackerChild `json:"children"`
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

		trackers = append(trackers, Tracker{
			ID:       t.Meta.ID,
			Title:    t.Meta.Title,
			Status:   string(t.Meta.Status),
			Brief:    t.Meta.Brief,
			Total:    len(kids),
			Progress: progress,
			Children: children,
		})
	}
	sort.Slice(trackers, func(i, j int) bool { return trackers[i].ID < trackers[j].ID })
	return trackers, suppressed
}
