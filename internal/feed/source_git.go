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

	"github.com/mbalazy/pm/internal/storage"
)

// GitSource reads, per project with a checkout: commits on every branch a
// pm task names (`git log --since`), and the open pull requests of the repo
// (`gh pr list`). That is the v1 scope the user chose - branches pinned to
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
}

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
		prs, err := s.openPRs(ctx, p, from, to)
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

// prListFields is what both git (open PRs) and github (their discussions)
// ask gh for; one spelling so the two sources parse one shape.
const prListFields = "number,title,url,updatedAt,createdAt,headRefName,author,isDraft,reviewDecision"

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
	Author         struct {
		Login string `json:"login"`
	} `json:"author"`
}

// listOpenPRs runs `gh pr list` in the checkout. gh's own auth applies -
// the user's per-repo GH_TOKEN through direnv, which pm knows nothing
// about beyond running gh in the project's path.
func listOpenPRs(ctx context.Context, run Runner, p Project) ([]pr, error) {
	stdout, err := run(ctx, p.Path, "gh", "pr", "list", "--state", "open", "--limit", "50", "--json", prListFields)
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

// openPRs emits one event per open PR touched in the window.
func (s *GitSource) openPRs(ctx context.Context, p Project, from, to time.Time) ([]Event, error) {
	ctx, ok, err := githubContext(ctx, s.Run, p)
	if !ok {
		return nil, err
	}
	prs, err := listOpenPRs(ctx, s.Run, p)
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, r := range prs {
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
		out = append(out, Event{
			ID: EventID("git", "pr", p.Slug, fmt.Sprint(r.Number), r.UpdatedAt), TS: ts.Format(time.RFC3339),
			Project: p.Slug, Group: p.Group, TaskID: taskForBranch(p, r.HeadRefName), Title: r.Title,
			Detail: detail, URL: r.URL, Severity: SeverityInfo,
		})
	}
	return out, nil
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
