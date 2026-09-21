package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

func TestFindSlackLink(t *testing.T) {
	l, ok := findSlackLink("zobacz https://acme.slack.com/archives/C07ABC123/p1694012345123456?thread_ts=1694000000.000100&cid=C07ABC123 prosze")
	if !ok || l.Workspace != "acme" || l.Channel != "C07ABC123" || l.TS != "1694012345.123456" || l.ThreadTS != "1694000000.000100" {
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
		if err != nil || res.PR.Path != "Org/Other" || res.PR.Number != 12 || res.Slack != nil || prompt != "" {
			t.Fatalf("res = %+v err = %v prompt = %q", res, err, prompt)
		}
	})

	t.Run("free text goes to the model with the repositories", func(t *testing.T) {
		res, err := c.Resolve(bg, "pr 555 w repo app")
		if err != nil || res.PR.Path != "org/app" || res.PR.Number != 555 {
			t.Fatalf("res = %+v err = %v", res, err)
		}
		for _, want := range []string{"- github.com/org/app - app", "- github.com/org/other - other", "pr 555 w repo app"} {
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
			return "ts,user,text\n1,anna,możesz zerknąć? https://github.com/org/app/pull/31", "slack-work", nil
		}
		res, err := c.Resolve(bg, "https://acme.slack.com/archives/C1/p1694012345123456")
		if err != nil || res.PR.Number != 31 || !strings.Contains(res.SlackText, "zerknąć") || prompt != "" {
			t.Fatalf("res = %+v err = %v", res, err)
		}
		if res.Slack == nil || res.Slack.Server != "slack-work" || res.Slack.TS != "1694012345.123456" {
			t.Fatalf("slack = %+v", res.Slack)
		}
	})

	t.Run("a Slack message without a URL goes to the model with its text", func(t *testing.T) {
		c.ReadSlack = func(context.Context, SlackLink) (string, string, error) {
			return "anna: review PR 555 w mobile?", "slack-work", nil
		}
		res, err := c.Resolve(bg, "https://acme.slack.com/archives/C1/p1694012345123456")
		if err != nil || res.PR.Number != 555 || res.Slack == nil || !strings.Contains(prompt, "anna: review PR 555 w mobile?") {
			t.Fatalf("res = %+v err = %v prompt = %q", res, err, prompt)
		}
	})

	t.Run("a GitLab merge request URL resolves like a PR URL", func(t *testing.T) {
		gitlabProject(t, c)
		prompt = ""
		res, err := c.Resolve(bg, "moge prosic o review https://gitlab.com/acme-group/lending/acme-web/-/merge_requests/43 ?")
		if err != nil || res.PR.Host != GitLab || res.PR.Path != "acme-group/lending/acme-web" || res.PR.Number != 43 || prompt != "" {
			t.Fatalf("res = %+v err = %v prompt = %q", res, err, prompt)
		}
		// And from a Slack message, with the model still untouched.
		c.ReadSlack = func(context.Context, SlackLink) (string, string, error) {
			return "ts,user,text\n1,reviewer-two,!43 is waiting for approval https://gitlab.com/acme-group/lending/acme-web/-/merge_requests/43", "slack-work", nil
		}
		res, err = c.Resolve(bg, "https://acme.slack.com/archives/C1/p1694012345123456")
		if err != nil || res.PR.Host != GitLab || res.PR.Number != 43 || res.Slack == nil || prompt != "" {
			t.Fatalf("slack res = %+v err = %v", res, err)
		}
		// Free text names the GitLab repository by its listed spelling or by
		// its path alone; both are the same candidate.
		for _, answer := range []string{`{"repo":"gitlab.com/acme-group/lending/acme-web","number":43}`, `{"repo":"acme-group/lending/acme-web","number":43}`} {
			c.AskModel = func(context.Context, string) (string, error) { return answer, nil }
			res, err := c.Resolve(bg, "review the web MR")
			if err != nil || res.PR.Host != GitLab || res.PR.Text() != "acme-group/lending/acme-web!43" {
				t.Fatalf("%s: res = %+v err = %v", answer, res, err)
			}
		}
	})

	t.Run("failures are input errors", func(t *testing.T) {
		var ie *InputError
		c.ReadSlack = func(context.Context, SlackLink) (string, string, error) { return "", "", errors.New("not_in_channel") }
		if _, err := c.Resolve(bg, "https://acme.slack.com/archives/C1/p1694012345123456"); !errors.As(err, &ie) || !strings.Contains(err.Error(), "not_in_channel") {
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

// hostCmd is the only place pm itself talks to a host: one read (the title)
// and one write (the approve), per host, with the project path as given -
// nested GitLab groups included.
func TestHostCmdArgv(t *testing.T) {
	c, _, _ := setup(t)
	c.GH, c.Glab = "/bin/gh", "/bin/glab"
	for _, tc := range []struct {
		pr     PR
		action string
		want   string
	}{
		{PR{Host: GitHub, Path: "org/app", Number: 7}, "view", "/bin/gh pr view 7 --repo org/app --json title --jq .title"},
		{PR{Host: GitHub, Path: "org/app", Number: 7}, "approve", "/bin/gh pr review 7 --repo org/app --approve"},
		{PR{Host: GitLab, Path: "acme-group/lending/acme-web", Number: 43}, "view", "/bin/glab mr view 43 -R acme-group/lending/acme-web -F json --jq .title"},
		{PR{Host: GitLab, Path: "acme-group/lending/acme-web", Number: 43}, "approve", "/bin/glab mr approve 43 -R acme-group/lending/acme-web"},
	} {
		cmd, err := c.hostCmd(bg, &storage.Project{}, tc.pr, tc.action)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(cmd.Args, " "); got != tc.want {
			t.Errorf("%s %s:\n got %s\nwant %s", tc.pr.Text(), tc.action, got, tc.want)
		}
	}
}
