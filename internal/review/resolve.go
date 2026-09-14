package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/feed"
	"github.com/mbalazy/pm/internal/report"
	"github.com/mbalazy/pm/internal/storage"
)

// What the user pastes is not always a PR URL: it can be a sentence ("pr 555
// w repo orbit mobile") or a Slack link to a message asking for a review. The
// resolution runs in steps, cheapest first:
//
//  1. a GitHub PR URL anywhere in the text wins;
//  2. a Slack permalink is read through the user's Slack MCP server (the
//     servers registered in ~/.claude.json, tried in parallel - the first
//     that returns the message wins), and a PR URL in that message wins;
//  3. otherwise a one-turn, tool-less haiku maps the text (+ the Slack
//     message) to one of the projects' GitHub repositories and a PR number;
//
// and whatever came out is confirmed with `gh pr view` before a review
// starts, so a wrong guess is a 400 naming the PR, not a 20-minute review of
// nothing.

// ResolveModel is the model that reads a free-text request.
const ResolveModel = "haiku"

// ResolveTimeout caps the whole resolution (Slack server start, the model,
// gh) inside the POST.
const ResolveTimeout = 2 * time.Minute

var prURLAnywhere = regexp.MustCompile(`https?://(?:www\.)?github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/pull/(\d+)`)

// findPRURL returns the first GitHub PR URL in text.
func findPRURL(text string) (PR, bool) {
	m := prURLAnywhere.FindStringSubmatch(text)
	if m == nil {
		return PR{}, false
	}
	n, _ := strconv.Atoi(m[3])
	return PR{Owner: m[1], Repo: strings.TrimSuffix(m[2], ".git"), Number: n}, true
}

// SlackLink is a parsed Slack message permalink.
type SlackLink struct {
	Workspace string `json:"workspace"` // the subdomain
	Channel   string `json:"channel"`
	TS        string `json:"ts"`        // the message
	ThreadTS  string `json:"thread_ts"` // the thread it belongs to; the message itself when absent
	// Server is the Slack MCP server (a ~/.claude.json name) that read it.
	Server string `json:"server"`
}

var slackPermalink = regexp.MustCompile(`https://([a-z0-9-]+)\.slack\.com/archives/([A-Z0-9]+)/p(\d{10})(\d{6})(\S*)`)
var threadTSParam = regexp.MustCompile(`[?&]thread_ts=(\d+\.\d+)`)

// findSlackLink returns the first Slack message permalink in text.
func findSlackLink(text string) (SlackLink, bool) {
	m := slackPermalink.FindStringSubmatch(text)
	if m == nil {
		return SlackLink{}, false
	}
	l := SlackLink{Workspace: m[1], Channel: m[2], TS: m[3] + "." + m[4]}
	l.ThreadTS = l.TS
	if t := threadTSParam.FindStringSubmatch(m[5]); t != nil {
		l.ThreadTS = t[1]
	}
	return l, true
}

// candidate is a project whose checkout is a GitHub repository.
type candidate struct {
	Slug string
	Repo string // owner/repo, as spelled in the repo field or the remote
	Proj *storage.Project
}

// candidates lists the active projects with an existing checkout of a
// GitHub repository, in slug order: the repo field when it names one, else
// the checkout's first github.com remote.
func (c *Controller) candidates() ([]candidate, error) {
	slugs, err := c.Store.ListActiveProjects()
	if err != nil {
		return nil, err
	}
	sort.Strings(slugs)
	var out []candidate
	for _, s := range slugs {
		p, err := c.Store.GetProject(s)
		if err != nil || p.Archived || p.Path == "" {
			continue
		}
		if fi, err := os.Stat(p.Path); err != nil || !fi.IsDir() {
			continue
		}
		repo := repoSlug(p.Repo)
		if repo == "" {
			if out, err := exec.Command("git", "-C", p.Path, "remote", "-v").Output(); err == nil {
				for _, f := range strings.Fields(string(out)) {
					if repo = repoSlug(f); repo != "" {
						break
					}
				}
			}
		}
		if repo != "" {
			out = append(out, candidate{Slug: s, Repo: repo, Proj: p})
		}
	}
	return out, nil
}

// Resolution is what Resolve found: the PR, and the Slack message it came
// from when the input was a Slack link (the server that read it is the one
// that reacts to it after an approve).
type Resolution struct {
	PR        PR
	Slack     *SlackLink
	SlackText string
}

// Resolve turns the pasted input into a PR (see the steps above).
func (c *Controller) Resolve(ctx context.Context, input string) (Resolution, error) {
	text := strings.TrimSpace(input)
	if text == "" {
		return Resolution{}, &InputError{Msg: "paste a PR URL, a Slack link or a sentence naming the PR"}
	}
	var res Resolution
	request := text
	if link, ok := findSlackLink(text); ok {
		msg, server, err := c.readSlack(ctx, link)
		if err != nil {
			return Resolution{}, &InputError{Msg: "read the Slack message: " + err.Error()}
		}
		link.Server = server
		res.Slack, res.SlackText = &link, msg
		if pr, ok := findPRURL(msg); ok {
			res.PR = pr
			return res, nil
		}
		request = text + "\n\nThe linked Slack message (and its thread):\n" + msg
	}
	if pr, ok := findPRURL(text); ok {
		res.PR = pr
		return res, nil
	}
	cands, err := c.candidates()
	if err != nil {
		return res, err
	}
	if len(cands) == 0 {
		return res, &InputError{Msg: "no pm project checks out a GitHub repository"}
	}
	answer, err := c.askModel(ctx, resolvePrompt(request, cands))
	if err != nil {
		return res, fmt.Errorf("resolve the request with %s: %w", ResolveModel, err)
	}
	res.PR, err = parseResolveAnswer(answer, cands)
	return res, err
}

func resolvePrompt(request string, cands []candidate) string {
	var b strings.Builder
	b.WriteString("You map a request for a code review to exactly one GitHub pull request.\n\n")
	b.WriteString("Repositories checked out locally (owner/repo - project name - group - stack - tags):\n")
	for _, c := range cands {
		fmt.Fprintf(&b, "- %s - %s - %s - %s - %s\n", c.Repo, c.Proj.Name, c.Proj.GroupSlug(c.Slug), c.Proj.Stack, strings.Join(c.Proj.Tags, ", "))
	}
	b.WriteString("\nRequest (may be Polish, may abbreviate names: \"orb\" = orbit, \"mobile\"/\"app\" = the React Native app, \"web\" = the web app):\n")
	b.WriteString(request)
	b.WriteString("\n\nAnswer with ONE line of JSON and nothing else: {\"repo\":\"owner/repo\",\"number\":123} with a repo from the list above, " +
		"or {\"error\":\"<one sentence why>\"} when the request names no PR number or no listed repository fits.\n")
	return b.String()
}

// parseResolveAnswer reads the model's JSON line and checks it names a
// listed repository and a number.
func parseResolveAnswer(answer string, cands []candidate) (PR, error) {
	i, j := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if i < 0 || j < i {
		return PR{}, &InputError{Msg: "could not tell which PR is meant (the model answered: " + firstLines(answer, 1) + ")"}
	}
	var got struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(answer[i:j+1]), &got); err != nil {
		return PR{}, &InputError{Msg: "could not tell which PR is meant (the model answered: " + firstLines(answer, 1) + ")"}
	}
	if got.Error != "" {
		return PR{}, &InputError{Msg: "could not tell which PR is meant: " + got.Error}
	}
	for _, c := range cands {
		if strings.EqualFold(c.Repo, got.Repo) && got.Number > 0 {
			parts := strings.SplitN(c.Repo, "/", 2)
			return PR{Owner: parts[0], Repo: parts[1], Number: got.Number}, nil
		}
	}
	return PR{}, &InputError{Msg: fmt.Sprintf("could not tell which PR is meant (got %q #%d, not a checked-out repository)", got.Repo, got.Number)}
}

func (c *Controller) askModel(ctx context.Context, prompt string) (string, error) {
	if c.AskModel != nil {
		return c.AskModel(ctx, prompt)
	}
	oc, err := (&report.Writer{}).Run(ctx, prompt, ResolveModel)
	if err != nil {
		return "", err
	}
	return oc.Text, nil
}

// readSlack returns the message's text and the server that read it.
func (c *Controller) readSlack(ctx context.Context, link SlackLink) (string, string, error) {
	if c.ReadSlack != nil {
		return c.ReadSlack(ctx, link)
	}
	names, err := feed.ClaudeServerNames("")
	if err != nil {
		return "", "", err
	}
	var slack []string
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), "slack") {
			slack = append(slack, n)
		}
	}
	if len(slack) == 0 {
		return "", "", errors.New("no Slack MCP server in ~/.claude.json")
	}
	// Which server is which workspace is not written anywhere: ask them all,
	// the first that has the message wins.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		server, text string
		err          error
	}
	results := make(chan result, len(slack))
	for _, name := range slack {
		go func(name string) {
			text, err := callSlack(ctx, name, nil, "conversations_replies", map[string]any{"channel_id": link.Channel, "thread_ts": link.ThreadTS})
			if err != nil {
				err = fmt.Errorf("%s: %w", name, err)
			}
			results <- result{name, text, err}
		}(name)
	}
	var errs []error
	for range slack {
		r := <-results
		if r.err == nil && strings.TrimSpace(r.text) != "" {
			return r.text, r.server, nil
		}
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	if len(errs) == 0 {
		return "", "", errors.New("the message is empty")
	}
	return "", "", errors.Join(errs...)
}

// callSlack runs one tool on a Slack MCP server from ~/.claude.json, with
// extra environment for that process only.
func callSlack(ctx context.Context, server string, env []string, tool string, args map[string]any) (string, error) {
	spec, err := feed.ResolveServer(storage.SlackServer{ClaudeServer: server}, "")
	if err != nil {
		return "", err
	}
	spec.Env = append(spec.Env, env...)
	s, err := feed.DialStdio(ctx, spec)
	if err != nil {
		return "", err
	}
	defer s.Close()
	return s.CallText(ctx, tool, args)
}

// viewPR confirms the PR exists and returns its title.
func (c *Controller) viewPR(ctx context.Context, proj *storage.Project, pr PR) (string, error) {
	if c.ViewPR != nil {
		return c.ViewPR(ctx, proj, pr)
	}
	gh := c.GH
	if gh == "" {
		gh = "gh"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "pr", "view", strconv.Itoa(pr.Number), "--repo", pr.Slug(), "--json", "title", "--jq", ".title")
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_PAGER=")
	if proj.GHAccount != "" {
		tok, err := c.ghToken(proj.GHAccount)
		if err != nil {
			return "", fmt.Errorf("gh_account %s: %w", proj.GHAccount, err)
		}
		cmd.Env = append(withoutKey(cmd.Env, "GH_TOKEN"), "GH_TOKEN="+tok)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &InputError{Msg: fmt.Sprintf("%s#%d: gh pr view failed: %s", pr.Slug(), pr.Number, firstLines(stderr.String()+" "+err.Error(), 1))}
	}
	return strings.TrimSpace(string(out)), nil
}
