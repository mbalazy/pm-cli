package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func TestFindSlackLink(t *testing.T) {
	l, ok := findSlackLink("zobacz https://orbit.slack.com/archives/C07ABC123/p1694012345123456?thread_ts=1694000000.000100&cid=C07ABC123 prosze")
	if !ok || l.Workspace != "orbit" || l.Channel != "C07ABC123" || l.TS != "1694012345.123456" || l.ThreadTS != "1694000000.000100" {
		t.Fatalf("link = %+v %v", l, ok)
	}
	l, ok = findSlackLink("https://x.slack.com/archives/D01/p1694012345123456")
	if !ok || l.ThreadTS != l.TS {
		t.Fatalf("no thread_ts: %+v", l)
	}
	if _, ok := findSlackLink("https://github.com/o/r/pull/1"); ok {
		t.Fatal("github link read as slack")
	}
}

func TestResolve(t *testing.T) {
	c, _, _ := setup(t)
	var prompt string
	c.AskModel = func(_ context.Context, p string) (string, error) {
		prompt = p
		return "Sure:\n{\"repo\":\"org/app\",\"number\":555}", nil
	}

	t.Run("a PR URL inside a sentence needs no model", func(t *testing.T) {
		prompt = ""
		res, err := c.Resolve(bg, "zrob review https://github.com/Org/Other/pull/12/files pls")
		if err != nil || res.PR.Slug() != "Org/Other" || res.PR.Number != 12 || res.Slack != nil || prompt != "" {
			t.Fatalf("res = %+v err = %v prompt = %q", res, err, prompt)
		}
	})

	t.Run("free text goes to the model with the repositories", func(t *testing.T) {
		res, err := c.Resolve(bg, "pr 555 w repo app")
		if err != nil || res.PR.Slug() != "org/app" || res.PR.Number != 555 {
			t.Fatalf("res = %+v err = %v", res, err)
		}
		for _, want := range []string{"- org/app - app", "- org/other - other", "pr 555 w repo app"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("prompt lacks %q:\n%s", want, prompt)
			}
		}
	})

	t.Run("a Slack link with a PR URL in the message keeps the message", func(t *testing.T) {
		prompt = ""
		c.ReadSlack = func(_ context.Context, l SlackLink) (string, string, error) {
			if l.Channel != "C1" {
				t.Errorf("channel = %s", l.Channel)
			}
			return "ts,user,text\n1,anna,możesz zerknąć? https://github.com/org/app/pull/31", "slack-orbit", nil
		}
		res, err := c.Resolve(bg, "https://example.slack.com/archives/C1/p1694012345123456")
		if err != nil || res.PR.Number != 31 || !strings.Contains(res.SlackText, "zerknąć") || prompt != "" {
			t.Fatalf("res = %+v err = %v", res, err)
		}
		if res.Slack == nil || res.Slack.Server != "slack-orbit" || res.Slack.TS != "1694012345.123456" {
			t.Fatalf("slack = %+v", res.Slack)
		}
	})

	t.Run("a Slack message without a URL goes to the model with its text", func(t *testing.T) {
		c.ReadSlack = func(context.Context, SlackLink) (string, string, error) {
			return "anna: review PR 555 w mobile?", "slack-orbit", nil
		}
		res, err := c.Resolve(bg, "https://example.slack.com/archives/C1/p1694012345123456")
		if err != nil || res.PR.Number != 555 || res.Slack == nil || !strings.Contains(prompt, "anna: review PR 555 w mobile?") {
			t.Fatalf("res = %+v err = %v prompt = %q", res, err, prompt)
		}
	})

	t.Run("failures are input errors", func(t *testing.T) {
		var ie *InputError
		c.ReadSlack = func(context.Context, SlackLink) (string, string, error) { return "", "", errors.New("not_in_channel") }
		if _, err := c.Resolve(bg, "https://example.slack.com/archives/C1/p1694012345123456"); !errors.As(err, &ie) || !strings.Contains(err.Error(), "not_in_channel") {
			t.Fatalf("slack err = %v", err)
		}
		for answer, want := range map[string]string{
			`{"error":"no number"}`:         "no number",
			`{"repo":"evil/x","number":1}`:  "not a checked-out repository",
			`{"repo":"org/app","number":0}`: "not a checked-out repository",
			`I think it is app`:             "the model answered",
		} {
			c.AskModel = func(context.Context, string) (string, error) { return answer, nil }
			if _, err := c.Resolve(bg, "review something"); !errors.As(err, &ie) || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: err = %v", answer, err)
			}
		}
		if _, err := c.Resolve(bg, "  "); !errors.As(err, &ie) {
			t.Errorf("empty: %v", err)
		}
	})
}

func TestStartConfirmsThePRAndKeepsTheInput(t *testing.T) {
	c, _, _ := setup(t)
	c.AskModel = func(context.Context, string) (string, error) { return `{"repo":"org/app","number":9}`, nil }
	c.ViewPR = func(_ context.Context, p *storage.Project, pr PR) (string, error) {
		if pr.Number == 404 {
			return "", &InputError{Msg: "org/app#404: gh pr view failed: not found"}
		}
		return "Fix login", nil
	}
	r, err := c.Start(bg, "pr 9 w app")
	if err != nil {
		t.Fatal(err)
	}
	if r.Title != "Fix login" || r.Input != "pr 9 w app" || r.URL != "https://github.com/org/app/pull/9" {
		t.Fatalf("review = %+v", r)
	}
	waitState(t, c, r.ID, StateDone)
	got, _ := c.Get(r.ID)
	if got.Title != "Fix login" || got.Input != "pr 9 w app" || !got.NoIssues {
		t.Fatalf("stored = %+v", got)
	}
	r, err = c.Start(bg, "https://github.com/org/app/pull/5")
	if err != nil || r.Input != "" {
		t.Fatalf("bare URL keeps no input: %+v %v", r, err)
	}
	waitState(t, c, r.ID, StateDone)
	if _, err := c.Start(bg, "https://github.com/org/app/pull/404"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing PR: %v", err)
	}
}
