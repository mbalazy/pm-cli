package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/review"
	"github.com/mbalazy/pm/internal/storage"
)

// POST a PR URL -> 202 running -> the fake claude's report on GET.
func TestReviews(t *testing.T) {
	store := newTestStore(t)
	checkout := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", checkout}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
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
	srv := newServer(t, store, Options{Review: &review.Controller{
		Store: store, Exe: exe,
		AskModel: func(_ context.Context, prompt string) (string, error) {
			if strings.Contains(prompt, "pr 8") {
				return `{"repo":"org/app","number":8}`, nil
			}
			return `{"error":"no PR named"}`, nil
		},
		ViewPR: func(context.Context, *storage.Project, review.PR) (string, error) { return "Fix login", nil },
	}})

	postJSON(t, srv.URL+"/api/reviews", `{"url":"https://github.com/nobody/x/pull/1"}`, 400)
	postJSON(t, srv.URL+"/api/reviews", `{"input":"nope"}`, 400)
	m := postJSON(t, srv.URL+"/api/reviews", `{"url":"https://github.com/org/app/pull/7/changes"}`, 202)
	if m["project"] != "test" || m["state"] != "running" || m["title"] != "Fix login" {
		t.Fatalf("start = %v", m)
	}
	free := postJSON(t, srv.URL+"/api/reviews", `{"input":"pr 8 w app"}`, 202)
	if free["number"] != float64(8) || free["input"] != "pr 8 w app" {
		t.Fatalf("free text = %v", free)
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
	if len(list) != 2 {
		t.Fatalf("list = %v", list)
	}
	getJSON(t, srv.URL+"/api/reviews/nosuch", 404)
}
