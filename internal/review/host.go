package review

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Everything that differs between the code hosts a review runs against lives
// in this file: how a change request's URL is spelled, which ref carries its
// head, which CLI reads and approves it, and which of that CLI's commands
// the headless reviewer may run. A third host is one entry in `hosts` -
// nothing outside this file spells github or gitlab.

// Host names as stored on a review. An empty one reads as GitHub: every
// review written before GitLab support was a GitHub one, and pm never
// migrates its files.
const (
	HostGitHub = "github"
	HostGitLab = "gitlab"
)

// Host is one code host.
type Host struct {
	// Name is the stored id, Display the human spelling.
	Name    string
	Display string
	Domain  string
	// Unit is what the host calls a change request; Sigil how it writes the
	// number after the project path (o/r#7 on GitHub, group/sub/repo!43 on
	// GitLab).
	Unit  string
	Sigil string
	// CLI is the command line that reads it.
	CLI string
	// seg is the URL segment between the project path and the number; ref
	// the ref origin serves that change's head at.
	seg string
	ref string
	// pattern matches one change URL: group 1 the project path, group 2 the
	// number.
	pattern string
	// segments is how many path segments a repo URL or git remote of this
	// host carries; 0 = all of them (GitLab nests groups).
	segments int
	// read is this CLI's read-only commands, given to a review that runs
	// against this host; write is its writing commands, denied to EVERY
	// review whichever host it runs against - a reviewer has no writing
	// command of any host.
	read  []string
	write []string

	re, reFull *regexp.Regexp
}

// GitHub and GitLab are the hosts pm reviews from.
var (
	GitHub = &Host{
		Name: HostGitHub, Display: "GitHub", Domain: "github.com",
		Unit: "pull request", Sigil: "#", CLI: "gh",
		seg: "pull", ref: "refs/pull/%d/head", segments: 2,
		pattern: `https?://(?:www\.)?github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/(\d+)`,
		read: []string{
			"Bash(gh pr view:*)", "Bash(gh pr diff:*)", "Bash(gh pr list:*)", "Bash(gh pr checks:*)",
			"Bash(gh issue view:*)", "Bash(gh issue list:*)", "Bash(gh search:*)", "Bash(gh api:*)",
		},
		write: []string{
			"Bash(gh pr checkout:*)", "Bash(gh pr review:*)", "Bash(gh pr comment:*)", "Bash(gh pr merge:*)",
			"Bash(gh pr edit:*)", "Bash(gh pr close:*)", "Bash(gh pr ready:*)", "Bash(gh issue comment:*)",
			"Bash(gh api *-X*)", "Bash(gh api *--method*)", "Bash(gh api *-f *)", "Bash(gh api *-F *)",
			"Bash(gh api *--field*)", "Bash(gh api *--raw-field*)", "Bash(gh api *--input*)",
		},
	}
	// GitLab: the project path nests groups (hwis-tft-group/poland-mvp-lending/
	// flash-lending-poc), which is why a PR carries one Path and not an owner
	// and a repo, and the URL puts `/-/` between the path and the unit.
	GitLab = &Host{
		Name: HostGitLab, Display: "GitLab", Domain: "gitlab.com",
		Unit: "merge request", Sigil: "!", CLI: "glab",
		seg: "-/merge_requests", ref: "refs/merge-requests/%d/head", segments: 0,
		pattern: `https?://(?:www\.)?gitlab\.com/([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+?)/-/merge_requests/(\d+)`,
		read: []string{
			"Bash(glab mr view:*)", "Bash(glab mr diff:*)", "Bash(glab mr list:*)", "Bash(glab mr approvers:*)",
			"Bash(glab issue view:*)", "Bash(glab issue list:*)", "Bash(glab search:*)", "Bash(glab api:*)",
		},
		write: []string{
			"Bash(glab mr approve:*)", "Bash(glab mr revoke:*)", "Bash(glab mr note:*)", "Bash(glab mr merge:*)",
			"Bash(glab mr update:*)", "Bash(glab mr close:*)", "Bash(glab mr reopen:*)", "Bash(glab mr create:*)",
			"Bash(glab mr delete:*)", "Bash(glab mr rebase:*)", "Bash(glab mr checkout:*)", "Bash(glab mr todo:*)",
			"Bash(glab mr for:*)", "Bash(glab mr subscribe:*)", "Bash(glab mr unsubscribe:*)",
			"Bash(glab issue note:*)", "Bash(glab issue create:*)", "Bash(glab issue update:*)",
			"Bash(glab issue close:*)", "Bash(glab issue reopen:*)", "Bash(glab issue delete:*)",
			"Bash(glab api *-X*)", "Bash(glab api *--method*)", "Bash(glab api *-f *)", "Bash(glab api *-F *)",
			"Bash(glab api *--field*)", "Bash(glab api *--raw-field*)", "Bash(glab api *--form*)",
			"Bash(glab api *--input*)",
		},
	}
	hosts = []*Host{GitHub, GitLab}
)

func init() {
	for _, h := range hosts {
		h.re = regexp.MustCompile(h.pattern)
		h.reFull = regexp.MustCompile(`^` + h.pattern + `(?:[/?#].*)?$`)
	}
}

// HostByName maps a stored host name to its host. Anything unknown - an
// empty field on a review written before GitLab support among it - is
// GitHub.
func HostByName(name string) *Host {
	for _, h := range hosts {
		if h.Name == name {
			return h
		}
	}
	return GitHub
}

// URL is the canonical URL of one change request.
func (h *Host) URL(path string, number int) string {
	return "https://" + h.Domain + "/" + path + "/" + h.seg + "/" + strconv.Itoa(number)
}

// Ref is the ref origin serves a change's head at, so a review can be
// prepared without anyone checking the branch out.
func (h *Host) Ref(number int) string { return fmt.Sprintf(h.ref, number) }

// AllowedTools is what a review of this host may run: the host-neutral list
// plus that host's read-only CLI.
func (h *Host) AllowedTools() []string {
	return append(append([]string{}, AllowedTools...), h.read...)
}

// DisallowedTools is what it may never run: the host-neutral writes plus
// EVERY host's writing commands - a reviewer holds no command that writes
// anywhere, whichever host it was started for.
func (h *Host) DisallowedTools() []string {
	out := append([]string{}, DisallowedTools...)
	for _, other := range hosts {
		out = append(out, other.write...)
	}
	return out
}

// Prompt is what the headless review is asked. The user's /review command is
// worded for GitHub and its gh commands (it lives in ~/.claude/commands and
// is not pm's to rewrite), so a review on any other host carries one line
// naming the CLI that can read this change and restating that writes are
// denied - cheaper than letting the reviewer discover it by running gh
// against a repository gh cannot see.
func (h *Host) Prompt(pr PR) string {
	p := "/review " + h.URL(pr.Path, pr.Number)
	if h == GitHub {
		return p
	}
	return p + fmt.Sprintf("\n\nThis is a %s %s, not a GitHub pull request: `gh` cannot see it. "+
		"Read it with `%s mr view %d -R %s`, `%s mr diff %d -R %s` and `%s mr list -R %s`. "+
		"Every writing command is denied (no `%s mr note`, `approve` or `merge`) - report in this conversation only, as the command says.",
		h.Display, h.Unit, h.CLI, pr.Number, pr.Path, h.CLI, pr.Number, pr.Path, h.CLI, pr.Path, h.CLI)
}

// repoPath reduces a repo URL or git remote (https, ssh, scp form) on this
// host to its lower-case project path; anything else to "".
func (h *Host) repoPath(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	i := strings.Index(s, h.Domain)
	if i < 0 {
		return ""
	}
	s = strings.TrimLeft(s[i+len(h.Domain):], ":/")
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return ""
	}
	if h.segments > 0 && len(parts) > h.segments {
		parts = parts[:h.segments]
	}
	for _, p := range parts {
		if p == "" {
			return ""
		}
	}
	return strings.Join(parts, "/")
}

// splitRepo reads a repo URL or git remote and returns the host it names and
// the project path on it; a nil host when it is no host pm knows.
func splitRepo(s string) (*Host, string) {
	for _, h := range hosts {
		if p := h.repoPath(s); p != "" {
			return h, p
		}
	}
	return nil, ""
}

// PR is one change request to review: a GitHub pull request or a GitLab
// merge request. Path is the project's full path on the host - two segments
// on GitHub (owner/repo), any number on GitLab (group/subgroup/project),
// which is why it is one string and not an owner plus a repo.
type PR struct {
	Host   *Host
	Path   string
	Number int
}

// Text is how the host writes this change: o/r#7, group/sub/repo!43.
func (p PR) Text() string {
	return p.Path + p.host().Sigil + strconv.Itoa(p.Number)
}

// URL is its canonical URL.
func (p PR) URL() string { return p.host().URL(p.Path, p.Number) }

func (p PR) host() *Host {
	if p.Host == nil {
		return GitHub
	}
	return p.Host
}

// ParseURL reads a URL that is nothing but one change request; anything
// after the number (/changes, /files, a query) is ignored.
func ParseURL(raw string) (PR, error) {
	raw = strings.TrimSpace(raw)
	for _, h := range hosts {
		if m := h.reFull.FindStringSubmatch(raw); m != nil {
			n, _ := strconv.Atoi(m[2])
			return PR{Host: h, Path: strings.TrimSuffix(m[1], ".git"), Number: n}, nil
		}
	}
	return PR{}, &InputError{Msg: fmt.Sprintf("not a pull request or merge request URL: %q (want https://github.com/<owner>/<repo>/pull/<n> or https://gitlab.com/<group>/<project>/-/merge_requests/<n>)", raw)}
}

// findChange returns the first change request URL in a text, whichever host
// it is on - the earliest in the text wins.
func findChange(text string) (PR, bool) {
	found, at := PR{}, -1
	for _, h := range hosts {
		loc := h.re.FindStringSubmatchIndex(text)
		if loc == nil || (at >= 0 && loc[0] >= at) {
			continue
		}
		n, _ := strconv.Atoi(text[loc[4]:loc[5]])
		found, at = PR{Host: h, Path: strings.TrimSuffix(text[loc[2]:loc[3]], ".git"), Number: n}, loc[0]
	}
	return found, at >= 0
}
