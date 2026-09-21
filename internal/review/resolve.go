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

	"github.com/mbalazy/pm-cli/internal/feed"
	"github.com/mbalazy/pm-cli/internal/report"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// What the user pastes is not always the URL of a pull or merge request: it
// can be a sentence ("pr 555 in the mobile repo") or a Slack link to a
// message asking for a review. The resolution runs in steps, cheapest first:
//
//  1. a GitHub PR or GitLab MR URL anywhere in the text wins (the earliest
//     one);
//  2. a Slack permalink is read through the user's Slack MCP server (the
//     servers registered in ~/.claude.json, tried in parallel - the first
//     that returns the message wins), and such a URL in that message wins;
//  3. otherwise a one-turn, tool-less haiku maps the text (+ the Slack
//     message) to one of the projects' repositories and a number;
//
// and whatever came out is confirmed with `gh pr view` / `glab mr view`
// before a review starts, so a wrong guess is a 400 naming the change, not a
// 20-minute review of nothing.

// ResolveModel is the model that reads a free-text request.
const ResolveModel = "haiku"

// ResolveTimeout caps the whole resolution (Slack server start, the model,
// gh) inside the POST.
const ResolveTimeout = 2 * time.Minute

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

// candidate is a project whose checkout is a repository on a host pm knows.
type candidate struct {
	Slug string
	Host *Host
	Path string // the project path on that host, lower case
	Proj *storage.Project
}

// Key is how a candidate is listed to the model and matched against its
// answer: the host's domain and the project path, so two repositories of the
// same name on different hosts stay apart.
func (c candidate) Key() string { return c.Host.Domain + "/" + c.Path }

// candidates lists the active projects with an existing checkout of a
// repository on a host pm knows, in slug order: the repo field when it names
// one, else the checkout's first remote on such a host.
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
		host, path := splitRepo(p.Repo)
		if host == nil {
			if out, err := exec.Command("git", "-C", p.Path, "remote", "-v").Output(); err == nil {
				for _, f := range strings.Fields(string(out)) {
					if host, path = splitRepo(f); host != nil {
						break
					}
				}
			}
		}
		if host != nil {
			out = append(out, candidate{Slug: s, Host: host, Path: path, Proj: p})
		}
	}
	return out, nil
}

// Resolution is what Resolve found: the change request, and the Slack
// message it came from when the input was a Slack link (the server that read
// it is the one that reacts to it after an approve).
type Resolution struct {
	PR        PR
	Slack     *SlackLink
	SlackText string
}

// Resolve turns the pasted input into one change request (see the steps
// above).
func (c *Controller) Resolve(ctx context.Context, input string) (Resolution, error) {
	text := strings.TrimSpace(input)
	if text == "" {
		return Resolution{}, &InputError{Msg: "paste a pull request or merge request URL, a Slack link or a sentence naming it"}
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
		if pr, ok := findChange(msg); ok {
			res.PR = pr
			return res, nil
		}
		request = text + "\n\nThe linked Slack message (and its thread):\n" + msg
	}
	if pr, ok := findChange(text); ok {
		res.PR = pr
		return res, nil
	}
	cands, err := c.candidates()
	if err != nil {
		return res, err
	}
	if len(cands) == 0 {
		return res, &InputError{Msg: "no pm project checks out a GitHub or GitLab repository"}
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
	b.WriteString("You map a request for a code review to exactly one GitHub pull request or GitLab merge request.\n\n")
	b.WriteString("Repositories checked out locally (repository - project name - group - stack - tags):\n")
	for _, c := range cands {
		fmt.Fprintf(&b, "- %s - %s - %s - %s - %s\n", c.Key(), c.Proj.Name, c.Proj.GroupSlug(c.Slug), c.Proj.Stack, strings.Join(c.Proj.Tags, ", "))
	}
	b.WriteString("\nRequest (may be in any language, and may name a repo by its project name, its group or its stack rather than by its path):\n")
	b.WriteString(request)
	b.WriteString("\n\nAnswer with ONE line of JSON and nothing else: {\"repo\":\"<repository exactly as listed above>\",\"number\":123}, " +
		"or {\"error\":\"<one sentence why>\"} when the request names no number or no listed repository fits.\n")
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
		// The model is asked for the listed spelling (host domain + path) but
		// often answers with the path alone; both name one candidate.
		if (strings.EqualFold(c.Key(), got.Repo) || strings.EqualFold(c.Path, got.Repo)) && got.Number > 0 {
			return PR{Host: c.Host, Path: c.Path, Number: got.Number}, nil
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

// viewPR confirms the change exists and returns its title, through the CLI
// of its host.
func (c *Controller) viewPR(ctx context.Context, proj *storage.Project, pr PR) (string, error) {
	if c.ViewPR != nil {
		return c.ViewPR(ctx, proj, pr)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd, err := c.hostCmd(ctx, proj, pr, "view")
	if err != nil {
		return "", err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &InputError{Msg: fmt.Sprintf("%s: %s failed: %s", pr.Text(), strings.Join(cmd.Args[:3], " "), firstLines(stderr.String()+" "+err.Error(), 1))}
	}
	return strings.TrimSpace(string(out)), nil
}

// hostCmd builds the one read ("view", the title) or the one write
// ("approve") pm itself runs against a host, under the project's account.
func (c *Controller) hostCmd(ctx context.Context, proj *storage.Project, pr PR, action string) (*exec.Cmd, error) {
	n := strconv.Itoa(pr.Number)
	var cmd *exec.Cmd
	if pr.Host == GitLab {
		glab := c.Glab
		if glab == "" {
			glab = "glab"
		}
		args := []string{"mr", "approve", n, "-R", pr.Path}
		if action == "view" {
			args = []string{"mr", "view", n, "-R", pr.Path, "-F", "json", "--jq", ".title"}
		}
		cmd = exec.CommandContext(ctx, glab, args...)
		// glab reads the user's own login (keyring or GITLAB_TOKEN); there is
		// no `glab auth token` to pin an account with, the way gh_account does.
		cmd.Env = append(os.Environ(), "NO_COLOR=1", "GLAB_CHECK_UPDATE=0")
		return cmd, nil
	}
	gh := c.GH
	if gh == "" {
		gh = "gh"
	}
	args := []string{"pr", "review", n, "--repo", pr.Path, "--approve"}
	if action == "view" {
		args = []string{"pr", "view", n, "--repo", pr.Path, "--json", "title", "--jq", ".title"}
	}
	cmd = exec.CommandContext(ctx, gh, args...)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_PAGER=")
	if proj.GHAccount != "" {
		tok, err := c.ghToken(proj.GHAccount)
		if err != nil {
			return nil, fmt.Errorf("gh_account %s: %w", proj.GHAccount, err)
		}
		cmd.Env = append(withoutKey(cmd.Env, "GH_TOKEN"), "GH_TOKEN="+tok)
	}
	return cmd, nil
}
