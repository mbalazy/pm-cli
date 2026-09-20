package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// GitSource reads, per project with a checkout: commits on every branch a
// pm task names (`git log --since`), and the pull requests of the repo that
// changed since the cutoff, in any state (`gh pr list --state all`), so a
// merge or a close is a change too. That is the v1 scope the user chose - branches pinned to
// tasks and open PRs, not the whole repo's activity; AllBranches is the one
// switch that widens it to every ref, so growing the scope is an option
// rather than a rewrite.
//
// A project without a path, whose path is not a git checkout, or where gh
// is missing/unauthenticated is a *ProjectError: reported, and the next
// project still runs.
type GitSource struct {
	Run Runner
	// AllBranches widens the commit scan from task branches to every ref.
	AllBranches bool
	// prs is the last Fetch's PR listing as current states (PRSnapshotter).
	prs []PRState
}

// PRStates hands the feed the current state of every PR the last Fetch
// listed (feed.PRSnapshotter).
func (s *GitSource) PRStates() []PRState { return s.prs }

func (s *GitSource) Name() string { return "git" }

// Configure takes AllBranches from cockpit.git.all_branches (feed.Configurable).
func (s *GitSource) Configure(cfg *storage.CockpitConfig) {
	if cfg != nil {
		s.AllBranches = cfg.Git.AllBranches
	}
}

// gitLogFormat separates fields with \x1f and records with \x1e so a
// subject holding a newline or a quote cannot break the parse.
const gitLogFormat = "%H%x1f%aI%x1f%an%x1f%s%x1e"

func (s *GitSource) Fetch(ctx context.Context, from, to time.Time, projects []Project) ([]Event, error) {
	var out []Event
	var errs []error
	s.prs = nil
	for _, p := range projects {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if p.Path == "" {
			continue // a project without a checkout has no git side; not an error
		}
		if !isGitRepo(p.Path) {
			errs = append(errs, &ProjectError{Project: p.Slug, Err: fmt.Errorf("%s is not a git checkout", p.Path)})
			continue
		}
		commits, err := s.commits(ctx, p, from, to)
		out = append(out, commits...)
		if err != nil {
			errs = append(errs, &ProjectError{Project: p.Slug, Err: err})
		}
		prs, err := s.prEvents(ctx, p, from, to)
		out = append(out, prs...)
		if err != nil {
			errs = append(errs, &ProjectError{Project: p.Slug, Err: err})
		}
	}
	return out, errors.Join(errs...)
}

func isGitRepo(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// commits scans the task branches (or every ref). A branch a task names
// that does not exist locally is skipped silently: pm records branch names
// the moment a task is authored, before the branch is created.
func (s *GitSource) commits(ctx context.Context, p Project, from, to time.Time) ([]Event, error) {
	type ref struct{ branch, taskID, title string }
	var refs []ref
	if s.AllBranches {
		refs = append(refs, ref{branch: "--all"})
	} else {
		seen := map[string]bool{}
		for _, t := range p.Tasks {
			b := strings.TrimSpace(t.Meta.Branch)
			if b == "" || seen[b] || strings.HasPrefix(b, "-") {
				continue
			}
			seen[b] = true
			refs = append(refs, ref{branch: b, taskID: t.Meta.ID, title: t.Meta.Title})
		}
	}
	var out []Event
	var firstErr error
	for _, r := range refs {
		args := []string{"log", "--since=" + from.Format(time.RFC3339), "--until=" + to.Format(time.RFC3339), "--format=" + gitLogFormat}
		if r.branch == "--all" {
			args = append(args, "--all")
		} else {
			args = append(args, r.branch, "--")
		}
		stdout, err := s.Run(ctx, p.Path, "git", args...)
		if err != nil {
			// "unknown revision" / "bad revision": the branch a task names is
			// not in this checkout (not created yet, or only on origin).
			if r.branch != "--all" && (strings.Contains(err.Error(), "unknown revision") || strings.Contains(err.Error(), "bad revision")) {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, rec := range strings.Split(stdout, "\x1e") {
			rec = strings.TrimSpace(rec)
			if rec == "" {
				continue
			}
			f := strings.SplitN(rec, "\x1f", 4)
			if len(f) < 4 {
				continue
			}
			hash, when, author, subject := f[0], f[1], f[2], f[3]
			ts, ok := inWindow(when, from, to)
			if !ok {
				continue
			}
			detail := fmt.Sprintf("commit %s by %s", hash[:min(8, len(hash))], author)
			if r.branch != "--all" {
				detail += " on " + r.branch
			}
			out = append(out, Event{
				ID: EventID("git", "commit", p.Slug, hash), TS: ts.Format(time.RFC3339),
				Project: p.Slug, Group: p.Group, TaskID: r.taskID, Title: subject, Detail: detail, Severity: SeverityInfo,
			})
		}
	}
	return out, firstErr
}

// prListFields is what both git (PR events) and github (their discussions)
// ask gh for; one spelling so the two sources parse one shape.
const prListFields = "number,title,url,state,updatedAt,createdAt,mergedAt,closedAt,mergedBy,headRefName,author,isDraft,reviewDecision,reviewRequests"

// prListLimit caps one listing; a day of PR activity in one repo stays far
// below it.
const prListLimit = "100"

// pr is one row of `gh pr list --json`.
type pr struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	UpdatedAt      string `json:"updatedAt"`
	CreatedAt      string `json:"createdAt"`
	HeadRefName    string `json:"headRefName"`
	IsDraft        bool   `json:"isDraft"`
	ReviewDecision string `json:"reviewDecision"`
	// State is OPEN, MERGED or CLOSED (empty reads as open).
	State    string `json:"state"`
	MergedAt string `json:"mergedAt"`
	ClosedAt string `json:"closedAt"`
	MergedBy struct {
		Login string `json:"login"`
	} `json:"mergedBy"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	// ReviewRequests holds users (login set) and teams (login empty).
	ReviewRequests []struct {
		Login string `json:"login"`
	} `json:"reviewRequests"`
}

// listPRs runs `gh pr list` in the checkout for the PRs updated since from,
// in any state, and keeps only the PRs that concern the user (concernsUser)
// - both the git and the github source read PRs through here, so neither
// shows other people's PRs.
func listPRs(ctx context.Context, run Runner, p Project, from time.Time) ([]pr, error) {
	prs, err := listAllPRs(ctx, run, p, from)
	if err != nil || len(prs) == 0 {
		return nil, err
	}
	login, err := ghLogin(ctx, run, p)
	if err != nil {
		return nil, err
	}
	var mine []pr
	for _, r := range prs {
		if concernsUser(p, login, r) {
			mine = append(mine, r)
		}
	}
	return mine, nil
}

// ghLogin is the user's GitHub login for p: gh_account when the project
// names one (githubContext already put that account's token on ctx),
// otherwise gh's active account. Called once per project per Fetch.
func ghLogin(ctx context.Context, run Runner, p Project) (string, error) {
	if p.GHAccount != "" {
		return p.GHAccount, nil
	}
	out, err := run(ctx, p.Path, "gh", "api", "user", "--jq", ".login")
	if err != nil {
		return "", fmt.Errorf("gh api user: %w", err)
	}
	login := strings.TrimSpace(out)
	if login == "" {
		return "", fmt.Errorf("gh api user: empty login")
	}
	return login, nil
}

// concernsUser keeps a PR the user wrote, one waiting on the user's review,
// or one whose head is a pm task's branch. Logins compare case-insensitively.
func concernsUser(p Project, login string, r pr) bool {
	if strings.EqualFold(r.Author.Login, login) {
		return true
	}
	for _, rr := range r.ReviewRequests {
		if rr.Login != "" && strings.EqualFold(rr.Login, login) {
			return true
		}
	}
	return taskForBranch(p, r.HeadRefName) != ""
}

// listAllPRs runs `gh pr list` in the checkout, unfiltered by author. The
// search is by DATE (GitHub's updated: qualifier), one day early so no
// zone offset can drop a PR; the exact window is applied per event.
func listAllPRs(ctx context.Context, run Runner, p Project, from time.Time) ([]pr, error) {
	since := from.UTC().AddDate(0, 0, -1).Format("2006-01-02")
	stdout, err := run(ctx, p.Path, "gh", "pr", "list", "--state", "all", "--search", "updated:>="+since, "--limit", prListLimit, "--json", prListFields)
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("gh is not installed")
		}
		return nil, err
	}
	var prs []pr
	if err := json.Unmarshal([]byte(stdout), &prs); err != nil {
		return nil, fmt.Errorf("gh pr list: unparsable output: %w", err)
	}
	return prs, nil
}

// githubContext decides whether gh has anything to say about p and, if so,
// with whose token. A checkout with no GitHub remote (GitLab, or no remote
// at all) has no PRs gh could list - that is not an error, so ok is false
// and nothing is reported. A project naming gh_account gets that account's
// token on the returned context (WithEnv), because `pm serve` never loads
// the repo's direnv GH_TOKEN.
func githubContext(ctx context.Context, run Runner, p Project) (context.Context, bool, error) {
	remotes, err := run(ctx, p.Path, "git", "remote", "-v")
	if err != nil {
		return ctx, false, err
	}
	if !strings.Contains(remotes, "github.com") {
		return ctx, false, nil
	}
	if p.GHAccount == "" {
		return ctx, true, nil
	}
	tok, err := run(ctx, p.Path, "gh", "auth", "token", "--user", p.GHAccount)
	if err != nil {
		return ctx, false, fmt.Errorf("gh_account %s: %w", p.GHAccount, err)
	}
	return WithEnv(ctx, "GH_TOKEN="+strings.TrimSpace(tok)), true, nil
}

// prEvents emits, per PR touched in the window: "merged" or "closed" when
// that happened in the window (instead of "updated" - the merge IS the
// update), else "opened"/"updated", with the state named when the PR is no
// longer open. Every listed PR also lands in the snapshot of current states.
func (s *GitSource) prEvents(ctx context.Context, p Project, from, to time.Time) ([]Event, error) {
	ctx, ok, err := githubContext(ctx, s.Run, p)
	if !ok {
		return nil, err
	}
	prs, err := listPRs(ctx, s.Run, p, from)
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, r := range prs {
		taskID := taskForBranch(p, r.HeadRefName)
		state := prStateWord(r)
		s.prs = append(s.prs, PRState{
			Project: p.Slug, Number: r.Number, Title: r.Title, URL: r.URL, State: state,
			Draft: r.IsDraft, ReviewDecision: r.ReviewDecision, UpdatedAt: r.UpdatedAt, TaskID: taskID,
		})
		ev := Event{Project: p.Slug, Group: p.Group, TaskID: taskID, Title: r.Title, URL: r.URL, Severity: SeverityInfo}
		if ts, ok := inWindow(r.MergedAt, from, to); ok && state == "merged" {
			by := r.MergedBy.Login
			if by == "" {
				by = "someone"
			}
			ev.ID, ev.TS, ev.Severity = EventID("git", "pr-merged", p.Slug, fmt.Sprint(r.Number)), ts.Format(time.RFC3339), SeverityOK
			ev.Detail = fmt.Sprintf("PR #%d by %s merged by %s", r.Number, r.Author.Login, by)
			out = append(out, ev)
			continue
		}
		if ts, ok := inWindow(r.ClosedAt, from, to); ok && state == "closed" {
			ev.ID, ev.TS = EventID("git", "pr-closed", p.Slug, fmt.Sprint(r.Number)), ts.Format(time.RFC3339)
			ev.Detail = fmt.Sprintf("PR #%d by %s closed without merging", r.Number, r.Author.Login)
			out = append(out, ev)
			continue
		}
		ts, ok := inWindow(r.UpdatedAt, from, to)
		if !ok {
			continue
		}
		detail := fmt.Sprintf("PR #%d by %s updated", r.Number, r.Author.Login)
		if created, ok := inWindow(r.CreatedAt, from, to); ok && created.Equal(ts) {
			detail = fmt.Sprintf("PR #%d by %s opened", r.Number, r.Author.Login)
		}
		if r.IsDraft {
			detail += " (draft)"
		}
		if state != "open" {
			detail += " (already " + state + ")"
		}
		ev.ID, ev.TS, ev.Detail = EventID("git", "pr", p.Slug, fmt.Sprint(r.Number), r.UpdatedAt), ts.Format(time.RFC3339), detail
		out = append(out, ev)
	}
	return out, nil
}

// prStateWord is gh's state in the feed's words: open, merged, closed.
func prStateWord(r pr) string {
	switch strings.ToUpper(r.State) {
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "closed"
	}
	return "open"
}

// taskForBranch finds the pm task whose branch is the PR's head.
func taskForBranch(p Project, branch string) string {
	if branch == "" {
		return ""
	}
	for _, t := range p.Tasks {
		if t.Meta.Branch == branch {
			return t.Meta.ID
		}
	}
	return ""
}
