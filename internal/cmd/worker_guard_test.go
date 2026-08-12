package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func TestBashCommandBlocked(t *testing.T) {
	blocked := []struct{ name, cmd string }{
		{"leading no-verify", `git commit --no-verify -m "x"`},
		// The form prefix-based permission rules cannot catch, and the one a
		// model actually reaches for.
		{"trailing no-verify", `git commit -m "x" --no-verify`},
		{"short -n on commit", `git commit -m "x" -n`},
		{"short -n on push", `git push -n origin main`},
		{"cluster -nm on commit", `git commit -nm "add feature"`},
		{"cluster -anm on commit", `git commit -anm "add feature"`},
		{"cluster -nf on push", `git push -nf origin main`},
		{"attached cluster, no separator", `git commit -nm"add feature"`},
		{"hooksPath via -c", `git -c core.hooksPath=/dev/null commit -m "x"`},
		{"hooksPath via config", `git config core.hooksPath /dev/null`},
		{"hooksPath case", `git -c core.HooksPath=/dev/null commit -m x`},
		{"husky env kill", `HUSKY=0 git commit -m "x"`},
		{"husky skip env", `HUSKY_SKIP_HOOKS=1 git commit -m "x"`},
		{"delete the hook", `rm .husky/pre-commit`},
		{"disarm the hook", `chmod -x .git/hooks/pre-commit`},
		{"hook removal after a pipe", `git status && rm .husky/pre-commit`},
		{"push no-verify", `git push --no-verify origin HEAD`},
		// Rewriting the hook, not deleting it: sed and echo are BOTH on the
		// worker allowlist, so these needed nobody's permission.
		{"sed the hook to a no-op", `sed -i "1i exit 0" .git/hooks/pre-commit`},
		{"sed --in-place", `sed --in-place "s/^/exit 0\n/" .husky/pre-commit`},
		{"redirect into the hook", `echo "exit 0" > .git/hooks/pre-commit`},
		{"append into the hook", `echo "exit 0" >> .husky/pre-commit`},
		{"tee into the hook", `echo "exit 0" | tee .git/hooks/pre-commit`},
		{"copy over the hook", `cp /dev/null .git/hooks/pre-commit`},
		{"quoted hook path", `echo "exit 0" > ".git/hooks/pre-commit"`},
		// The message payload is stripped before matching; the flags around it
		// are not, so a real bypass hiding behind a chatty message still lands.
		{"prose plus a real trailing flag", `git commit -m "add -n flag support" -n`},
		{"prose plus a real long flag", `git commit -m "describe the -n flag" --no-verify`},
		{"message then a hook removal", `git commit -m "tidy up" && rm .husky/pre-commit`},
	}
	for _, c := range blocked {
		if ok, _ := bashCommandBlocked(c.cmd); !ok {
			t.Errorf("%s: %q must be blocked", c.name, c.cmd)
		}
	}

	// -n is an ordinary flag almost everywhere. Blocking it outside a
	// commit/push would break routine work (and a guard that cries wolf gets
	// worked around, which is the failure this whole change is about).
	allowed := []struct{ name, cmd string }{
		{"plain commit", `git commit -m "ACME-1387: move the row"`},
		{"log -n", `git log -n 3 --oneline`},
		{"grep -n", `grep -n "TODO" src/app.ts`},
		{"head -n", `head -n 20 package.json`},
		{"echo -n", `echo -n hello`},
		{"sort -n", `sort -n numbers.txt`},
		{"husky in a path", `cat .husky/pre-commit`},
		{"ls the hook dir", `ls .husky`},
		// Reading a hook is how a worker finds out what it is complaining
		// about - only writing is the bypass.
		{"grep the hook dir", `grep -rn gitleaks .husky`},
		{"redirect a hook read elsewhere", `cat .husky/pre-commit > /tmp/hook.txt`},
		{"redirect to a lookalike name", `git diff > .husky.diff`},
		{"sed reading the hook", `sed -n "1,5p" .git/hooks/pre-commit`},
		{"commit message mentioning the word", `git commit -m "document the no-verify ban"`},
		// Prose in a commit message is not an action. Each of these was blocked
		// before the payload was stripped, with a lecture about bypassing hooks.
		{"message describing the -n flag", `git commit -m "add -n flag support to the parser"`},
		{"message describing --no-verify", `git commit -m "document the --no-verify ban"`},
		{"message mentioning core.hooksPath", `git commit -m "set core.hooksPath in the installer"`},
		{"combined short flag", `git commit -am "drop the --no-verify escape hatch"`},
		{"long message flag", `git commit --message="stop honouring HUSKY=0"`},
		{"message naming a hook file", `git commit -m "regenerate .husky/pre-commit from the template"`},
		{"single-quoted message", `git commit -m 'explain why -n is refused'`},
		{"message describing the -nm cluster bypass", `git commit -m "fix the -nm cluster bypass in worker-guard"`},
		{"empty", ``},
	}
	for _, c := range allowed {
		if ok, what := bashCommandBlocked(c.cmd); ok {
			t.Errorf("%s: %q must be allowed, blocked as %s", c.name, c.cmd, what)
		}
	}
}

// TestBashCommandBlockedClusterGuard is the -nm/-anm regression named so
// `go test -run Guard` picks it up directly, independent of where the cluster
// cases also live in TestBashCommandBlocked's tables: git's short-option
// parser splits `-nm "msg"` into `-n` (--no-verify) + `-m <msg>`, and the
// scoped `-n` pattern used to require a standalone token, so the cluster form
// slipped through (verified empirically 2026-08-12).
func TestBashCommandBlockedClusterGuard(t *testing.T) {
	blocked := []struct{ name, cmd string }{
		{"cluster -nm on commit", `git commit -nm "add feature"`},
		{"cluster -anm on commit", `git commit -anm "add feature"`},
		{"cluster -nf on push", `git push -nf origin main`},
		{"attached cluster, quoted payload, no separator", `git commit -nm"add feature"`},
		{"attached cluster, bare payload, no separator", `git commit -nmfix-typo`},
		{"attached cluster with -a, quoted payload, no separator", `git commit -anm"add feature"`},
		// Two git segments in one line: the first is a harmless -am, the
		// second carries the bypass. FindAllString must not merge them into
		// one span that only the first flag set is checked against.
		{"second of two chained git segments carries the bypass", `git commit -am "x" && git commit -nm "y"`},
	}
	for _, c := range blocked {
		if ok, _ := bashCommandBlocked(c.cmd); !ok {
			t.Errorf("%s: %q must be blocked", c.name, c.cmd)
		}
	}

	allowed := []struct{ name, cmd string }{
		{"combined short flag without n", `git commit -am "drop the --no-verify escape hatch"`},
		{"cluster mentioned only in a stripped message", `git commit -m "fix the -nm cluster bypass in worker-guard"`},
		// The scoped `-n` check is judged per git-commit/push segment, not
		// against the whole line - an unrelated flag containing `n` in a
		// command chained ahead of the commit must not trip it, whatever the
		// separator is (&&, ;, |, or a newline - a worker's Bash call is
		// routinely a whole multi-line script in one `command` string).
		{"unrelated -n-bearing flag chained before a commit", `go test ./internal/cmd/ -run Guard -v && git commit -am "fix"`},
		{"unrelated -n-bearing flag chained after a commit", `git commit -am "fix" && go test -run Guard -v`},
		{"unrelated -n-bearing flag after a semicolon", `git commit -am "fix"; grep -rn TODO src`},
		{"unrelated -n-bearing flag after a pipe", `git commit -am "fix" | tee log; grep -rn TODO src`},
		{"unrelated -n-bearing flag on the next line", "git commit -am \"fix guard\"\ngo test ./internal/cmd/ -run Guard -v"},
	}
	for _, c := range allowed {
		if ok, what := bashCommandBlocked(c.cmd); ok {
			t.Errorf("%s: %q must be allowed, blocked as %s", c.name, c.cmd, what)
		}
	}
}

// The boundary of the message fix, stated directly: the payload goes, the flags
// around it stay. A test on the tables alone would not say which half moved.
func TestStripMessagePayload(t *testing.T) {
	cases := []struct{ in, want string }{
		{`git commit -m "add -n flag support"`, `git commit -m MSG`},
		{`git commit -m "x" --no-verify`, `git commit -m MSG --no-verify`},
		{`git commit -am 'both -n and --no-verify'`, `git commit -am MSG`},
		{`git commit --message="HUSKY=0 is banned"`, `git commit --message MSG`},
		{`git commit -m fix && rm .husky/pre-commit`, `git commit -m MSG && rm .husky/pre-commit`},
		{`git push --no-verify origin HEAD`, `git push --no-verify origin HEAD`},
		// Attached form, no separator at all: git's own short-option parser
		// accepts the value glued directly onto the flag cluster.
		{`git commit -nm"add feature"`, `git commit -nm MSG`},
		{`git commit -nmfix-typo`, `git commit -nm MSG`},
	}
	for _, c := range cases {
		if got := stripMessagePayload(c.in); got != c.want {
			t.Errorf("stripMessagePayload(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBashCommandBlockedMessageNamesTheDoor(t *testing.T) {
	_, what := bashCommandBlocked(`git commit -m x --no-verify`)
	if !strings.Contains(guardDenyMessage(what), "--no-verify") {
		t.Errorf("deny message must name what matched, got %q", guardDenyMessage(what))
	}
	if !strings.Contains(guardDenyMessage(what), "BLOCKED-ENV:") {
		t.Error("deny message must tell the worker what to do instead")
	}
}

func TestRunWorkerGuard(t *testing.T) {
	payload := func(tool, cmd string) string {
		b, _ := json.Marshal(map[string]any{"tool_name": tool, "tool_input": map[string]string{"command": cmd}})
		return string(b)
	}

	t.Run("blocks with exit 2 and a reason", func(t *testing.T) {
		var errOut bytes.Buffer
		code := runWorkerGuard(strings.NewReader(payload("Bash", `git commit -m x --no-verify`)), io.Discard, &errOut, guardOptions{})
		if code != 2 {
			t.Fatalf("exit code = %d, want 2 (the code Claude Code reads as a block)", code)
		}
		if !strings.Contains(errOut.String(), "bypasses the project's git hooks") {
			t.Errorf("stderr = %q, want the reason the model gets shown", errOut.String())
		}
	})

	t.Run("passes ordinary commands", func(t *testing.T) {
		var errOut bytes.Buffer
		if code := runWorkerGuard(strings.NewReader(payload("Bash", `git commit -m x`)), io.Discard, &errOut, guardOptions{}); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if errOut.Len() != 0 {
			t.Errorf("nothing to say on a pass, got %q", errOut.String())
		}
	})

	t.Run("ignores other tools", func(t *testing.T) {
		if code := runWorkerGuard(strings.NewReader(payload("Read", `git commit --no-verify`)), io.Discard, &bytes.Buffer{}, guardOptions{}); code != 0 {
			t.Errorf("exit code = %d, want 0 - a command only counts as one on Bash", code)
		}
	})

	// Fail-open on anything unexpected: a guard that blocked on a payload it
	// could not read would stop every worker on the machine the first time the
	// hook schema changed.
	t.Run("fails open on junk", func(t *testing.T) {
		for _, in := range []string{"", "not json", "{}", `{"tool_name":"Bash"}`} {
			if code := runWorkerGuard(strings.NewReader(in), io.Discard, &bytes.Buffer{}, guardOptions{}); code != 0 {
				t.Errorf("input %q: exit code = %d, want 0", in, code)
			}
		}
	})
}

// The second half of the same door: a worker denied `git commit --no-verify`
// can rewrite the hook itself, and `Write`/`Edit` are on the allowlist with no
// path restriction at all.
func TestRunWorkerGuardFileWrites(t *testing.T) {
	payload := func(tool, filePath string) string {
		b, _ := json.Marshal(map[string]any{"tool_name": tool, "tool_input": map[string]string{"file_path": filePath}})
		return string(b)
	}

	blocked := []struct{ name, tool, path string }{
		{"write a git hook", "Write", "/repo/.git/hooks/pre-commit"},
		{"write a husky hook", "Write", ".husky/pre-commit"},
		{"edit a git hook", "Edit", ".git/hooks/pre-push"},
		{"multi-edit a husky hook", "MultiEdit", "/repo/.husky/commit-msg"},
		{"relative dance into the hook dir", "Write", "src/../.git/hooks/pre-commit"},
		{"the hook dir itself", "Write", ".husky"},
		// core.hooksPath lives here, so this is the same door one level up.
		{"the repo config", "Write", ".git/config"},
	}
	for _, c := range blocked {
		var errOut bytes.Buffer
		if code := runWorkerGuard(strings.NewReader(payload(c.tool, c.path)), io.Discard, &errOut, guardOptions{}); code != 2 {
			t.Errorf("%s: exit code = %d, want 2", c.name, code)
		} else if !strings.Contains(errOut.String(), c.path) {
			t.Errorf("%s: deny message must name the path, got %q", c.name, errOut.String())
		}
	}

	allowed := []struct{ name, tool, path string }{
		{"ordinary source file", "Write", "internal/cmd/work.go"},
		{"a file that merely looks like the hook dir", "Write", "docs/.husky.md"},
		{"gitignore is not under .git", "Write", ".gitignore"},
		{"a workflow is not under .git", "Write", ".github/workflows/ci.yml"},
		{"reading a hook", "Read", ".husky/pre-commit"},
		{"listing tool with a hook path", "Glob", ".git/hooks/*"},
		{"no path at all", "Write", ""},
	}
	for _, c := range allowed {
		var errOut bytes.Buffer
		if code := runWorkerGuard(strings.NewReader(payload(c.tool, c.path)), io.Discard, &errOut, guardOptions{}); code != 0 {
			t.Errorf("%s: exit code = %d, want 0 (%s)", c.name, code, errOut.String())
		}
	}
}

func TestWorkerGuardSettingsIsValidAndAttached(t *testing.T) {
	s := workerGuardSettings(guardOptions{telemetryPath: "/tmp/pm-telemetry.jsonl", diffBase: "abc123", reviewModel: "sonnet"})
	if s == "" {
		t.Fatal("guard settings must render (os.Executable resolves under go test)")
	}
	var parsed struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		t.Fatalf("settings payload must be valid JSON: %v (%s)", err, s)
	}
	// Separate entries on purpose: the Bash matcher is the one verified against
	// a live claude and must keep its exact spelling; the write and agent
	// matchers are separate entries so a build that matches tool names literally
	// simply does not fire them instead of losing the Bash one too.
	if len(parsed.Hooks.PreToolUse) != 4 {
		t.Fatalf("expected Bash, write-tool, agent and exploration PreToolUse matchers, got %s", s)
	}
	if parsed.Hooks.PreToolUse[0].Matcher != "Bash" {
		t.Errorf("first matcher = %q, want the verified exact \"Bash\"", parsed.Hooks.PreToolUse[0].Matcher)
	}
	for _, tool := range []string{"Write", "Edit", "MultiEdit", "NotebookEdit"} {
		if !strings.Contains(parsed.Hooks.PreToolUse[1].Matcher, tool) {
			t.Errorf("write matcher %q must name %s", parsed.Hooks.PreToolUse[1].Matcher, tool)
		}
	}
	// Both spellings of the spawn tool: 2.1.224 sends "Agent" to the hook and
	// calls the same call "Task" in the envelope's permission_denials.
	for _, tool := range []string{"Agent", "Task"} {
		if !strings.Contains(parsed.Hooks.PreToolUse[2].Matcher, tool) {
			t.Errorf("agent matcher %q must name %s", parsed.Hooks.PreToolUse[2].Matcher, tool)
		}
	}
	// The exploration tools the per-agent budget meters.
	for _, tool := range []string{"Read", "Grep", "Glob"} {
		if !strings.Contains(parsed.Hooks.PreToolUse[3].Matcher, tool) {
			t.Errorf("exploration matcher %q must name %s", parsed.Hooks.PreToolUse[3].Matcher, tool)
		}
	}
	for i, m := range parsed.Hooks.PreToolUse {
		got := m.Hooks[0].Command
		if !strings.Contains(got, " worker-guard") {
			t.Errorf("matcher %d hook command = %q, want it to invoke pm worker-guard", i, got)
		}
		for _, arg := range []string{"--telemetry /tmp/pm-telemetry.jsonl", "--diff-base abc123"} {
			if !strings.Contains(got, arg) {
				t.Errorf("matcher %d hook command = %q, want %s baked in", i, got, arg)
			}
		}
	}

	// Both modes carry it: --yolo waives permission prompts, not the project's
	// hooks.
	for _, yolo := range []bool{false, true} {
		args := strings.Join(buildClaudeArgs("p", "sp", "sess", "opus", 10, yolo, guardOptions{telemetryPath: "/tmp/t.jsonl", diffBase: "abc123", reviewModel: "sonnet"}, "", false), " ")
		if !strings.Contains(args, "--settings") || !strings.Contains(args, "worker-guard") {
			t.Errorf("yolo=%v: worker argv must attach the guard, got %s", yolo, args)
		}
	}
}

func TestQuoteForShell(t *testing.T) {
	if got := quoteForShell("/usr/local/bin/pm"); got != "/usr/local/bin/pm" {
		t.Errorf("plain path must stay bare, got %q", got)
	}
	if got := quoteForShell("/Users/a b/go/bin/pm"); got != `'/Users/a b/go/bin/pm'` {
		t.Errorf("path with a space must be quoted, got %q", got)
	}
	if got := quoteForShell(`/tmp/we"ird/pm`); got != `'/tmp/we"ird/pm'` {
		t.Errorf("path with a double quote must be quoted, got %q", got)
	}
}

// The payloads in this test are the SHAPES measured against a live claude
// 2.1.224 on 2026-08-07 (pm-cli-96-1), not invented ones: a top-level Agent call
// carries description/prompt/subagent_type/run_in_background and no agent_id,
// while a call made from inside a subagent carries agent_id + agent_type.
func TestWorkerGuardRecordsAgentSpawns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.jsonl")

	topLevel := `{
	  "session_id": "dc1ae94b-acf8-40e6-89bf-a114c8b04af1",
	  "hook_event_name": "PreToolUse",
	  "tool_name": "Agent",
	  "tool_input": {
	    "description": "Review the diff",
	    "prompt": "Review this change:\ndiff --git a/main.go b/main.go\n",
	    "subagent_type": "Explore",
	    "model": "opus",
	    "run_in_background": false
	  },
	  "tool_use_id": "toolu_013QmYmQr9eFyXAB9tcYnHGw"
	}`
	// The older spelling of the same tool - both are live in one build.
	legacyName := `{"tool_name":"Task","tool_input":{"prompt":"go look around","subagent_type":"general-purpose"}}`
	nested := `{
	  "agent_id": "a3115b1787a99ef82",
	  "agent_type": "Explore",
	  "tool_name": "Agent",
	  "tool_input": {"prompt": "audit the containers", "subagent_type": "Explore"}
	}`

	// No --diff-base, so the cap cannot be sized and degrades to allowing every
	// top-level spawn - what this case is about is what gets RECORDED.
	for _, in := range []string{topLevel, legacyName} {
		var errOut bytes.Buffer
		if code := runWorkerGuard(strings.NewReader(in), io.Discard, &errOut, guardOptions{telemetryPath: path}); code != 0 {
			t.Fatalf("an unsized cap must allow the spawn, got %d (%s)", code, errOut.String())
		}
	}
	// A nested spawn is refused regardless: it is the case a cap on the worker
	// cannot see, and no diff size makes it acceptable.
	var nestedOut bytes.Buffer
	if code := runWorkerGuard(strings.NewReader(nested), io.Discard, &nestedOut, guardOptions{telemetryPath: path}); code != 2 {
		t.Fatalf("a nested spawn must be refused, got %d (%s)", code, nestedOut.String())
	}

	spawns, err := storage.ReadReviewSpawns(path)
	if err != nil {
		t.Fatalf("read telemetry: %v", err)
	}
	if len(spawns) != 3 {
		t.Fatalf("got %d recorded spawns, want 3", len(spawns))
	}
	if spawns[0].Model != "opus" || spawns[0].SubagentType != "Explore" || !spawns[0].HasDiff {
		t.Errorf("top-level spawn recorded wrong: %+v", spawns[0])
	}
	if spawns[0].Nested {
		t.Error("a call with no agent_id is the worker's own, not nested")
	}
	if spawns[1].Model != "" || spawns[1].HasDiff {
		t.Errorf("legacy-name spawn recorded wrong: %+v", spawns[1])
	}
	if !spawns[2].Nested || spawns[2].AgentType != "Explore" || !spawns[2].Denied {
		t.Errorf("a call carrying agent_id must be recorded as nested AND denied: %+v", spawns[2])
	}
	if spawns[0].TS == "" {
		t.Error("every spawn needs a timestamp - round clustering reads it")
	}
}

// Telemetry is observability: a worker must never die because it could not be
// written, and a guard with no telemetry path configured must behave exactly as
// it did before telemetry existed.
func TestWorkerGuardTelemetryIsBestEffort(t *testing.T) {
	agent := `{"tool_name":"Agent","tool_input":{"prompt":"x","subagent_type":"Explore"}}`
	for _, path := range []string{"", filepath.Join(t.TempDir(), "no", "such", "dir", "..", "\x00bad")} {
		var errOut bytes.Buffer
		if code := runWorkerGuard(strings.NewReader(agent), io.Discard, &errOut, guardOptions{telemetryPath: path}); code != 0 {
			t.Errorf("path %q: got exit %d, want 0", path, code)
		}
	}
}

// The per-agent exploration budget: a subagent's Read/Grep/Glob calls are
// counted per agent_id and refused past the budget, so a runaway reviewer is
// trimmed without touching the worker's own calls or any other agent's budget.
func TestAgentToolBudget(t *testing.T) {
	payload := func(tool, agentID string) string {
		m := map[string]any{"tool_name": tool, "tool_input": map[string]string{"file_path": "internal/cmd/work.go"}}
		if agentID != "" {
			m["agent_id"] = agentID
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	shrinkBudget := func(t *testing.T, n int) {
		t.Helper()
		old := reviewerToolBudget
		reviewerToolBudget = n
		t.Cleanup(func() { reviewerToolBudget = old })
	}

	t.Run("counts per agent_id, denies past the budget", func(t *testing.T) {
		shrinkBudget(t, 2)
		path := filepath.Join(t.TempDir(), "review.jsonl")
		opts := guardOptions{telemetryPath: path}
		for i := 0; i < 2; i++ {
			var errOut bytes.Buffer
			if code := runWorkerGuard(strings.NewReader(payload("Read", "agent-a")), io.Discard, &errOut, opts); code != 0 {
				t.Fatalf("call %d within budget: exit %d (%s)", i, code, errOut.String())
			}
		}
		var errOut bytes.Buffer
		if code := runWorkerGuard(strings.NewReader(payload("Grep", "agent-a")), io.Discard, &errOut, opts); code != 2 {
			t.Fatalf("call past the budget: exit %d, want 2", code)
		}
		if !strings.Contains(errOut.String(), "exploration budget") || !strings.Contains(errOut.String(), "finalize your report") {
			t.Errorf("deny message must name the budget and the legitimate move, got %q", errOut.String())
		}
		// A second agent has its OWN budget - the counter is per agent_id, not
		// per session or per file.
		if code := runWorkerGuard(strings.NewReader(payload("Read", "agent-b")), io.Discard, &bytes.Buffer{}, opts); code != 0 {
			t.Error("agent-b must not inherit agent-a's spent budget")
		}
		// Denied calls are recorded as evidence but never consume budget:
		// agent-a's next call is still denied (not double-counted into a bigger
		// number), and the file carries the denial.
		calls, err := storage.ReadAgentToolCalls(storage.AgentCallsPath(path))
		if err != nil {
			t.Fatalf("read calls: %v", err)
		}
		if got := storage.CountAgentToolCalls(calls, "agent-a"); got != 2 {
			t.Errorf("agent-a used budget = %d, want 2 (denied calls do not count)", got)
		}
		denied := 0
		for _, c := range calls {
			if c.Denied {
				denied++
			}
		}
		if denied != 1 {
			t.Errorf("denied calls recorded = %d, want 1", denied)
		}
	})

	t.Run("the worker's own calls are never metered", func(t *testing.T) {
		shrinkBudget(t, 1)
		path := filepath.Join(t.TempDir(), "review.jsonl")
		for i := 0; i < 5; i++ {
			if code := runWorkerGuard(strings.NewReader(payload("Read", "")), io.Discard, &bytes.Buffer{}, guardOptions{telemetryPath: path}); code != 0 {
				t.Fatalf("worker-level Read %d: exit %d, want 0", i, code)
			}
		}
		if calls, _ := storage.ReadAgentToolCalls(storage.AgentCallsPath(path)); len(calls) != 0 {
			t.Errorf("worker-level calls must not be recorded, got %d", len(calls))
		}
	})

	t.Run("fails open without a telemetry path or on an unreadable state file", func(t *testing.T) {
		shrinkBudget(t, 0)
		// Budget disabled entirely.
		if code := runWorkerGuard(strings.NewReader(payload("Read", "agent-a")), io.Discard, &bytes.Buffer{}, guardOptions{}); code != 0 {
			t.Error("no telemetry path must mean no budget")
		}
		shrinkBudget(t, 1)
		if code := runWorkerGuard(strings.NewReader(payload("Read", "agent-a")), io.Discard, &bytes.Buffer{}, guardOptions{}); code != 0 {
			t.Error("no telemetry path must mean no budget even with one configured")
		}
		// The calls path resolves to a DIRECTORY: reading it errors, and the
		// budget must allow rather than block on state it cannot establish.
		dir := t.TempDir()
		path := filepath.Join(dir, "review.jsonl")
		if err := os.MkdirAll(storage.AgentCallsPath(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if code := runWorkerGuard(strings.NewReader(payload("Read", "agent-a")), io.Discard, &bytes.Buffer{}, guardOptions{telemetryPath: path}); code != 0 {
			t.Error("an unreadable calls file must fail open")
		}
	})

	t.Run("parallel agents cannot leak past their budgets", func(t *testing.T) {
		shrinkBudget(t, 5)
		path := filepath.Join(t.TempDir(), "review.jsonl")
		opts := guardOptions{telemetryPath: path}
		var wg sync.WaitGroup
		for _, agent := range []string{"agent-a", "agent-b"} {
			for i := 0; i < 12; i++ {
				wg.Add(1)
				go func(agent string) {
					defer wg.Done()
					runWorkerGuard(strings.NewReader(payload("Read", agent)), io.Discard, &bytes.Buffer{}, opts)
				}(agent)
			}
		}
		wg.Wait()
		calls, err := storage.ReadAgentToolCalls(storage.AgentCallsPath(path))
		if err != nil {
			t.Fatalf("read calls: %v", err)
		}
		for _, agent := range []string{"agent-a", "agent-b"} {
			if got := storage.CountAgentToolCalls(calls, agent); got > 5 {
				t.Errorf("%s ran %d calls, budget is 5 - the lock leaked", agent, got)
			}
		}
	})
}

// The guard's original job must be untouched by the new branch: a hook-bypassing
// commit is still blocked even when a telemetry path is configured.
func TestWorkerGuardStillBlocksWithTelemetryConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.jsonl")
	in := `{"tool_name":"Bash","tool_input":{"command":"git commit -m x --no-verify"}}`
	var errOut bytes.Buffer
	if code := runWorkerGuard(strings.NewReader(in), io.Discard, &errOut, guardOptions{telemetryPath: path}); code != 2 {
		t.Errorf("got exit %d, want 2 (%s)", code, errOut.String())
	}
}
