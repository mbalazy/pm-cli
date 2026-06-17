package storage

import (
	"sort"
	"strings"
)

// TrackerChild is a subtask as seen in a parent's rollup.
type TrackerChild struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Branch    string `json:"branch,omitempty"`
	BriefLine string `json:"brief_line,omitempty"`
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
// other task names it as parent. Returns the rollup for every active tracker
// plus the set of task IDs that should be suppressed from flat doing lists
// (every child and every tracker - they belong under the tracker view).
func BuildTrackers(tasks []*Task) ([]Tracker, map[string]bool) {
	childrenByParent := make(map[string][]*Task)
	for _, t := range tasks {
		if p := t.Meta.Parent; p != "" {
			childrenByParent[p] = append(childrenByParent[p], t)
		}
	}

	suppressed := make(map[string]bool)
	for _, t := range tasks {
		if t.Meta.Parent != "" {
			suppressed[t.Meta.ID] = true
		}
	}

	var trackers []Tracker
	for _, t := range tasks {
		kids, ok := childrenByParent[t.Meta.ID]
		if !ok {
			continue
		}
		suppressed[t.Meta.ID] = true
		if t.Meta.Status == StatusArchived {
			continue
		}

		progress := make(map[string]int)
		children := make([]TrackerChild, 0, len(kids))
		for _, k := range kids {
			progress[string(k.Meta.Status)]++
			children = append(children, TrackerChild{
				ID:        k.Meta.ID,
				Title:     k.Meta.Title,
				Status:    string(k.Meta.Status),
				Branch:    k.Meta.Branch,
				BriefLine: BriefLine(k.Meta.Brief),
			})
		}
		sort.Slice(children, func(i, j int) bool { return children[i].ID < children[j].ID })

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
