package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBashCommandBlocked(t *testing.T) {
	blocked := []struct{ name, cmd string }{
		{"leading no-verify", `git commit --no-verify -m "x"`},
		// The form prefix-based permission rules cannot catch, and the one a
		// model actually reaches for.
		{"trailing no-verify", `git commit -m "x" --no-verify`},
		{"short -n on commit", `git commit -m "x" -n`},
		{"short -n on push", `git push -n origin main`},
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
		{"empty", ``},
	}
	for _, c := range allowed {
		if ok, what := bashCommandBlocked(c.cmd); ok {
			t.Errorf("%s: %q must be allowed, blocked as %s", c.name, c.cmd, what)
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
		code := runWorkerGuard(strings.NewReader(payload("Bash", `git commit -m x --no-verify`)), &errOut)
		if code != 2 {
			t.Fatalf("exit code = %d, want 2 (the code Claude Code reads as a block)", code)
		}
		if !strings.Contains(errOut.String(), "bypasses the project's git hooks") {
			t.Errorf("stderr = %q, want the reason the model gets shown", errOut.String())
		}
	})

	t.Run("passes ordinary commands", func(t *testing.T) {
		var errOut bytes.Buffer
		if code := runWorkerGuard(strings.NewReader(payload("Bash", `git commit -m x`)), &errOut); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if errOut.Len() != 0 {
			t.Errorf("nothing to say on a pass, got %q", errOut.String())
		}
	})

	t.Run("ignores other tools", func(t *testing.T) {
		if code := runWorkerGuard(strings.NewReader(payload("Read", `git commit --no-verify`)), &bytes.Buffer{}); code != 0 {
			t.Errorf("exit code = %d, want 0 - a command only counts as one on Bash", code)
		}
	})

	// Fail-open on anything unexpected: a guard that blocked on a payload it
	// could not read would stop every worker on the machine the first time the
	// hook schema changed.
	t.Run("fails open on junk", func(t *testing.T) {
		for _, in := range []string{"", "not json", "{}", `{"tool_name":"Bash"}`} {
			if code := runWorkerGuard(strings.NewReader(in), &bytes.Buffer{}); code != 0 {
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
		if code := runWorkerGuard(strings.NewReader(payload(c.tool, c.path)), &errOut); code != 2 {
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
		if code := runWorkerGuard(strings.NewReader(payload(c.tool, c.path)), &errOut); code != 0 {
			t.Errorf("%s: exit code = %d, want 0 (%s)", c.name, code, errOut.String())
		}
	}
}

func TestWorkerGuardSettingsIsValidAndAttached(t *testing.T) {
	s := workerGuardSettings()
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
	// Two entries on purpose: the Bash matcher is the one verified against a
	// live claude and must keep its exact spelling; the write matcher is a
	// separate entry so a build that matches tool names literally simply does
	// not fire it instead of losing the Bash one too.
	if len(parsed.Hooks.PreToolUse) != 2 {
		t.Fatalf("expected a Bash and a write-tool PreToolUse matcher, got %s", s)
	}
	if parsed.Hooks.PreToolUse[0].Matcher != "Bash" {
		t.Errorf("first matcher = %q, want the verified exact \"Bash\"", parsed.Hooks.PreToolUse[0].Matcher)
	}
	for _, tool := range []string{"Write", "Edit", "MultiEdit", "NotebookEdit"} {
		if !strings.Contains(parsed.Hooks.PreToolUse[1].Matcher, tool) {
			t.Errorf("write matcher %q must name %s", parsed.Hooks.PreToolUse[1].Matcher, tool)
		}
	}
	for i, m := range parsed.Hooks.PreToolUse {
		if got := m.Hooks[0].Command; !strings.HasSuffix(got, " worker-guard") {
			t.Errorf("matcher %d hook command = %q, want it to invoke pm worker-guard", i, got)
		}
	}

	// Both modes carry it: --yolo waives permission prompts, not the project's
	// hooks.
	for _, yolo := range []bool{false, true} {
		args := strings.Join(buildClaudeArgs("p", "sp", "sess", "opus", 10, yolo), " ")
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
}
