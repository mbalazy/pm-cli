package board

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func codexArgs(prompt string, bypass bool) []string {
	args := []string{}
	if bypass {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	return append(args, prompt)
}

func codexShellCommand(prompt string, bypass bool) string {
	args := codexArgs(prompt, bypass)
	parts := []string{"codex"}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func codexInteractiveShellCommand(prompt string, bypass bool) string {
	return codexShellCommand(prompt, bypass) + "; status=$?; if [ $status -ne 0 ]; then printf '\\n[pm] Codex exited with status %s. Press Enter to return to board...' \"$status\"; read _; fi; exit $status"
}

func codexWindowName(id string) string {
	return "cx:" + id
}

func (m Model) launchLLM(kind string) (tea.Model, tea.Cmd) {
	switch m.launchAgent {
	case launchAgentCodex:
		return m.launchCodex(kind)
	case launchAgentExecutor:
		return m.launchExecutor(kind)
	}
	return m.launchClaude(kind)
}

func (m Model) launchProjectCodex(kind string) (tea.Model, tea.Cmd) {
	m.projectScopeLaunch = false
	slug := m.projectScopeSlug
	m.projectScopeSlug = ""

	proj, err := m.store.GetProject(slug)
	if err != nil || proj == nil {
		return m, nil
	}

	prompt := buildProjectPrompt(proj, slug)

	var projDir string
	if proj.Path != "" {
		if info, err := os.Stat(proj.Path); err == nil && info.IsDir() {
			projDir = proj.Path
		}
	}

	switch kind {
	case "here":
		c := exec.Command("sh", "-c", codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms))
		if projDir != "" {
			c.Dir = projDir
		}
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(codexWindowName(slug))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return launchResultMsg{agent: launchAgentCodex, kind: "project here", err: err}
		})

	case "tmux":
		shellCmd := codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms)
		if projDir != "" {
			shellCmd = fmt.Sprintf("cd %s && %s", shellQuote(projDir), shellCmd)
		}
		winName := codexWindowName(slug)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "Codex tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched Codex in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}
	}

	return m, nil
}

func (m Model) launchCodex(kind string) (tea.Model, tea.Cmd) {
	if m.projectScopeLaunch {
		return m.launchProjectCodex(kind)
	}

	t := m.menuTask()
	if t == nil {
		return m, nil
	}

	if kind == "project" {
		m.openProjectClaudeMenuWithAgent(t.Project, launchAgentCodex)
		return m, nil
	}

	prompt := buildClaudePrompt(t, m.store)

	var projDir string
	if proj, err := m.store.GetProject(t.Project); err == nil && proj.Path != "" {
		if info, err := os.Stat(proj.Path); err == nil && info.IsDir() {
			projDir = proj.Path
		}
	}

	setDir := func(c *exec.Cmd) {
		if projDir != "" {
			c.Dir = projDir
		}
	}

	withCd := func(cmd string) string {
		if projDir != "" {
			return fmt.Sprintf("cd %s && %s", shellQuote(projDir), cmd)
		}
		return cmd
	}

	// worktreeDir mirrors launchClaude's worktree setup: a failed copy aborts
	// the launch (via the returned error) instead of silently falling back to
	// the plain project dir, which would start Codex without its gitignored
	// configs and say nothing about it.
	worktreeDir := func() (string, error) {
		if projDir == "" {
			return "", nil
		}
		wtName := worktreeName(t)
		if err := copyWorktreeFiles(projDir, wtName); err != nil {
			return "", err
		}
		wtPath := filepath.Join(projDir, ".claude", "worktrees", wtName)
		if info, err := os.Stat(wtPath); err == nil && info.IsDir() {
			return wtPath, nil
		}
		return "", nil
	}

	switch kind {
	case "here":
		c := exec.Command("sh", "-c", codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms))
		setDir(c)
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(codexWindowName(t.Meta.ID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return launchResultMsg{agent: launchAgentCodex, kind: "here", err: err}
		})

	case "tmux":
		shellCmd := withCd(codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms))
		winName := codexWindowName(t.Meta.ID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "Codex tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched Codex in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}

	case "worktree":
		wtDir, err := worktreeDir()
		if err != nil {
			m.showErrorToast("worktree setup failed", err)
			return m, nil
		}
		c := exec.Command("sh", "-c", codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms))
		if wtDir != "" {
			c.Dir = wtDir
		} else {
			setDir(c)
		}
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(codexWindowName(t.Meta.ID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return launchResultMsg{agent: launchAgentCodex, kind: "worktree", err: err}
		})

	case "worktree-tmux":
		wtDir, err := worktreeDir()
		if err != nil {
			m.showErrorToast("worktree setup failed", err)
			return m, nil
		}
		interactiveCmd := codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms)
		shellCmd := withCd(interactiveCmd)
		if wtDir != "" {
			shellCmd = fmt.Sprintf("cd %s && %s", shellQuote(wtDir), interactiveCmd)
		}
		winName := codexWindowName(t.Meta.ID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "Codex worktree tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched Codex worktree in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}
	}

	return m, nil
}
