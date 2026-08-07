package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// The worker guard is pm's PreToolUse hook for headless workers: it inspects
// every Bash command the worker is about to run - and every file write it is
// about to make - and blocks the ones that would neuter the project's git
// hooks. Watching only the commit is not enough: a worker denied `--no-verify`
// still has `Write`, `sed -i` and a shell redirect, and a hook rewritten to
// `exit 0` bypasses the same gates one turn later.
//
// Why a hook and not just --disallowedTools: permission patterns match a command
// PREFIX, so `Bash(git commit --no-verify:*)` catches
// `git commit --no-verify -m x` and misses `git commit -m x --no-verify` - which
// is the form a model reaches for anyway. Measured 2026-08-06 against a probe
// repo with a deliberately failing pre-commit hook: the trailing-flag form ran
// and committed under the full disallow list, and was blocked once this guard
// was attached. `--settings` merges with the project's own settings.json (its
// hooks still fire), so attaching this costs the project nothing.

// hookDirRef matches a reference to the project's hook directory, as a path
// component and not as a prefix of something else - `> .husky.diff` is an
// ordinary redirect, `> .husky/pre-commit` is not.
const hookDirRef = `(\.husky|\.git/hooks)(/|\s|$)`

// hookBypassPatterns are the ways a worker can get a commit past the project's
// hooks. Each carries the shape it catches; the guard reports the match so the
// worker learns which door it tried rather than guessing.
//
// The write-shaped entries (sed -i, tee, redirect) exist because deleting the
// hook was never the cheap way through: `Bash(sed:*)` and `Bash(echo:*)` are
// both on the worker allowlist, so `sed -i "1i exit 0" .git/hooks/pre-commit`
// and `echo "exit 0" > .git/hooks/pre-commit` needed nobody's permission while
// only `rm|mv|chmod` were watched.
var hookBypassPatterns = []struct {
	re     *regexp.Regexp
	what   string
	scoped bool // only meaningful on a git commit/push - `-n` is a common flag elsewhere
}{
	{re: regexp.MustCompile(`(?i)--no-verify`), what: "--no-verify"},
	{re: regexp.MustCompile(`(?i)core\.hookspath`), what: "core.hooksPath"},
	{re: regexp.MustCompile(`(?i)\bhusky(_skip_hooks)?=`), what: "a HUSKY= env kill"},
	{re: regexp.MustCompile(`(?i)(^|[;&|]\s*)(rm|mv|cp|ln|chmod|truncate|install)\s[^;&|]*` + hookDirRef), what: "a destructive edit of the hook directory"},
	{re: regexp.MustCompile(`(?i)\bsed\s[^;&|]*(-i|--in-place)[^;&|]*` + hookDirRef), what: "an in-place sed on the hook directory"},
	{re: regexp.MustCompile(`(?i)\btee\s[^;&|]*` + hookDirRef), what: "a tee into the hook directory"},
	{re: regexp.MustCompile(`>\s*[^\s;&|]*` + hookDirRef), what: "a redirect into the hook directory"},
	{re: regexp.MustCompile(`(^|\s)-n(\s|$)`), what: "-n (short --no-verify)", scoped: true},
}

var gitCommitOrPush = regexp.MustCompile(`git\s+(commit|push)\b`)

// commitMessageArg matches a message flag together with the payload that belongs
// to it: `--message`, `-m`, and the combined short forms a model writes without
// thinking (`-am`). The payload is a quoted string when there is one, otherwise
// a single bare word - and never crosses a command separator, so
// `git commit -m fix && rm .husky/pre-commit` keeps its second half.
var commitMessageArg = regexp.MustCompile(`(^|\s)(--message|-[A-Za-z]*m)(=|\s+)("(?:[^"\\]|\\.)*"|'[^']*'|[^\s;&|]+)`)

// stripMessagePayload blanks out commit-message prose before the bypass patterns
// ever see it. Describing a door is not opening one: `git commit -m "add -n flag
// support"` is an ordinary commit, and the guard used to refuse it with a lecture
// about bypassing hooks - a deny for something the worker did not do, which is
// the same failure the guard exists to prevent, one level up (a guard that cries
// wolf is a guard that gets worked around).
//
// Only the payload goes. The flags AROUND it stay exactly where they were, so
// `git commit -m "x" --no-verify` and `git commit -m "x" -n` - the trailing form
// that permission patterns cannot see and this guard is the only defence against
// - are still blocked.
func stripMessagePayload(cmd string) string {
	return commitMessageArg.ReplaceAllString(cmd, "$1$2 MSG")
}

// bashCommandBlocked reports whether a Bash command tries to bypass git hooks,
// and which shape it matched.
func bashCommandBlocked(cmd string) (bool, string) {
	if strings.TrimSpace(cmd) == "" {
		return false, ""
	}
	cmd = stripMessagePayload(cmd)
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

// protectedWritePath matches a file path a worker must not write to: the husky
// dir, and the whole of `.git`. Editing a hook is the same bypass as skipping
// it, one turn later - and `.git/config` is the same bypass again, since
// core.hooksPath lives there. Nothing a worker legitimately does writes inside
// `.git` with an editor tool; git itself gets there through the git command,
// which the bash patterns judge separately. `.gitignore` and `.github/` are not
// under `.git/` and stay writable (the trailing separator is what decides).
var protectedWritePath = regexp.MustCompile(`(^|/)(\.git|\.husky)(/|$)`)

// writeToolNames are the tools that CREATE or MODIFY a file. The path check is
// gated on the tool name rather than on file_path merely being present: the
// read-only tools carry that field too, and reading a hook (`Read .husky/pre-commit`)
// stays legal - a worker fixing what a hook complains about needs to see it.
var writeToolNames = map[string]bool{
	"Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true,
}

// hookPathBlocked reports whether a write to raw would land in a protected dir.
// The path is cleaned first, so `src/../.git/hooks/pre-commit` is judged as what
// it resolves to.
func hookPathBlocked(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	return protectedWritePath.MatchString(path.Clean(raw))
}

// guardDenyTail is the half of the refusal that is the same whichever door the
// worker tried. A bare refusal would send it hunting for the next one; this
// names the only two legitimate moves, so a runner missing a hook's tool ends
// as a reported BLOCKED-ENV rather than an unvetted commit on a shared branch.
const guardDenyTail = "Hooks are where the project gates secret scanning, lint and formatting; a commit that skipped them reads as checked and is not. " +
	"If a hook is failing because a tool it needs is missing from this machine (`gitleaks: not found`), that is a runner defect: " +
	"leave the change uncommitted, record `BLOCKED-ENV: <hook> needs <tool>, not installed on this runner - work is in the working tree, uncommitted` " +
	"in `unresolved`, and return \"blocked\". Otherwise fix what the hook is complaining about and commit normally."

// guardDenyMessage is fed back to the worker verbatim when a Bash command is
// blocked.
func guardDenyMessage(what string) string {
	return "pm worker-guard: blocked - this command carries " + what + ", which bypasses the project's git hooks. " + guardDenyTail
}

// guardWriteDenyMessage is the same refusal for a file write that would rewrite
// a hook (or the config that points at one) rather than skip it.
func guardWriteDenyMessage(p string) string {
	return "pm worker-guard: blocked - writing " + p + " reaches into the repo's hook machinery, which bypasses it as surely as --no-verify does. " + guardDenyTail
}

// hookEvent is the PreToolUse payload Claude Code writes to the hook's stdin.
//
// AgentID/AgentType are present ONLY when the call comes from inside a subagent
// (measured 2026-08-07 against claude 2.1.224) - that is what makes a nested
// spawn distinguishable from one the worker issued itself. SessionID is NOT a
// discriminator: the worker and its subagents share it.
type hookEvent struct {
	ToolName  string `json:"tool_name"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	ToolInput struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		Model        string `json:"model"`
		SubagentType string `json:"subagent_type"`
		Prompt       string `json:"prompt"`
	} `json:"tool_input"`
}

// agentToolNames are the spellings of the subagent-spawning tool. Both are
// live in ONE build: claude 2.1.224 sends `tool_name: "Agent"` to the hook and
// reports the very same call as `"tool_name": "Task"` in the envelope's
// permission_denials (measured 2026-08-07), and older builds used Task
// throughout. Matching one name only would miss half the reality.
var agentToolNames = map[string]bool{"Agent": true, "Task": true}

// diffMarkers are the shapes a real diff carries in a prompt. Used to record
// whether the worker handed its reviewer the diff or sent it to go dig one up
// itself - the 16 reviewers in epic orbit-106 pulled 2.27M characters
// out of the repo because they were handed a 1.9-4.8kB prompt and no diff.
var diffMarkers = []string{"diff --git", "\n@@ ", "```diff"}

func promptCarriesDiff(prompt string) bool {
	for _, m := range diffMarkers {
		if strings.Contains(prompt, m) {
			return true
		}
	}
	return false
}

// spawnRecord is the telemetry line for one observed spawn.
func spawnRecord(ev hookEvent, now time.Time) storage.ReviewSpawn {
	return storage.ReviewSpawn{
		// Stamped with the instant the decision is made, not left to the append
		// to fill in, so the time a spawn is JUDGED against and the time it is
		// RECORDED at can never disagree.
		TS:           now.Format(time.RFC3339Nano),
		Model:        ev.ToolInput.Model,
		SubagentType: ev.ToolInput.SubagentType,
		HasDiff:      promptCarriesDiff(ev.ToolInput.Prompt),
		Nested:       ev.AgentID != "",
		AgentType:    ev.AgentType,
	}
}

// judgeAgentSpawn decides what happens to a subagent spawn: it is recorded
// either way, and refused when it would exceed the round's cap. Returns the
// deny reason, or "" to allow.
//
// The recording is deliberately unconditional. A refused spawn is the single
// most interesting event this telemetry can capture - it is the evidence that
// the cap is doing something - and dropping it would leave the run looking like
// one where the worker simply behaved.
//
// Degrades to allow-and-record on anything it cannot establish: no telemetry
// path, no diff base, a git command that fails. A cap that guesses would refuse
// legitimate work on a repo shape nobody anticipated, and the failure it is
// guarding against costs tokens, not correctness.
func judgeAgentSpawn(telemetryPath, diffBase string, ev hookEvent, now time.Time) string {
	rec := spawnRecord(ev, now)
	if telemetryPath == "" {
		if rec.Nested {
			return nestedDenyMessage
		}
		return ""
	}
	if rec.Nested {
		rec.Denied = true
		_ = storage.AppendReviewSpawn(telemetryPath, rec)
		return nestedDenyMessage
	}
	dir := ev.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	files, lines, ok := diffStats(dir, diffBase)
	if !ok {
		_ = storage.AppendReviewSpawn(telemetryPath, rec)
		return ""
	}
	cap := reviewerCap(files, lines)

	deny := ""
	_ = storage.WithReviewTelemetryLock(telemetryPath, func() error {
		spawns, err := storage.ReadReviewSpawns(telemetryPath)
		if err == nil && spawnsThisRound(spawns, now, storage.ReviewRoundGap) >= cap {
			deny = capDenyMessage(cap, files, lines)
			rec.Denied = true
		}
		// Recorded inside the lock, so a concurrent hook deciding the same round
		// sees this spawn - and sees whether it was allowed.
		_ = storage.AppendReviewSpawn(telemetryPath, rec)
		return nil
	})
	return deny
}

// runWorkerGuard implements the hook: exit 2 (with the reason on stderr) blocks
// the call and hands the reason to the model; exit 0 lets it through.
//
// Anything unexpected - unparseable payload, a different tool, no command - exits
// 0. A guard that fails closed would break every worker on the machine the first
// time the payload shape changes, and it is not the last line of defence: the
// disallow list and the prompt rule cover the same ground.
func runWorkerGuard(in io.Reader, errOut io.Writer, telemetryPath, diffBase string) int {
	data, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil || len(data) == 0 {
		return 0
	}
	var ev hookEvent
	if json.Unmarshal(data, &ev) != nil {
		return 0
	}
	switch {
	case ev.ToolName == "" || ev.ToolName == "Bash":
		if blocked, what := bashCommandBlocked(ev.ToolInput.Command); blocked {
			fmt.Fprintln(errOut, guardDenyMessage(what))
			return 2
		}
	case writeToolNames[ev.ToolName]:
		if hookPathBlocked(ev.ToolInput.FilePath) {
			fmt.Fprintln(errOut, guardWriteDenyMessage(ev.ToolInput.FilePath))
			return 2
		}
	case agentToolNames[ev.ToolName]:
		if reason := judgeAgentSpawn(telemetryPath, diffBase, ev, time.Now()); reason != "" {
			fmt.Fprintln(errOut, reason)
			return 2
		}
	}
	return 0
}

func newWorkerGuardCmd() *cobra.Command {
	var telemetry, diffBase string
	c := &cobra.Command{
		Use:    "worker-guard",
		Short:  "PreToolUse hook for headless workers (internal)",
		Long:   "Reads a Claude Code PreToolUse payload on stdin, blocks Bash commands and file writes that would bypass the project's git hooks, and records subagent spawns for review telemetry. Attached automatically to every executor worker; not meant to be run by hand.",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if code := runWorkerGuard(cmd.InOrStdin(), cmd.ErrOrStderr(), telemetry, diffBase); code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
	c.Flags().StringVar(&telemetry, "telemetry", "", "path to the review telemetry JSONL for this worker")
	c.Flags().StringVar(&diffBase, "diff-base", "", "commit the worker started from; the reviewer cap is sized against the diff since it")
	return c
}

// workerGuardSettings renders the --settings payload that attaches the guard to a
// worker, or "" when pm cannot resolve its own binary (the guard is then simply
// absent - the disallow list and prompt rule still stand).
func workerGuardSettings(telemetryPath, diffBase string) string {
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
	cmdLine := quoteForShell(exe) + " worker-guard"
	if telemetryPath != "" {
		cmdLine += " --telemetry " + quoteForShell(telemetryPath)
	}
	if diffBase != "" {
		cmdLine += " --diff-base " + quoteForShell(diffBase)
	}
	hook := []hookCmd{{Type: "command", Command: cmdLine}}
	// Two entries, not one alternation: the "Bash" matcher is the one verified
	// against a live claude (2026-08-06) and stays spelled exactly as it was.
	// The write matcher is a second, independent entry, so if this build of
	// Claude Code matches tool names literally rather than as a regex it simply
	// never fires - the Bash side, which is where the cheap doors were, keeps
	// working. The alternatives are named explicitly (an "Edit" that only
	// substring-matches would already cover MultiEdit/NotebookEdit; one that
	// full-matches would not).
	//
	// Why the guard and not a path-negative allowlist entry for Write/Edit: the
	// guard is attached in --yolo too, where the allow/disallow lists are not
	// passed at all - a rule that only exists in the allowlist protects exactly
	// the runs that need it least.
	//
	// The third entry is the subagent-spawn tool, matched under BOTH its
	// spellings (see agentToolNames). It is observation-only today: the guard
	// records the spawn and allows it. Measured 2026-08-07 on claude 2.1.224
	// via this exact --settings path, with the repo's own settings.json removed:
	// the matcher fires and the payload carries subagent_type/model/prompt.
	payload := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []matcher{
				{Matcher: "Bash", Hooks: hook},
				{Matcher: "Write|Edit|MultiEdit|NotebookEdit", Hooks: hook},
				{Matcher: "Agent|Task", Hooks: hook},
			},
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
