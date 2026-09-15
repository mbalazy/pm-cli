package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func appendTimeline(t *testing.T, store *storage.Store, kind, text string, ago time.Duration) {
	t.Helper()
	e := &storage.TimelineEntry{Kind: kind, Text: text, TS: time.Now().Add(-ago).Format(time.RFC3339)}
	if err := storage.AppendTimelineEntry(store.ProjectDir("test"), e); err != nil {
		t.Fatal(err)
	}
}

// /api/timeline/{project} is the default read: no timeline at all, an
// events-only timeline (no state), then a state with entries after it.
func TestTimeline(t *testing.T) {
	store := newTestStore(t)
	srv := newServer(t, store, Options{})
	url := srv.URL + "/api/timeline/test"

	m := getJSON(t, url, 200)
	wantKeys(t, m, "project", "state", "since", "stale", "total", "note")
	if m["state"] != nil || len(m["since"].([]any)) != 0 || m["total"].(float64) != 0 {
		t.Fatalf("empty timeline: %v", m)
	}

	appendTimeline(t, store, storage.TimelineEvent, "kickoff", 72*time.Hour)
	m = getJSON(t, url, 200)
	if m["state"] != nil || len(m["since"].([]any)) != 1 || !strings.HasPrefix(m["note"].(string), "no state yet") {
		t.Fatalf("events only: %v", m)
	}

	appendTimeline(t, store, storage.TimelineState, "where we stand\nwhat is next", 48*time.Hour)
	appendTimeline(t, store, storage.TimelineDecision, "one product on two rails", 24*time.Hour)
	appendTimeline(t, store, storage.TimelineEvent, "the client moved the deadline", time.Hour)
	m = getJSON(t, url, 200)
	state, _ := m["state"].(map[string]any)
	since := m["since"].([]any)
	if state["text"] != "where we stand\nwhat is next" || len(since) != 2 || m["stale"] != false || m["project"] != "test" {
		t.Fatalf("state + 2: %v", m)
	}
	if since[0].(map[string]any)["kind"] != "decision" || since[1].(map[string]any)["text"] != "the client moved the deadline" {
		t.Fatalf("since order: %v", since)
	}

	if e := getJSON(t, srv.URL+"/api/timeline/nope", 404); e["error"] == nil {
		t.Fatalf("unknown project: %v", e)
	}

	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", resp.StatusCode)
	}
}

// A timeline write is a tasks event for its project; a file outside the
// fingerprinted set is not.
func TestEventsTimeline(t *testing.T) {
	store := newTestStore(t)
	srv := newServer(t, store, Options{PollInterval: 20 * time.Millisecond, PingInterval: 150 * time.Millisecond})
	events, cancel := sseClient(t, srv.URL+"/api/events")
	defer cancel()
	waitFor(t, events, "ping {}")

	// Negative control: a stray file in the project dir. Several polls run
	// before the next ping; none of them may report a change.
	if err := os.WriteFile(filepath.Join(store.ProjectDir("test"), "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
quiet:
	for {
		select {
		case ev := <-events:
			if strings.HasPrefix(ev, "tasks ") {
				t.Fatalf("a file outside the fingerprint produced %q", ev)
			}
			if ev == "ping {}" {
				break quiet
			}
		case <-deadline:
			t.Fatal("no ping within 5s")
		}
	}

	appendTimeline(t, store, storage.TimelineEvent, "the client moved the deadline", time.Hour)
	waitFor(t, events, `tasks {"project":"test"}`)
}
