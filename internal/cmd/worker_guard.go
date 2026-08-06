package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// The worker guard is pm's PreToolUse hook for headless workers: it inspects
// every Bash command the worker is about to run and blocks the ones that would
// neuter the project's git hooks.
//
// Why a hook and not just --disallowedTools: permission patterns match a command
// PREFIX, so `Bash(git commit --no-verify:*)` catches
// `git commit --no-verify -m x` and misses `git commit -m x --no-verify` - which
// is the form a model reaches for anyway. Measured 2026-08-06 against a probe
// repo with a deliberately failing pre-commit hook: the trailing-flag form ran
// and committed under the full disallow list, and was blocked once this guard
// was attached. `--settings` merges with the project's own settings.json (its
// hooks still fire), so attaching this costs the project nothing.

// hookBypassPatterns are the ways a worker can get a commit past the project's
// hooks. Each carries the shape it catches; the guard reports the match so the
// worker learns which door it tried rather than guessing.
var hookBypassPatterns = []struct {
	re     *regexp.Regexp
	what   string
	scoped bool // only meaningful on a git commit/push - `-n` is a common flag elsewhere
}{
	{re: regexp.MustCompile(`(?i)--no-verify`), what: "--no-verify"},
	{re: regexp.MustCompile(`(?i)core\.hookspath`), what: "core.hooksPath"},
	{re: regexp.MustCompile(`(?i)\bhusky(_skip_hooks)?=`), what: "a HUSKY= env kill"},
	{re: regexp.MustCompile(`(?i)(^|[;&|]\s*)(rm|mv|chmod)\s[^;&|]*(\.husky|\.git/hooks)`), what: "a destructive edit of the hook directory"},
	{re: regexp.MustCompile(`(^|\s)-n(\s|$)`), what: "-n (short --no-verify)", scoped: true},
}

var gitCommitOrPush = regexp.MustCompile(`git\s+(commit|push)\b`)

// bashCommandBlocked reports whether a Bash command tries to bypass git hooks,
// and which shape it matched.
func bashCommandBlocked(cmd string) (bool, string) {
	if strings.TrimSpace(cmd) == "" {
		return false, ""
	}
	isCommitOrPush := gitCommitOrPush.MatchString(cmd)
	for _, p := range hookBypassPatterns {
		if p.scoped && !isCommitOrPush {
			continue
		}
		if p.re.MatchString(cmd) {
			return true, p.what
		}
	}
	return false, ""
}

// guardDenyMessage is fed back to the worker verbatim on a block. A bare refusal
// would send it hunting for the next door; this one names the only two legitimate
// moves, so a runner missing a hook's tool ends as a reported BLOCKED-ENV rather
// than an unvetted commit on a shared branch.
func guardDenyMessage(what string) string {
	return "pm worker-guard: blocked - this command carries " + what + ", which bypasses the project's git hooks. " +
		"Hooks are where the project gates secret scanning, lint and formatting; a commit that skipped them reads as checked and is not. " +
		"If a hook is failing because a tool it needs is missing from this machine (`gitleaks: not found`), that is a runner defect: " +
		"leave the change uncommitted, record `BLOCKED-ENV: <hook> needs <tool>, not installed on this runner - work is in the working tree, uncommitted` " +
		"in `unresolved`, and return \"blocked\". Otherwise fix what the hook is complaining about and commit normally."
}

// hookEvent is the PreToolUse payload Claude Code writes to the hook's stdin.
type hookEvent struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// runWorkerGuard implements the hook: exit 2 (with the reason on stderr) blocks
// the call and hands the reason to the model; exit 0 lets it through.
//
// Anything unexpected - unparseable payload, a different tool, no command - exits
// 0. A guard that fails closed would break every worker on the machine the first
// time the payload shape changes, and it is not the last line of defence: the
// disallow list and the prompt rule cover the same ground.
func runWorkerGuard(in io.Reader, errOut io.Writer) int {
	data, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil || len(data) == 0 {
		return 0
	}
	var ev hookEvent
	if json.Unmarshal(data, &ev) != nil {
		return 0
	}
	if ev.ToolName != "" && ev.ToolName != "Bash" {
		return 0
	}
	if blocked, what := bashCommandBlocked(ev.ToolInput.Command); blocked {
		fmt.Fprintln(errOut, guardDenyMessage(what))
		return 2
	}
	return 0
}

func newWorkerGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "worker-guard",
		Short:  "PreToolUse hook for headless workers (internal)",
		Long:   "Reads a Claude Code PreToolUse payload on stdin and blocks Bash commands that would bypass the project's git hooks. Attached automatically to every executor worker; not meant to be run by hand.",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if code := runWorkerGuard(cmd.InOrStdin(), cmd.ErrOrStderr()); code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
}

// workerGuardSettings renders the --settings payload that attaches the guard to a
// worker, or "" when pm cannot resolve its own binary (the guard is then simply
// absent - the disallow list and prompt rule still stand).
func workerGuardSettings() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return ""
	}
	type hookCmd struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type matcher struct {
		Matcher string    `json:"matcher"`
		Hooks   []hookCmd `json:"hooks"`
	}
	payload := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []matcher{{
				Matcher: "Bash",
				Hooks:   []hookCmd{{Type: "command", Command: quoteForShell(exe) + " worker-guard"}},
			}},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(b)
}

// quoteForShell single-quotes a path for the hook's shell invocation - a pm
// installed under a path with spaces would otherwise split into two words.
func quoteForShell(p string) string {
	if !strings.ContainsAny(p, " \t'\"$`\\") {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}
