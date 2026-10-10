package board

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// execProcess is tea.ExecProcess, held in a variable so a test can see the
// command a launch would hand to the terminal without running it.
var execProcess = tea.ExecProcess

// claudeSessionArgs builds the argv for a plain `claude --session-id ... prompt`
// launch (kinds "here"/"tmux", and the project-scope launch which has no
// worktree/resume/fork variants). Pure so it is unit-testable without
// spawning a process, like executorPMArgs/codexArgs.
func claudeSessionArgs(sessionID, skipFlag, prompt string) []string {
	args := []string{"--session-id", sessionID}
	if skipFlag != "" {
		args = append(args, skipFlag)
	}
	return append(args, prompt)
}

// claudeWorktreeArgs is claudeSessionArgs prefixed with `-w <name>` (kinds
// "worktree"/"worktree-tmux").
func claudeWorktreeArgs(wtName, sessionID, skipFlag, prompt string) []string {
	args := []string{"-w", wtName, "--session-id", sessionID}
	if skipFlag != "" {
		args = append(args, skipFlag)
	}
	return append(args, prompt)
}

// claudeResumeArgs builds the argv for `claude --resume <id>` (kinds
// "resume"/"resume-tmux").
func claudeResumeArgs(sessionID, skipFlag string) []string {
	args := []string{"--resume", sessionID}
	if skipFlag != "" {
		args = append(args, skipFlag)
	}
	return args
}

// claudeForkArgs builds the argv for `claude --resume <parent> --fork-session
// --session-id <new>` (kinds "fork"/"fork-tmux").
func claudeForkArgs(parentID, newID, skipFlag string) []string {
	args := []string{"--resume", parentID, "--fork-session", "--session-id", newID}
	if skipFlag != "" {
		args = append(args, skipFlag)
	}
	return args
}

// claudeShellCommand renders `claude <args...>` with each token shell-quoted,
// mirroring pmShellCommand/codexShellCommand - the tmux/shell-string launch
// paths embed this in a larger command line (cd, env prefix, lock script).
func claudeShellCommand(args []string) string {
	parts := []string{"claude"}
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func (m Model) launchProjectClaude(kind string) (tea.Model, tea.Cmd) {
	m.projectScopeLaunch = false
	slug := m.projectScopeSlug
	m.projectScopeSlug = ""

	proj, err := m.store.GetProject(slug)
	if err != nil || proj == nil {
		if err == nil {
			err = fmt.Errorf("project %q not found", slug)
		}
		m.showErrorToast("launch failed", err)
		return m, nil
	}

	prompt := buildProjectPrompt(proj, slug)
	skipFlag := ""
	if m.claudeMenuSkipPerms {
		skipFlag = "--dangerously-skip-permissions"
	}

	var projDir string
	if proj.Path != "" {
		if info, err := os.Stat(proj.Path); err == nil && info.IsDir() {
			projDir = proj.Path
		}
	}

	configDir := proj.ResolveClaudeConfigDir()
	sessionID := generateSessionID()

	switch kind {
	case "here":
		c := exec.Command("claude", claudeSessionArgs(sessionID, skipFlag, prompt)...)
		if projDir != "" {
			c.Dir = projDir
		}
		if env := claudeLaunchEnv(configDir); env != nil {
			c.Env = env
		}
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(tmuxWindowName("cc", slug, sessionID))
		return m, execProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return reloadMsg{}
		})

	case "tmux":
		withCd := func(cmd string) string {
			cmd = claudeEnvPrefix(configDir) + cmd
			if projDir != "" {
				return fmt.Sprintf("cd %s && %s", shellQuote(projDir), cmd)
			}
			return cmd
		}
		shellCmd := withCd(claudeShellCommand(claudeSessionArgs(sessionID, skipFlag, prompt)))
		winName := fmt.Sprintf("cc:%s:%s", sessionID[:4], slug)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}
	}

	return m, nil
}

// launchClaude starts Claude Code for the menu task in the way kind names:
// here / tmux (a new session), worktree / worktree-tmux (a new session in a
// worktree), resume / resume-tmux, fork / fork-tmux - each "-tmux" kind in a
// new tmux window, the others in this terminal - or "project" (reopen the menu
// scoped to the task's project). What runs is resolved first
// (resolveClaudeLaunch), then started by the kind's own function.
func (m Model) launchClaude(kind string) (tea.Model, tea.Cmd) {
	if m.projectScopeLaunch {
		return m.launchProjectClaude(kind)
	}

	t := m.menuTask()
	if t == nil {
		return m, nil
	}
	l := m.resolveClaudeLaunch(t, kind)

	var cmd tea.Cmd
	switch kind {
	case "project":
		m.openProjectClaudeMenu(t.Project)
	case "here", "tmux":
		cmd = m.launchClaudeNew(l, kind == "tmux")
	case "worktree", "worktree-tmux":
		cmd = m.launchClaudeWorktree(l, kind == "worktree-tmux")
	case "resume", "resume-tmux":
		cmd = m.launchClaudeResume(l, kind == "resume-tmux")
	case "fork", "fork-tmux":
		cmd = m.launchClaudeFork(l, kind == "fork-tmux")
	}
	return m, cmd
}

// claudeLaunch is what a task launch resolves before anything runs: the task
// and its prompt, the permission flag, the project's working and config dirs,
// and - for resume and fork - where Claude Code keeps the session.
type claudeLaunch struct {
	t         *storage.Task
	proj      *storage.Project // kept for pickWorktreeSlot
	prompt    string
	skipFlag  string
	projDir   string
	configDir string
	// worktreeDir is the worktree a resumed session was created in, and
	// sessionDir the directory found by a global scan when neither the
	// project dir nor a worktree holds it.
	worktreeDir string
	sessionDir  string
}

func (m Model) resolveClaudeLaunch(t *storage.Task, kind string) claudeLaunch {
	l := claudeLaunch{t: t, prompt: buildClaudePrompt(t, m.store)}
	if m.claudeMenuSkipPerms {
		l.skipFlag = "--dangerously-skip-permissions"
	}

	// Resolve project working directory (proj is kept for worktreeLaunchEnv)
	if p, err := m.store.GetProject(t.Project); err == nil {
		l.proj = p
		if p.Path != "" {
			if info, err := os.Stat(p.Path); err == nil && info.IsDir() {
				l.projDir = p.Path
			}
		}
	}

	// Resolve the Claude config dir for this project (default ~/.claude unless the
	// project sets claude_config_dir, e.g. a company account in its own dir).
	l.configDir = m.claudeConfigDir(t.Project)

	// Check if the session lives in a worktree. Claude Code stores conversations
	// in <config-dir>/projects/<path-hash>/ where path-hash is derived from cwd.
	// When a session was created inside a worktree, we need to resume from that
	// worktree dir so CC finds the conversation.
	if kind == "resume" || kind == "resume-tmux" || kind == "fork" || kind == "fork-tmux" {
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		if l.projDir != "" {
			l.worktreeDir = findWorktreeForSession(l.projDir, sessionID, l.configDir)
		}
		// Fallback: if session not found via projDir (or projDir is empty),
		// scan all CC project dirs globally.
		if l.worktreeDir == "" && resolveSessionPath(l.projDir, sessionID, l.configDir) == "" {
			l.sessionDir = findSessionDirGlobal(sessionID, m.store, l.configDir)
		}
	}
	return l
}

// setDir points c at the project dir with the launch env.
func (l claudeLaunch) setDir(c *exec.Cmd, extraEnv ...string) {
	if l.projDir != "" {
		c.Dir = l.projDir
	}
	if env := claudeLaunchEnv(l.configDir, extraEnv...); env != nil {
		c.Env = env
	}
}

// resumeDir is where a resumed session has to run for Claude Code to find it.
func (l claudeLaunch) resumeDir() string {
	if l.worktreeDir != "" {
		return l.worktreeDir
	}
	if l.projDir != "" && l.sessionDir == "" {
		return l.projDir
	}
	if l.sessionDir != "" {
		return l.sessionDir
	}
	return ""
}

func (l claudeLaunch) setResumeDir(c *exec.Cmd) {
	if d := l.resumeDir(); d != "" {
		c.Dir = d
	}
	if env := claudeLaunchEnv(l.configDir); env != nil {
		c.Env = env
	}
}

// withCd prefixes a shell command with the launch env and a cd into the
// project dir.
func (l claudeLaunch) withCd(cmd string, extraEnv ...string) string {
	cmd = claudeEnvPrefix(l.configDir, extraEnv...) + cmd
	if l.projDir != "" {
		return fmt.Sprintf("cd %s && %s", shellQuote(l.projDir), cmd)
	}
	return cmd
}

func (l claudeLaunch) withResumeCd(cmd string) string {
	cmd = claudeEnvPrefix(l.configDir) + cmd
	if d := l.resumeDir(); d != "" {
		return fmt.Sprintf("cd %s && %s", shellQuote(d), cmd)
	}
	return cmd
}

// runInTerminal hands c the terminal, with the tmux window renamed to
// winName while it runs, and reloads when it exits.
func runInTerminal(c *exec.Cmd, winName string) tea.Cmd {
	origWin := tmuxGetWindowName()
	tmuxRenameWindow(winName)
	return execProcess(c, func(err error) tea.Msg {
		tmuxRenameWindow(origWin)
		return reloadMsg{}
	})
}

// runInTmuxWindow opens a tmux window running shellCmd and toasts the outcome.
func (m *Model) runInTmuxWindow(winName, shellCmd, label string) {
	sess, err := tmuxNewWindow(winName, shellCmd)
	if err != nil {
		m.toastMsg = "tmux failed: " + err.Error()
		m.toastExpiry = time.Now().Add(15 * time.Second)
	} else {
		m.toastMsg = tmuxLaunchToast(label, winName, sess)
		m.toastExpiry = time.Now().Add(3 * time.Second)
	}
}

// launchClaudeNew starts a new session on the task (kinds here / tmux).
func (m *Model) launchClaudeNew(l claudeLaunch, inTmux bool) tea.Cmd {
	t := l.t
	sessionID := generateSessionID()
	if err := m.saveSession(t, sessionID); err != nil {
		m.showErrorToast("save session failed", err)
	}
	args := claudeSessionArgs(sessionID, l.skipFlag, l.prompt)
	winName := tmuxWindowName("cc", t.Meta.ID, sessionID)
	if inTmux {
		m.runInTmuxWindow(winName, l.withCd(claudeShellCommand(args)), "Launched in tmux")
		return nil
	}
	c := exec.Command("claude", args...)
	l.setDir(c)
	return runInTerminal(c, winName)
}

// launchClaudeWorktree starts a new session in the task's worktree (kinds
// worktree / worktree-tmux), claiming a free runtime slot when the project
// has them. A failed worktree setup aborts before a session is saved.
func (m *Model) launchClaudeWorktree(l claudeLaunch, inTmux bool) tea.Cmd {
	t := l.t
	sessionID := generateSessionID()
	wtName := worktreeName(t)
	if l.projDir != "" {
		if err := copyWorktreeFiles(l.projDir, wtName); err != nil {
			m.showErrorToast("worktree setup failed", err)
			return nil
		}
	}
	if err := m.saveSession(t, sessionID); err != nil {
		m.showErrorToast("save session failed", err)
	}
	claim := m.pickWorktreeSlot(l.proj, t.Project, sessionID)
	args := claudeWorktreeArgs(wtName, sessionID, l.skipFlag, l.prompt)
	winName := tmuxWindowName("wt", t.Meta.ID, sessionID)
	if inTmux {
		// Lock write goes between `cd` and the env-prefixed claude command;
		// sh execs the trailing command (or waits on it), so the lock's $$
		// tracks the session's lifetime either way.
		inner := claim.lockCmd + claudeEnvPrefix(l.configDir, claim.env...) + claudeShellCommand(args)
		shellCmd := inner
		if l.projDir != "" {
			shellCmd = fmt.Sprintf("cd %s && %s", shellQuote(l.projDir), inner)
		}
		m.runInTmuxWindow(winName, shellCmd, "Launched worktree in tmux")
		return nil
	}
	var c *exec.Cmd
	if claim.lockCmd != "" {
		// Claim the slot with the shell's pid, then exec claude so that
		// pid becomes claude's - the lock holder is the live session.
		shArgs := append([]string{"-c", claim.lockCmd + `exec claude "$@"`, "claude"}, args...)
		c = exec.Command("sh", shArgs...)
	} else {
		c = exec.Command("claude", args...)
	}
	l.setDir(c, claim.env...)
	return runInTerminal(c, winName)
}

// takeResumeSession returns the session to resume - the one picked in the
// session menu, else the task's last - and consumes the pick.
func (m *Model) takeResumeSession(t *storage.Task) string {
	sessionID := m.resumeSessionID
	if sessionID == "" {
		sessionID = lastSession(t)
	}
	m.resumeSessionID = ""
	return sessionID
}

// launchClaudeResume resumes a session where it lives (kinds resume /
// resume-tmux).
func (m *Model) launchClaudeResume(l claudeLaunch, inTmux bool) tea.Cmd {
	sessionID := m.takeResumeSession(l.t)
	args := claudeResumeArgs(sessionID, l.skipFlag)
	winName := tmuxWindowName("cc", l.t.Meta.ID, sessionID)
	if inTmux {
		m.runInTmuxWindow(winName, l.withResumeCd(claudeShellCommand(args)), "Resumed in tmux")
		return nil
	}
	c := exec.Command("claude", args...)
	l.setResumeDir(c)
	return runInTerminal(c, winName)
}

// launchClaudeFork forks a session into a new one saved on the task (kinds
// fork / fork-tmux).
func (m *Model) launchClaudeFork(l claudeLaunch, inTmux bool) tea.Cmd {
	t := l.t
	parentID := m.takeResumeSession(t)
	m.forkMode = false
	newID := generateSessionID()
	if err := m.saveSession(t, newID); err != nil {
		m.showErrorToast("save session failed", err)
	}
	args := claudeForkArgs(parentID, newID, l.skipFlag)
	winName := tmuxWindowName("cc", t.Meta.ID, newID)
	if inTmux {
		m.runInTmuxWindow(winName, l.withResumeCd(claudeShellCommand(args)), "Forked in tmux")
		return nil
	}
	c := exec.Command("claude", args...)
	l.setResumeDir(c)
	return runInTerminal(c, winName)
}

// worktreeSlotClaim is the result of picking an interactive-session slot for
// a worktree launch: the runtime-isolation env (Metro port, sim UDID, ...) to
// inject plus the shell snippet that claims the slot from inside the launched
// process. An empty lockCmd means there is nothing to claim: no slots
// configured (legacy executor.env passthrough) or every slot busy (degraded
// to slot 1's env, matching the pre-registry behaviour).
type worktreeSlotClaim struct {
	env     []string
	lockCmd string
}

// pickWorktreeSlot picks the first slot WITHOUT a live interactive-session
// lock for a worktree launch, so two interactive worktree sessions get
// DIFFERENT slots (own port/sim). Non-worktree launches never call this: a
// main-checkout session must keep the default port/simulator. The session
// lock is separate from the executor's worktree lock - a live --additional
// run doesn't conflict on runtime resources (headless workers never boot
// Metro/sims, PM_HEADLESS), so only session locks gate the pick.
func (m *Model) pickWorktreeSlot(proj *storage.Project, slug, sessionID string) worktreeSlotClaim {
	if proj == nil {
		return worktreeSlotClaim{}
	}
	ex := proj.GetExecutor()
	slots := ex.ResolveWorktrees(proj.Path)
	if len(slots) == 0 {
		return worktreeSlotClaim{env: ex.EnvSlice()}
	}
	projDir := m.store.ProjectDir(slug)
	for i, s := range slots {
		if storage.LiveSessionHolder(projDir, i+1) == nil {
			return worktreeSlotClaim{env: s.Env, lockCmd: sessionLockScript(projDir, i+1, sessionID)}
		}
	}
	// Every slot already hosts a live interactive session: fall back to slot
	// 1's env without claiming (pre-registry behaviour, sessions collide).
	return worktreeSlotClaim{env: slots[0].Env}
}

// sessionLockScript returns a shell snippet (terminated by "; ") that writes
// the slot's session lock with the shell's own pid ($$). The launch execs
// claude from that same shell, so the recorded pid is claude's pid; when the
// session ends the pid dies and the lock reads as stale (= free) - no cleanup
// needed. Lock-write failure must never block the launch, hence ";" not "&&"
// before the command that follows.
//
// The write itself goes through a per-pid tmp file + "mv" (atomic rename
// within the same directory), mirroring storage.atomicWriteFile - a bare
// "printf > file" truncates in place, and a rival reading mid-write would see
// a torn/empty lock and steal a live holder's slot (the exact failure
// worktree.go's rename-aside guards against for the executor's lock).
func sessionLockScript(projDir string, slot int, sessionID string) string {
	lock := storage.SessionLockPath(projDir, slot)
	payload := fmt.Sprintf(`{"pid":%%d,"session_id":%q,"slot":%d,"started":%q}`,
		sessionID, slot, time.Now().UTC().Format(time.RFC3339))
	quotedLock := shellQuote(lock)
	tmp := quotedLock + `.tmp.$$`
	// "|| rm -f tmp" cleans up a leftover tmp file if either the printf or the
	// mv step failed (both silenced above) - mirrors atomicWriteFile removing
	// its scratch file on every failure path. A no-op when the write succeeds.
	return fmt.Sprintf("mkdir -p %s && printf %s \"$$\" > %s 2>/dev/null && mv %s %s 2>/dev/null || rm -f %s 2>/dev/null; ",
		shellQuote(filepath.Dir(lock)), shellQuote(payload), tmp, tmp, quotedLock, tmp)
}

func buildClaudePrompt(t *storage.Task, store storage.TaskStore) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Working on: #%s %s [%s]\n", t.Meta.ID, t.Meta.Title, t.Meta.Status)
	fmt.Fprintf(&sb, "Project: %s\n", t.Project)
	if t.Meta.Branch != "" {
		fmt.Fprintf(&sb, "Branch: %s\n", t.Meta.Branch)
	}

	if proj, err := store.GetProject(t.Project); err == nil {
		if proj.Path != "" {
			fmt.Fprintf(&sb, "Path: %s\n", proj.Path)
		}
		if proj.Stack != "" {
			fmt.Fprintf(&sb, "Stack: %s\n", proj.Stack)
		}
		if proj.Repo != "" {
			fmt.Fprintf(&sb, "Repo: %s\n", proj.Repo)
		}
		if proj.Notes != "" {
			fmt.Fprintf(&sb, "Notes: %s\n", proj.Notes)
		}
	}

	if t.Meta.Brief != "" {
		fmt.Fprintf(&sb, "\nBrief: %s\n", t.Meta.Brief)
	}

	if t.Meta.AC != "" {
		fmt.Fprintf(&sb, "\nAcceptance Criteria:\n%s\n", t.Meta.AC)
	}

	if body := strings.TrimSpace(t.Body); body != "" {
		fmt.Fprintf(&sb, "\n---\n%s\n", body)
	}

	return sb.String()
}

func buildProjectPrompt(proj *storage.Project, slug string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Project: %s\n", slug)
	if proj.Name != "" {
		fmt.Fprintf(&sb, "Name: %s\n", proj.Name)
	}
	if proj.Path != "" {
		fmt.Fprintf(&sb, "Path: %s\n", proj.Path)
	}
	if proj.Stack != "" {
		fmt.Fprintf(&sb, "Stack: %s\n", proj.Stack)
	}
	if proj.Repo != "" {
		fmt.Fprintf(&sb, "Repo: %s\n", proj.Repo)
	}
	if proj.Notes != "" {
		fmt.Fprintf(&sb, "Notes: %s\n", proj.Notes)
	}
	return sb.String()
}
