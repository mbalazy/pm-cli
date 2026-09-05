package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GitHubSource reads the discussion under the repo's open PRs: comments,
// reviews and the review decision (changes requested / approved), through
// `gh pr view --json` per PR. Only PRs touched in the window are opened -
// one gh call per PR is the cost, and a PR nobody wrote on since the cutoff
// has nothing new to say.
type GitHubSource struct {
	Run Runner
}

func (s *GitHubSource) Name() string { return "github" }

const prViewFields = "number,title,url,reviewDecision,comments,reviews"

type prView struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	ReviewDecision string `json:"reviewDecision"`
	Comments       []struct {
		ID        string `json:"id"`
		Body      string `json:"body"`
		CreatedAt string `json:"createdAt"`
		URL       string `json:"url"`
		Author    struct {
			Login string `json:"login"`
		} `json:"author"`
	} `json:"comments"`
	Reviews []struct {
		ID          string `json:"id"`
		State       string `json:"state"`
		Body        string `json:"body"`
		SubmittedAt string `json:"submittedAt"`
		URL         string `json:"url"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
	} `json:"reviews"`
}

func (s *GitHubSource) Fetch(ctx context.Context, from, to time.Time, projects []Project) ([]Event, error) {
	var out []Event
	var errs []error
	for _, p := range projects {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if p.Path == "" || !isGitRepo(p.Path) {
			continue // the git source already reports a bad checkout
		}
		prs, err := listOpenPRs(ctx, s.Run, p)
		if err != nil {
			errs = append(errs, &ProjectError{Project: p.Slug, Err: err})
			continue
		}
		for _, r := range prs {
			if _, touched := inWindow(r.UpdatedAt, from, to); !touched {
				continue
			}
			stdout, err := s.Run(ctx, p.Path, "gh", "pr", "view", fmt.Sprint(r.Number), "--json", prViewFields)
			if err != nil {
				errs = append(errs, &ProjectError{Project: p.Slug, Err: fmt.Errorf("PR #%d: %w", r.Number, err)})
				continue
			}
			var v prView
			if err := json.Unmarshal([]byte(stdout), &v); err != nil {
				errs = append(errs, &ProjectError{Project: p.Slug, Err: fmt.Errorf("PR #%d: unparsable gh output: %w", r.Number, err)})
				continue
			}
			taskID := taskForBranch(p, r.HeadRefName)
			for _, c := range v.Comments {
				ts, ok := inWindow(c.CreatedAt, from, to)
				if !ok {
					continue
				}
				out = append(out, Event{
					ID: EventID("github", "comment", p.Slug, fmt.Sprint(v.Number), c.ID), TS: ts.Format(time.RFC3339),
					Project: p.Slug, Group: p.Group, TaskID: taskID, Title: v.Title,
					Detail: fmt.Sprintf("PR #%d comment by %s: %s", v.Number, c.Author.Login, excerpt(c.Body)),
					URL:    firstNonEmpty(c.URL, v.URL), Severity: SeverityInfo,
				})
			}
			for _, rv := range v.Reviews {
				ts, ok := inWindow(rv.SubmittedAt, from, to)
				if !ok {
					continue
				}
				sev := SeverityInfo
				switch rv.State {
				case "CHANGES_REQUESTED":
					sev = SeverityWarn
				case "APPROVED":
					sev = SeverityOK
				}
				detail := fmt.Sprintf("PR #%d review by %s: %s", v.Number, rv.Author.Login, strings.ToLower(strings.ReplaceAll(rv.State, "_", " ")))
				if ex := excerpt(rv.Body); ex != "" {
					detail += " - " + ex
				}
				out = append(out, Event{
					ID: EventID("github", "review", p.Slug, fmt.Sprint(v.Number), rv.ID), TS: ts.Format(time.RFC3339),
					Project: p.Slug, Group: p.Group, TaskID: taskID, Title: v.Title,
					Detail: detail, URL: firstNonEmpty(rv.URL, v.URL), Severity: sev,
				})
			}
		}
	}
	return out, errors.Join(errs...)
}

func excerpt(body string) string {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[:i]
	}
	if len(body) > 140 {
		body = body[:137] + "..."
	}
	return body
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
