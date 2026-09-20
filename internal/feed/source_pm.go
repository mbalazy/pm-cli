package feed

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// PMSource reads pm's own files - no network, no commands: status changes
// (status_changed), created tasks (created is a bare DATE, so "since 18:00"
// is approximated to the cutoff's day), executor journal lines
// (start/end/killed/crashed), acceptance run-states, and the focus plan.
type PMSource struct {
	// Root is the pm root, for focus.yaml; empty = the focus plan is skipped.
	Root string
}

func (s *PMSource) Name() string { return "pm" }

func (s *PMSource) Fetch(ctx context.Context, from, to time.Time, projects []Project) ([]Event, error) {
	var out []Event
	fromDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	for _, p := range projects {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		for _, t := range p.Tasks {
			if t.Meta.Status == storage.StatusArchived {
				continue
			}
			if ts, ok := inWindow(t.Meta.StatusChanged, from, to); ok {
				out = append(out, Event{
					ID: EventID("pm", "status", p.Slug, t.Meta.ID, t.Meta.StatusChanged), TS: ts.Format(time.RFC3339),
					Project: p.Slug, Group: p.Group, TaskID: t.Meta.ID, Title: t.Meta.Title,
					Detail: "moved to " + string(t.Meta.Status), Severity: statusSeverity(t.Meta.Status),
				})
			}
			// Created on the cutoff's day or later: created is date-only.
			if created, ok := storage.ParseStamp(t.Meta.Created); ok && !created.Before(fromDay) && created.Before(to) {
				// A task created AND moved in the window would otherwise be
				// two rows saying one thing; the status row wins.
				if _, moved := inWindow(t.Meta.StatusChanged, from, to); !moved {
					out = append(out, Event{
						ID: EventID("pm", "created", p.Slug, t.Meta.ID), TS: created.Format(time.RFC3339),
						Project: p.Slug, Group: p.Group, TaskID: t.Meta.ID, Title: t.Meta.Title,
						Detail: "created", Severity: SeverityInfo,
					})
				}
			}
		}
		out = append(out, s.journalEvents(p, from, to)...)
		out = append(out, s.acceptanceEvents(p, from, to)...)
	}
	if s.Root != "" {
		if fp, err := storage.ReadFocusPlan(s.Root); err == nil && fp.Date != "" {
			if day, ok := storage.ParseStamp(fp.Date); ok && !day.Before(fromDay) && day.Before(to) && len(fp.Tasks) > 0 {
				out = append(out, Event{
					ID: EventID("pm", "focus", fp.Date, strings.Join(fp.Tasks, ",")), TS: day.Format(time.RFC3339),
					Title:  fmt.Sprintf("focus plan for %s: %d task(s)", fp.Date, len(fp.Tasks)),
					Detail: strings.Join(fp.Tasks, ", "), Severity: SeverityInfo,
				})
			}
		}
	}
	return out, nil
}

func (s *PMSource) journalEvents(p Project, from, to time.Time) []Event {
	entries, err := storage.ReadJournal(p.Dir)
	if err != nil {
		return nil
	}
	var out []Event
	for _, e := range entries {
		ts, ok := inWindow(e.TS, from, to)
		if !ok {
			continue
		}
		ev := Event{
			ID: EventID("pm", "journal", p.Slug, e.Event, e.TaskID, e.RunID, e.TS), TS: ts.Format(time.RFC3339),
			Project: p.Slug, Group: p.Group, TaskID: e.TaskID, Title: taskTitle(p, e.TaskID), Severity: SeverityInfo,
		}
		switch e.Event {
		case "start":
			ev.Detail = e.Kind + " started"
		case "end":
			ev.Detail = fmt.Sprintf("%s ended: %s", e.Kind, e.Status)
			if e.Status == storage.RunStatusFailed {
				ev.Severity = SeverityCrit
			} else {
				ev.Severity = SeverityOK
			}
			if n := len(e.Subs); n > 0 {
				ev.Detail += fmt.Sprintf(" (%s)", subOutcomes(e.Subs))
			}
		case "killed":
			ev.Detail = e.Kind + " killed"
			ev.Severity = SeverityWarn
		case "crashed":
			ev.Detail = e.Kind + " crashed"
			ev.Severity = SeverityCrit
		default:
			ev.Detail = e.Kind + " " + e.Event
		}
		if e.Error != "" {
			ev.Detail += ": " + e.Error
		}
		out = append(out, ev)
	}
	return out
}

func (s *PMSource) acceptanceEvents(p Project, from, to time.Time) []Event {
	var out []Event
	for id, st := range storage.ReadFinishRunStates(p.Dir) {
		if st.Status == storage.RunStatusRunning {
			continue // the journal's start line covers a live one
		}
		ts, ok := inWindow(st.Updated, from, to)
		if !ok {
			continue
		}
		verdict := st.Status
		if len(st.Subs) > 0 && st.Subs[0].Status != "" {
			verdict = st.Subs[0].Status
		}
		sev := SeverityOK
		switch verdict {
		case storage.AcceptCellPartial, storage.AcceptCellBlocked:
			sev = SeverityWarn
		case storage.RunStatusFailed:
			sev = SeverityCrit
		}
		detail := "acceptance " + verdict
		if n := st.VisualClaimsOpen(); n > 0 {
			detail += fmt.Sprintf(", %d visual claim(s) open", n)
		}
		out = append(out, Event{
			ID: EventID("pm", "acceptance", p.Slug, id, st.Updated), TS: ts.Format(time.RFC3339),
			Project: p.Slug, Group: p.Group, TaskID: id, Title: taskTitle(p, id), Detail: detail, Severity: sev,
		})
	}
	return out
}

func taskTitle(p Project, id string) string {
	for _, t := range p.Tasks {
		if t.Meta.ID == id {
			return t.Meta.Title
		}
	}
	return id
}

func subOutcomes(subs []storage.JournalSub) string {
	counts := map[string]int{}
	order := []string{}
	for _, s := range subs {
		if counts[s.Result] == 0 {
			order = append(order, s.Result)
		}
		counts[s.Result]++
	}
	parts := make([]string, 0, len(order))
	for _, r := range order {
		parts = append(parts, fmt.Sprintf("%d %s", counts[r], r))
	}
	return strings.Join(parts, ", ")
}

func statusSeverity(s storage.TaskStatus) string {
	switch s {
	case storage.StatusWaiting:
		return SeverityWarn
	case storage.StatusDone, storage.StatusMerged, storage.StatusPushed:
		return SeverityOK
	}
	return SeverityInfo
}
