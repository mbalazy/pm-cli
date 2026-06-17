package board

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/version"
)

func (m Model) renderTaskDetail(t *storage.Task) string {
	termWidth := m.width
	contentWidth := termWidth - 4
	if contentWidth > 100 {
		contentWidth = 100
	}

	// Head: title + status + parent link (rendered via glamour).
	var head strings.Builder
	fmt.Fprintf(&head, "# %s", t.Meta.Title)
	if t.Meta.ID != "" {
		fmt.Fprintf(&head, " (#%s)", t.Meta.ID)
	}
	fmt.Fprintln(&head)
	fmt.Fprintf(&head, "\n**Status:** %s | **Project:** %s | **Updated:** %s\n", t.Meta.Status, t.Project, t.Meta.Updated)
	if p := m.taskParent(t); p != nil {
		fmt.Fprintf(&head, "\n**Parent:** %s `#%s` %s  _(press p)_\n", statusGlyph(p.Meta.Status), p.Meta.ID, p.Meta.Title)
	}

	// Rest: branch, links, tags, sessions, ac, brief, body (rendered via glamour).
	var rest strings.Builder
	if t.Meta.Branch != "" {
		fmt.Fprintf(&rest, "\n**Branch:** `%s`\n", t.Meta.Branch)
	}
	if len(t.Meta.Links) > 0 {
		var parts []string
		for name, url := range t.Meta.Links {
			parts = append(parts, fmt.Sprintf("[%s](%s)", name, url))
		}
		fmt.Fprintf(&rest, "\n**Links:** %s\n", strings.Join(parts, " | "))
	}
	if len(t.Meta.Tags) > 0 {
		fmt.Fprintf(&rest, "\n**Tags:** %s\n", strings.Join(t.Meta.Tags, ", "))
	}
	if len(t.Meta.Sessions) > 0 {
		fmt.Fprintf(&rest, "\n**Sessions:** (%d)\n", len(t.Meta.Sessions))
		for i, s := range t.Meta.Sessions {
			fmt.Fprintf(&rest, "- `%s`", s)
			if i == len(t.Meta.Sessions)-1 {
				fmt.Fprint(&rest, " *(latest)*")
			}
			fmt.Fprintln(&rest)
		}
	}
	if t.Meta.AC != "" {
		fmt.Fprintf(&rest, "\n**Acceptance Criteria:**\n%s\n", t.Meta.AC)
	}
	if t.Meta.Brief != "" {
		fmt.Fprintf(&rest, "\n**Brief:**\n%s\n", t.Meta.Brief)
	}
	if t.Body != "" {
		fmt.Fprintf(&rest, "\n---\n\n%s\n", bodyToDisplayMarkdown(t.Body))
	}

	var out strings.Builder
	out.WriteString(glamourRender(head.String(), contentWidth))
	// Subtask table (tracker -> children): real bordered rows, press p to pick.
	if kids := m.taskChildren(t); len(kids) > 0 {
		out.WriteString(renderSubtaskTable(kids, contentWidth))
		out.WriteString("\n")
	}
	// Executor run dashboard (live or last run), for a tracker or standalone task.
	if run := m.runStates[t.Meta.ID]; run != nil {
		out.WriteString(renderExecutorDashboard(run, contentWidth))
		out.WriteString("\n\n")
	}
	out.WriteString(glamourRender(rest.String(), contentWidth))

	pad := (termWidth - contentWidth) / 2
	if pad < 0 {
		pad = 0
	}
	return lipgloss.NewStyle().PaddingLeft(pad).Render(out.String())
}

// bodyToDisplayMarkdown turns the raw body into display markdown with visible
// zone headers: the Spec block (current truth) and the Log (append-only
// history). Bodies without spec markers pass through unchanged.
func bodyToDisplayMarkdown(body string) string {
	spec := storage.ExtractSpec(body)
	if spec == "" {
		return body
	}
	start := strings.Index(body, storage.SpecStart)
	end := strings.Index(body, storage.SpecEnd) + len(storage.SpecEnd)
	before := strings.TrimSpace(body[:start])
	after := strings.TrimSpace(body[end:])
	log := strings.TrimSpace(before + "\n\n" + after)

	var b strings.Builder
	b.WriteString("## Spec (current truth)\n\n")
	b.WriteString(spec)
	if log != "" {
		b.WriteString("\n\n## Log (history, append-only)\n\n")
		b.WriteString(log)
	}
	return b.String()
}

// glamourRender renders markdown to ANSI at the given wrap width, falling back
// to the raw markdown on error.
func glamourRender(md string, contentWidth int) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(contentWidth),
	)
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return out
}

// renderSubtaskTable renders a tracker's children as a bordered table with a
// horizontal rule between every row and titles wrapped to at most two lines.
func renderSubtaskTable(kids []*storage.Task, width int) string {
	done := 0
	for _, k := range kids {
		if k.Meta.Status == storage.StatusDone || k.Meta.Status == "merged" {
			done++
		}
	}
	header := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render(fmt.Sprintf("Subtasks (%d/%d)  ", done, len(kids))) +
		helpStyle.Render("(press p to pick & open)")

	titleW := width - 30
	if titleW < 18 {
		titleW = 18
	}
	tbl := table.New().
		Border(lipgloss.NormalBorder()).
		BorderRow(true).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#555555"})).
		Width(width).
		StyleFunc(func(row, col int) lipgloss.Style {
			return lipgloss.NewStyle().Padding(0, 1)
		}).
		Headers("STATUS", "ID", "TITLE")
	for _, k := range kids {
		tbl.Row(
			statusGlyph(k.Meta.Status)+" "+string(k.Meta.Status),
			"#"+k.Meta.ID,
			truncLines(k.Meta.Title, titleW, 2),
		)
	}
	return header + "\n" + tbl.String()
}

// truncLines word-wraps s to at most maxLines lines of the given rune width,
// appending an ellipsis to the last line if content was dropped.
func truncLines(s string, width, maxLines int) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var lines []string
	cur := ""
	i := 0
	for ; i < len(words); i++ {
		w := words[i]
		cand := w
		if cur != "" {
			cand = cur + " " + w
		}
		if len([]rune(cand)) <= width {
			cur = cand
			continue
		}
		if cur == "" {
			// single word wider than the column: hard-truncate onto its own line
			r := []rune(w)
			if len(r) > width {
				r = r[:width]
			}
			cur = string(r)
			continue
		}
		lines = append(lines, cur)
		cur = w
		if len(lines) == maxLines {
			cur = ""
			break
		}
	}
	if cur != "" {
		lines = append(lines, cur)
		i = len(words)
	}
	if i < len(words) && len(lines) > 0 {
		last := []rune(lines[len(lines)-1])
		if len(last) > width-1 {
			last = last[:width-1]
		}
		lines[len(lines)-1] = string(last) + "…"
	}
	return strings.Join(lines, "\n")
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
	if m.yankMenu {
		return m.viewYankMenu()
	}
	if m.projectPicker {
		return m.viewProjectPicker()
	}
	if m.colVisMenu {
		return m.viewColVisMenu()
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
	if m.currentView == viewFocus {
		return m.viewFocus()
	}
	if m.currentView == viewExecutor {
		return m.viewExecutor()
	}
	return m.viewBoard()
}

func (m Model) viewDetail() string {
	if m.subtaskPicker {
		return m.viewSubtaskPicker()
	}
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

	if m.detailSearching {
		sb.WriteString(m.detailSearchInput.View())
	} else if m.detailSearchQuery != "" {
		pct := fmt.Sprintf("%3.f%%", m.detailViewport.ScrollPercent()*100)
		matchInfo := fmt.Sprintf("[%d/%d]", m.detailSearchIdx+1, len(m.detailSearchMatches))
		if len(m.detailSearchMatches) == 0 {
			matchInfo = "[no matches]"
		}
		sb.WriteString(helpStyle.Render(fmt.Sprintf("/%s %s  n/N: next/prev  esc: clear  %s", m.detailSearchQuery, matchInfo, pct)))
	} else {
		pct := fmt.Sprintf("%3.f%%", m.detailViewport.ScrollPercent()*100)
		help := "o/q: back  e: edit  r: refresh  m/w/d/A: move/wait/done/archive  y/Y: yank  L: links  s: sessions  " + pct
		if m.currentView == viewProjectInfo {
			help = "o/esc/q: back  ↑/↓/j/k scroll  c: claude  y/Y: yank  L: links  " + pct
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
		if m.confirmAction == "kill-run" {
			help = "press K again to stop the executor run  " + pct
		}
		sb.WriteString(helpStyle.Render(help))
	}
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
		{"v", "Select mode (multi-select)"},
		{"V", "Toggle columns"},
		{"i", "Project info"},
		{"c", "Claude Code"},
		{"X", "Run executor (pm work / run-epic)"},
		{"W", "Watch executor run (live agent-view)"},
		{"K", "Kill a live executor run (confirm)"},
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
		left := style.Width(leftW).Render(fmt.Sprintf("%s %-7s #%s", statusGlyph(k.Meta.Status), string(k.Meta.Status), k.Meta.ID))
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
	titleText := "Launch LLM"
	if m.projectScopeLaunch {
		titleText = "Launch LLM (project)"
	} else if m.launchAgent == launchAgentExecutor {
		titleText = "Run task (pm work)"
		if m.executorIsTracker {
			titleText = "Run epic (pm run-epic)"
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
	lines = append(lines, helpStyle.Render("press key or enter  @ toggle agent  ! toggle perms  esc back"))

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
	sb.WriteString(m.renderTabs(false))
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
			card := renderCard(t, cardWidth, isSelected, "", "")
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

func (m Model) viewFocus() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("pm focus"))
	sb.WriteString("\n")

	dateLabel := m.focusPlan.Date
	if dateLabel == "" {
		dateLabel = storage.Today()
	}
	sb.WriteString(helpStyle.Render("  " + dateLabel))
	sb.WriteString("\n\n")

	tasks := m.focusTasks()
	if len(tasks) == 0 {
		sb.WriteString(helpStyle.Render("  No focused tasks."))
		sb.WriteString("\n")
		sb.WriteString(helpStyle.Render("  Press t on any task in the board to add it."))
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
			isSelected := i == m.focusCursor
			var card string
			if m.zoomed {
				card = renderZoomCard(t, cardWidth, isSelected, "", "")
			} else {
				card = renderCard(t, cardWidth, isSelected, "", "")
			}
			sb.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render(card))
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\n")
	help := helpStyle.Render(fmt.Sprintf(
		"focus: %d  ↑/↓ navigate  t/x remove  C-j/C-k reorder  m move  d done  o detail  ; zoom  esc back",
		len(tasks)))
	sb.WriteString(help)

	return m.applyToast(sb.String())
}

func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func relativeTime(dateStr string) string {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return dateStr
	}
	days := int(time.Since(t).Hours() / 24)
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "1d ago"
	default:
		return fmt.Sprintf("%dd ago", days)
	}
}

func (m Model) viewProjectPicker() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Projects")
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})
	hiddenDimStyle := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#444444"})

	items := m.filteredPickerItems()

	const leftWidth = 40

	// --- LEFT PANEL: project list ---
	var leftLines []string
	leftLines = append(leftLines, title)
	leftLines = append(leftLines, "")

	lastWasVisible := true
	for i, item := range items {
		if lastWasVisible && item.hidden {
			leftLines = append(leftLines, "")
			leftLines = append(leftLines, dimStyle.Render("  -- hidden --"))
			lastWasVisible = false
		}

		prefix := "  "
		style := dimStyle
		if i == m.pickerCursor {
			prefix = "> "
			style = lipgloss.NewStyle().Bold(true).Foreground(special)
		} else if item.hidden {
			style = hiddenDimStyle
		}

		label := item.name
		count := fmt.Sprintf(" %d", item.taskCount)
		maxName := leftWidth - 4 - len(count) // prefix + count + margin
		if len(label) > maxName {
			label = label[:maxName-3] + "..."
		}

		line := style.Render(prefix+label) + dimStyle.Render(count)
		leftLines = append(leftLines, line)
		if !item.hidden {
			lastWasVisible = true
		}
	}

	if len(items) == 0 {
		leftLines = append(leftLines, dimStyle.Render("  no matches"))
	}

	// --- RIGHT PANEL: detail for cursor item ---
	var rightLines []string
	if m.pickerCursor < len(items) {
		item := items[m.pickerCursor]
		nameStyle := lipgloss.NewStyle().Bold(true).Foreground(highlight)
		labelStyle := lipgloss.NewStyle().Bold(true)

		// name + slug
		nameLabel := item.name
		if item.name != item.slug {
			nameLabel += dimStyle.Render(" (" + item.slug + ")")
		}
		rightLines = append(rightLines, nameStyle.Render(nameLabel))

		// stack
		if item.stack != "" {
			rightLines = append(rightLines, dimStyle.Render(item.stack))
		}

		// status counts
		if len(item.statusCounts) > 0 {
			rightLines = append(rightLines, "")
			var counts []string
			for _, s := range item.statuses {
				c := item.statusCounts[s]
				if c > 0 {
					counts = append(counts, fmt.Sprintf("%s %d", s, c))
				}
			}
			if len(counts) > 0 {
				rightLines = append(rightLines, strings.Join(counts, "  "))
			}
		} else {
			rightLines = append(rightLines, "")
			rightLines = append(rightLines, dimStyle.Render("No tasks"))
		}

		// path + repo
		if item.path != "" || item.repo != "" {
			rightLines = append(rightLines, "")
			if item.path != "" {
				rightLines = append(rightLines, labelStyle.Render("Path: ")+dimStyle.Render(shortenPath(item.path)))
			}
			if item.repo != "" {
				rightLines = append(rightLines, labelStyle.Render("Repo: ")+dimStyle.Render(item.repo))
			}
		}

		// links
		if len(item.links) > 0 {
			rightLines = append(rightLines, "")
			var names []string
			for k := range item.links {
				names = append(names, k)
			}
			sort.Strings(names)
			rightLines = append(rightLines, labelStyle.Render("Links: ")+dimStyle.Render(strings.Join(names, ", ")))
		}

		// tags
		if len(item.tags) > 0 {
			rightLines = append(rightLines, labelStyle.Render("Tags: ")+dimStyle.Render(strings.Join(item.tags, ", ")))
		}

		// last updated
		if item.lastUpdated != "" {
			rightLines = append(rightLines, "")
			rightLines = append(rightLines, dimStyle.Render("Updated: "+relativeTime(item.lastUpdated)))
		}

		// hidden indicator
		if item.hidden {
			rightLines = append(rightLines, "")
			rightLines = append(rightLines, hiddenDimStyle.Render("(hidden from tab bar)"))
		}
	}

	// --- COMBINE PANELS ---
	// fixed content height (excluding footer)
	const panelHeight = 20
	maxH := panelHeight
	// truncate if too many lines
	if len(leftLines) > maxH {
		leftLines = leftLines[:maxH]
	}
	if len(rightLines) > maxH {
		rightLines = rightLines[:maxH]
	}
	// pad to fixed height
	for len(leftLines) < maxH {
		leftLines = append(leftLines, "")
	}
	for len(rightLines) < maxH {
		rightLines = append(rightLines, "")
	}

	// build separator column
	sepLines := make([]string, maxH)
	for i := range sepLines {
		sepLines[i] = dimStyle.Render(" │ ")
	}

	// pad both columns to fixed width
	const rightWidth = 40
	for i, l := range leftLines {
		plain := stripANSI(l)
		pad := leftWidth - len(plain)
		if pad > 0 {
			leftLines[i] = l + strings.Repeat(" ", pad)
		}
	}
	for i, l := range rightLines {
		plain := stripANSI(l)
		if len(plain) > rightWidth {
			// truncate long lines
			rightLines[i] = l[:rightWidth-3] + "..."
		} else {
			pad := rightWidth - len(plain)
			if pad > 0 {
				rightLines[i] = l + strings.Repeat(" ", pad)
			}
		}
	}

	leftCol := strings.Join(leftLines, "\n")
	sepCol := strings.Join(sepLines, "\n")
	rightCol := strings.Join(rightLines, "\n")

	content := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, sepCol, rightCol)

	// footer
	var footer string
	if m.pickerInput.Focused() {
		footer = m.pickerInput.View()
	} else if m.pickerFilter != "" {
		footer = helpStyle.Render(fmt.Sprintf("filter: %q  / edit  esc close", m.pickerFilter))
	} else {
		footer = helpStyle.Render("enter switch  space hide  C-j/k reorder  / filter  y path  Y yank  c claude  i info  esc")
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(content + "\n\n" + footer)

	return m.applyToast(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box))
}

func (m Model) renderTabs(showCounts bool) string {
	visible := m.visibleProjects()
	var tabs []string
	for vi, p := range visible {
		name := p
		if p == "all" {
			name = "ALL"
		}
		var label string
		if showCounts {
			count := m.projectCounts[p]
			label = fmt.Sprintf("%s (%d)", name, count)
		} else {
			label = name
		}
		if vi < 9 {
			label = fmt.Sprintf("%d %s", vi+1, label)
		}
		isActive := false
		for i, mp := range m.projects {
			if mp == p && i == m.activeProject {
				isActive = true
				break
			}
		}
		if isActive {
			tabs = append(tabs, activeTabStyle.Render("["+label+"]"))
		} else {
			tabs = append(tabs, tabStyle.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

func (m Model) viewBoard() string {
	var sb strings.Builder

	badges := m.trackerBadges()

	// title
	sb.WriteString(titleStyle.Render("pm board"))
	sb.WriteString("\n")

	// project tabs with counts
	sb.WriteString(m.renderTabs(true))
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
			selectMark := ""
			if m.selecting {
				if m.selected[tasks[j].Meta.ID] {
					selectMark = "[x] "
				} else {
					selectMark = "[ ] "
				}
			} else if m.focusSet[tasks[j].Meta.ID] {
				selectMark = "● "
			}
			badge := badges[tasks[j].Meta.ID]
			if rb := m.runBadge(tasks[j].Meta.ID); rb != "" {
				if badge != "" {
					badge += " " + rb
				} else {
					badge = rb
				}
			}
			var card string
			if m.zoomed {
				card = renderZoomCard(tasks[j], colWidth-6, isSelected, selectMark, badge)
			} else {
				card = renderCard(tasks[j], colWidth-6, isSelected, selectMark, badge)
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
		case "delete-selected":
			prompt = fmt.Sprintf("  press x again to delete %d tasks", len(m.selected))
		case "kill-run":
			prompt = "  press K again to stop the executor run"
		}
		sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF6666"}).Render(prompt))
	}
	sb.WriteString("\n")

	// status bar
	statusBar := func() string {
		if m.selecting {
			selectStyle := lipgloss.NewStyle().Bold(true).Foreground(special)
			label := selectStyle.Render(fmt.Sprintf(" SELECT: %d selected ", len(m.selected)))
			help := helpStyle.Render("v toggle  m/M move  d done  w wait  A archive  x del  y yank  Y menu  esc")
			gap := m.width - lipgloss.Width(label) - lipgloss.Width(help)
			if gap < 1 {
				gap = 1
			}
			return label + strings.Repeat(" ", gap) + help
		}
		startup := fmt.Sprintf("%dms", m.startupDuration.Milliseconds())
		ver := helpStyle.Render("pm " + version.Version + " " + startup)
		help := helpStyle.Render("m/M move  d done  a add  c claude  X exec  W watch  e edit  y/Y yank  L links  i info  C-a archived")
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

// statusGlyph maps a task status to a compact glyph for detail/rollup display.
func statusGlyph(status storage.TaskStatus) string {
	switch status {
	case storage.StatusDone:
		return "✅"
	case "merged":
		return "🔀"
	case storage.StatusDoing:
		return "🔨"
	case storage.StatusWaiting:
		return "⏳"
	case storage.StatusArchived:
		return "🗄"
	default:
		return "○"
	}
}

// cardIDLine builds a card's secondary line: "#id" (or project), prefixed with
// "↳" when the task is a subtask (has a parent), and suffixed with a tracker
// progress badge when the task is a parent tracker.
func cardIDLine(t *storage.Task, badge string) string {
	id := t.Project
	if t.Meta.ID != "" {
		id = "#" + t.Meta.ID
	}
	if t.Meta.Parent != "" {
		id = "↳ " + id
	}
	if badge != "" {
		id += "  " + badge
	}
	return id
}

func renderCard(t *storage.Task, width int, selected bool, selectMark, badge string) string {
	style := cardStyle
	if selected {
		style = activeCardStyle
	}
	style = style.Width(width)

	var lines []string

	title := t.Meta.Title
	if selectMark != "" {
		title = selectMark + title
	}
	if len(title) > width-2 {
		title = title[:width-5] + "..."
	}
	lines = append(lines, cardTitleStyle.Render(title))
	lines = append(lines, cardProjectStyle.Render(cardIDLine(t, badge)))

	if len(t.Meta.Tags) > 0 {
		lines = append(lines, cardTagStyle.Render(strings.Join(t.Meta.Tags, ", ")))
	}

	return style.Render(strings.Join(lines, "\n"))
}

func renderZoomCard(t *storage.Task, width int, selected bool, selectMark, badge string) string {
	style := cardStyle
	if selected {
		style = activeCardStyle
	}
	style = style.Width(width)

	var lines []string

	// Title (full, no truncation - we have space)
	title := t.Meta.Title
	if selectMark != "" {
		title = selectMark + title
	}
	lines = append(lines, cardTitleStyle.Render(title))

	// ID (+ subtask marker / tracker badge) + updated date
	meta := cardIDLine(t, badge)
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
