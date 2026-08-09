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
	// Reset symmetrically with openExecutorMenu: without this, toggling # on
	// while in executor mode, then Esc, then opening this menu for a DIFFERENT
	// task and @-cycling back to executor carries the stale additional-worktree
	// choice into a launch the user never opted into from this menu.
	m.claudeMenuAdditional = false
	m.executorAdditionalAvail = false
	m.claudeMenuThenFinish = false
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
	m.claudeMenuThenFinish = false
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

// trackerRunNoun is what one `pm run-epic` of this tracker is CALLED: an epic
// when its subs merge into one integration branch, a batch when they each stand
// alone (epic_mode: independent). One helper for the menu title and the item
// labels, so a tracker cannot be a batch in one line of the overlay and an epic
// in the next.
func trackerRunNoun(t *storage.Task) string {
	if t != nil && t.Meta.EpicMode == storage.EpicModeIndependent {
		return "batch"
	}
	return "epic"
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
		// at least one worktree slot configured (executor.worktrees, or the legacy
		// additional_worktree pair). The user picks default vs additional per
		// launch via the `#` toggle (never inferred); the slot itself is claimed
		// first-free at run time by pm work/run-epic. The per-slot lock holders
		// are gathered here (menu open = decision time) for the slot indicator.
		m.executorAdditionalAvail = false
		m.executorSlots = nil
		if t != nil && m.store != nil {
			if proj, err := m.store.GetProject(t.Project); err == nil {
				m.executorSlots = executorSlotStatuses(proj)
				m.executorAdditionalAvail = len(m.executorSlots) > 0
			}
		}
		if !m.executorAdditionalAvail {
			m.claudeMenuAdditional = false
		}
		bgLabel := "Run task in background (watch in pm)"
		runLabel := "Run task here (pm work)"
		tmuxLabel := "Run task in tmux (pm work)"
		// "epic" vs "batch" follows epic_mode, the ONLY thing that decides
		// integration from independent (the board passes no --independent
		// flag - the tracker declares it). The command is `pm run-epic`
		// either way and the label says so, since that is what a user
		// reproducing this launch by hand has to type.
		noun := trackerRunNoun(t)
		if m.executorIsTracker {
			bgLabel = "Run " + noun + " in background (watch in pm)"
			runLabel = "Run " + noun + " here (pm run-epic)"
			tmuxLabel = "Run " + noun + " in tmux (pm run-epic)"
		}
		// Background is the default: non-blocking, observable natively in pm.
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{bgLabel, "bg", "b"})
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{runLabel, "here", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{tmuxLabel, "tmux", "t"})
		}
		if m.executorIsTracker {
			// The acceptance of this tracker's run (`pm finish`) - always
			// detached and simulator-free, like chainFinish's spawn; watch it
			// via W, kill it via K, see it in the Runs view (R).
			//
			// The wording carries its TIMING, because the word "acceptance"
			// appears twice in this overlay and the two mean opposite things:
			// the `&` toggle arms one for AFTER the run, this key starts one
			// NOW. Naming all three moments is not padding - "before, during
			// or after" is the actual contract (`pm finish` never checks
			// whether the run has finished, because batch-finish-auto is built
			// to accept subs as they land), so pressing this mid-run is a use
			// rather than a mistake. It also says, by saying "the run", that it
			// starts none: the item sits under three that all launch the epic.
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{
				"Accept NOW - before, during or after the " + noun + " (pm finish, detached)", "finish", "a"})
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
	// Toggle chaining the acceptance after the epic run (--then-finish;
	// executor + tracker only - the flag exists only on `pm run-epic`).
	if typed == "&" {
		if m.launchAgent == launchAgentExecutor && m.executorIsTracker {
			m.claudeMenuThenFinish = !m.claudeMenuThenFinish
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

// executorSlotStatus is one worktree slot's occupancy for the launch-menu
// indicator: the resolved path and the LIVE lock holder (nil = free).
type executorSlotStatus struct {
	path   string
	holder *storage.WorktreeLock
}

// executorSlotStatuses resolves the project's worktree slot pool and reads each
// slot's live lock holder. Cheap (two small file reads per slot), called when
// the executor launch menu opens so the indicator reflects decision-time state.
func executorSlotStatuses(proj *storage.Project) []executorSlotStatus {
	if proj == nil {
		return nil
	}
	slots := proj.GetExecutor().ResolveWorktrees(proj.Path)
	out := make([]executorSlotStatus, 0, len(slots))
	for _, s := range slots {
		out = append(out, executorSlotStatus{path: s.Path, holder: storage.LiveWorktreeHolder(s.Path)})
	}
	return out
}
