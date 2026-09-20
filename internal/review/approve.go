package review

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// Approving from the cockpit: the user reads a finished review and clicks
// approve. pm runs `gh pr review <n> --approve` - no body, no comment, an
// approve and nothing else - under the project's gh account, and when the
// request came from a Slack message it reacts to that message with ✅
// through the server that read it. slack-mcp-server keeps its reaction tools
// off by default; pm turns them on for its own subprocess only, limited to
// that one channel (SLACK_MCP_REACTION_TOOL=<channel>), never in the user's
// config. The outcome lives in files next to the review
// (<id>.approved / .approve_error / .reacted / .react_error).

// ApproveEmoji is the reaction added after an approve; StartEmoji (👀) the
// one added when a review starts from a Slack link, so the person who asked
// sees it is being looked at.
const (
	ApproveEmoji = "white_check_mark"
	StartEmoji   = "eyes"
)

// SlackTimeout caps one Slack server start + call.
const SlackTimeout = 90 * time.Second

// Approve approves the review's PR on GitHub and reacts on Slack. An already
// approved review is not approved twice; a failed reaction is retried.
func (c *Controller) Approve(ctx context.Context, id string) (*Review, error) {
	r, err := c.Get(id)
	if err != nil {
		return nil, err
	}
	if r.Approved == "" {
		if r.State != StateDone {
			return nil, &InputError{Msg: "only a finished review can approve its PR (this one is " + r.State + ")"}
		}
		proj, err := c.Store.GetProject(r.Project)
		if err != nil {
			return nil, err
		}
		pr := PR{Number: r.Number}
		if parts := strings.SplitN(r.Repo, "/", 2); len(parts) == 2 {
			pr.Owner, pr.Repo = parts[0], parts[1]
		}
		if err := c.approvePR(ctx, proj, pr); err != nil {
			_ = os.WriteFile(c.path(id, ".approve_error"), []byte(err.Error()+"\n"), 0o644)
			return nil, err
		}
		_ = os.Remove(c.path(id, ".approve_error"))
		if err := os.WriteFile(c.path(id, ".approved"), []byte(c.now().Format(time.RFC3339)+"\n"), 0o644); err != nil {
			return nil, err
		}
	}
	if r.Slack != nil && r.SlackReacted == "" {
		err := c.reactSlack(ctx, *r.Slack, ApproveEmoji)
		if err != nil && !strings.Contains(err.Error(), "already_reacted") {
			_ = os.WriteFile(c.path(id, ".react_error"), []byte(err.Error()+"\n"), 0o644)
		} else {
			_ = os.Remove(c.path(id, ".react_error"))
			_ = os.WriteFile(c.path(id, ".reacted"), []byte(c.now().Format(time.RFC3339)+"\n"), 0o644)
		}
	}
	return c.Get(id)
}

// applyApproval fills the approve fields from the review's files.
func (c *Controller) applyApproval(r *Review) {
	read := func(ext string) string {
		b, err := os.ReadFile(c.path(r.ID, ext))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	r.Approved, r.ApproveError = read(".approved"), read(".approve_error")
	r.SlackReacted, r.SlackReactError = read(".reacted"), read(".react_error")
	r.SlackSeen, r.SlackSeenError = read(".seen"), read(".seen_error")
}

// reactStarted puts 👀 on the Slack message a review was started from and
// records the outcome (<id>.seen / .seen_error). It runs in the background
// with its own context: starting the Slack server takes seconds, and the
// request that started the review has already been answered. The stamp uses
// time.Now, not c.now - the injected clock belongs to the caller's goroutine.
func (c *Controller) reactStarted(link SlackLink, id string) {
	err := c.reactSlack(context.Background(), link, StartEmoji)
	if err != nil && !strings.Contains(err.Error(), "already_reacted") {
		_ = os.WriteFile(c.path(id, ".seen_error"), []byte(err.Error()+"\n"), 0o644)
		return
	}
	_ = os.WriteFile(c.path(id, ".seen"), []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644)
}

func (c *Controller) approvePR(ctx context.Context, proj *storage.Project, pr PR) error {
	if c.ApprovePR != nil {
		return c.ApprovePR(ctx, proj, pr)
	}
	gh := c.GH
	if gh == "" {
		gh = "gh"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "pr", "review", strconv.Itoa(pr.Number), "--repo", pr.Slug(), "--approve")
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_PAGER=")
	if proj.GHAccount != "" {
		tok, err := c.ghToken(proj.GHAccount)
		if err != nil {
			return fmt.Errorf("gh_account %s: %w", proj.GHAccount, err)
		}
		cmd.Env = append(withoutKey(cmd.Env, "GH_TOKEN"), "GH_TOKEN="+tok)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &InputError{Msg: fmt.Sprintf("gh pr review --approve %s#%d failed: %s", pr.Slug(), pr.Number, firstLines(stderr.String()+" "+err.Error(), 1))}
	}
	return nil
}

func (c *Controller) reactSlack(ctx context.Context, link SlackLink, emoji string) error {
	if c.ReactSlack != nil {
		return c.ReactSlack(ctx, link, emoji)
	}
	if link.Server == "" {
		return fmt.Errorf("no Slack server recorded for the message")
	}
	ctx, cancel := context.WithTimeout(ctx, SlackTimeout)
	defer cancel()
	_, err := callSlack(ctx, link.Server, []string{"SLACK_MCP_REACTION_TOOL=" + link.Channel}, "reactions_add",
		map[string]any{"channel_id": link.Channel, "timestamp": link.TS, "emoji": emoji})
	return err
}
