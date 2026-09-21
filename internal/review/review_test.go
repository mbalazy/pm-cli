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

	"github.com/mbalazy/pm-cli/internal/storage"
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
		ReactSlack: func(context.Context, SlackLink, string) error {
			return fmt.Errorf("no slack in tests")
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
		{"https://github.com/orbit-org/app.orbit/pull/1003/changes", "orbit-org/app.orbit#1003", true},
		{"https://github.com/o/r/pull/7", "o/r#7", true},
		{" https://github.com/o/r/pull/7/files?diff=split ", "o/r#7", true},
		{"https://gitlab.com/acme-group/lending/acme-web/-/merge_requests/43", "acme-group/lending/acme-web!43", true},
		{"https://gitlab.com/acme-group/acme-web/-/merge_requests/7/diffs", "acme-group/acme-web!7", true},
		{"https://github.com/o/r/issues/7", "", false},
		{"https://gitlab.com/o/r/pull/7", "", false},
		{"https://gitlab.com/acme-web/-/merge_requests/7", "", false},
		{"o/r#7", "", false},
	} {
		pr, err := ParseURL(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("%q: err = %v", tc.in, err)
			continue
		}
		if tc.ok {
			if got := pr.Text(); got != tc.want {
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
	for _, want := range []string{"-p\n/review https://github.com/org/app/pull/7\n", "Bash(gh pr diff:*)", "--permission-mode\ndontAsk\n", "--disallowedTools\nEdit\n", "Bash(git checkout:*)", "Bash(glab mr note:*)"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv lacks %q: %s", want, argv)
		}
	}
	// A GitHub review is told nothing extra and holds no other host's reads.
	if strings.Contains(argv, "Bash(glab mr view:*)") || strings.Contains(argv, "gh cannot see it") {
		t.Errorf("a GitHub review carries GitLab tooling: %s", argv)
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
	slug, _, err := c.Match(PR{Host: GitHub, Path: "Org/Other", Number: 1})
	if err != nil || slug != "other" {
		t.Fatalf("match = %q %v", slug, err)
	}
	if _, err := c.Start(bg, "https://github.com/nobody/nothing/pull/1"); err == nil || !strings.Contains(err.Error(), "no pm project checks out nobody/nothing") {
		t.Errorf("unknown repo err = %v", err)
	}
	// The same path on the other host is a different repository.
	if _, err := c.Start(bg, "https://gitlab.com/Org/Other/-/merge_requests/1"); err == nil ||
		!strings.Contains(err.Error(), "set `repo: https://gitlab.com/Org/Other`") {
		t.Errorf("host is part of the match: %v", err)
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

// gitlabProject adds a project whose checkout is a GitLab repository under a
// nested group, with an origin that serves merge request 43's head at the
// ref GitLab publishes it on. The checkout's own HEAD is one commit behind
// it, so a review that ends up at the MR head cannot have fallen back to it.
func gitlabProject(t *testing.T, c *Controller) (checkout, mrHead string) {
	t.Helper()
	checkout, origin := t.TempDir(), t.TempDir()
	gitCommit(t, checkout)
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(origin, "init", "-q", "--bare")
	git(checkout, "remote", "add", "origin", origin)
	git(checkout, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "the merge request")
	mrHead = git(checkout, "rev-parse", "HEAD")
	git(checkout, "push", "-q", "origin", "HEAD:refs/merge-requests/43/head")
	git(checkout, "reset", "--hard", "-q", "HEAD~1")
	store := c.Store.(*storage.Store)
	if err := store.CreateProject("web", &storage.Project{
		Name: "web", Prefix: "web", Path: checkout,
		Repo: "https://gitlab.com/acme-group/lending/acme-web",
	}); err != nil {
		t.Fatal(err)
	}
	return checkout, mrHead
}

// A GitLab merge request is reviewed like a PR - same worktree, same report -
// but from the MR's own ref, with glab's read-only commands in place of gh's
// and a line telling the reviewer so.
func TestStartAGitLabMergeRequest(t *testing.T) {
	c, _, argvFile := setup(t)
	checkout, mrHead := gitlabProject(t, c)
	r, err := c.Start(bg, "https://gitlab.com/acme-group/lending/acme-web/-/merge_requests/43/diffs")
	if err != nil {
		t.Fatal(err)
	}
	if r.Project != "web" || r.Host != HostGitLab || r.Dir != checkout ||
		r.URL != "https://gitlab.com/acme-group/lending/acme-web/-/merge_requests/43" ||
		r.Repo != "acme-group/lending/acme-web" || r.Number != 43 {
		t.Fatalf("started = %+v", r)
	}
	if !strings.HasPrefix(r.Commit, mrHead[:12]) || !strings.Contains(r.Commit, "(merge request head)") {
		t.Errorf("commit = %q, want the MR head %s", r.Commit, mrHead[:12])
	}
	done := waitState(t, c, r.ID, StateDone)
	if !strings.Contains(done.Report, "No issues found.") {
		t.Errorf("done = %+v", done)
	}
	argv := strings.Join(strings.Split(mustRead(t, argvFile), "\n")[3:], "\n")
	for _, want := range []string{
		"/review https://gitlab.com/acme-group/lending/acme-web/-/merge_requests/43\n",
		"glab mr view 43 -R acme-group/lending/acme-web", "gh` cannot see it",
		"Bash(glab mr diff:*)", "Bash(glab mr note:*)", "Bash(gh pr review:*)", "Bash(git checkout:*)",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv lacks %q: %s", want, argv)
		}
	}
	if strings.Contains(argv, "Bash(gh pr diff:*)") {
		t.Errorf("a GitLab review carries gh reads: %s", argv)
	}
}

// A stored review whose host field predates GitLab support reads as GitHub,
// and every host's writing commands are denied whichever host is reviewed.
func TestHostDefaultsAndToolLists(t *testing.T) {
	if HostByName("").Name != HostGitHub || HostByName("bitbucket").Name != HostGitHub || HostByName(HostGitLab) != GitLab {
		t.Fatalf("host lookup = %+v", HostByName(""))
	}
	for _, h := range hosts {
		denied := strings.Join(h.DisallowedTools(), " ")
		for _, want := range []string{"Bash(gh pr review:*)", "Bash(glab mr approve:*)", "Bash(git push:*)", "Edit"} {
			if !strings.Contains(denied, want) {
				t.Errorf("%s denies not %q", h.Name, want)
			}
		}
		allowed := strings.Join(h.AllowedTools(), " ")
		if strings.Contains(allowed, "Edit") || !strings.Contains(allowed, "Bash("+h.CLI+" ") {
			t.Errorf("%s allows %q", h.Name, allowed)
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
