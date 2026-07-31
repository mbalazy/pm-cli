package board

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mbalazy/pm/internal/storage"
)

func (m Model) launchProjectClaude(kind string) (tea.Model, tea.Cmd) {
	m.projectScopeLaunch = false
	slug := m.projectScopeSlug
	m.projectScopeSlug = ""

	proj, err := m.store.GetProject(slug)
	if err != nil || proj == nil {
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
		args := []string{"--session-id", sessionID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		args = append(args, prompt)
		c := exec.Command("claude", args...)
		if projDir != "" {
			c.Dir = projDir
		}
		if env := claudeLaunchEnv(configDir); env != nil {
			c.Env = env
		}
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(tmuxWindowName("cc", slug, sessionID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
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
		shellCmd := withCd(fmt.Sprintf("claude --session-id %s %s %s", sessionID, skipFlag, shellQuote(prompt)))
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

func (m Model) launchClaude(kind string) (tea.Model, tea.Cmd) {
	if m.projectScopeLaunch {
		return m.launchProjectClaude(kind)
	}

	t := m.menuTask()
	if t == nil {
		return m, nil
	}

	prompt := buildClaudePrompt(t, m.store)
	skipFlag := ""
	if m.claudeMenuSkipPerms {
		skipFlag = "--dangerously-skip-permissions"
	}

	// Resolve project working directory (proj is kept for worktreeLaunchEnv)
	var projDir string
	var proj *storage.Project
	if p, err := m.store.GetProject(t.Project); err == nil {
		proj = p
		if proj.Path != "" {
			if info, err := os.Stat(proj.Path); err == nil && info.IsDir() {
				projDir = proj.Path
			}
		}
	}

	// Resolve the Claude config dir for this project (default ~/.claude unless the
	// project sets claude_config_dir, e.g. a company account in ~/.claude-alt).
	configDir := m.claudeConfigDir(t.Project)

	// Check if the session lives in a worktree. Claude Code stores conversations
	// in <config-dir>/projects/<path-hash>/ where path-hash is derived from cwd.
	// When a session was created inside a worktree, we need to resume from that
	// worktree dir so CC finds the conversation.
	var worktreeDir string
	var sessionDir string // fallback: global scan for session file
	if kind == "resume" || kind == "resume-tmux" || kind == "fork" || kind == "fork-tmux" {
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		if projDir != "" {
			worktreeDir = findWorktreeForSession(projDir, sessionID, configDir)
		}
		// Fallback: if session not found via projDir (or projDir is empty),
		// scan all CC project dirs globally.
		if worktreeDir == "" && resolveSessionPath(projDir, sessionID, configDir) == "" {
			sessionDir = findSessionDirGlobal(sessionID, m.store, configDir)
		}
	}

	setDir := func(c *exec.Cmd, extraEnv ...string) {
		if projDir != "" {
			c.Dir = projDir
		}
		if env := claudeLaunchEnv(configDir, extraEnv...); env != nil {
			c.Env = env
		}
	}

	resumeDir := func() string {
		if worktreeDir != "" {
			return worktreeDir
		}
		if projDir != "" && sessionDir == "" {
			return projDir
		}
		if sessionDir != "" {
			return sessionDir
		}
		return ""
	}

	setResumeDir := func(c *exec.Cmd) {
		if d := resumeDir(); d != "" {
			c.Dir = d
		}
		if env := claudeLaunchEnv(configDir); env != nil {
			c.Env = env
		}
	}

	withCd := func(cmd string, extraEnv ...string) string {
		cmd = claudeEnvPrefix(configDir, extraEnv...) + cmd
		if projDir != "" {
			return fmt.Sprintf("cd %s && %s", shellQuote(projDir), cmd)
		}
		return cmd
	}

	withResumeCd := func(cmd string) string {
		cmd = claudeEnvPrefix(configDir) + cmd
		if d := resumeDir(); d != "" {
			return fmt.Sprintf("cd %s && %s", shellQuote(d), cmd)
		}
		return cmd
	}

	switch kind {
	case "project":
		m.openProjectClaudeMenu(t.Project)
		return m, nil

	case "here":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		args := []string{"--session-id", sessionID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		args = append(args, prompt)
		c := exec.Command("claude", args...)
		setDir(c)
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(tmuxWindowName("cc", t.Meta.ID, sessionID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return reloadMsg{}
		})

	case "tmux":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		shellCmd := withCd(fmt.Sprintf("claude --session-id %s %s %s", sessionID, skipFlag, shellQuote(prompt)))
		winName := tmuxWindowName("cc", t.Meta.ID, sessionID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}

	case "worktree":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		wtName := worktreeName(t)
		if projDir != "" {
			copyWorktreeFiles(projDir, wtName)
		}
		claim := m.pickWorktreeSlot(proj, t.Project, sessionID)
		args := []string{"-w", wtName, "--session-id", sessionID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		args = append(args, prompt)
		var c *exec.Cmd
		if claim.lockCmd != "" {
			// Claim the slot with the shell's pid, then exec claude so that
			// pid becomes claude's - the lock holder is the live session.
			shArgs := append([]string{"-c", claim.lockCmd + `exec claude "$@"`, "claude"}, args...)
			c = exec.Command("sh", shArgs...)
		} else {
			c = exec.Command("claude", args...)
		}
		setDir(c, claim.env...)
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(tmuxWindowName("wt", t.Meta.ID, sessionID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return reloadMsg{}
		})

	case "worktree-tmux":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		wtName := worktreeName(t)
		if projDir != "" {
			copyWorktreeFiles(projDir, wtName)
		}
		claim := m.pickWorktreeSlot(proj, t.Project, sessionID)
		// Lock write goes between `cd` and the env-prefixed claude command;
		// sh execs the trailing command (or waits on it), so the lock's $$
		// tracks the session's lifetime either way.
		inner := claim.lockCmd + claudeEnvPrefix(configDir, claim.env...) +
			fmt.Sprintf("claude -w %s --session-id %s %s %s", shellQuote(wtName), sessionID, skipFlag, shellQuote(prompt))
		shellCmd := inner
		if projDir != "" {
			shellCmd = fmt.Sprintf("cd %s && %s", shellQuote(projDir), inner)
		}
		winName := tmuxWindowName("wt", t.Meta.ID, sessionID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched worktree in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}

	case "resume":
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		m.resumeSessionID = ""
		args := []string{"--resume", sessionID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		c := exec.Command("claude", args...)
		setResumeDir(c)
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(tmuxWindowName("cc", t.Meta.ID, sessionID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return reloadMsg{}
		})

	case "resume-tmux":
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		m.resumeSessionID = ""
		shellCmd := withResumeCd(fmt.Sprintf("claude --resume %s %s", sessionID, skipFlag))
		winName := tmuxWindowName("cc", t.Meta.ID, sessionID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Resumed in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}

	case "fork":
		parentID := m.resumeSessionID
		if parentID == "" {
			parentID = lastSession(t)
		}
		m.resumeSessionID = ""
		m.forkMode = false
		newID := generateSessionID()
		m.saveSession(t, newID)
		args := []string{"--resume", parentID, "--fork-session", "--session-id", newID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		c := exec.Command("claude", args...)
		setResumeDir(c)
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(tmuxWindowName("cc", t.Meta.ID, newID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return reloadMsg{}
		})

	case "fork-tmux":
		parentID := m.resumeSessionID
		if parentID == "" {
			parentID = lastSession(t)
		}
		m.resumeSessionID = ""
		m.forkMode = false
		newID := generateSessionID()
		m.saveSession(t, newID)
		shellCmd := withResumeCd(fmt.Sprintf("claude --resume %s --fork-session --session-id %s %s", parentID, newID, skipFlag))
		winName := tmuxWindowName("cc", t.Meta.ID, newID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Forked in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}
	}

	return m, nil
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
	return fmt.Sprintf("mkdir -p %s && printf %s \"$$\" > %s 2>/dev/null && mv %s %s 2>/dev/null; ",
		shellQuote(filepath.Dir(lock)), shellQuote(payload), tmp, tmp, quotedLock)
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
