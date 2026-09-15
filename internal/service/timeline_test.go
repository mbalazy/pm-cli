package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func seedServiceTimeline(t *testing.T, store *storage.Store, kind, text string, daysAgo, minutes int) {
	t.Helper()
	ts := time.Now().AddDate(0, 0, -daysAgo).Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339)
	if err := storage.AppendTimelineEntry(store.ProjectDir("test"), &storage.TimelineEntry{Kind: kind, Text: text, TS: ts}); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineAddService(t *testing.T) {
	store := newTestStore(t)

	var verr *ValidationError
	if _, err := TimelineAdd(store, TimelineAddInput{Project: "test", Kind: "note", Text: "x"}); !errors.As(err, &verr) {
		t.Fatalf("unknown kind: want a ValidationError, got %v", err)
	}
	if _, err := TimelineAdd(store, TimelineAddInput{Project: "test", Kind: "event", Text: "\n "}); !errors.As(err, &verr) {
		t.Fatalf("empty text: want a ValidationError, got %v", err)
	}
	if _, err := TimelineAdd(store, TimelineAddInput{Project: "nope", Kind: "event", Text: "x"}); !errors.Is(err, storage.ErrProjectNotFound) {
		t.Fatalf("unknown project: got %v", err)
	}

	notes := []struct {
		kind, want string
	}{
		{"event", "no state yet"},
		{"state", "state recorded"},
		{"decision", "1 entry since the state of " + storage.Today()},
	}
	for _, n := range notes {
		res, err := TimelineAdd(store, TimelineAddInput{Project: "test", Kind: " " + n.kind + " ", Text: n.kind + " text"})
		if err != nil {
			t.Fatalf("add %s: %v", n.kind, err)
		}
		if res.Entry.Kind != n.kind || !strings.Contains(res.Note, n.want) {
			t.Fatalf("add %s: kind %q, note %q (want %q)", n.kind, res.Entry.Kind, res.Note, n.want)
		}
	}
}

func TestTimelineListService(t *testing.T) {
	store := newTestStore(t)
	for i := 1; i <= 3; i++ {
		seedServiceTimeline(t, store, storage.TimelineEvent, fmt.Sprintf("E%d", i), 4-i, 0)
	}

	got, err := TimelineList(store, TimelineListInput{Project: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := got.(*TimelineReadResult); !ok || r.Project != "test" || r.State != nil || len(r.Since) != 3 {
		t.Fatalf("no filters must be the default read, got %#v", got)
	}

	// A negative limit is a filter with the default budget, not the default read.
	got, err = TimelineList(store, TimelineListInput{Project: "test", Limit: -1})
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := got.(*TimelineEntriesResult); !ok || r.Shown != 3 || r.Entries[0].Text != "E3" || r.Note != "" {
		t.Fatalf("limit -1: %#v", got)
	}

	if _, err := TimelineDefaultRead(store, "nope"); !errors.Is(err, storage.ErrProjectNotFound) {
		t.Fatalf("unknown project: %v", err)
	}
}

func TestContextTimeline(t *testing.T) {
	now := time.Now()

	t.Run("nil without entries", func(t *testing.T) {
		store := newTestStore(t)
		if c := contextTimeline(store.ProjectDir("test"), now); c != nil {
			t.Fatalf("block for an empty timeline: %+v", c)
		}
		if p := timelineStatePointer(store.ProjectDir("test"), now); p != "" {
			t.Fatalf("pointer for an empty timeline: %q", p)
		}
	})

	t.Run("no state: newest entries uncapped by the context budget", func(t *testing.T) {
		store := newTestStore(t)
		for i := 1; i <= 12; i++ {
			seedServiceTimeline(t, store, storage.TimelineEvent, fmt.Sprintf("E%02d", i), 20, i)
		}
		c := contextTimeline(store.ProjectDir("test"), now)
		if c == nil || c.State != nil || len(c.Since) != storage.TimelineNoStateLimit || c.SinceOmitted != 0 || c.Total != 12 {
			t.Fatalf("block: %+v", c)
		}
		if p := timelineStatePointer(store.ProjectDir("test"), now); p != "" {
			t.Fatalf("pointer without a state: %q", p)
		}
	})

	t.Run("at the cap nothing is omitted", func(t *testing.T) {
		store := newTestStore(t)
		seedServiceTimeline(t, store, storage.TimelineState, "S", 1, 0)
		for i := 1; i <= ContextTimelineLimit; i++ {
			seedServiceTimeline(t, store, storage.TimelineEvent, fmt.Sprintf("E%02d", i), 1, i)
		}
		c := contextTimeline(store.ProjectDir("test"), now)
		if len(c.Since) != ContextTimelineLimit || c.SinceOmitted != 0 || strings.Contains(c.Note, "omitted") {
			t.Fatalf("block: %d since, %d omitted, note %q", len(c.Since), c.SinceOmitted, c.Note)
		}
	})

	t.Run("pointer takes the first line, cut long, fresh state unmarked", func(t *testing.T) {
		store := newTestStore(t)
		long := strings.Repeat("ż", 200)
		seedServiceTimeline(t, store, storage.TimelineState, "  "+long+"\nsecond line", 1, 0)
		p := timelineStatePointer(store.ProjectDir("test"), now)
		want := time.Now().AddDate(0, 0, -1).Format("2006-01-02") + ": " + strings.Repeat("ż", 160) + "..."
		if p != want {
			t.Fatalf("pointer = %q", p)
		}
	})
}
