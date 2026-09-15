package mcpserver

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/service"
	"github.com/mbalazy/pm/internal/storage"
)

// seedTimeline appends an entry dated daysAgo (plus minutes, to order entries
// within a day) straight through storage. Days are whole 24-hour spans, the
// unit the stale check counts in, so a daylight-saving change inside the span
// cannot turn 8 days into 7.
func seedTimeline(t *testing.T, store *storage.Store, kind, text string, daysAgo, minutes int) {
	t.Helper()
	ts := daysBack(daysAgo).Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339)
	if err := storage.AppendTimelineEntry(store.ProjectDir("test"), &storage.TimelineEntry{Kind: kind, Text: text, TS: ts}); err != nil {
		t.Fatalf("seed %q: %v", text, err)
	}
}

func daysBack(days int) time.Time {
	return time.Now().Add(-time.Duration(days) * 24 * time.Hour)
}

func timelineEntries(t *testing.T, store *storage.Store) []storage.TimelineEntry {
	t.Helper()
	entries, err := storage.ReadTimeline(store.ProjectDir("test"))
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestE2ETimelineAdd(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)

	text, isErr := call(t, sess, "pm_timeline_add", map[string]any{
		"project": "test", "kind": "event", "text": "the client moved the deadline to October",
		"refs": []string{"t-1"}, "session": "sess-1",
	})
	if isErr {
		t.Fatalf("timeline_add error: %s", text)
	}
	var added service.TimelineAddResult
	mustUnmarshal(t, text, &added)
	if added.Project != "test" || added.Entry.ID == "" || added.Entry.TS == "" || added.Stale {
		t.Fatalf("add result: %+v", added)
	}
	if !strings.Contains(added.Note, "no state yet") {
		t.Fatalf("an event on a timeline with no state should ask for one: %q", added.Note)
	}
	entries := timelineEntries(t, store)
	if len(entries) != 1 || entries[0].ID != added.Entry.ID || entries[0].Session != "sess-1" || entries[0].Refs[0] != "t-1" {
		t.Fatalf("entries on disk: %+v", entries)
	}

	for _, tt := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"unknown kind", map[string]any{"project": "test", "kind": "note", "text": "x"}, "invalid timeline kind"},
		{"empty text", map[string]any{"project": "test", "kind": "event", "text": "  "}, "text is required"},
		{"unknown project", map[string]any{"project": "nope", "kind": "event", "text": "x"}, "nope"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text, isErr := call(t, sess, "pm_timeline_add", tt.args)
			if !isErr || !strings.Contains(text, tt.want) {
				t.Fatalf("want a tool error containing %q, got isErr=%v %s", tt.want, isErr, text)
			}
			if got := timelineEntries(t, store); len(got) != 1 {
				t.Fatalf("a rejected add wrote an entry: %+v", got)
			}
		})
	}
}

func TestE2ETimelineAddReportsStale(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	seedTimeline(t, store, storage.TimelineState, "where we stood", 8, 0)
	sess := startMCP(t, store)

	text, isErr := call(t, sess, "pm_timeline_add", map[string]any{"project": "test", "kind": "decision", "text": "one product on two rails"})
	if isErr {
		t.Fatalf("timeline_add error: %s", text)
	}
	var added service.TimelineAddResult
	mustUnmarshal(t, text, &added)
	if !added.Stale || added.EntriesSince != 1 || added.DaysSince != 8 || !strings.HasPrefix(added.Note, "stale: ") {
		t.Fatalf("stale signal missing from the add result: %+v", added)
	}
}

func TestE2ETimelineList(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	seedTimeline(t, store, storage.TimelineState, "S1", 6, 0)
	seedTimeline(t, store, storage.TimelineEvent, "E1", 5, 0)
	seedTimeline(t, store, storage.TimelineState, "S2", 4, 0)
	seedTimeline(t, store, storage.TimelineEvent, "E2", 3, 0)
	seedTimeline(t, store, storage.TimelineDecision, "D1", 2, 0)
	seedTimeline(t, store, storage.TimelineEvent, "E3", 1, 0)
	sess := startMCP(t, store)

	texts := func(es []storage.TimelineEntry) string {
		var out []string
		for _, e := range es {
			out = append(out, e.Text)
		}
		return strings.Join(out, ",")
	}

	t.Run("default read", func(t *testing.T) {
		text, isErr := call(t, sess, "pm_timeline_list", map[string]any{"project": "test"})
		if isErr {
			t.Fatalf("timeline_list error: %s", text)
		}
		var r service.TimelineReadResult
		mustUnmarshal(t, text, &r)
		if r.State == nil || r.State.Text != "S2" || texts(r.Since) != "E2,D1,E3" || r.Stale || r.Total != 6 {
			t.Fatalf("default read: %+v", r)
		}
		for _, key := range []string{`"state"`, `"since"`, `"stale"`} {
			if !strings.Contains(text, key) {
				t.Fatalf("default read JSON missing %s: %s", key, text)
			}
		}
	})

	t.Run("filtered", func(t *testing.T) {
		text, _ := call(t, sess, "pm_timeline_list", map[string]any{"project": "test", "since": time.Now().AddDate(0, 0, -7).Format("2006-01-02")})
		var r service.TimelineEntriesResult
		mustUnmarshal(t, text, &r)
		// The entries before the latest state are here, newest first.
		if texts(r.Entries) != "E3,D1,E2,S2,E1,S1" || r.Total != 6 || r.Shown != 6 {
			t.Fatalf("since: %+v", r)
		}
		text, _ = call(t, sess, "pm_timeline_list", map[string]any{"project": "test", "kind": "state"})
		mustUnmarshal(t, text, &r)
		if texts(r.Entries) != "S2,S1" || r.Total != 2 {
			t.Fatalf("kind: %+v", r)
		}
	})

	t.Run("limit and clamp", func(t *testing.T) {
		text, _ := call(t, sess, "pm_timeline_list", map[string]any{"project": "test", "limit": 2})
		var r service.TimelineEntriesResult
		mustUnmarshal(t, text, &r)
		if texts(r.Entries) != "E3,D1" || r.Total != 6 || r.Shown != 2 || !strings.Contains(r.Note, "2 of 6") {
			t.Fatalf("limit: %+v", r)
		}
		text, _ = call(t, sess, "pm_timeline_list", map[string]any{"project": "test", "limit": 10000})
		mustUnmarshal(t, text, &r)
		if r.Shown != 6 || !strings.Contains(r.Note, fmt.Sprintf("capped at %d", service.MaxTimelineLimit)) {
			t.Fatalf("clamp: %+v", r)
		}
	})

	t.Run("bad filters", func(t *testing.T) {
		for _, args := range []map[string]any{
			{"project": "test", "since": "15.09.2026"},
			{"project": "test", "kind": "note"},
			{"project": "nope"},
		} {
			if text, isErr := call(t, sess, "pm_timeline_list", args); !isErr {
				t.Fatalf("want an error for %v, got %s", args, text)
			}
		}
	})
}

func TestE2EContextTimelineBlock(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)

	crossProject := func() service.CrossProjectContextResult {
		t.Helper()
		text, isErr := call(t, sess, "pm_context", map[string]any{})
		if isErr {
			t.Fatalf("pm_context error: %s", text)
		}
		var c service.CrossProjectContextResult
		mustUnmarshal(t, text, &c)
		if len(c.Projects) != 1 {
			t.Fatalf("projects: %+v", c.Projects)
		}
		return c
	}
	projectContext := func() (string, service.ProjectContextResult) {
		t.Helper()
		text, isErr := call(t, sess, "pm_context", map[string]any{"project": "test"})
		if isErr {
			t.Fatalf("pm_context error: %s", text)
		}
		var c service.ProjectContextResult
		mustUnmarshal(t, text, &c)
		return text, c
	}

	t.Run("absent with no timeline", func(t *testing.T) {
		text, _ := projectContext()
		if strings.Contains(text, `"timeline`) {
			t.Fatalf("timeline key present with no timeline:\n%s", text)
		}
		if crossProject().Projects[0].TimelineState != "" {
			t.Fatal("timeline_state present with no timeline")
		}
	})

	t.Run("events only: block without a state, no cross-project pointer", func(t *testing.T) {
		seedTimeline(t, store, storage.TimelineEvent, "kickoff with the client", 9, 0)
		_, c := projectContext()
		if c.Timeline == nil || c.Timeline.State != nil || len(c.Timeline.Since) != 1 || !strings.Contains(c.Timeline.Note, "no state yet") {
			t.Fatalf("block: %+v", c.Timeline)
		}
		if crossProject().Projects[0].TimelineState != "" {
			t.Fatal("timeline_state present with no state")
		}
	})

	t.Run("state whole, entries capped, stale", func(t *testing.T) {
		var lines []string
		for i := 1; i <= 15; i++ {
			lines = append(lines, fmt.Sprintf("line %02d of the state", i))
		}
		state := strings.Join(lines, "\n")
		seedTimeline(t, store, storage.TimelineState, state, 8, 0)
		for i := 1; i <= 25; i++ {
			seedTimeline(t, store, storage.TimelineEvent, fmt.Sprintf("event %02d", i), 7, i)
		}

		_, c := projectContext()
		tl := c.Timeline
		if tl == nil || tl.State == nil || tl.State.Text != state {
			t.Fatalf("state not whole: %+v", tl)
		}
		if len(tl.Since) != service.ContextTimelineLimit || tl.SinceOmitted != 5 || tl.EntriesSince != 25 {
			t.Fatalf("since: %d shown, %d omitted, %d after the state", len(tl.Since), tl.SinceOmitted, tl.EntriesSince)
		}
		// The newest entries are kept, oldest first.
		if tl.Since[0].Text != "event 06" || tl.Since[19].Text != "event 25" {
			t.Fatalf("kept %q .. %q", tl.Since[0].Text, tl.Since[19].Text)
		}
		if !tl.Stale || !strings.Contains(tl.Note, "stale: 25 entries and 8 days") || !strings.Contains(tl.Note, "5 oldest entries after the state are omitted") {
			t.Fatalf("stale/note: %+v", tl)
		}

		ptr := crossProject().Projects[0].TimelineState
		wantDate := daysBack(8).Format("2006-01-02")
		if ptr != wantDate+": line 01 of the state (stale)" {
			t.Fatalf("timeline_state = %q", ptr)
		}
	})
}
