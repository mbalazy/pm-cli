package board

import (
	"fmt"
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
		{"X", "Delete task (confirm)"},
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
		{"x", "Run executor (pm work / run-epic)"},
		{"W", "Watch executor run (live agent-view)"},
		{"K", "Kill a live executor run (confirm)"},
		{"F", "Acceptance report (detail / runs view)"},
		{"R", "Runs view (every run + acceptance)"},
		{"o / Enter", "Task detail"},
		{"/ ", "Search tasks (-<id> = ID only)"},
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

// viewClaudeMenu is the launch overlay's router. Each mode has a renderer of
// its own: the executor's grew four toggles and a slot table, the LLM's has to
// say where a session runs and which one it joins, and one function trying to
// be both is what left every line explaining itself in prose.
func (m Model) viewClaudeMenu() string {
	if m.launchAgent == launchAgentExecutor && !m.projectScopeLaunch {
		return m.viewExecutorMenu()
	}
	return m.viewLLMMenu()
}
