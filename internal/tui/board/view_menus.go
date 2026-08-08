package board

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) viewHelp() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Keyboard Shortcuts")
	allKeys := []struct{ key, desc string }{
		{"↑ / k", "Move up"},
		{"↓ / j", "Move down"},
		{"← / h", "Previous column"},
		{"→ / l", "Next column"},
		{"g", "Jump to top"},
		{"G", "Jump to bottom"},
		{"Ctrl+d", "Half page down"},
		{"Ctrl+u", "Half page up"},
		{"Ctrl+j", "Reorder task down"},
		{"Ctrl+k", "Reorder task up"},
		{"m", "Move task forward"},
		{"M", "Move task back"},
		{"w", "Mark waiting (confirm)"},
		{"d", "Mark done (confirm)"},
		{"x", "Delete task (confirm)"},
		{"A", "Archive task (confirm)"},
		{"a", "Add new task"},
		{"e", "Edit in $EDITOR"},
		{"y", "Yank ID"},
		{"Y", "Yank menu"},
		{"L", "Open links"},
		{"v", "Select mode (multi-select)"},
		{"V", "Toggle columns"},
		{"i", "Project info"},
		{"c", "Claude Code"},
		{"X", "Run executor (pm work / run-epic)"},
		{"W", "Watch executor run (live agent-view)"},
		{"K", "Kill a live executor run (confirm)"},
		{"R", "Runs view (every run + acceptance)"},
		{"o / Enter", "Task detail"},
		{"/ ", "Search tasks"},
		{";", "Zoom toggle"},
		{"t", "Toggle focus"},
		{"T", "Focus view"},
		{"Ctrl+a", "Archive view"},
		{"P", "Project picker"},
		{"1-9", "Jump to project"},
		{"Tab", "Next project"},
		{"S-Tab", "Previous project"},
		{"u", "Undo last action"},
		{"r", "Refresh"},
		{"?", "This help"},
		{"q", "Quit (confirm)"},
	}

	// filter keys if search active
	var keys []struct{ key, desc string }
	if m.helpFilter != "" {
		q := strings.ToLower(m.helpFilter)
		for _, k := range allKeys {
			if strings.Contains(strings.ToLower(k.key), q) || strings.Contains(strings.ToLower(k.desc), q) {
				keys = append(keys, k)
			}
		}
	} else {
		keys = allKeys
	}

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	if len(keys) == 0 {
		lines = append(lines, helpStyle.Render("  no matches"))
	} else {
		mid := (len(keys) + 1) / 2
		leftKeys := keys[:mid]
		rightKeys := keys[mid:]
		var leftLines, rightLines []string
		keyStyle := lipgloss.NewStyle().Bold(true).Foreground(special).Width(12)
		for _, k := range leftKeys {
			leftLines = append(leftLines, keyStyle.Render(k.key)+"  "+k.desc)
		}
		for _, k := range rightKeys {
			rightLines = append(rightLines, keyStyle.Render(k.key)+"  "+k.desc)
		}
		leftCol := strings.Join(leftLines, "\n")
		rightCol := strings.Join(rightLines, "\n")
		cols := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, "    ", rightCol)
		lines = append(lines, cols)
	}
	lines = append(lines, "")

	if m.helpSearch {
		lines = append(lines, m.helpInput.View())
	} else if m.helpFilter != "" {
		lines = append(lines, helpStyle.Render(fmt.Sprintf("filter: %q  / edit  esc clear", m.helpFilter)))
	} else {
		lines = append(lines, helpStyle.Render("/ filter  esc close"))
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewYankMenu() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Yank to clipboard")

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	for i, item := range m.yankItems {
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})
		if i == m.yankCursor {
			prefix = "> "
			style = lipgloss.NewStyle().Bold(true).Foreground(special)
		}
		display := truncateWidth(item.value, 50)
		lines = append(lines, style.Render(fmt.Sprintf("%s%s: %s", prefix, item.label, display)))
	}
	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("enter copy  esc back"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewSubtaskPicker() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render(
		fmt.Sprintf("Subtasks (%d)", len(m.subtaskItems)))

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

	boxW := m.width - 12
	if boxW > 96 {
		boxW = 96
	}
	if boxW < 44 {
		boxW = 44
	}
	leftW := 24 // glyph + status + id
	titleW := boxW - leftW - 4
	if titleW < 18 {
		titleW = 18
	}

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	for i, k := range m.subtaskItems {
		prefix := "  "
		style := dimStyle
		if i == m.subtaskCursor {
			prefix = "> "
			style = lipgloss.NewStyle().Bold(true).Foreground(special)
		}
		left := style.Width(leftW).Render(fmt.Sprintf("%s %-7s #%s", statusGlyphAligned(k.Meta.Status), string(k.Meta.Status), k.Meta.ID))
		titleBlock := style.Width(titleW).Render(truncLines(k.Meta.Title, titleW, 2))
		row := lipgloss.JoinHorizontal(lipgloss.Top, style.Render(prefix), left, titleBlock)
		lines = append(lines, row)
	}
	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("↑/↓ select  enter open  esc back"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewLinksMenu() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Open link")

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	for i, item := range m.linkItems {
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})
		if i == m.linksCursor {
			prefix = "> "
			style = lipgloss.NewStyle().Bold(true).Foreground(special)
		}
		display := truncateWidth(item.url, 50)
		lines = append(lines, style.Render(fmt.Sprintf("%s%s: %s", prefix, item.name, display)))
	}
	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("enter open  y yank  esc back"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewSessionMenu() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render(
		fmt.Sprintf("Sessions (%d)", len(m.sessionMenuItems)))

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	for i, item := range m.sessionMenuItems {
		prefix := "  "
		primaryStyle := dimStyle
		secondaryStyle := dimStyle
		if i == m.sessionCursor {
			prefix = "> "
			primaryStyle = lipgloss.NewStyle().Bold(true).Foreground(special)
			secondaryStyle = lipgloss.NewStyle().Foreground(special)
		}

		// Line 1: summary or meta (msgs | date) + (latest) tag
		var primaryParts []string
		if item.summary != "" {
			primaryParts = append(primaryParts, truncateWidth(item.summary, 45))
		}
		if item.msgCount > 0 {
			label := fmt.Sprintf("%d msgs", item.msgCount)
			if item.msgCount == 1 {
				label = "1 msg"
			}
			primaryParts = append(primaryParts, label)
		}
		if item.modified != "" {
			primaryParts = append(primaryParts, item.modified)
		}
		primaryLine := strings.Join(primaryParts, " | ")
		if item.isLatest {
			primaryLine += " (latest)"
		}
		lines = append(lines, primaryStyle.Render(prefix+primaryLine))

		// Line 2: session ID + branch
		idLabel := item.sessionID
		if len(idLabel) > 10 {
			idLabel = idLabel[:10] + "..."
		}
		if item.branch != "" {
			idLabel += " | " + item.branch
		}
		lines = append(lines, secondaryStyle.Render("    "+idLabel))
	}
	lines = append(lines, "")
	if m.confirmAction == "delete-session" {
		warnStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF6666"})
		lines = append(lines, warnStyle.Render("  press x again to delete session"))
	} else {
		lines = append(lines, helpStyle.Render("enter/r resume  f fork  x delete  y yank  esc back"))
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewColVisMenu() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Visible columns")

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	for i, item := range m.colVisItems {
		check := "[ ]"
		if item.visible {
			check = "[x]"
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})
		prefix := "  "
		if i == m.colVisCursor {
			prefix = "> "
			style = lipgloss.NewStyle().Bold(true).Foreground(special)
		}
		lines = append(lines, style.Render(fmt.Sprintf("%s%s %s", prefix, check, string(item.status))))
	}
	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("enter toggle  esc back"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewClaudeMenu() string {
	titleText := "Launch LLM"
	if m.projectScopeLaunch {
		titleText = "Launch LLM (project)"
	} else if m.launchAgent == launchAgentExecutor {
		titleText = "Run task (pm work)"
		if m.executorIsTracker {
			titleText = "Run " + trackerRunNoun(m.menuTask()) + " (pm run-epic)"
		}
	} else if m.launchAgent != launchAgentCodex && m.resumeOnly {
		sid := m.resumeSessionID
		if len(sid) > 8 {
			sid = sid[:8] + "..."
		}
		if m.forkMode {
			titleText = "Fork session " + sid
		} else {
			titleText = "Resume session " + sid
		}
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render(titleText)

	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(highlight)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")

	lines = append(lines, "  "+keyStyle.Render("@")+" Agent: "+lipgloss.NewStyle().Bold(true).Foreground(special).Render(m.launchAgent.label()))
	lines = append(lines, "")

	// skip-permissions toggle
	check := "[ ]"
	checkStyle := dimStyle
	if m.claudeMenuSkipPerms {
		check = "[x]"
		checkStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF6B6B"))
	}
	permsLabel := "skip permissions"
	switch m.launchAgent {
	case launchAgentCodex:
		permsLabel = "bypass sandbox"
	case launchAgentExecutor:
		permsLabel = "yolo (bypass autonomy envelope)"
	}
	lines = append(lines, "  "+keyStyle.Render("!")+checkStyle.Render(" "+check+" "+permsLabel))

	// Executor-only: choose default (main checkout) vs an isolated "additional"
	// worktree slot for THIS launch. Shown only when the project has slots
	// configured, with per-slot occupancy so the user sees which slot a launch
	// would claim (first free wins).
	if m.launchAgent == launchAgentExecutor && m.executorAdditionalAvail {
		addCheck := "[ ]"
		addStyle := dimStyle
		if m.claudeMenuAdditional {
			addCheck = "[x]"
			addStyle = lipgloss.NewStyle().Bold(true).Foreground(special)
		}
		label := " additional worktree (first free slot; own branch/port/sim)"
		if len(m.executorSlots) == 1 {
			label = " additional worktree (isolated branch/port/sim)"
		}
		lines = append(lines, "  "+keyStyle.Render("#")+addStyle.Render(" "+addCheck+label))

		freeStyle := lipgloss.NewStyle().Foreground(special)
		busyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
		allBusy := true
		for i, s := range m.executorSlots {
			name := filepath.Base(s.path)
			if s.holder == nil {
				allBusy = false
				lines = append(lines, dimStyle.Render(fmt.Sprintf("       slot %d  %s  ", i+1, name))+freeStyle.Render("○ free"))
			} else {
				lines = append(lines, dimStyle.Render(fmt.Sprintf("       slot %d  %s  ", i+1, name))+busyStyle.Render(fmt.Sprintf("● busy: %s (pid %d)", s.holder.TaskID, s.holder.PID)))
			}
		}
		if m.claudeMenuAdditional && allBusy {
			lines = append(lines, "       "+busyStyle.Bold(true).Render("all slots busy - this launch will fail; wait or kill a run (K)"))
		}
	}

	// Executor tracker only: chain the acceptance (`pm finish`) when the epic
	// run ends (--then-finish). Off = the flag is not passed, so the tracker's
	// finish_mode keeps deciding - the label says so to keep the tri-state
	// honest on screen.
	if m.launchAgent == launchAgentExecutor && m.executorIsTracker {
		chainCheck := "[ ]"
		chainStyle := dimStyle
		if m.claudeMenuThenFinish {
			chainCheck = "[x]"
			chainStyle = lipgloss.NewStyle().Bold(true).Foreground(special)
		}
		lines = append(lines, "  "+keyStyle.Render("&")+chainStyle.Render(" "+chainCheck+" chain acceptance after the run (--then-finish; off = tracker's finish_mode decides)"))
	}
	lines = append(lines, "")

	for i, item := range m.claudeMenuItems {
		prefix := "  "
		labelStyle := dimStyle
		if i == m.claudeMenuCursor {
			prefix = "> "
			labelStyle = lipgloss.NewStyle().Bold(true).Foreground(special)
		}
		shortcut := keyStyle.Render("[" + item.shortcut + "]")
		lines = append(lines, prefix+shortcut+" "+labelStyle.Render(item.label))
	}
	lines = append(lines, "")
	help := "press key or enter  @ toggle agent  ! toggle perms  esc back"
	if m.launchAgent == launchAgentExecutor {
		var toggles string
		if m.executorAdditionalAvail {
			toggles += "  # additional"
		}
		if m.executorIsTracker {
			toggles += "  & chain acceptance"
		}
		if toggles != "" {
			help = "press key or enter  @ agent  ! perms" + toggles + "  esc back"
		}
	}
	lines = append(lines, helpStyle.Render(help))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
