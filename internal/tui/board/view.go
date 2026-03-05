package board

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/glamour"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/version"
)

func renderTaskDetail(t *storage.Task, termWidth int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s", t.Meta.Title)
	if t.Meta.ID != "" {
		fmt.Fprintf(&sb, " (#%s)", t.Meta.ID)
	}
	fmt.Fprintln(&sb)
	fmt.Fprintf(&sb, "\n**Status:** %s | **Project:** %s | **Updated:** %s\n", t.Meta.Status, t.Project, t.Meta.Updated)
	if t.Meta.Branch != "" {
		fmt.Fprintf(&sb, "\n**Branch:** `%s`\n", t.Meta.Branch)
	}
	if len(t.Meta.Links) > 0 {
		var parts []string
		for name, url := range t.Meta.Links {
			parts = append(parts, fmt.Sprintf("[%s](%s)", name, url))
		}
		fmt.Fprintf(&sb, "\n**Links:** %s\n", strings.Join(parts, " | "))
	}
	if len(t.Meta.Tags) > 0 {
		fmt.Fprintf(&sb, "\n**Tags:** %s\n", strings.Join(t.Meta.Tags, ", "))
	}
	if len(t.Meta.Sessions) > 0 {
		fmt.Fprintf(&sb, "\n**Sessions:** (%d)\n", len(t.Meta.Sessions))
		for i, s := range t.Meta.Sessions {
			fmt.Fprintf(&sb, "- `%s`", s)
			if i == len(t.Meta.Sessions)-1 {
				fmt.Fprint(&sb, " *(latest)*")
			}
			fmt.Fprintln(&sb)
		}
	}
	if t.Meta.Brief != "" {
		fmt.Fprintf(&sb, "\n**Brief:**\n%s\n", t.Meta.Brief)
	}
	if t.Body != "" {
		fmt.Fprintf(&sb, "\n---\n\n%s\n", t.Body)
	}

	contentWidth := termWidth - 4
	if contentWidth > 100 {
		contentWidth = 100
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(contentWidth),
	)
	if err != nil {
		return sb.String()
	}
	rendered, err := r.Render(sb.String())
	if err != nil {
		return sb.String()
	}

	pad := (termWidth - contentWidth) / 2
	if pad < 0 {
		pad = 0
	}
	style := lipgloss.NewStyle().PaddingLeft(pad)
	return style.Render(rendered)
}

func renderProjectInfo(proj *storage.Project, slug string, termWidth int) string {
	var sb strings.Builder
	name := proj.Name
	if name == "" {
		name = slug
	}
	fmt.Fprintf(&sb, "# %s\n", name)
	if proj.Stack != "" {
		fmt.Fprintf(&sb, "\n**Stack:** %s\n", proj.Stack)
	}
	if proj.Notes != "" {
		fmt.Fprintf(&sb, "\n**Notes:** %s\n", proj.Notes)
	}
	if proj.Repo != "" {
		fmt.Fprintf(&sb, "\n**Repo:** %s\n", proj.Repo)
	}
	if proj.Path != "" {
		fmt.Fprintf(&sb, "\n**Path:** %s\n", proj.Path)
	}
	if len(proj.Tags) > 0 {
		fmt.Fprintf(&sb, "\n**Tags:** %s\n", strings.Join(proj.Tags, ", "))
	}
	statuses := proj.GetStatuses()
	var statusNames []string
	for _, s := range statuses {
		statusNames = append(statusNames, string(s))
	}
	fmt.Fprintf(&sb, "\n**Statuses:** %s\n", strings.Join(statusNames, " → "))
	if len(proj.Links) > 0 {
		var parts []string
		for name, url := range proj.Links {
			parts = append(parts, fmt.Sprintf("[%s](%s)", name, url))
		}
		fmt.Fprintf(&sb, "\n**Links:** %s\n", strings.Join(parts, " | "))
	}

	contentWidth := termWidth - 4
	if contentWidth > 100 {
		contentWidth = 100
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(contentWidth),
	)
	if err != nil {
		return sb.String()
	}
	rendered, err := r.Render(sb.String())
	if err != nil {
		return sb.String()
	}

	pad := (termWidth - contentWidth) / 2
	if pad < 0 {
		pad = 0
	}
	style := lipgloss.NewStyle().PaddingLeft(pad)
	return style.Render(rendered)
}

func (m Model) View() string {
	// Overlay menus take priority over all views
	if m.claudeMenu {
		return m.viewClaudeMenu()
	}
	if m.currentView == viewDetail || m.currentView == viewProjectInfo {
		return m.viewDetail()
	}
	if m.colVisMenu {
		return m.viewColVisMenu()
	}
	if m.yankMenu {
		return m.viewYankMenu()
	}
	if m.linksMenu {
		return m.viewLinksMenu()
	}
	if m.showHelp {
		return m.viewHelp()
	}
	if m.currentView == viewArchive {
		return m.viewArchive()
	}
	return m.viewBoard()
}

func (m Model) viewDetail() string {
	if m.yankMenu {
		return m.viewYankMenu()
	}
	if m.linksMenu {
		return m.viewLinksMenu()
	}
	if m.sessionMenu {
		return m.viewSessionMenu()
	}
	var sb strings.Builder
	sb.WriteString(m.detailViewport.View())
	sb.WriteString("\n")
	pct := fmt.Sprintf("%3.f%%", m.detailViewport.ScrollPercent()*100)
	help := "o/q: back  e: edit  r: refresh  m/w/d/A: move/wait/done/archive  y/Y: yank  L: links  s: sessions  " + pct
	if m.currentView == viewProjectInfo {
		help = "o/esc/q: back  ↑/↓/j/k scroll  y/Y: yank  L: links  " + pct
	}
	if m.confirmAction == "done" {
		help = "press d again to confirm done  " + pct
	}
	if m.confirmAction == "waiting" {
		help = "press w again to mark waiting  " + pct
	}
	if m.confirmAction == "archive" {
		help = "press A again to archive  " + pct
	}
	sb.WriteString(helpStyle.Render(help))
	return m.applyToast(sb.String())
}

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
		{"v", "Toggle columns"},
		{"i", "Project info"},
		{"c", "Claude Code"},
		{"o / Enter", "Task detail"},
		{"/ ", "Search tasks"},
		{";", "Zoom toggle"},
		{"Ctrl+a", "Archive view"},
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
		display := item.value
		if len(display) > 50 {
			display = display[:47] + "..."
		}
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
		display := item.url
		if len(display) > 50 {
			display = display[:47] + "..."
		}
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
			s := item.summary
			if len(s) > 45 {
				s = s[:42] + "..."
			}
			primaryParts = append(primaryParts, s)
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
	titleText := "Launch Claude Code"
	if m.resumeOnly {
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

	// skip-permissions toggle
	check := "[ ]"
	checkStyle := dimStyle
	if m.claudeMenuSkipPerms {
		check = "[x]"
		checkStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF6B6B"))
	}
	lines = append(lines, "  "+keyStyle.Render("!")+checkStyle.Render(" "+check+" skip permissions"))
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
	lines = append(lines, helpStyle.Render("press key or enter  ! toggle perms  esc back"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewArchive() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("pm archive"))
	sb.WriteString("\n")

	// project tabs
	var tabs []string
	for i, p := range m.projects {
		name := p
		if i == 0 {
			name = "ALL"
		}
		if i == m.activeProject {
			tabs = append(tabs, activeTabStyle.Render("["+name+"]"))
		} else {
			tabs = append(tabs, tabStyle.Render(name))
		}
	}
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
	sb.WriteString("\n\n")

	tasks := m.archivedTasks()
	if len(tasks) == 0 {
		sb.WriteString(helpStyle.Render("  No archived tasks."))
		sb.WriteString("\n")
	} else {
		listHeight := m.height - 10
		if listHeight < 3 {
			listHeight = 3
		}
		cardWidth := m.width - 8
		if cardWidth > 100 {
			cardWidth = 100
		}
		for i, t := range tasks {
			if i >= listHeight {
				break
			}
			isSelected := i == m.archiveCursor
			card := renderCard(t, cardWidth, isSelected)
			sb.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render(card))
			sb.WriteString("\n")
		}
	}

	// confirmation prompt
	if m.confirmAction != "" {
		sb.WriteString("\n")
		prompt := fmt.Sprintf("  press x again to delete")
		sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF6666"}).Render(prompt))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	help := helpStyle.Render("archived: " + fmt.Sprint(len(tasks)) + "  ↑/↓ navigate  r restore  u undo  x delete  o detail  esc back")
	sb.WriteString(help)

	return sb.String()
}

func (m Model) viewBoard() string {
	var sb strings.Builder

	// title
	sb.WriteString(titleStyle.Render("pm board"))
	sb.WriteString("\n")

	// project tabs with counts
	var tabs []string
	for i, p := range m.projects {
		name := p
		if i == 0 {
			name = "ALL"
		}
		count := m.projectCounts[p]
		label := fmt.Sprintf("%s (%d)", name, count)
		if i == m.activeProject {
			tabs = append(tabs, activeTabStyle.Render("["+label+"]"))
		} else {
			tabs = append(tabs, tabStyle.Render(label))
		}
	}
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
	sb.WriteString("\n\n")

	// columns
	numCols := len(m.statuses)
	if numCols == 0 {
		numCols = 1
	}

	// In zoom mode, show only the active column at full width
	var visibleStatuses []storage.TaskStatus
	var visibleIndices []int
	if m.zoomed && len(m.statuses) > 0 {
		visibleStatuses = []storage.TaskStatus{m.statuses[m.activeCol]}
		visibleIndices = []int{m.activeCol}
		numCols = 1
	} else {
		visibleStatuses = m.statuses
		visibleIndices = make([]int, len(m.statuses))
		for i := range m.statuses {
			visibleIndices[i] = i
		}
	}

	colWidth := (m.width - 8) / numCols
	if colWidth < 20 {
		colWidth = 20
	}
	// Non-column overhead: title(2\n) + tabs(2\n) + after-cols(1\n) + confirm(1\n)
	// + status bar(1 line) + column border/padding(4\n) = 11 lines.
	// Search/add input adds 1 more when active.
	overhead := 11
	if m.adding || m.searching || m.searchQuery != "" {
		overhead++
	}
	maxCardHeight := m.height - overhead
	if maxCardHeight < 5 {
		maxCardHeight = 5
	}

	var cols []string
	for vi, status := range visibleStatuses {
		i := visibleIndices[vi]
		tasks := m.filteredTasks(status)
		isActive := i == m.activeCol

		header := strings.ToUpper(string(status))
		var content strings.Builder
		if m.zoomed {
			content.WriteString(columnHeaderStyle.Render(fmt.Sprintf("[ZOOM] %s (%d)", header, len(tasks))))
		} else {
			content.WriteString(columnHeaderStyle.Render(fmt.Sprintf("%s (%d)", header, len(tasks))))
		}
		content.WriteString("\n")

		// Render only visible cards within scroll window
		offset := 0
		if i < len(m.scrollOffsets) {
			offset = m.scrollOffsets[i]
		}

		// Card budget: maxCardHeight minus header lines (header + scroll-up indicator)
		headerLines := 1 // column header line
		if offset > 0 {
			content.WriteString(helpStyle.Render(fmt.Sprintf("  ▲ %d more", offset)))
			content.WriteString("\n")
			headerLines++
		}
		cardBudget := maxCardHeight - headerLines

		usedH := 0
		rendered := 0
		for j := offset; j < len(tasks); j++ {
			isSelected := isActive && j == m.cursors[i]
			var card string
			if m.zoomed {
				card = renderZoomCard(tasks[j], colWidth-6, isSelected)
			} else {
				card = renderCard(tasks[j], colWidth-6, isSelected)
			}
			actualH := strings.Count(card, "\n") + 1
			if usedH+actualH > cardBudget && rendered > 0 {
				remaining := len(tasks) - j
				content.WriteString(helpStyle.Render(fmt.Sprintf("  ▼ %d more", remaining)))
				content.WriteString("\n")
				break
			}
			content.WriteString(card)
			content.WriteString("\n")
			usedH += actualH
			rendered++
		}

		style := columnStyle
		if isActive {
			style = activeColumnStyle
		}
		// Clamp content to exactly maxCardHeight newlines so all columns
		// have identical height (text wrapping in cards can exceed cardHeight).
		contentStr := content.String()
		contentLines := strings.Count(contentStr, "\n")
		if contentLines > maxCardHeight {
			parts := strings.SplitN(contentStr, "\n", maxCardHeight+1)
			contentStr = strings.Join(parts[:maxCardHeight], "\n") + "\n"
		} else if contentLines < maxCardHeight {
			contentStr += strings.Repeat("\n", maxCardHeight-contentLines)
		}
		col := style.Width(colWidth).Render(contentStr)
		cols = append(cols, col)
	}

	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, cols...))
	sb.WriteString("\n")

	// search bar / add input
	if m.adding {
		sb.WriteString(m.addInput.View())
		sb.WriteString("\n")
	} else if m.searching {
		sb.WriteString(m.searchInput.View())
		sb.WriteString("\n")
	} else if m.searchQuery != "" {
		sb.WriteString(helpStyle.Render(fmt.Sprintf("filter: %q (/ to edit, esc to clear)", m.searchQuery)))
		sb.WriteString("\n")
	}

	// confirmation prompt (always reserve 1 line to prevent layout shift)
	if m.confirmAction != "" {
		var prompt string
		switch m.confirmAction {
		case "done":
			prompt = "  press d again to mark done"
		case "waiting":
			prompt = "  press w again to mark waiting"
		case "delete":
			prompt = "  press x again to delete"
		case "archive":
			prompt = "  press A again to archive"
		case "quit":
			prompt = "  press q again to quit"
		}
		sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF6666"}).Render(prompt))
	}
	sb.WriteString("\n")

	// status bar
	statusBar := func() string {
		startup := fmt.Sprintf("%dms", m.startupDuration.Milliseconds())
		ver := helpStyle.Render("pm " + version.Version + " " + startup)
		help := helpStyle.Render("m/M move  d done  a add  c claude  e edit  y/Y yank  L links  i info  C-a archived")
		gap := m.width - lipgloss.Width(ver) - lipgloss.Width(help)
		if gap < 1 {
			gap = 1
		}
		return ver + strings.Repeat(" ", gap) + help
	}()

	// Pad to fill terminal height so status bar sits at the bottom
	currentHeight := strings.Count(sb.String(), "\n") + 1 // +1 for status bar line
	if pad := m.height - currentHeight - 1; pad > 0 {
		sb.WriteString(strings.Repeat("\n", pad))
	}
	sb.WriteString(statusBar)

	return m.applyToast(sb.String())
}

func (m Model) applyToast(result string) string {
	if m.toastMsg == "" || !time.Now().Before(m.toastExpiry) {
		return result
	}
	toast := toastStyle.Render(" " + m.toastMsg + " ")
	toastWidth := lipgloss.Width(toast)
	lines := strings.Split(result, "\n")
	if len(lines) > 0 {
		first := lines[0]
		firstWidth := lipgloss.Width(first)
		if firstWidth+toastWidth+1 <= m.width {
			gap := m.width - firstWidth - toastWidth
			lines[0] = first + strings.Repeat(" ", gap) + toast
		} else {
			// Overlay: replace end of first line with toast
			// Truncate first line to make room
			target := m.width - toastWidth
			if target < 0 {
				target = 0
			}
			lines[0] = lipgloss.NewStyle().Width(target).Render(first) + toast
		}
		result = strings.Join(lines, "\n")
	}
	return result
}

func renderCard(t *storage.Task, width int, selected bool) string {
	style := cardStyle
	if selected {
		style = activeCardStyle
	}
	style = style.Width(width)

	var lines []string

	title := t.Meta.Title
	if t.Meta.ID != "" {
		title = "#" + t.Meta.ID + " " + title
	}
	// truncate title if too long
	if len(title) > width-2 {
		title = title[:width-5] + "..."
	}
	lines = append(lines, cardTitleStyle.Render(title))
	lines = append(lines, cardProjectStyle.Render(t.Project))

	if len(t.Meta.Tags) > 0 {
		lines = append(lines, cardTagStyle.Render(strings.Join(t.Meta.Tags, ", ")))
	}

	return style.Render(strings.Join(lines, "\n"))
}

func renderZoomCard(t *storage.Task, width int, selected bool) string {
	style := cardStyle
	if selected {
		style = activeCardStyle
	}
	style = style.Width(width)

	var lines []string

	// Title (full, no truncation - we have space)
	title := t.Meta.Title
	if t.Meta.ID != "" {
		title = "#" + t.Meta.ID + " " + title
	}
	lines = append(lines, cardTitleStyle.Render(title))

	// Project + updated date on same conceptual level
	meta := t.Project
	if t.Meta.Updated != "" {
		meta += "  " + helpStyle.Render(t.Meta.Updated)
	}
	lines = append(lines, cardProjectStyle.Render(meta))

	if len(t.Meta.Tags) > 0 {
		lines = append(lines, cardTagStyle.Render(strings.Join(t.Meta.Tags, ", ")))
	}

	if t.Meta.Branch != "" {
		lines = append(lines, helpStyle.Render("branch: "+t.Meta.Branch))
	}

	if len(t.Meta.Links) > 0 {
		var lk []string
		for name := range t.Meta.Links {
			lk = append(lk, name)
		}
		sort.Strings(lk)
		lines = append(lines, helpStyle.Render("links: "+strings.Join(lk, ", ")))
	}

	if t.Meta.Brief != "" {
		brief := t.Meta.Brief
		maxLen := width * 2 // ~2 lines worth
		if len(brief) > maxLen {
			brief = brief[:maxLen-3] + "..."
		}
		lines = append(lines, "")
		lines = append(lines, helpStyle.Render(brief))
	}

	return style.Render(strings.Join(lines, "\n"))
}

func zoomCardHeight(t *storage.Task) int {
	lines := 2 // title + project
	if len(t.Meta.Tags) > 0 {
		lines++
	}
	if t.Meta.Branch != "" {
		lines++
	}
	if len(t.Meta.Links) > 0 {
		lines++
	}
	if t.Meta.Brief != "" {
		lines += 3 // blank line + up to 2 lines of brief
	}
	return lines + 3 // +2 border, +1 margin
}
