package solo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// fakeClaude stands in for the claude binary: on `--bg` it records cwd, the
// environment rule and the argv, then prints the documented block; on
// `agents --json --all` it prints the canned rows of $FAKE_AGENTS; on
// `stop <id>` it records the call. FAKE_MODE=fail makes --bg exit 1 with
// no id; FAKE_MODE=noid exits 0 with unrelated output.
const fakeClaude = `#!/bin/sh
case "$1" in
agents)
  printf '%s\n' "agents $*" >> "$FAKE_CALLS"
  cat "$FAKE_AGENTS" ;;
stop)
  printf '%s\n' "stop $2 cfg=$CLAUDE_CONFIG_DIR" >> "$FAKE_CALLS"
  echo "stopped $2" ;;
*)
  { pwd -P; printf 'cfg=%s\n' "$CLAUDE_CONFIG_DIR"; printf 'source=%s\n' "$PM_LAUNCH_SOURCE"
    printf 'sid=%s headless=%s cc=%s job=%s\n' "$CLAUDE_CODE_SESSION_ID" "$PM_HEADLESS" "$CLAUDECODE" "$CLAUDE_JOB_DIR"
    printf '%s\n' "$@"; } > "$FAKE_OUT"
  case "$FAKE_MODE" in
  fail) echo "Error: not logged in" >&2; exit 1 ;;
  noid) echo "something else entirely"; exit 0 ;;
  esac
  echo "Starting background service…" >&2
  echo "backgrounded · ab12cd34 · solo-app"
  echo "  claude agents             list sessions"
  echo "  claude attach ab12cd34    open in this terminal" ;;
esac
`

func setup(t *testing.T) (*Controller, string, string) {
	t.Helper()
	root := t.TempDir()
	store := &storage.Store{Root: root}
	checkout := t.TempDir()
	gitInit(t, checkout)
	if err := store.CreateProject("app", &storage.Project{Name: "app", Prefix: "app", Path: checkout, ClaudeConfigDir: "/tmp/cfg-company"}); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	exe := filepath.Join(bin, "claude")
	if err := os.WriteFile(exe, []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(bin, "argv.txt")
	agents := filepath.Join(bin, "agents.json")
	if err := os.WriteFile(agents, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_OUT", out)
	t.Setenv("FAKE_AGENTS", agents)
	t.Setenv("FAKE_CALLS", filepath.Join(bin, "calls.txt"))
	t.Setenv("FAKE_MODE", "")
	// pm serve started from inside a Claude session leaks these; the
	// launch must not.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "parent-session")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("PM_HEADLESS", "1")
	return &Controller{Store: store, Exe: exe}, checkout, out
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "add", "README.md")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func setAgents(t *testing.T, rows string) {
	t.Helper()
	if err := os.WriteFile(os.Getenv("FAKE_AGENTS"), []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPromptAndArgv(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Input
		ask  int
		want string
	}{
		{"minimal", Input{Queue: "app-7"}, 0,
			`--dangerously-skip-permissions --autocompact 400k --settings {"worktree":{"bgIsolation":"none"}} --name solo-app --bg /solo app-7`},
		{"every flag", Input{Queue: "app-7 app-8", Runtime: RuntimeOff, Base: "main", Model: "opus", Push: true, PR: true, MaxTasks: 2, MaxHours: 3}, 0,
			`--dangerously-skip-permissions --autocompact 400k --settings {"worktree":{"bgIsolation":"none"}} --model opus --name solo-app --bg /solo app-7 app-8 --no-runtime --push --pr --max-tasks 2 --max-hours 3 --base main`},
		{"ask rules add the setting sources", Input{Queue: "ACME-1234", Runtime: RuntimeSim}, 2,
			`--dangerously-skip-permissions --autocompact 400k --settings {"worktree":{"bgIsolation":"none"}} --setting-sources user,local --name solo-app --bg /solo ACME-1234 --sim`},
		{"web runtime", Input{Queue: "https://linear.app/x/issue/APP-1"}, 0,
			`--dangerously-skip-permissions --autocompact 400k --settings {"worktree":{"bgIsolation":"none"}} --name solo-app --bg /solo https://linear.app/x/issue/APP-1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(buildArgv(tc.in, tc.ask, "solo-app"), " ")
			if got != tc.want {
				t.Fatalf("argv =\n %s\nwant\n %s", got, tc.want)
			}
		})
	}
	if p := (Input{Queue: " app-1 ", Runtime: RuntimeWeb}).Prompt(); p != "/solo app-1 --web" {
		t.Fatalf("prompt = %q", p)
	}
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Input
		want string
	}{
		{"no project", Input{Queue: "x"}, "project is required"},
		{"no queue", Input{Project: "app"}, "queue is required"},
		{"two lines", Input{Project: "app", Queue: "a\nb"}, "one line"},
		{"bad runtime", Input{Project: "app", Queue: "a", Runtime: "phone"}, "runtime must be"},
		{"bad model", Input{Project: "app", Queue: "a", Model: "opus; rm -rf"}, "model"},
		{"bad base", Input{Project: "app", Queue: "a", Base: "main --force"}, "branch name"},
		{"negative", Input{Project: "app", Queue: "a", MaxHours: -1}, ">= 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			var ie *InputError
			if err == nil || !errorsAs(err, &ie) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want InputError containing %q", err, tc.want)
			}
		})
	}
	if err := (Input{Project: "app", Queue: "app-1", Runtime: RuntimeSim, Model: "claude-opus-5", Base: "release/1.2"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPlan(t *testing.T) {
	c, checkout, _ := setup(t)

	t.Run("resolves the project, builds the argv, no warnings on a clean tree", func(t *testing.T) {
		p, err := c.Plan(Input{Project: "app", Queue: " app-7 ", Runtime: RuntimeOff, Base: "main"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Cwd != checkout || p.ConfigDir != "/tmp/cfg-company" || p.Name != "solo-app" || p.Prompt != "/solo app-7 --no-runtime --base main" {
			t.Fatalf("plan = %+v", p)
		}
		if p.Argv[len(p.Argv)-1] != p.Prompt || p.Argv[len(p.Argv)-2] != "--bg" {
			t.Fatalf("argv = %v", p.Argv)
		}
		if len(p.Warnings) != 0 {
			t.Fatalf("warnings = %v", p.Warnings)
		}
	})

	t.Run("unknown project, no path, bad input", func(t *testing.T) {
		if _, err := c.Plan(Input{Project: "nope", Queue: "x"}); err == nil || !errorsIs(err, storage.ErrProjectNotFound) {
			t.Fatalf("err = %v", err)
		}
		var ie *InputError
		if _, err := c.Plan(Input{Project: "app", Queue: ""}); !errorsAs(err, &ie) {
			t.Fatalf("err = %v", err)
		}
		if err := c.Store.CreateProject("nopath", &storage.Project{Name: "n", Prefix: "n"}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Plan(Input{Project: "nopath", Queue: "x"}); !errorsAs(err, &ie) || !strings.Contains(err.Error(), "no path") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("warnings: dirty tree, missing base, origin drift, ask rules without a local mirror, an open shift", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(checkout, "wip.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		p, err := c.Plan(Input{Project: "app", Queue: "app-7", Base: "nope"})
		if err != nil {
			t.Fatal(err)
		}
		if !hasWarning(p, "1 uncommitted change") || !hasWarning(p, "branch nope does not exist") {
			t.Fatalf("warnings = %v", p.Warnings)
		}
		// origin/main behind local main by one commit, and local behind
		// origin by one: both directions warn.
		git(t, checkout, "add", "wip.txt")
		git(t, checkout, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "wip")
		git(t, checkout, "update-ref", "refs/remotes/origin/main", "HEAD~1")
		p, _ = c.Plan(Input{Project: "app", Queue: "app-7", Base: "main"})
		if !hasWarning(p, "local main is 1 commit(s) ahead of origin/main") || hasWarning(p, "uncommitted") {
			t.Fatalf("warnings = %v", p.Warnings)
		}
		git(t, checkout, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "remote-only")
		git(t, checkout, "update-ref", "refs/remotes/origin/main", "HEAD")
		git(t, checkout, "reset", "-q", "--hard", "HEAD~1")
		p, _ = c.Plan(Input{Project: "app", Queue: "app-7", Base: "main"})
		if !hasWarning(p, "origin/main is 1 commit(s) ahead of local main") {
			t.Fatalf("warnings = %v", p.Warnings)
		}
		// The default base comes from executor.base_branch when the form
		// leaves it empty.
		if _, err := c.Store.MutateProject("app", func(pr *storage.Project) error {
			pr.Executor = &storage.Executor{BaseBranch: "main"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		p, _ = c.Plan(Input{Project: "app", Queue: "app-7"})
		if !hasWarning(p, "origin/main is 1 commit(s) ahead") {
			t.Fatalf("warnings = %v", p.Warnings)
		}
		// ask rules: the flag is added, and without settings.local.json a warning.
		if err := os.MkdirAll(filepath.Join(checkout, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(checkout, ".claude", "settings.json"), []byte(`{"permissions":{"ask":["Bash(pnpm test:*)"]}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		p, _ = c.Plan(Input{Project: "app", Queue: "app-7"})
		if p.AskRules != 1 || !strings.Contains(strings.Join(p.Argv, " "), "--setting-sources user,local") || !hasWarning(p, "no .claude/settings.local.json") {
			t.Fatalf("plan = %+v", p)
		}
		if err := os.WriteFile(filepath.Join(checkout, ".claude", "settings.local.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
		p, _ = c.Plan(Input{Project: "app", Queue: "app-7"})
		if hasWarning(p, "settings.local.json") {
			t.Fatalf("warnings = %v", p.Warnings)
		}
		// An open shift of the project.
		shiftDir := filepath.Join(c.Store.ProjectDir("app"), ".shift")
		if err := os.MkdirAll(shiftDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(shiftDir, "2026-09-15-ab12cd34-0000.md"), []byte("# solo shift\n\n## Status: open\n\n## Queue\n- app-7 · x · todo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		p, _ = c.Plan(Input{Project: "app", Queue: "app-7"})
		if !hasWarning(p, "already open") {
			t.Fatalf("warnings = %v", p.Warnings)
		}
	})
}

func hasWarning(p *Plan, s string) bool {
	for _, w := range p.Warnings {
		if strings.Contains(w, s) {
			return true
		}
	}
	return false
}

func TestStartListStop(t *testing.T) {
	c, checkout, out := setup(t)
	p, err := c.Plan(Input{Project: "app", Queue: "app-7", Runtime: RuntimeOff, Model: "opus"})
	if err != nil {
		t.Fatal(err)
	}

	l, err := c.Start(p)
	if err != nil {
		t.Fatal(err)
	}
	if l.ID != "ab12cd34" || l.Project != "app" || l.Name != "solo-app" || l.Cwd != checkout {
		t.Fatalf("launch = %+v", l)
	}
	// No supervisor row yet: starting, with the attach command carrying the
	// non-default config dir.
	if l.State != StateStarting || l.Attach != "CLAUDE_CONFIG_DIR=/tmp/cfg-company claude attach ab12cd34" || l.Logs != "CLAUDE_CONFIG_DIR=/tmp/cfg-company claude logs ab12cd34" {
		t.Fatalf("launch = %+v", l)
	}
	rec, _ := os.ReadFile(out)
	got := string(rec)
	for _, want := range []string{checkout + "\n", "cfg=/tmp/cfg-company\n", "source=cockpit\n", "sid= headless= cc= job=\n", "--bg\n/solo app-7 --no-runtime\n", "--model\nopus\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fake claude saw:\n%s\nwant %q", got, want)
		}
	}
	if log, _ := os.ReadFile(l.Log); !strings.Contains(string(log), "backgrounded · ab12cd34") || !strings.Contains(string(log), "Starting background service") {
		t.Fatalf("log = %s", log)
	}

	// The supervisor's row fills state, status, pid and the session id
	// (remembered on disk, so a later read without the row keeps it).
	setAgents(t, `[{"pid":1,"kind":"interactive","cwd":"/x","startedAt":1},
	  {"id":"ab12cd34","kind":"background","cwd":"`+checkout+`","startedAt":2,"sessionId":"ab12cd34-1111-2222-3333-444444444444","name":"solo-app","pid":4242,"status":"waiting","state":"blocked","waitingFor":"permission prompt"}]`)
	c.invalidate("/tmp/cfg-company")
	l, err = c.Get("ab12cd34")
	if err != nil {
		t.Fatal(err)
	}
	if l.State != StateBlocked || l.Status != "waiting" || l.WaitingFor != "permission prompt" || l.PID != 4242 || l.SessionID != "ab12cd34-1111-2222-3333-444444444444" || !l.Live() {
		t.Fatalf("launch = %+v", l)
	}
	setAgents(t, `[]`)
	c.invalidate("/tmp/cfg-company")
	c.Now = func() time.Time { return time.Now().Add(time.Hour) }
	l, _ = c.Get("ab12cd34")
	if l.State != StateUnknown || l.SessionID != "ab12cd34-1111-2222-3333-444444444444" {
		t.Fatalf("launch = %+v", l)
	}
	c.Now = nil

	// A second plan warns about the live launch.
	setAgents(t, `[{"id":"ab12cd34","kind":"background","cwd":"x","startedAt":2,"state":"working","status":"busy"}]`)
	c.invalidate("/tmp/cfg-company")
	p2, _ := c.Plan(Input{Project: "app", Queue: "app-8"})
	if !hasWarning(p2, "solo session ab12cd34 of app is still working") {
		t.Fatalf("warnings = %v", p2.Warnings)
	}

	// The answer of `claude agents` is cached for AgentsTTL.
	calls := func() int {
		b, _ := os.ReadFile(os.Getenv("FAKE_CALLS"))
		return strings.Count(string(b), "agents --json --all")
	}
	before := calls()
	_, _ = c.List()
	_, _ = c.Get("ab12cd34")
	if calls() != before {
		t.Fatalf("agents called %d times within the TTL", calls()-before)
	}

	// Stop = `claude stop <id>` under the launch's config dir, stamped.
	l, err = c.Stop("ab12cd34")
	if err != nil {
		t.Fatal(err)
	}
	if l.Stopped == "" {
		t.Fatalf("launch = %+v", l)
	}
	if b, _ := os.ReadFile(os.Getenv("FAKE_CALLS")); !strings.Contains(string(b), "stop ab12cd34 cfg=/tmp/cfg-company") {
		t.Fatalf("calls = %s", b)
	}
	setAgents(t, `[]`)
	c.invalidate("/tmp/cfg-company")
	c.Now = func() time.Time { return time.Now().Add(time.Hour) }
	if l, _ = c.Get("ab12cd34"); l.State != StateStopped {
		t.Fatalf("launch = %+v", l)
	}

	// Records survive a new controller; unknown ids are ErrNotFound.
	c2 := &Controller{Store: c.Store, Exe: c.Exe}
	ls, err := c2.List()
	if err != nil || len(ls) != 1 || ls[0].ID != "ab12cd34" {
		t.Fatalf("list = %v, %v", ls, err)
	}
	if _, err := c2.Get("nope"); err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
	if _, err := c2.Get("../x"); err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestStartFailures(t *testing.T) {
	c, _, _ := setup(t)
	p, err := c.Plan(Input{Project: "app", Queue: "app-7"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_MODE", "fail")
	_, err = c.Start(p)
	var se *StartError
	if !errorsAs(err, &se) || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("FAKE_MODE", "noid")
	_, err = c.Start(p)
	if !errorsAs(err, &se) || !strings.Contains(err.Error(), "no session id") {
		t.Fatalf("err = %v", err)
	}
	// Both failures are on disk as error launches; stop refuses them.
	ls, _ := c.List()
	if len(ls) != 2 || ls[0].State != StateError || ls[1].Error == "" {
		t.Fatalf("list = %+v", ls)
	}
	var ie *InputError
	if _, err := c.Stop(ls[0].ID); !errorsAs(err, &ie) {
		t.Fatalf("err = %v", err)
	}
	// No binary at all: a warning on the plan, an error on start.
	c.Exe = filepath.Join(t.TempDir(), "missing")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	c.Exe = ""
	p, err = c.Plan(Input{Project: "app", Queue: "app-7"})
	if err != nil || !hasWarning(p, "claude not found") {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	if _, err := c.Start(p); !errorsAs(err, &se) {
		t.Fatalf("err = %v", err)
	}
}

// The default config dir is NEVER put on the environment: an explicit
// CLAUDE_CONFIG_DIR=~/.claude keys the keychain login differently and claude
// answers "Not logged in" (the first cockpit launch, 2026-09-15).
func TestPinConfigDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	def := filepath.Join(home, ".claude")
	if PinConfigDir(def) || PinConfigDir(def+"/") || PinConfigDir("") {
		t.Fatal("the default dir must not be pinned")
	}
	if !PinConfigDir(filepath.Join(home, ".claude-work")) {
		t.Fatal("a non-default dir must be pinned")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/leaked-from-pm-serve")
	for _, kv := range Environ(def) {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			t.Fatalf("default dir on the environment: %s", kv)
		}
	}
	var pinned bool
	for _, kv := range Environ("/tmp/cfg-company") {
		pinned = pinned || kv == "CLAUDE_CONFIG_DIR=/tmp/cfg-company"
	}
	if !pinned {
		t.Fatal("non-default dir missing from the environment")
	}
}
