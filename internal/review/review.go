// Package review runs a code review of a GitHub pull request from the
// cockpit: the user pastes a PR URL (or a Slack link, or a sentence - see
// resolve.go), pm finds the local checkout of that repository among its
// projects, starts a headless `claude -p "/review <url>"` in a throwaway
// worktree of it under the project's Claude account, and keeps the report.
//
// The run is DETACHED (its own session, stdout and stderr written to files,
// not pipes), so a review survives a restart of `pm serve`; its state is
// derived from those files on every read - a result envelope on stdout is a
// finished review, a live pid is a running one, anything else failed. The
// review itself is the user's /review command (~/.claude/commands/review.md),
// which reports in the conversation only and never writes to GitHub; the
// deny list below and the worktree make sure of it whatever the account's
// settings allow.
package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mbalazy/pm/internal/report"
	"github.com/mbalazy/pm/internal/solo"
	"github.com/mbalazy/pm/internal/storage"
)

// Timeout is how long a review may run before its process group is killed.
const Timeout = 45 * time.Minute

// States of a review.
const (
	StateRunning = "running"
	StateDone    = "done"
	StateError   = "error"
)

// AllowedTools is what the headless review may use without asking (a
// headless run has nobody to ask, so everything else is refused): reading
// the checkout, sub-agents, and read-only gh/git.
var AllowedTools = []string{
	"Read", "Grep", "Glob", "LS", "Task", "Agent", "TodoWrite",
	"Bash(gh pr view:*)", "Bash(gh pr diff:*)", "Bash(gh pr list:*)", "Bash(gh pr checks:*)",
	"Bash(gh issue view:*)", "Bash(gh issue list:*)", "Bash(gh search:*)", "Bash(gh api:*)",
	"Bash(git log:*)", "Bash(git blame:*)", "Bash(git show:*)", "Bash(git diff:*)", "Bash(git grep:*)",
}

// DisallowedTools is denied whatever the account's settings allow. An
// allowlist only ADDS permissions: the company account's settings.json
// allows git checkout/reset/stash, and on 2026-09-14 a reviewer sub-agent ran
// `git checkout origin/<branch> -- .` in the user's checkout and lost an
// uncommitted change. Deny rules win over allow rules, so every write to the
// tree, to git state and to GitHub is listed here - on top of the review
// running in its own worktree (see prepareWorktree), never the user's.
var DisallowedTools = []string{
	"Edit", "Write", "NotebookEdit",
	"Bash(git checkout:*)", "Bash(git switch:*)", "Bash(git restore:*)", "Bash(git reset:*)",
	"Bash(git stash:*)", "Bash(git clean:*)", "Bash(git add:*)", "Bash(git commit:*)",
	"Bash(git push:*)", "Bash(git pull:*)", "Bash(git merge:*)", "Bash(git rebase:*)",
	"Bash(git cherry-pick:*)", "Bash(git revert:*)", "Bash(git worktree:*)", "Bash(git branch:*)",
	"Bash(gh pr checkout:*)", "Bash(gh pr review:*)", "Bash(gh pr comment:*)", "Bash(gh pr merge:*)",
	"Bash(gh pr edit:*)", "Bash(gh pr close:*)", "Bash(gh pr ready:*)", "Bash(gh issue comment:*)",
	"Bash(gh api *-X*)", "Bash(gh api *--method*)", "Bash(gh api *-f *)", "Bash(gh api *-F *)",
	"Bash(gh api *--field*)", "Bash(gh api *--raw-field*)", "Bash(gh api *--input*)",
	"Bash(rm:*)", "Bash(mv:*)",
}

// ErrNotFound is an unknown review id.
var ErrNotFound = errors.New("review not found")

// InputError is a caller-caused failure: a URL that is not a PR, a repository
// no project checks out.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// PR is a parsed pull request URL.
type PR struct {
	Owner  string
	Repo   string
	Number int
}

// Slug is owner/repo.
func (p PR) Slug() string { return p.Owner + "/" + p.Repo }

var prURL = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/pull/(\d+)(?:[/?#].*)?$`)

// ParseURL reads a GitHub PR URL; anything after the number (/changes,
// /files, a query) is ignored.
func ParseURL(raw string) (PR, error) {
	m := prURL.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return PR{}, &InputError{Msg: fmt.Sprintf("not a GitHub pull request URL: %q (want https://github.com/<owner>/<repo>/pull/<n>)", raw)}
	}
	n, _ := strconv.Atoi(m[3])
	return PR{Owner: m[1], Repo: strings.TrimSuffix(m[2], ".git"), Number: n}, nil
}

// Review is one review, as stored plus what is derived on read.
type Review struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title,omitempty"`
	// Input is what the user pasted, when it was not the PR URL itself.
	Input   string `json:"input,omitempty"`
	Project string `json:"project"`
	// Dir is the project's checkout; the review runs in Worktree, a
	// throwaway detached worktree of it at Commit.
	Dir       string `json:"dir"`
	Worktree  string `json:"worktree,omitempty"`
	Commit    string `json:"commit,omitempty"`
	ConfigDir string `json:"config_dir"`
	PID       int    `json:"pid"`
	Started   string `json:"started"`

	State    string              `json:"state"`
	Finished string              `json:"finished,omitempty"`
	Error    string              `json:"error,omitempty"`
	Tokens   *storage.TokenUsage `json:"tokens,omitempty"`
	// Report is the review's markdown; filled by Get only.
	Report string `json:"report,omitempty"`
	// NoIssues is a done review whose report says "No issues found".
	NoIssues bool `json:"no_issues,omitempty"`

	// Slack is the message the request came from, when it was a Slack link.
	Slack *SlackLink `json:"slack,omitempty"`
	// Approved is when the PR was approved from the cockpit; ApproveError
	// the last failed attempt. SlackReacted / SlackReactError: the ✅ on the
	// Slack message after the approve.
	Approved        string `json:"approved,omitempty"`
	ApproveError    string `json:"approve_error,omitempty"`
	SlackReacted    string `json:"slack_reacted,omitempty"`
	SlackReactError string `json:"slack_react_error,omitempty"`
	// SlackSeen is when the 👀 went on the Slack message as the review
	// started; SlackSeenError the failed attempt.
	SlackSeen      string `json:"slack_seen,omitempty"`
	SlackSeenError string `json:"slack_seen_error,omitempty"`
}

// Controller starts and reads reviews under <pm-root>/.cockpit/reviews.
type Controller struct {
	Store storage.TaskStore
	// Exe is the claude binary ("claude" on PATH); GH the gh binary.
	Exe string
	GH  string
	// Now is the clock; nil = time.Now.
	Now func() time.Time
	// Timeout overrides the package Timeout (tests).
	Timeout time.Duration
	// AskModel, ReadSlack and ViewPR replace the resolution's outside calls
	// (haiku, the Slack MCP servers, gh pr view) in tests; nil = the real ones.
	AskModel  func(ctx context.Context, prompt string) (string, error)
	ReadSlack func(ctx context.Context, link SlackLink) (text, server string, err error)
	ViewPR    func(ctx context.Context, proj *storage.Project, pr PR) (title string, err error)
	// ApprovePR and ReactSlack replace the approve's outside calls (gh pr
	// review --approve, the Slack reaction) in tests.
	ApprovePR  func(ctx context.Context, proj *storage.Project, pr PR) error
	ReactSlack func(ctx context.Context, link SlackLink, emoji string) error
	// WorktreeRoot is where review worktrees go; empty = $TMPDIR/pm-review.
	WorktreeRoot string
}

// New is a controller over the store with the real binaries.
func New(store storage.TaskStore) *Controller { return &Controller{Store: store} }

func (c *Controller) dir() string { return filepath.Join(c.Store.RootDir(), ".cockpit", "reviews") }

func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Match finds the project whose checkout is the PR's repository: its
// project.yaml `repo` names the repository, else a git remote of its `path`
// does. Active projects only; the first match in slug order wins.
func (c *Controller) Match(pr PR) (slug string, proj *storage.Project, err error) {
	cands, err := c.candidates()
	if err != nil {
		return "", nil, err
	}
	want := strings.ToLower(pr.Slug())
	for _, cd := range cands {
		if cd.Repo == want {
			return cd.Slug, cd.Proj, nil
		}
	}
	return "", nil, &InputError{Msg: fmt.Sprintf("no pm project checks out %s - set `repo: https://github.com/%s` in that project's project.yaml (its `path` must exist)", pr.Slug(), pr.Slug())}
}

// repoSlug reduces a repo URL or remote (https, ssh, scp form) on github.com
// to a lower-case owner/repo; anything else to "".
func repoSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	i := strings.Index(s, "github.com")
	if i < 0 {
		return ""
	}
	s = strings.TrimLeft(s[i+len("github.com"):], ":/")
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	if parts := strings.Split(s, "/"); len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

var idUnsafe = regexp.MustCompile(`[^a-z0-9._-]+`)
var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Start resolves the input to a PR (a URL, a Slack link, a sentence - see
// resolve.go), matches it to a project, confirms the PR exists and spawns
// the review in the background.
func (c *Controller) Start(ctx context.Context, input string) (*Review, error) {
	ctx, cancel := context.WithTimeout(ctx, ResolveTimeout)
	defer cancel()
	res, err := c.Resolve(ctx, input)
	if err != nil {
		return nil, err
	}
	pr := res.PR
	slug, proj, err := c.Match(pr)
	if err != nil {
		return nil, err
	}
	title, err := c.viewPR(ctx, proj, pr)
	if err != nil {
		return nil, err
	}
	now := c.now()
	id := idUnsafe.ReplaceAllString(strings.ToLower(fmt.Sprintf("%s-%s-%d-%s", pr.Owner, pr.Repo, pr.Number, now.Format("20060102-150405"))), "-")
	url := fmt.Sprintf("https://github.com/%s/pull/%d", pr.Slug(), pr.Number)
	r := &Review{
		ID: id, URL: url, Repo: pr.Slug(), Number: pr.Number, Title: title, Project: slug,
		Dir: proj.Path, ConfigDir: proj.ResolveClaudeConfigDir(), Started: now.Format(time.RFC3339),
	}
	if _, err := ParseURL(input); err != nil {
		r.Input = strings.TrimSpace(input)
	}
	r.Slack = res.Slack
	if err := os.MkdirAll(c.dir(), 0o755); err != nil {
		return nil, err
	}

	// The config dir goes on the environment only when it is not the default:
	// claude keys its keychain login by the dir CLAUDE_CONFIG_DIR names, so an
	// explicit ~/.claude reads as a separate, never-logged-in profile
	// (solo.PinConfigDir - the executor's workerEnv rule).
	env := withoutKey(report.Environ(), "CLAUDE_CONFIG_DIR")
	if solo.PinConfigDir(r.ConfigDir) {
		env = append(env, "CLAUDE_CONFIG_DIR="+r.ConfigDir)
	}
	if proj.GHAccount != "" {
		// pm serve never loads a repo's direnv token: the project's gh account
		// goes on the environment, never argv (the feed's rule).
		tok, err := c.ghToken(proj.GHAccount)
		if err != nil {
			return nil, fmt.Errorf("gh_account %s: %w", proj.GHAccount, err)
		}
		env = append(withoutKey(env, "GH_TOKEN"), "GH_TOKEN="+tok)
	}

	wt, commit, err := prepareWorktree(ctx, proj.Path, pr, c.worktreeDir(id), env)
	if err != nil {
		return nil, fmt.Errorf("prepare a worktree for the review (it never runs in your checkout): %w", err)
	}
	r.Worktree, r.Commit = wt, commit
	started := false
	defer func() {
		if !started {
			removeWorktree(proj.Path, wt)
		}
	}()

	stdout, err := os.Create(c.path(id, ".out"))
	if err != nil {
		return nil, err
	}
	stderr, err := os.Create(c.path(id, ".err"))
	if err != nil {
		stdout.Close()
		return nil, err
	}
	exe := c.Exe
	if exe == "" {
		exe = "claude"
	}
	// dontAsk: whatever is not allowed is refused, never classified by auto.
	args := append([]string{"-p", "/review " + url, "--output-format", "json", "--permission-mode", "dontAsk", "--allowedTools"}, AllowedTools...)
	args = append(append(args, "--disallowedTools"), DisallowedTools...)
	cmd := exec.Command(exe, args...)
	cmd.Dir = r.Worktree
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("start %s: %w", exe, err)
	}
	r.PID = cmd.Process.Pid
	if err := c.writeMeta(r); err != nil {
		_ = syscall.Kill(-r.PID, syscall.SIGKILL)
		return nil, err
	}
	started = true
	if r.Slack != nil {
		go c.reactStarted(*r.Slack, id)
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = Timeout
	}
	go func() {
		timer := time.AfterFunc(timeout, func() {
			_ = os.WriteFile(c.path(id, ".timeout"), []byte(timeout.String()+"\n"), 0o644)
			_ = syscall.Kill(-r.PID, syscall.SIGKILL)
		})
		_ = cmd.Wait()
		timer.Stop()
		stdout.Close()
		stderr.Close()
		removeWorktree(r.Dir, r.Worktree)
	}()
	r.State = StateRunning
	return r, nil
}

func (c *Controller) ghToken(account string) (string, error) {
	gh := c.GH
	if gh == "" {
		gh = "gh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, gh, "auth", "token", "--user", account).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func withoutKey(env []string, key string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

func (c *Controller) path(id, ext string) string { return filepath.Join(c.dir(), id+ext) }

func (c *Controller) writeMeta(r *Review) error {
	stored := *r
	stored.State, stored.Report = "", ""
	data, err := jsonMarshal(stored)
	if err != nil {
		return err
	}
	return os.WriteFile(c.path(r.ID, ".json"), data, 0o644)
}

// List returns every review, newest first, without the report text.
func (c *Controller) List() ([]Review, error) {
	entries, err := os.ReadDir(c.dir())
	if errors.Is(err, os.ErrNotExist) {
		return []Review{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Review{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		r, err := c.load(strings.TrimSuffix(name, ".json"), false)
		if err != nil {
			continue
		}
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started > out[j].Started })
	return out, nil
}

// Get returns one review with its report.
func (c *Controller) Get(id string) (*Review, error) {
	if !validID.MatchString(id) {
		return nil, ErrNotFound
	}
	return c.load(id, true)
}

// Cancel kills a running review's process group.
func (c *Controller) Cancel(id string) (*Review, error) {
	r, err := c.Get(id)
	if err != nil {
		return nil, err
	}
	if r.State == StateRunning && r.PID > 0 {
		_ = os.WriteFile(c.path(id, ".cancelled"), []byte(c.now().Format(time.RFC3339)+"\n"), 0o644)
		_ = syscall.Kill(-r.PID, syscall.SIGTERM)
	}
	return r, nil
}

func (c *Controller) load(id string, withReport bool) (*Review, error) {
	data, err := os.ReadFile(c.path(id, ".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var r Review
	if err := jsonUnmarshal(data, &r); err != nil {
		return nil, err
	}
	if !alive(r.PID) && r.Worktree != "" && fileExists(r.Worktree) {
		// A review whose pm serve restarted before it ended: nobody else
		// removes its worktree.
		removeWorktree(r.Dir, r.Worktree)
	}
	out, _ := os.ReadFile(c.path(id, ".out"))
	finished := ""
	if fi, err := os.Stat(c.path(id, ".out")); err == nil {
		finished = fi.ModTime().Format(time.RFC3339)
	}
	switch {
	case alive(r.PID):
		r.State = StateRunning
	case len(strings.TrimSpace(string(out))) > 0:
		oc, derr := report.Decode(out)
		if derr != nil {
			r.State, r.Error, r.Finished = StateError, derr.Error(), finished
			break
		}
		r.State, r.Finished, r.Tokens = StateDone, finished, oc.Tokens
		r.NoIssues = noIssues(oc.Text)
		if withReport {
			r.Report = oc.Text
		}
	default:
		r.State, r.Finished = StateError, finished
		errText, _ := os.ReadFile(c.path(id, ".err"))
		switch {
		case fileExists(c.path(id, ".cancelled")):
			r.Error = "cancelled"
		case fileExists(c.path(id, ".timeout")):
			r.Error = "killed after the " + Timeout.String() + " timeout"
		case strings.TrimSpace(string(errText)) != "":
			r.Error = firstLines(string(errText), 3)
		default:
			r.Error = "claude exited without a result"
		}
	}
	c.applyApproval(&r)
	return &r, nil
}

// noIssuesLine matches the /review skill's verdict when nothing was found,
// in the spellings it writes: "No issues found." and "No issues survived
// the confidence filter." (lower-confidence findings filtered out).
var noIssuesLine = regexp.MustCompile(`(?im)^\s*no issues (found|survived)\b`)

func noIssues(report string) bool { return noIssuesLine.MatchString(report) }

func (c *Controller) worktreeDir(id string) string {
	if c.WorktreeRoot != "" {
		return filepath.Join(c.WorktreeRoot, id)
	}
	return filepath.Join(os.TempDir(), "pm-review", id)
}

// prepareWorktree adds a detached worktree of repo at dir, at the PR's head
// when it can be fetched from origin, else at the checkout's HEAD (the
// review still reads the diff through gh). It returns the dir and a label
// of the commit. The user's checkout is never touched: a worktree has its
// own files and index.
func prepareWorktree(ctx context.Context, repo string, pr PR, dir string, env []string) (string, string, error) {
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(append([]string{}, env...), "GIT_TERMINAL_PROMPT=0")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %s", args[0], firstLines(stderr.String()+" "+err.Error(), 1))
		}
		return strings.TrimSpace(string(out)), nil
	}
	ref := fmt.Sprintf("refs/pull/%d/head", pr.Number)
	rev, label := "", ""
	lsOut, err := git("ls-remote", "origin", ref)
	if err == nil && len(strings.Fields(lsOut)) > 0 {
		sha := strings.Fields(lsOut)[0]
		if _, err = git("fetch", "--quiet", "--no-tags", "origin", ref); err == nil {
			if _, err = git("rev-parse", "--verify", "--quiet", sha+"^{commit}"); err == nil {
				rev, label = sha, sha[:min(12, len(sha))]+" (PR head)"
			}
		}
	} else if err == nil {
		err = fmt.Errorf("origin has no %s", ref)
	}
	if rev == "" {
		head, herr := git("rev-parse", "--verify", "HEAD")
		if herr != nil {
			return "", "", herr
		}
		rev, label = head, head[:min(12, len(head))]+" (your checkout's HEAD - the PR head was not fetched: "+err.Error()+")"
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", "", err
	}
	if _, err := git("worktree", "add", "--detach", dir, rev); err != nil {
		return "", "", err
	}
	return dir, label, nil
}

// removeWorktree removes a review's worktree; best effort.
func removeWorktree(repo, dir string) {
	if dir == "" {
		return
	}
	if err := exec.Command("git", "-C", repo, "worktree", "remove", "--force", dir).Run(); err != nil {
		_ = os.RemoveAll(dir)
		_ = exec.Command("git", "-C", repo, "worktree", "prune").Run()
	}
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " / ")
}
