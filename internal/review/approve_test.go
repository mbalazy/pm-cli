package review

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

func TestNoIssues(t *testing.T) {
	for report, want := range map[string]bool{
		"### Code review - PR #7\n\nNo issues found. Checked for bugs.":                                      true,
		"### Code review - PR #1005\n\nNo issues survived the confidence filter. Checked for bugs.":          true,
		"### Code review\n\nFound 2 issues:\n1. a bug\n\n(no issues found in tests is not the verdict line)": false,
		"": false,
	} {
		if got := noIssues(report); got != want {
			t.Errorf("%q = %v", report, got)
		}
	}
}

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
		approved = append(approved, pr.Text())
		return nil
	}
	c.ReactSlack = func(_ context.Context, l SlackLink, emoji string) error {
		if emoji == StartEmoji {
			return nil // the start's 👀, from its own goroutine - TestStartReactsEyes
		}
		if reactErr != nil {
			return reactErr
		}
		reacted = append(reacted, l.Server+" "+l.Channel+" "+l.TS+" "+emoji)
		return nil
	}
	c.ReadSlack = func(context.Context, SlackLink) (string, string, error) {
		return "anna: https://github.com/org/app/pull/7", "slack-work", nil
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
		r, err := c.Start(bg, "https://acme.slack.com/archives/C1/p1694012345123456")
		if err != nil {
			t.Fatal(err)
		}
		if r.Slack == nil || r.Slack.Server != "slack-work" {
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
		if strings.Join(approved, ",") != "org/app#7" || strings.Join(reacted, ",") != "slack-work C1 1694012345.123456 white_check_mark" {
			t.Fatalf("calls: approved %v reacted %v", approved, reacted)
		}
	})

	t.Run("a GitLab review approves its merge request", func(t *testing.T) {
		approved, reacted = nil, nil
		gitlabProject(t, c)
		r, err := c.Start(bg, "https://gitlab.com/acme-group/lending/acme-web/-/merge_requests/43")
		if err != nil {
			t.Fatal(err)
		}
		waitState(t, c, r.ID, StateDone)
		got, err := c.Approve(bg, r.ID)
		if err != nil || got.Approved == "" {
			t.Fatalf("got = %+v err = %v", got, err)
		}
		if strings.Join(approved, ",") != "acme-group/lending/acme-web!43" {
			t.Fatalf("approved %v", approved)
		}
	})

	t.Run("a failed reaction keeps the approve and is retried", func(t *testing.T) {
		approved, reacted = nil, nil
		r, _ := c.Start(bg, "https://acme.slack.com/archives/C1/p1694012345123456")
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

func TestStartReactsEyes(t *testing.T) {
	c, _, _ := setup(t)
	n := 0
	c.Now = func() time.Time { n++; return time.Date(2026, 9, 15, 9, 0, n, 0, time.Local) } // distinct ids
	calls := make(chan string, 4)
	var mu sync.Mutex
	var reactErr error
	c.ReactSlack = func(_ context.Context, l SlackLink, emoji string) error {
		calls <- l.Server + " " + l.Channel + " " + l.TS + " " + emoji
		mu.Lock()
		defer mu.Unlock()
		return reactErr
	}
	c.ReadSlack = func(context.Context, SlackLink) (string, string, error) {
		return "anna: https://github.com/org/app/pull/7", "slack-work", nil
	}
	const link = "https://acme.slack.com/archives/C1/p1694012345123456"
	nextCall := func() string {
		t.Helper()
		select {
		case got := <-calls:
			return got
		case <-time.After(10 * time.Second):
			t.Fatal("no reaction")
			return ""
		}
	}
	waitSeen := func(id string) *Review {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			r, err := c.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if r.SlackSeen != "" || r.SlackSeenError != "" || time.Now().After(deadline) {
				return r
			}
		}
	}

	r, err := c.Start(bg, link)
	if err != nil {
		t.Fatal(err)
	}
	if got := nextCall(); got != "slack-work C1 1694012345.123456 eyes" {
		t.Fatalf("reaction = %q", got)
	}
	if seen := waitSeen(r.ID); seen.SlackSeen == "" || seen.SlackSeenError != "" {
		t.Fatalf("seen = %+v", seen)
	}
	waitState(t, c, r.ID, StateDone)

	mu.Lock()
	reactErr = errors.New("reactions_add: not_in_channel")
	mu.Unlock()
	r2, err := c.Start(bg, link)
	if err != nil {
		t.Fatal(err)
	}
	nextCall()
	if seen := waitSeen(r2.ID); seen.SlackSeen != "" || !strings.Contains(seen.SlackSeenError, "not_in_channel") || seen.State == StateError {
		t.Fatalf("a failed 👀 must not fail the review: %+v", seen)
	}
	waitState(t, c, r2.ID, StateDone)

	r3, err := c.Start(bg, "https://github.com/org/app/pull/7")
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, c, r3.ID, StateDone)
	select {
	case got := <-calls:
		t.Fatalf("a PR URL input reacted: %s", got)
	default:
	}
}
