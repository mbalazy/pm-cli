package board

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// launchRig observes launchClaude without starting anything: execProcess is
// swapped for a recorder, and `tmux` on PATH is a script that logs every call
// (calls separated by \x1e, arguments by \x1f - a prompt spans lines) and
// fails new-window when the fail file exists.
type launchRig struct {
	t        *testing.T
	store    *storage.Store
	task     *storage.Task
	projPath string
	home     string
	tmuxLog  string
	tmuxFail string
	cmd      *exec.Cmd
}

const fakeTmux = `#!/bin/sh
for a in "$@"; do printf '%s\037' "$a" >> "$FAKE_TMUX_LOG"; done
printf '\036' >> "$FAKE_TMUX_LOG"
case "$1" in
display-message) echo orig-win ;;
new-window) if [ -f "$FAKE_TMUX_FAIL" ]; then echo boom >&2; exit 1; fi ;;
esac
exit 0
`

func newLaunchRig(t *testing.T, proj *storage.Project) *launchRig {
	t.Helper()
	r := &launchRig{t: t, home: t.TempDir()}
	bin := t.TempDir()
	r.tmuxLog = filepath.Join(bin, "tmux.log")
	r.tmuxFail = filepath.Join(bin, "fail")
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(fakeTmux), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_TMUX_LOG", r.tmuxLog)
	t.Setenv("FAKE_TMUX_FAIL", r.tmuxFail)
	t.Setenv("HOME", r.home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	orig := execProcess
	t.Cleanup(func() { execProcess = orig })
	execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		r.cmd = c
		return func() tea.Msg { return fn(nil) }
	}

	if proj.Path == "launch-repo" {
		r.projPath = t.TempDir()
		proj.Path = r.projPath
		for _, args := range [][]string{{"init", "-q"}, {"commit", "--allow-empty", "-q", "-m", "init"}} {
			c := exec.Command("git", args...)
			c.Dir = r.projPath
			c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
				"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
	}
	r.store = &storage.Store{Root: t.TempDir()}
	if err := r.store.CreateProject("acme", proj); err != nil {
		t.Fatal(err)
	}
	r.task = &storage.Task{Meta: storage.TaskMeta{ID: "acme-1", Title: "Launch me", Status: storage.StatusTodo,
		Brief: "the brief", Sessions: []string{"s-old"}}, Body: "the body"}
	if err := r.store.AddTask("acme", r.task); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *launchRig) model() Model {
	return Model{
		store:       r.store,
		projects:    []string{"all", "acme"},
		currentView: viewDetail,
		detailState: detailState{detailTask: r.task},
		width:       80, height: 24,
	}
}

// tmuxCalls returns every logged tmux call as its argument list.
func (r *launchRig) tmuxCalls() [][]string {
	data, _ := os.ReadFile(r.tmuxLog)
	var calls [][]string
	for _, line := range strings.Split(string(data), "\x1e") {
		if line == "" {
			continue
		}
		calls = append(calls, strings.Split(strings.TrimSuffix(line, "\x1f"), "\x1f"))
	}
	return calls
}

// newWindow returns the window name and shell command of the one new-window
// call.
func (r *launchRig) newWindow() (string, string) {
	r.t.Helper()
	for _, c := range r.tmuxCalls() {
		if c[0] == "new-window" {
			// new-window -n <name> sh -c <cmd>
			if len(c) != 6 || c[3] != "sh" || c[4] != "-c" {
				r.t.Fatalf("new-window call = %q", c)
			}
			return c[2], c[5]
		}
	}
	r.t.Fatalf("no new-window call in %q", r.tmuxCalls())
	return "", ""
}

func (r *launchRig) newSession() string {
	r.t.Helper()
	s := r.task.Meta.Sessions
	if len(s) == 0 || s[len(s)-1] == "s-old" {
		r.t.Fatalf("no new session saved: %v", s)
	}
	return s[len(s)-1]
}

func (r *launchRig) storedSessions() []string {
	r.t.Helper()
	fresh, err := r.store.FindTask("acme", "acme-1")
	if err != nil {
		r.t.Fatal(err)
	}
	return fresh.Meta.Sessions
}

func repoProject() *storage.Project { return &storage.Project{Name: "Acme", Path: "launch-repo"} }

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

// TestLaunchClaudeKinds pins what every launch kind runs and records: the
// argv, working dir and env handed to the terminal, the tmux window and shell
// command, the sessions saved on the task and the toast.
func TestLaunchClaudeKinds(t *testing.T) {
	t.Run("no task does nothing", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		m := r.model()
		m.detailTask = nil
		m.currentView = viewBoard
		next, cmd := m.launchClaude("here")
		if cmd != nil || r.cmd != nil || next.(Model).toastMsg != "" {
			t.Error("launched without a task")
		}
	})

	t.Run("project opens the project launch menu", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		next, cmd := r.model().launchClaude("project")
		m := next.(Model)
		if cmd != nil || !m.claudeMenu || !m.projectScopeLaunch || m.projectScopeSlug != "acme" {
			t.Errorf("claudeMenu = %v scope = %v %q", m.claudeMenu, m.projectScopeLaunch, m.projectScopeSlug)
		}
	})

	t.Run("here", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		prompt := buildClaudePrompt(r.task, r.store)
		_, cmd := r.model().launchClaude("here")
		sid := r.newSession()
		assertArgsEqual(t, r.cmd.Args, []string{"claude", "--session-id", sid, prompt})
		if r.cmd.Dir != r.projPath || r.cmd.Env != nil {
			t.Errorf("dir = %q env = %v", r.cmd.Dir, r.cmd.Env)
		}
		if got := r.storedSessions(); len(got) != 2 || got[1] != sid {
			t.Errorf("stored sessions = %v", got)
		}
		calls := r.tmuxCalls()
		if len(calls) != 2 || calls[1][0] != "rename-window" || calls[1][1] != tmuxWindowName("cc", "acme-1", sid) {
			t.Errorf("tmux calls before the launch = %q", calls)
		}
		if _, ok := cmd().(reloadMsg); !ok {
			t.Error("the launch should end in a reload")
		}
		calls = r.tmuxCalls()
		if last := calls[len(calls)-1]; last[0] != "rename-window" || last[1] != "orig-win" {
			t.Errorf("window not renamed back: %q", last)
		}
	})

	t.Run("here with skip-permissions and a config dir", func(t *testing.T) {
		cfg := t.TempDir()
		r := newLaunchRig(t, &storage.Project{Name: "Acme", Path: "launch-repo", ClaudeConfigDir: cfg})
		m := r.model()
		m.claudeMenuSkipPerms = true
		m.launchClaude("here")
		if r.cmd.Args[3] != "--dangerously-skip-permissions" || !hasEnv(r.cmd.Env, "CLAUDE_CONFIG_DIR="+cfg) {
			t.Errorf("args = %q env has config dir = %v", r.cmd.Args, hasEnv(r.cmd.Env, "CLAUDE_CONFIG_DIR="+cfg))
		}
	})

	t.Run("here when the session cannot be saved still launches", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		r.task.FilePath = filepath.Join(t.TempDir(), "missing", "acme-1.md") // the write fails
		next, cmd := r.model().launchClaude("here")
		if !strings.HasPrefix(next.(Model).toastMsg, "save session failed") || cmd == nil || r.cmd == nil {
			t.Errorf("toast = %q launched = %v", next.(Model).toastMsg, r.cmd != nil)
		}
	})

	t.Run("here with no project path runs in the current dir", func(t *testing.T) {
		r := newLaunchRig(t, &storage.Project{Name: "Acme"})
		r.model().launchClaude("here")
		if r.cmd.Dir != "" {
			t.Errorf("dir = %q", r.cmd.Dir)
		}
	})

	t.Run("tmux", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		prompt := buildClaudePrompt(r.task, r.store)
		next, cmd := r.model().launchClaude("tmux")
		sid := r.newSession()
		name, shell := r.newWindow()
		want := "cd " + shellQuote(r.projPath) + " && " + claudeShellCommand(claudeSessionArgs(sid, "", prompt))
		if cmd != nil || r.cmd != nil || name != tmuxWindowName("cc", "acme-1", sid) || shell != want {
			t.Errorf("name = %q shell = %q\nwant %q", name, shell, want)
		}
		if got := next.(Model).toastMsg; got != "Launched in tmux: "+name {
			t.Errorf("toast = %q", got)
		}
	})

	t.Run("tmux with a config dir and no project path", func(t *testing.T) {
		cfg := t.TempDir()
		r := newLaunchRig(t, &storage.Project{Name: "Acme", ClaudeConfigDir: cfg})
		r.model().launchClaude("tmux")
		_, shell := r.newWindow()
		if !strings.HasPrefix(shell, claudeEnvPrefix(cfg)+"claude ") {
			t.Errorf("shell = %q", shell)
		}
	})

	t.Run("tmux failure", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		if err := os.WriteFile(r.tmuxFail, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		next, _ := r.model().launchClaude("tmux")
		if got := next.(Model).toastMsg; got != "tmux failed: boom" {
			t.Errorf("toast = %q", got)
		}
	})

	t.Run("worktree", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		prompt := buildClaudePrompt(r.task, r.store)
		_, cmd := r.model().launchClaude("worktree")
		sid := r.newSession()
		assertArgsEqual(t, r.cmd.Args, []string{"claude", "-w", worktreeName(r.task), "--session-id", sid, prompt})
		if r.cmd.Dir != r.projPath || r.cmd.Env != nil {
			t.Errorf("dir = %q env = %v", r.cmd.Dir, r.cmd.Env)
		}
		if _, ok := cmd().(reloadMsg); !ok {
			t.Error("the launch should end in a reload")
		}
		if calls := r.tmuxCalls(); calls[1][1] != tmuxWindowName("wt", "acme-1", sid) {
			t.Errorf("tmux calls = %q", calls)
		}
	})

	t.Run("worktree with a free slot claims it", func(t *testing.T) {
		r := newLaunchRig(t, &storage.Project{Name: "Acme", Path: "launch-repo",
			Executor: &storage.Executor{Worktrees: []storage.WorktreeSlot{{Path: "../slot", Env: map[string]string{"PORT": "7001"}}}}})
		prompt := buildClaudePrompt(r.task, r.store)
		r.model().launchClaude("worktree")
		sid := r.newSession()
		a := r.cmd.Args
		if len(a) < 4 || a[0] != "sh" || a[1] != "-c" || a[3] != "claude" ||
			!strings.HasSuffix(a[2], `exec claude "$@"`) || !strings.Contains(a[2], storage.SessionLockPath(r.store.ProjectDir("acme"), 1)) {
			t.Fatalf("args = %q", a)
		}
		assertArgsEqual(t, a[4:], claudeWorktreeArgs(worktreeName(r.task), sid, "", prompt))
		if !hasEnv(r.cmd.Env, "PORT=7001") {
			t.Errorf("slot env missing: %v", r.cmd.Env)
		}
	})

	t.Run("worktree-tmux", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		prompt := buildClaudePrompt(r.task, r.store)
		next, cmd := r.model().launchClaude("worktree-tmux")
		sid := r.newSession()
		name, shell := r.newWindow()
		want := "cd " + shellQuote(r.projPath) + " && " + claudeShellCommand(claudeWorktreeArgs(worktreeName(r.task), sid, "", prompt))
		if cmd != nil || name != tmuxWindowName("wt", "acme-1", sid) || shell != want {
			t.Errorf("name = %q shell = %q\nwant %q", name, shell, want)
		}
		if got := next.(Model).toastMsg; got != "Launched worktree in tmux: "+name {
			t.Errorf("toast = %q", got)
		}
	})

	t.Run("worktree-tmux with a free slot claims it", func(t *testing.T) {
		r := newLaunchRig(t, &storage.Project{Name: "Acme", Path: "launch-repo",
			Executor: &storage.Executor{Worktrees: []storage.WorktreeSlot{{Path: "../slot", Env: map[string]string{"PORT": "7001"}}}}})
		r.model().launchClaude("worktree-tmux")
		_, shell := r.newWindow()
		lock := storage.SessionLockPath(r.store.ProjectDir("acme"), 1)
		if !strings.HasPrefix(shell, "cd "+shellQuote(r.projPath)+" && mkdir -p ") || !strings.Contains(shell, lock) ||
			!strings.Contains(shell, "; PORT='7001' claude '-w' ") {
			t.Errorf("shell = %q", shell)
		}
	})

	t.Run("worktree-tmux failure", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		if err := os.WriteFile(r.tmuxFail, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		next, _ := r.model().launchClaude("worktree-tmux")
		if got := next.(Model).toastMsg; got != "tmux failed: boom" {
			t.Errorf("toast = %q", got)
		}
	})

	t.Run("resume the last session", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		next, cmd := r.model().launchClaude("resume")
		assertArgsEqual(t, r.cmd.Args, []string{"claude", "--resume", "s-old"})
		if r.cmd.Dir != r.projPath || r.cmd.Env != nil || next.(Model).resumeSessionID != "" {
			t.Errorf("dir = %q env = %v", r.cmd.Dir, r.cmd.Env)
		}
		if got := r.storedSessions(); len(got) != 1 {
			t.Errorf("resume must not save a session: %v", got)
		}
		if _, ok := cmd().(reloadMsg); !ok {
			t.Error("the launch should end in a reload")
		}
		if calls := r.tmuxCalls(); calls[1][1] != tmuxWindowName("cc", "acme-1", "s-old") {
			t.Errorf("tmux calls = %q", calls)
		}
	})

	t.Run("resume a picked session with skip-permissions", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		m := r.model()
		m.resumeSessionID = "s-picked"
		m.claudeMenuSkipPerms = true
		next, _ := m.launchClaude("resume")
		assertArgsEqual(t, r.cmd.Args, []string{"claude", "--resume", "s-picked", "--dangerously-skip-permissions"})
		if next.(Model).resumeSessionID != "" {
			t.Error("the picked session should be consumed")
		}
	})

	t.Run("resume a session that lives in a worktree", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		wt := filepath.Join(r.projPath, ".claude", "worktrees", "wt1")
		ccDir := filepath.Join(r.home, ".claude", "projects", pathToCCProject(wt))
		for _, d := range []string{wt, ccDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(ccDir, "s-old.jsonl"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		r.model().launchClaude("resume")
		if r.cmd.Dir != wt {
			t.Errorf("dir = %q, want the worktree %q", r.cmd.Dir, wt)
		}
	})

	t.Run("resume a session found outside any project", func(t *testing.T) {
		r := newLaunchRig(t, &storage.Project{Name: "Acme"})
		elsewhere := filepath.Join(r.home, "elsewhere")
		ccDir := filepath.Join(r.home, ".claude", "projects", pathToCCProject(elsewhere))
		for _, d := range []string{elsewhere, ccDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(ccDir, "s-old.jsonl"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		r.model().launchClaude("resume")
		if r.cmd.Dir != elsewhere {
			t.Errorf("dir = %q, want %q", r.cmd.Dir, elsewhere)
		}
	})

	t.Run("resume with nowhere to resume from runs in the current dir", func(t *testing.T) {
		r := newLaunchRig(t, &storage.Project{Name: "Acme"})
		r.model().launchClaude("resume")
		if r.cmd.Dir != "" {
			t.Errorf("dir = %q", r.cmd.Dir)
		}
	})

	t.Run("resume-tmux", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		next, cmd := r.model().launchClaude("resume-tmux")
		name, shell := r.newWindow()
		want := "cd " + shellQuote(r.projPath) + " && " + claudeShellCommand(claudeResumeArgs("s-old", ""))
		if cmd != nil || name != tmuxWindowName("cc", "acme-1", "s-old") || shell != want {
			t.Errorf("name = %q shell = %q", name, shell)
		}
		if m := next.(Model); m.toastMsg != "Resumed in tmux: "+name || m.resumeSessionID != "" {
			t.Errorf("toast = %q", m.toastMsg)
		}
	})

	t.Run("resume-tmux with nowhere to resume from has no cd", func(t *testing.T) {
		r := newLaunchRig(t, &storage.Project{Name: "Acme"})
		r.model().launchClaude("resume-tmux")
		if _, shell := r.newWindow(); shell != claudeShellCommand(claudeResumeArgs("s-old", "")) {
			t.Errorf("shell = %q", shell)
		}
	})

	t.Run("resume-tmux failure", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		if err := os.WriteFile(r.tmuxFail, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		next, _ := r.model().launchClaude("resume-tmux")
		if got := next.(Model).toastMsg; got != "tmux failed: boom" {
			t.Errorf("toast = %q", got)
		}
	})

	t.Run("fork", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		m := r.model()
		m.forkMode = true
		next, cmd := m.launchClaude("fork")
		sid := r.newSession()
		assertArgsEqual(t, r.cmd.Args, []string{"claude", "--resume", "s-old", "--fork-session", "--session-id", sid})
		if r.cmd.Dir != r.projPath || next.(Model).forkMode || next.(Model).resumeSessionID != "" {
			t.Errorf("dir = %q forkMode = %v", r.cmd.Dir, next.(Model).forkMode)
		}
		if got := r.storedSessions(); len(got) != 2 || got[1] != sid {
			t.Errorf("stored sessions = %v", got)
		}
		if _, ok := cmd().(reloadMsg); !ok {
			t.Error("the launch should end in a reload")
		}
		if calls := r.tmuxCalls(); calls[1][1] != tmuxWindowName("cc", "acme-1", sid) {
			t.Errorf("tmux calls = %q", calls)
		}
	})

	t.Run("fork a picked session when the new one cannot be saved", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		r.task.FilePath = filepath.Join(t.TempDir(), "missing", "acme-1.md") // the write fails
		m := r.model()
		m.resumeSessionID = "s-picked"
		next, _ := m.launchClaude("fork")
		if r.cmd.Args[2] != "s-picked" || !strings.HasPrefix(next.(Model).toastMsg, "save session failed") {
			t.Errorf("args = %q toast = %q", r.cmd.Args, next.(Model).toastMsg)
		}
	})

	t.Run("fork-tmux", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		m := r.model()
		m.forkMode = true
		next, cmd := m.launchClaude("fork-tmux")
		sid := r.newSession()
		name, shell := r.newWindow()
		want := "cd " + shellQuote(r.projPath) + " && " + claudeShellCommand(claudeForkArgs("s-old", sid, ""))
		if cmd != nil || name != tmuxWindowName("cc", "acme-1", sid) || shell != want {
			t.Errorf("name = %q shell = %q", name, shell)
		}
		if m := next.(Model); m.toastMsg != "Forked in tmux: "+name || m.forkMode {
			t.Errorf("toast = %q forkMode = %v", m.toastMsg, m.forkMode)
		}
	})

	t.Run("fork-tmux failure", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		if err := os.WriteFile(r.tmuxFail, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		next, _ := r.model().launchClaude("fork-tmux")
		if got := next.(Model).toastMsg; got != "tmux failed: boom" {
			t.Errorf("toast = %q", got)
		}
	})

	t.Run("an unknown kind does nothing", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		next, cmd := r.model().launchClaude("nope")
		if cmd != nil || r.cmd != nil || len(r.tmuxCalls()) != 0 || next.(Model).toastMsg != "" {
			t.Error("an unknown kind launched something")
		}
	})
}

// TestLaunchClaudeProjectScope pins the project-scope branch launchClaude
// hands over to before anything else.
func TestLaunchClaudeProjectScope(t *testing.T) {
	t.Run("here", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		m := r.model()
		m.projectScopeLaunch = true
		m.projectScopeSlug = "acme"
		proj, _ := r.store.GetProject("acme")
		next, cmd := m.launchClaude("here")
		a := r.cmd.Args
		if len(a) != 4 || a[1] != "--session-id" || a[3] != buildProjectPrompt(proj, "acme") || r.cmd.Dir != r.projPath {
			t.Errorf("args = %q dir = %q", a, r.cmd.Dir)
		}
		if nm := next.(Model); nm.projectScopeLaunch || nm.projectScopeSlug != "" {
			t.Error("the project scope should be consumed")
		}
		if _, ok := cmd().(reloadMsg); !ok {
			t.Error("the launch should end in a reload")
		}
	})

	t.Run("tmux", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		m := r.model()
		m.projectScopeLaunch = true
		m.projectScopeSlug = "acme"
		next, _ := m.launchClaude("tmux")
		name, shell := r.newWindow()
		if !strings.HasPrefix(name, "cc:") || !strings.HasSuffix(name, ":acme") || !strings.HasPrefix(shell, "cd "+shellQuote(r.projPath)+" && claude ") {
			t.Errorf("name = %q shell = %q", name, shell)
		}
		if got := next.(Model).toastMsg; got != "Launched in tmux: "+name {
			t.Errorf("toast = %q", got)
		}
	})

	t.Run("an unknown project", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		m := r.model()
		m.projectScopeLaunch = true
		m.projectScopeSlug = "ghost"
		next, cmd := m.launchClaude("here")
		if cmd != nil || r.cmd != nil || !strings.HasPrefix(next.(Model).toastMsg, "launch failed") {
			t.Errorf("toast = %q", next.(Model).toastMsg)
		}
	})
}

// writeSessionFile puts session sid where Claude Code keeps the sessions of a
// run started in dir.
func (r *launchRig) writeSessionFile(dir, sid string) {
	r.t.Helper()
	ccDir := filepath.Join(r.home, ".claude", "projects", pathToCCProject(dir))
	for _, d := range []string{dir, ccDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			r.t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ccDir, sid+".jsonl"), nil, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// TestLaunchClaudeResumeDirs pins where a resume runs when the session is
// found in more than one place.
func TestLaunchClaudeResumeDirs(t *testing.T) {
	t.Run("a session only found elsewhere wins over the project dir", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		elsewhere := filepath.Join(r.home, "elsewhere")
		r.writeSessionFile(elsewhere, "s-old")
		r.model().launchClaude("resume")
		if r.cmd.Dir != elsewhere {
			t.Errorf("dir = %q, want %q", r.cmd.Dir, elsewhere)
		}
	})

	t.Run("a session in the project dir is resumed there without a global scan", func(t *testing.T) {
		r := newLaunchRig(t, repoProject())
		r.writeSessionFile(r.projPath, "s-old")
		r.writeSessionFile(filepath.Join(r.home, "elsewhere"), "s-old")
		r.model().launchClaude("resume")
		if r.cmd.Dir != r.projPath {
			t.Errorf("dir = %q, want the project dir %q", r.cmd.Dir, r.projPath)
		}
	})
}

// TestLaunchClaudeWorktreeEdges pins the worktree kinds' two edges: a failed
// setup launches nothing, and a project with no path skips the setup and the
// cd.
func TestLaunchClaudeWorktreeEdges(t *testing.T) {
	for _, kind := range []string{"worktree", "worktree-tmux"} {
		t.Run(kind+" setup failure launches nothing", func(t *testing.T) {
			r := newLaunchRig(t, &storage.Project{Name: "Acme", Path: t.TempDir()}) // not a git repo
			next, cmd := r.model().launchClaude(kind)
			if cmd != nil || r.cmd != nil || !strings.HasPrefix(next.(Model).toastMsg, "worktree setup failed") {
				t.Errorf("cmd = %v toast = %q", cmd != nil, next.(Model).toastMsg)
			}
			if len(r.tmuxCalls()) != 0 || len(r.storedSessions()) != 1 {
				t.Errorf("tmux calls = %q sessions = %v", r.tmuxCalls(), r.storedSessions())
			}
		})
	}

	t.Run("worktree with no project path", func(t *testing.T) {
		r := newLaunchRig(t, &storage.Project{Name: "Acme"})
		r.model().launchClaude("worktree")
		if r.cmd == nil || r.cmd.Args[1] != "-w" || r.cmd.Dir != "" {
			t.Errorf("cmd = %v", r.cmd)
		}
	})

	t.Run("worktree-tmux with no project path has no cd", func(t *testing.T) {
		r := newLaunchRig(t, &storage.Project{Name: "Acme"})
		prompt := buildClaudePrompt(r.task, r.store)
		r.model().launchClaude("worktree-tmux")
		sid := r.newSession()
		if _, shell := r.newWindow(); shell != claudeShellCommand(claudeWorktreeArgs(worktreeName(r.task), sid, "", prompt)) {
			t.Errorf("shell = %q", shell)
		}
	})
}
