package board

import (
	"os"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/common"
)

func (m *Model) openClaudeMenu(t *storage.Task) {
	m.claudeMenuItems = nil
	m.claudeMenuCursor = 0
	m.claudeMenuSkipPerms = false
	m.launchAgent = launchAgentClaude
	m.rebuildClaudeMenuItems(t)
	m.claudeMenu = true
}

// openExecutorMenu opens the launch overlay directly in executor mode for task t
// (which may be a tracker -> run-epic, or a leaf task -> work). Reuses the same
// overlay as the Claude/Codex launch menu.
func (m *Model) openExecutorMenu(t *storage.Task) {
	if t == nil {
		return
	}
	m.claudeMenuItems = nil
	m.claudeMenuCursor = 0
	m.claudeMenuSkipPerms = false
	m.launchAgent = launchAgentExecutor
	m.claudeMenuAdditional = false
	m.resumeOnly = false
	m.forkMode = false
	m.projectScopeLaunch = false
	m.projectScopeSlug = ""
	m.rebuildClaudeMenuItems(t)
	m.claudeMenu = true
}

// menuTask returns the task the launch overlay acts on: the detail task when in
// the detail view (correct even after a relation jump), otherwise the
// board-selected task.
func (m Model) menuTask() *storage.Task {
	if m.currentView == viewDetail && m.detailTask != nil {
		return m.detailTask
	}
	return m.selectedTask()
}

func (m *Model) rebuildClaudeMenuItems(t *storage.Task) {
	inTmux := os.Getenv("TMUX") != ""
	m.claudeMenuItems = nil
	m.claudeMenuCursor = 0

	if m.projectScopeLaunch {
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Here (takes over terminal)", "here", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Tmux window", "tmux", "t"})
			m.claudeMenuCursor = 1
		}
		return
	}

	if m.launchAgent == launchAgentExecutor {
		// Executor runs `pm work` (task) or `pm run-epic` (tracker). Long-running,
		// so default to tmux when available. No worktree/resume/fork options.
		m.executorIsTracker = len(m.taskChildren(t)) > 0
		// The isolated "additional" worktree is only offered when the project has
		// it configured (executor.additional_worktree). The user picks default vs
		// additional per launch via the `#` toggle (never inferred).
		m.executorAdditionalAvail = false
		if t != nil && m.store != nil {
			if proj, err := m.store.GetProject(t.Project); err == nil {
				m.executorAdditionalAvail = proj.GetExecutor().AdditionalWorktree
			}
		}
		if !m.executorAdditionalAvail {
			m.claudeMenuAdditional = false
		}
		bgLabel := "Run task in background (watch in pm)"
		runLabel := "Run task here (pm work)"
		tmuxLabel := "Run task in tmux (pm work)"
		if m.executorIsTracker {
			bgLabel = "Run epic in background (watch in pm)"
			runLabel = "Run epic here (pm run-epic)"
			tmuxLabel = "Run epic in tmux (pm run-epic)"
		}
		// Background is the default: non-blocking, observable natively in pm.
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{bgLabel, "bg", "b"})
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{runLabel, "here", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{tmuxLabel, "tmux", "t"})
		}
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Dry-run preview", "dry-run", "d"})
		m.claudeMenuCursor = 0 // default to background
		return
	}

	if m.launchAgent == launchAgentCodex {
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Here (takes over terminal)", "here", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Tmux window", "tmux", "t"})
		}
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Worktree (here)", "worktree", "w"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Worktree + tmux", "worktree-tmux", "W"})
		}
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Project scope (no task)", "project", "p"})
		if inTmux {
			m.claudeMenuCursor = 1
		}
		return
	}

	if m.resumeOnly && m.forkMode {
		// Fork sub-menu: fork from selected session
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Fork here", "fork", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Fork in tmux", "fork-tmux", "t"})
			m.claudeMenuCursor = 1 // default to tmux
		}
		return
	}

	hasSession := t != nil && lastSession(t) != ""
	if m.resumeOnly {
		// Resume sub-menu: only show resume options
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume here", "resume", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume in tmux", "resume-tmux", "t"})
			m.claudeMenuCursor = 1 // default to tmux
		}
	} else {
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Here (takes over terminal)", "here", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Tmux window", "tmux", "t"})
		}
		if hasSession {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume session", "resume", "r"})
			if inTmux {
				m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume in tmux", "resume-tmux", "R"})
			}
		}
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Worktree (here)", "worktree", "w"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Worktree + tmux", "worktree-tmux", "W"})
		}
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Project scope (no task)", "project", "p"})
		// Smart default: tmux if available, else here
		if inTmux {
			for i, item := range m.claudeMenuItems {
				if item.kind == "tmux" {
					m.claudeMenuCursor = i
					break
				}
			}
		}
	}
}

func (m *Model) openProjectClaudeMenu(slug string) {
	m.openProjectClaudeMenuWithAgent(slug, launchAgentClaude)
}

func (m *Model) openProjectClaudeMenuWithAgent(slug string, agent launchAgent) {
	m.claudeMenuItems = nil
	m.claudeMenuCursor = 0
	m.claudeMenuSkipPerms = false
	m.launchAgent = agent
	m.resumeOnly = false
	m.forkMode = false
	m.projectScopeLaunch = true
	m.projectScopeSlug = slug
	m.rebuildClaudeMenuItems(nil)
	m.claudeMenu = true
}

func (m Model) updateClaudeMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Toggle skip-permissions
	typed := msg.String()
	if typed == "!" {
		m.claudeMenuSkipPerms = !m.claudeMenuSkipPerms
		return m, nil
	}
	// Toggle the isolated "additional" worktree (executor only, project-configured).
	if typed == "#" {
		if m.launchAgent == launchAgentExecutor && m.executorAdditionalAvail {
			m.claudeMenuAdditional = !m.claudeMenuAdditional
		}
		return m, nil
	}
	if typed == "@" {
		if m.projectScopeLaunch {
			// Project scope has no task: executor is not applicable, cycle claude<->codex.
			if m.launchAgent == launchAgentCodex {
				m.launchAgent = launchAgentClaude
			} else {
				m.launchAgent = launchAgentCodex
			}
		} else {
			// Task scope: cycle claude -> codex -> executor -> claude.
			switch m.launchAgent {
			case launchAgentClaude:
				m.launchAgent = launchAgentCodex
			case launchAgentCodex:
				m.launchAgent = launchAgentExecutor
			default:
				m.launchAgent = launchAgentClaude
			}
		}
		m.rebuildClaudeMenuItems(m.menuTask())
		return m, nil
	}

	// Check mnemonic shortcut keys first
	for _, item := range m.claudeMenuItems {
		if typed == item.shortcut {
			m.claudeMenu = false
			m.resumeOnly = false
			m.forkMode = false
			return m.launchLLM(item.kind)
		}
	}

	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.claudeMenu = false
		m.resumeOnly = false
		m.forkMode = false
		m.resumeSessionID = ""
		m.projectScopeLaunch = false
		m.projectScopeSlug = ""
	case key.Matches(msg, common.Keys.Up):
		if m.claudeMenuCursor > 0 {
			m.claudeMenuCursor--
		}
	case key.Matches(msg, common.Keys.Down):
		if m.claudeMenuCursor < len(m.claudeMenuItems)-1 {
			m.claudeMenuCursor++
		}
	case key.Matches(msg, common.Keys.Enter):
		if m.claudeMenuCursor < len(m.claudeMenuItems) {
			item := m.claudeMenuItems[m.claudeMenuCursor]
			m.claudeMenu = false
			m.resumeOnly = false
			m.forkMode = false
			return m.launchLLM(item.kind)
		}
	}
	return m, nil
}
