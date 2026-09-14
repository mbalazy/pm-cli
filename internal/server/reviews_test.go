package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/review"
	"github.com/mbalazy/pm/internal/storage"
)

// POST a PR URL -> 202 running -> the fake claude's report on GET.
func TestReviews(t *testing.T) {
	store := newTestStore(t)
	checkout := t.TempDir()
	if _, err := store.MutateProject("test", func(p *storage.Project) error {
		p.Path, p.Repo = checkout, "https://github.com/org/app"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\nprintf '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"### Code review - PR #7\"}'\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := newServer(t, store, Options{Review: &review.Controller{Store: store, Exe: exe}})

	postJSON(t, srv.URL+"/api/reviews", `{"url":"https://github.com/nobody/x/pull/1"}`, 400)
	postJSON(t, srv.URL+"/api/reviews", `{"url":"nope"}`, 400)
	m := postJSON(t, srv.URL+"/api/reviews", `{"url":"https://github.com/org/app/pull/7/changes"}`, 202)
	if m["project"] != "test" || m["state"] != "running" {
		t.Fatalf("start = %v", m)
	}
	id := m["id"].(string)
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := getJSON(t, srv.URL+"/api/reviews/"+id, 200)
		if got["state"] == "done" {
			if got["report"] != "### Code review - PR #7" {
				t.Fatalf("report = %v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never done: %v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	list := getJSON(t, srv.URL+"/api/reviews", 200)["reviews"].([]any)
	if len(list) != 1 {
		t.Fatalf("list = %v", list)
	}
	getJSON(t, srv.URL+"/api/reviews/nosuch", 404)
}
