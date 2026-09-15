package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func runTimelineCmd(t *testing.T, store storage.TaskStore, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := newTimelineCmd(store)
	var out strings.Builder
	cmd.SetArgs(append([]string{}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return out.String(), err
}

// timelineStore is a store with one project and no Claude session in the
// environment, so nothing stamps a session unless a test asks for it.
func timelineStore(t *testing.T) (*storage.Store, string) {
	t.Helper()
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	return tempStore(t)
}

func mustTimeline(t *testing.T, store storage.TaskStore, args ...string) string {
	t.Helper()
	out, err := runTimelineCmd(t, store, "", args...)
	if err != nil {
		t.Fatalf("pm timeline %v: %v\n%s", args, err, out)
	}
	return out
}

func daysAgo(n int) string {
	return time.Now().AddDate(0, 0, -n).Format("2006-01-02")
}

func TestTimelineDefaultReadCLI(t *testing.T) {
	store, slug := timelineStore(t)
	mustTimeline(t, store, "add", "-p", slug, "--kind", "state", "--text", "old state S1", "--date", daysAgo(3))
	mustTimeline(t, store, "add", "-p", slug, "--kind", "event", "--text", "before E1", "--date", daysAgo(2))
	mustTimeline(t, store, "add", "-p", slug, "--kind", "state", "--text", "new state S2\nsecond line", "--date", daysAgo(1), "--ref", "proj-7")
	out := mustTimeline(t, store, "add", "-p", slug, "--kind", "decision", "--text", "after D1", "--ref", "docs/x.md")
	if !strings.Contains(out, "proj: decision added (") {
		t.Fatalf("add output: %q", out)
	}

	t.Run("positional project", func(t *testing.T) {
		out := mustTimeline(t, store, slug)
		for _, want := range []string{
			"# timeline: proj",
			"## state " + daysAgo(1),
			"new state S2\nsecond line\nrefs: proj-7",
			"## since the state (1)",
			"decision  after D1",
			"refs: docs/x.md",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q:\n%s", want, out)
			}
		}
		for _, absent := range []string{"old state S1", "before E1", "stale:"} {
			if strings.Contains(out, absent) {
				t.Fatalf("%q must not be in the default read:\n%s", absent, out)
			}
		}
	})

	t.Run("project flag and json", func(t *testing.T) {
		out := mustTimeline(t, store, "--project", slug, "--json")
		var r storage.TimelineRead
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatalf("not a TimelineRead: %v\n%s", err, out)
		}
		if r.State == nil || r.State.Text != "new state S2\nsecond line" || len(r.Since) != 1 || r.Since[0].Text != "after D1" || r.Total != 4 {
			t.Fatalf("read = %+v", r)
		}
	})

	t.Run("unknown project", func(t *testing.T) {
		if _, err := runTimelineCmd(t, store, "", "nope"); err == nil {
			t.Fatal("expected an error for an unknown project")
		}
	})
}

func TestTimelineStaleLineCLI(t *testing.T) {
	store, slug := timelineStore(t)
	mustTimeline(t, store, "add", "-p", slug, "--kind", "state", "--text", "S", "--date", daysAgo(8))
	out := mustTimeline(t, store, slug)
	if !strings.Contains(out, "stale: 0 entries and 8 days since the state") {
		t.Fatalf("stale line missing:\n%s", out)
	}
	// Adding to a stale timeline says so at the moment of writing.
	out = mustTimeline(t, store, "add", "-p", slug, "--kind", "event", "--text", "E")
	if !strings.Contains(out, "stale: 1 entry and 8 days") {
		t.Fatalf("add on a stale timeline: %q", out)
	}
}

func TestTimelineNoStateCLI(t *testing.T) {
	store, slug := timelineStore(t)
	if out := mustTimeline(t, store, slug); !strings.Contains(out, "no timeline entries yet") {
		t.Fatalf("empty timeline: %q", out)
	}
	if _, err := os.Stat(storage.TimelineDir(store.ProjectDir(slug))); !os.IsNotExist(err) {
		t.Fatalf("a read created the timeline dir: %v", err)
	}
	for i := 1; i <= 12; i++ {
		mustTimeline(t, store, "add", "-p", slug, "--kind", "event", "--text", fmt.Sprintf("event %02d", i), "--date", daysAgo(13-i))
	}
	out := mustTimeline(t, store, slug)
	if !strings.Contains(out, "no state yet - the newest 10 of 12 entries") {
		t.Fatalf("no-state note missing:\n%s", out)
	}
	if strings.Contains(out, "event 01") || strings.Contains(out, "event 02") || !strings.Contains(out, "event 12") {
		t.Fatalf("want the newest 10:\n%s", out)
	}
}

func TestTimelineAddCLI(t *testing.T) {
	t.Run("multi-line text from stdin", func(t *testing.T) {
		store, slug := timelineStore(t)
		if out, err := runTimelineCmd(t, store, "where we stand\nwhat blocks\n", "add", "-p", slug, "--kind", "state", "--text", "-"); err != nil {
			t.Fatalf("add: %v\n%s", err, out)
		}
		entries, err := storage.ReadTimeline(store.ProjectDir(slug))
		if err != nil || len(entries) != 1 || entries[0].Text != "where we stand\nwhat blocks" {
			t.Fatalf("entries = %+v, err %v", entries, err)
		}
	})

	t.Run("refs stored verbatim", func(t *testing.T) {
		store, slug := timelineStore(t)
		mustTimeline(t, store, "add", "-p", slug, "--kind", "event", "--text", "x",
			"--ref", "https://x.test/a?ids=1,2", "--ref", `say "hi"`)
		entries, err := storage.ReadTimeline(store.ProjectDir(slug))
		if err != nil || len(entries) != 1 {
			t.Fatalf("entries = %+v, err %v", entries, err)
		}
		if refs := entries[0].Refs; len(refs) != 2 || refs[0] != "https://x.test/a?ids=1,2" || refs[1] != `say "hi"` {
			t.Fatalf("refs = %q", refs)
		}
	})

	t.Run("rejected without writing", func(t *testing.T) {
		store, slug := timelineStore(t)
		for _, args := range [][]string{
			{"add", "-p", slug, "--kind", "note", "--text", "x"},
			{"add", "-p", slug, "--kind", "event"},
			{"add", "-p", slug, "--kind", "event", "--text", "x", "--date", "15.09.2026"},
		} {
			if _, err := runTimelineCmd(t, store, "", args...); err == nil {
				t.Fatalf("expected an error for %v", args)
			}
		}
		if _, err := os.Stat(storage.TimelineDir(store.ProjectDir(slug))); !os.IsNotExist(err) {
			t.Fatalf("a rejected add created the timeline dir: %v", err)
		}
	})

	t.Run("session", func(t *testing.T) {
		store, slug := timelineStore(t)
		mustTimeline(t, store, "add", "-p", slug, "--kind", "event", "--text", "outside claude")
		t.Setenv("CLAUDECODE", "1")
		t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-auto")
		mustTimeline(t, store, "add", "-p", slug, "--kind", "event", "--text", "inside claude")
		mustTimeline(t, store, "add", "-p", slug, "--kind", "event", "--text", "explicit", "--session", "sess-flag")
		entries, err := storage.ReadTimeline(store.ProjectDir(slug))
		if err != nil || len(entries) != 3 {
			t.Fatalf("entries = %+v, err %v", entries, err)
		}
		got := map[string]string{}
		for _, e := range entries {
			got[e.Text] = e.Session
		}
		if got["outside claude"] != "" || got["inside claude"] != "sess-auto" || got["explicit"] != "sess-flag" {
			t.Fatalf("sessions = %v", got)
		}
	})
}

func TestTimelineListCLI(t *testing.T) {
	store, slug := timelineStore(t)
	mustTimeline(t, store, "add", "-p", slug, "--kind", "state", "--text", "S old", "--date", daysAgo(10))
	mustTimeline(t, store, "add", "-p", slug, "--kind", "event", "--text", "E old", "--date", daysAgo(9))
	mustTimeline(t, store, "add", "-p", slug, "--kind", "decision", "--text", "D new", "--date", daysAgo(2))
	mustTimeline(t, store, "add", "-p", slug, "--kind", "event", "--text", "E new", "--date", daysAgo(1))

	out := mustTimeline(t, store, "list", "-p", slug)
	if !strings.Contains(out, "(4 of 4 entries, newest first)") ||
		strings.Index(out, "E new") > strings.Index(out, "D new") ||
		strings.Index(out, "E old") > strings.Index(out, "S old") {
		t.Fatalf("list order:\n%s", out)
	}

	out = mustTimeline(t, store, "list", "-p", slug, "--since", daysAgo(5))
	if strings.Contains(out, "old") || !strings.Contains(out, "D new") || !strings.Contains(out, "(2 of 4") {
		t.Fatalf("--since:\n%s", out)
	}

	out = mustTimeline(t, store, "list", "-p", slug, "--kind", "event", "--json")
	var got struct {
		Entries []storage.TimelineEntry `json:"entries"`
		Matched int                     `json:"matched"`
		Total   int                     `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if got.Matched != 2 || got.Total != 4 || got.Entries[0].Text != "E new" || got.Entries[1].Text != "E old" {
		t.Fatalf("--kind event --json = %+v", got)
	}

	for _, args := range [][]string{
		{"list", "-p", slug, "--kind", "note"},
		{"list", "-p", slug, "--since", "yesterday"},
	} {
		if _, err := runTimelineCmd(t, store, "", args...); err == nil {
			t.Fatalf("expected an error for %v", args)
		}
	}
}
