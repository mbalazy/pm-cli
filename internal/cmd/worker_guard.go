package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
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
		// Pointer on purpose: absent and false are different answers - absent
		// means the tool's own default, which is BACKGROUND on the measured
		// build (see forceSyncSpawn).
		RunInBackground *bool `json:"run_in_background"`
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

// budgetedToolNames are the exploration tools a subagent's per-agent budget
// meters. A closed list on purpose: StructuredOutput and the report itself must
// never be blocked - a reviewer over budget still has to be able to hand in
// what it has.
var budgetedToolNames = map[string]bool{"Read": true, "Grep": true, "Glob": true}

// reviewerToolBudget is how many exploration calls one subagent may make before
// further Read/Grep/Glob are refused. From run pm-cli-100's data: the typical
// reviewer finished in 8-13 turns while the runaways pulled 33+ tool calls /
// $3+ each with the same diff packet in hand - the budget trims that tail
// without removing a reviewer or a round. A var, not a flag or a project.yaml
// knob (the value is compiled into the hook; tests shrink it in-process).
var reviewerToolBudget = 25

// toolBudgetDenyMessage names the legitimate move, like every other refusal in
// this file: finish the report, don't hunt for the next door.
func toolBudgetDenyMessage(budget int) string {
	return fmt.Sprintf("pm worker-guard: blocked - this subagent has used its exploration budget (%d Read/Grep/Glob calls). "+
		"The full diff was attached to your prompt; finalize your report with what you have already established. "+
		"A claim you could not verify within the budget belongs in the report, marked unverified - not in another round of reading.", budget)
}

// judgeAgentToolCall meters exploration calls made from INSIDE a subagent.
// Worker-level calls (no agent_id) pass untouched - the budget exists for the
// runaway reviewer tail, not for the worker doing its job. Fail-open like the
// rest of the guard: no telemetry path (the counter's home) or an unreadable
// calls file means no budget, because what this guards costs tokens, not
// correctness.
func judgeAgentToolCall(opts guardOptions, ev hookEvent, now time.Time) string {
	if ev.AgentID == "" || opts.telemetryPath == "" || reviewerToolBudget <= 0 {
		return ""
	}
	callsPath := storage.AgentCallsPath(opts.telemetryPath)
	rec := storage.AgentToolCall{
		TS:      now.Format(time.RFC3339Nano),
		AgentID: ev.AgentID,
		Tool:    ev.ToolName,
	}
	deny := ""
	// Same read-decide-append cycle under the same flock discipline as the spawn
	// cap: parallel reviewers are parallel hook processes, and the count each one
	// judges against must include what the others just wrote.
	_ = storage.WithReviewTelemetryLock(callsPath, func() error {
		calls, err := storage.ReadAgentToolCalls(callsPath)
		if err == nil && storage.CountAgentToolCalls(calls, ev.AgentID) >= reviewerToolBudget {
			deny = toolBudgetDenyMessage(reviewerToolBudget)
			rec.Denied = true
		}
		_ = storage.AppendAgentToolCall(callsPath, rec)
		return nil
	})
	return deny
}

// guardDir is the repo the hook is judging. Claude Code sends the worker's cwd
// in the payload; the process cwd is the fallback, and it is the same directory
// in practice - the hook runs as a child of the worker.
func guardDir(ev hookEvent) string {
	if ev.Cwd != "" {
		return ev.Cwd
	}
	dir, _ := os.Getwd()
	return dir
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
		// What the WORKER asked for, before pm forces the spawn synchronous -
		// absent means the tool's background default, so it counts.
		Background: ev.ToolInput.RunInBackground == nil || *ev.ToolInput.RunInBackground,
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
// The two caps degrade differently, and the difference is not an oversight.
// The COUNT cap needs a measurable diff, so no diff base or a failing git means
// it does not apply - it would otherwise refuse legitimate work on a repo shape
// nobody anticipated, and what it guards costs tokens, not correctness. The
// ROUND cap needs nothing but the telemetry it writes itself, so it applies
// whenever it is configured. Only a missing telemetry path takes both out.
func judgeAgentSpawn(opts guardOptions, ev hookEvent, now time.Time) string {
	rec := spawnRecord(ev, now)
	if opts.telemetryPath == "" {
		if rec.Nested {
			return nestedDenyMessage
		}
		return ""
	}
	if rec.Nested {
		rec.Denied = true
		_ = storage.AppendReviewSpawn(opts.telemetryPath, rec)
		return nestedDenyMessage
	}
	size := diffStats(guardDir(ev), opts.diffBase)
	// A doc-only change lowers the round ceiling as well as the reviewer count,
	// and both halves depend on the diff being measurable at all - an
	// unestablishable diff leaves the project's own configured number standing.
	rounds := opts.fixRounds
	if size.ok {
		rounds = effectiveFixRounds(opts.fixRounds, size.docOnly)
	}

	deny := ""
	_ = storage.WithReviewTelemetryLock(opts.telemetryPath, func() error {
		spawns, err := storage.ReadReviewSpawns(opts.telemetryPath)
		if err == nil {
			// Rounds first: being one round past the cap is a different refusal
			// from being one reviewer past this round's budget, and telling the
			// worker to add a reviewer to a round it may not open would send it
			// straight back here.
			switch {
			case rounds > 0 && roundIndex(spawns, now, storage.ReviewRoundGap) > rounds:
				deny = roundDenyMessage(rounds, size.ok && size.docOnly)
				rec.Denied = true
			case size.ok && spawnsThisRound(spawns, now, storage.ReviewRoundGap) >= size.cap():
				deny = capDenyMessage(size.cap(), size.files, size.lines, size.docOnly)
				rec.Denied = true
			}
		}
		// Recorded inside the lock, so a concurrent hook deciding the same round
		// sees this spawn - and sees whether it was allowed.
		_ = storage.AppendReviewSpawn(opts.telemetryPath, rec)
		return nil
	})
	return deny
}

// guardOptions is what the hook needs to know about the run it is guarding.
// Grouped rather than passed positionally because this is the third thing to be
// threaded through in as many tickets, and every one of them broke every call
// site in the tests.
type guardOptions struct {
	telemetryPath string
	diffBase      string
	reviewModel   string
	fixRounds     int
}

// rawToolInput pulls tool_input back out of the payload as an untyped map.
// hookEvent's typed ToolInput is for DECIDING; this is for REWRITING, and the
// two cannot be the same value: updatedInput replaces the entire input, so a
// rewrite built from the typed struct would drop every field pm does not model
// (`description`, `run_in_background`, whatever ships next).
func rawToolInput(data []byte) map[string]any {
	var outer struct {
		ToolInput map[string]any `json:"tool_input"`
	}
	if json.Unmarshal(data, &outer) != nil {
		return nil
	}
	return outer.ToolInput
}

// writeUpdatedInput emits the PreToolUse response that hands Claude Code a
// rewritten tool input. Verified against claude 2.1.224 on 2026-08-07 on the
// EFFECT side, not just the transcript: a subagent spawned through a rewrite
// like this one recorded the substituted model in its own .meta.json and
// answered on it.
func writeUpdatedInput(out io.Writer, ti map[string]any, reason string) {
	payload := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "allow",
			"permissionDecisionReason": reason,
			"updatedInput":             ti,
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintln(out, string(b))
}

// runWorkerGuard implements the hook: exit 2 (with the reason on stderr) blocks
// the call and hands the reason to the model; exit 0 lets it through, optionally
// with a rewritten input on stdout.
//
// Anything unexpected - unparseable payload, a different tool, no command - exits
// 0. A guard that fails closed would break every worker on the machine the first
// time the payload shape changes, and it is not the last line of defence: the
// disallow list and the prompt rule cover the same ground.
func runWorkerGuard(in io.Reader, out, errOut io.Writer, opts guardOptions) int {
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
	case budgetedToolNames[ev.ToolName]:
		if reason := judgeAgentToolCall(opts, ev, time.Now()); reason != "" {
			fmt.Fprintln(errOut, reason)
			return 2
		}
	case agentToolNames[ev.ToolName]:
		if reason := judgeAgentSpawn(opts, ev, time.Now()); reason != "" {
			fmt.Fprintln(errOut, reason)
			return 2
		}
		// The spawn is allowed; what is left is what it runs on and what it is
		// given. Note the ORDER: the telemetry above records the model and the
		// prompt the WORKER asked for, which is the measurement that says
		// whether the prompt rules are being followed. Recording pm's own
		// substitutions instead would make the telemetry agree with pm by
		// construction.
		if ti := rawToolInput(data); ti != nil {
			if applied := rewriteAgentSpawn(ti, guardDir(ev), opts.diffBase, opts.reviewModel); len(applied) > 0 {
				writeUpdatedInput(out, ti, "pm "+strings.Join(applied, "; pm "))
			}
		}
	}
	return 0
}

func newWorkerGuardCmd() *cobra.Command {
	var opts guardOptions
	c := &cobra.Command{
		Use:    "worker-guard",
		Short:  "PreToolUse hook for headless workers (internal)",
		Long:   "Reads a Claude Code PreToolUse payload on stdin, blocks Bash commands and file writes that would bypass the project's git hooks, caps and records subagent spawns for review telemetry, and pins reviewer subagents to the project's review model. Attached automatically to every executor worker; not meant to be run by hand.",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if code := runWorkerGuard(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts); code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
	c.Flags().StringVar(&opts.telemetryPath, "telemetry", "", "path to the review telemetry JSONL for this worker")
	c.Flags().StringVar(&opts.diffBase, "diff-base", "", "commit the worker started from; the reviewer cap is sized against the diff since it")
	c.Flags().StringVar(&opts.reviewModel, "review-model", "", "model to pin onto reviewer subagents (empty = leave the call's model alone)")
	c.Flags().IntVar(&opts.fixRounds, "fix-rounds", 0, "how many review->fix rounds this run allows (0 = unbounded)")
	return c
}

// workerGuardSettings renders the --settings payload that attaches the guard to a
// worker, or "" when pm cannot resolve its own binary (the guard is then simply
// absent - the disallow list and prompt rule still stand).
func workerGuardSettings(opts guardOptions) string {
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
	if opts.telemetryPath != "" {
		cmdLine += " --telemetry " + quoteForShell(opts.telemetryPath)
	}
	if opts.diffBase != "" {
		cmdLine += " --diff-base " + quoteForShell(opts.diffBase)
	}
	if opts.reviewModel != "" {
		cmdLine += " --review-model " + quoteForShell(opts.reviewModel)
	}
	if opts.fixRounds > 0 {
		cmdLine += " --fix-rounds " + strconv.Itoa(opts.fixRounds)
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
	// spellings (see agentToolNames). It does three things: records the spawn,
	// refuses it past the round's cap (review_cap.go), and pins the model of a
	// spawn that would otherwise inherit the run's (reviewer_agent.go). Measured
	// 2026-08-07 on claude 2.1.224 via this exact --settings path, with the
	// repo's own settings.json removed: the matcher fires and the payload
	// carries subagent_type/model/prompt.
	//
	// The fourth entry meters the exploration tools for the per-agent budget
	// (judgeAgentToolCall). Its own entry for the same reason as the write
	// matcher: if a build matches tool names literally, it simply never fires
	// and the budget degrades to absent - the verified Bash spelling is never
	// put at risk. Worker-level calls pass through it untouched (no agent_id),
	// costing one hook exec per Read.
	payload := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []matcher{
				{Matcher: "Bash", Hooks: hook},
				{Matcher: "Write|Edit|MultiEdit|NotebookEdit", Hooks: hook},
				{Matcher: "Agent|Task", Hooks: hook},
				{Matcher: "Read|Grep|Glob", Hooks: hook},
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
