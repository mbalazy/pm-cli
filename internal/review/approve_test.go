package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func TestApprove(t *testing.T) {
	c, _, _ := setup(t)
	n := 0
	c.Now = func() time.Time { n++; return time.Date(2026, 9, 14, 12, 0, n, 0, time.Local) } // distinct ids
	var approved []string
	var reacted []string
	approveErr, reactErr := error(nil), error(nil)
	c.ApprovePR = func(_ context.Context, _ *storage.Project, pr PR) error {
		if approveErr != nil {
			return approveErr
		}
		approved = append(approved, fmt.Sprintf("%s#%d", pr.Slug(), pr.Number))
		return nil
	}
	c.ReactSlack = func(_ context.Context, l SlackLink, emoji string) error {
		if reactErr != nil {
			return reactErr
		}
		reacted = append(reacted, l.Server+" "+l.Channel+" "+l.TS+" "+emoji)
		return nil
	}
	c.ReadSlack = func(context.Context, SlackLink) (string, string, error) {
		return "anna: https://github.com/org/app/pull/7", "slack-orbit", nil
	}

	t.Run("a running review cannot approve", func(t *testing.T) {
		t.Setenv("FAKE_MODE", "hang")
		r, err := c.Start(bg, "https://github.com/org/app/pull/7")
		if err != nil {
			t.Fatal(err)
		}
		var ie *InputError
		if _, err := c.Approve(bg, r.ID); !errors.As(err, &ie) || len(approved) != 0 {
			t.Fatalf("approve while running = %v %v", err, approved)
		}
		_, _ = c.Cancel(r.ID)
		waitState(t, c, r.ID, StateError)
	})

	t.Run("approve + the ✅ on the Slack message, once", func(t *testing.T) {
		r, err := c.Start(bg, "https://example.slack.com/archives/C1/p1694012345123456")
		if err != nil {
			t.Fatal(err)
		}
		if r.Slack == nil || r.Slack.Server != "slack-orbit" {
			t.Fatalf("slack = %+v", r.Slack)
		}
		if done := waitState(t, c, r.ID, StateDone); !done.NoIssues || done.Slack == nil {
			t.Fatalf("done = %+v", done)
		}
		got, err := c.Approve(bg, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Approved == "" || got.SlackReacted == "" || got.SlackReactError != "" {
			t.Fatalf("approved = %+v", got)
		}
		if _, err := c.Approve(bg, r.ID); err != nil {
			t.Fatal(err)
		}
		if strings.Join(approved, ",") != "org/app#7" || strings.Join(reacted, ",") != "slack-orbit C1 1694012345.123456 white_check_mark" {
			t.Fatalf("calls: approved %v reacted %v", approved, reacted)
		}
	})

	t.Run("a failed reaction keeps the approve and is retried", func(t *testing.T) {
		approved, reacted = nil, nil
		r, _ := c.Start(bg, "https://example.slack.com/archives/C1/p1694012345123456")
		waitState(t, c, r.ID, StateDone)
		reactErr = errors.New("reactions_add: not_in_channel")
		got, err := c.Approve(bg, r.ID)
		if err != nil || got.Approved == "" || got.SlackReacted != "" || !strings.Contains(got.SlackReactError, "not_in_channel") {
			t.Fatalf("got = %+v err = %v", got, err)
		}
		reactErr = nil
		got, _ = c.Approve(bg, r.ID)
		if got.SlackReacted == "" || got.SlackReactError != "" || len(approved) != 1 || len(reacted) != 1 {
			t.Fatalf("retry = %+v approved %v reacted %v", got, approved, reacted)
		}
	})

	t.Run("a failed approve is recorded, nothing reacts, a URL input never reacts", func(t *testing.T) {
		approved, reacted = nil, nil
		r, _ := c.Start(bg, "https://github.com/org/app/pull/7")
		waitState(t, c, r.ID, StateDone)
		approveErr = &InputError{Msg: "Can not approve your own pull request"}
		if _, err := c.Approve(bg, r.ID); err == nil {
			t.Fatal("approve error swallowed")
		}
		got, _ := c.Get(r.ID)
		if got.Approved != "" || !strings.Contains(got.ApproveError, "your own pull request") {
			t.Fatalf("got = %+v", got)
		}
		approveErr = nil
		got, err := c.Approve(bg, r.ID)
		if err != nil || got.Approved == "" || got.ApproveError != "" || len(reacted) != 0 {
			t.Fatalf("got = %+v err = %v reacted %v", got, err, reacted)
		}
	})
}
