package review

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

const fakeClaude = `#!/bin/sh
{ pwd -P; printf '%s\n' "$CLAUDE_CONFIG_DIR"; if [ -f README.md ]; then echo tracked-file-present; else echo tracked-file-missing; fi; printf '%s\n' "$@"; } > "$FAKE_OUT"
case "$FAKE_MODE" in
fail) echo "boom: not logged in" >&2; exit 1 ;;
hang) sleep 30 ;;
esac
printf '{"type":"result","subtype":"success","is_error":false,"result":"### Code review - PR #7\\n\\nNo issues found.","usage":{"input_tokens":10,"output_tokens":5}}'
`

func setup(t *testing.T) (*Controller, string, string) {
	t.Helper()
	root := t.TempDir()
	store := &storage.Store{Root: root}
	checkout := t.TempDir()
	gitCommit(t, checkout)
	if err := store.CreateProject("app", &storage.Project{Name: "app", Prefix: "app", Path: checkout, Repo: "https://github.com/Org/App.git", ClaudeConfigDir: "/tmp/cfg-company"}); err != nil {
		t.Fatal(err)
	}
	// A second project matched through its git remote only.
	other := t.TempDir()
	if out, err := exec.Command("git", "-C", other, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if out, err := exec.Command("git", "-C", other, "remote", "add", "origin", "git@github.com:Org/Other.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote: %v %s", err, out)
	}
	if err := store.CreateProject("other", &storage.Project{Name: "other", Prefix: "other", Path: other}); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	exe := filepath.Join(bin, "claude")
	if err := os.WriteFile(exe, []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(bin, "argv.txt")
	t.Setenv("FAKE_OUT", out)
	t.Setenv("FAKE_MODE", "")
	return &Controller{
		Store: store, Exe: exe, WorktreeRoot: t.TempDir(),
		// Never a real model, Slack or gh in tests.
		AskModel: func(context.Context, string) (string, error) {
			return `{"error":"no PR number"}`, nil
		},
		ReadSlack: func(context.Context, SlackLink) (string, string, error) {
			return "", "", fmt.Errorf("no slack in tests")
		},
		ViewPR: func(_ context.Context, _ *storage.Project, pr PR) (string, error) {
			return fmt.Sprintf("PR %d", pr.Number), nil
		},
	}, checkout, out
}

var bg = context.Background()

// gitCommit makes dir a git repository with one commit holding README.md.
func gitCommit(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "README.md"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func waitState(t *testing.T, c *Controller, id, want string) *Review {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r, err := c.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == want {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %s (%s), want %s", r.State, r.Error, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestParseURL(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"https://github.com/OrbitOrg/app.orbit/pull/1003/changes", "OrbitOrg/app.orbit#1003", true},
		{"https://github.com/o/r/pull/7", "o/r#7", true},
		{" https://github.com/o/r/pull/7/files?diff=split ", "o/r#7", true},
		{"https://github.com/o/r/issues/7", "", false},
		{"https://gitlab.com/o/r/pull/7", "", false},
		{"o/r#7", "", false},
	} {
		pr, err := ParseURL(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("%q: err = %v", tc.in, err)
			continue
		}
		if tc.ok {
			if got := fmt.Sprintf("%s#%d", pr.Slug(), pr.Number); got != tc.want {
				t.Errorf("%q = %s", tc.in, got)
			}
		}
	}
}

// The review runs in a throwaway worktree of the checkout (never the
// checkout itself), with the write tools denied, and the worktree is gone
// once it ends.
func TestStartRunsTheReviewInAWorktree(t *testing.T) {
	c, checkout, argvFile := setup(t)
	r, err := c.Start(bg, "https://github.com/org/app/pull/7/changes")
	if err != nil {
		t.Fatal(err)
	}
	if r.Project != "app" || r.State != StateRunning || r.URL != "https://github.com/org/app/pull/7" || r.Dir != checkout {
		t.Fatalf("started = %+v", r)
	}
	if !strings.Contains(r.Commit, "your checkout's HEAD") {
		t.Errorf("commit label = %q (no origin: falls back to HEAD)", r.Commit)
	}
	done := waitState(t, c, r.ID, StateDone)
	if !strings.Contains(done.Report, "No issues found.") || done.Tokens == nil || done.Tokens.Output != 5 {
		t.Errorf("done = %+v", done)
	}
	data, _ := os.ReadFile(argvFile)
	lines := strings.Split(string(data), "\n")
	wtRoot, _ := filepath.EvalSymlinks(c.WorktreeRoot)
	realCheckout, _ := filepath.EvalSymlinks(checkout)
	if lines[0] != filepath.Join(wtRoot, r.ID) || lines[0] == realCheckout || lines[1] != "/tmp/cfg-company" || lines[2] != "tracked-file-present" {
		t.Errorf("cwd/config dir/files = %q", lines[:3])
	}
	argv := strings.Join(lines[3:], "\n")
	for _, want := range []string{"-p\n/review https://github.com/org/app/pull/7\n", "Bash(gh pr diff:*)", "--permission-mode\ndontAsk\n", "--disallowedTools\nEdit\n", "Bash(git checkout:*)"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv lacks %q: %s", want, argv)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for fileExists(r.Worktree) {
		if time.Now().After(deadline) {
			t.Fatalf("worktree %s not removed", r.Worktree)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if out, _ := exec.Command("git", "-C", checkout, "worktree", "list").Output(); strings.Count(strings.TrimSpace(string(out)), "\n") != 0 {
		t.Errorf("worktrees left: %s", out)
	}
	list, err := c.List()
	if err != nil || len(list) != 1 || list[0].Report != "" || list[0].State != StateDone {
		t.Errorf("list = %+v %v", list, err)
	}
}

func TestMatchByGitRemote(t *testing.T) {
	c, _, _ := setup(t)
	slug, _, err := c.Match(PR{Owner: "Org", Repo: "Other", Number: 1})
	if err != nil || slug != "other" {
		t.Fatalf("match = %q %v", slug, err)
	}
	if _, err := c.Start(bg, "https://github.com/nobody/nothing/pull/1"); err == nil || !strings.Contains(err.Error(), "no pm project checks out nobody/nothing") {
		t.Errorf("unknown repo err = %v", err)
	}
	if _, err := c.Start(bg, "not a url"); err == nil {
		t.Error("a bad URL must fail")
	}
}

func TestFailedAndCancelledReviews(t *testing.T) {
	c, _, _ := setup(t)
	t.Setenv("FAKE_MODE", "fail")
	r, err := c.Start(bg, "https://github.com/org/app/pull/8")
	if err != nil {
		t.Fatal(err)
	}
	failed := waitState(t, c, r.ID, StateError)
	if !strings.Contains(failed.Error, "boom: not logged in") {
		t.Errorf("error = %q", failed.Error)
	}

	t.Setenv("FAKE_MODE", "hang")
	c.Now = func() time.Time { return time.Now().Add(time.Second) } // a distinct id
	r, err = c.Start(bg, "https://github.com/org/app/pull/8")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	if got := waitState(t, c, r.ID, StateError); got.Error != "cancelled" {
		t.Errorf("cancelled = %q", got.Error)
	}
	if _, err := c.Get("../../config"); err != ErrNotFound {
		t.Errorf("traversal id = %v", err)
	}
}
