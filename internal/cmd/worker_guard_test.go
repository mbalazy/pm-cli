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
			t.Errorf("exit code = %d, want 0 - the guard only judges Bash", code)
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
	if len(parsed.Hooks.PreToolUse) != 1 || parsed.Hooks.PreToolUse[0].Matcher != "Bash" {
		t.Fatalf("expected one Bash PreToolUse matcher, got %s", s)
	}
	if got := parsed.Hooks.PreToolUse[0].Hooks[0].Command; !strings.HasSuffix(got, " worker-guard") {
		t.Errorf("hook command = %q, want it to invoke pm worker-guard", got)
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
