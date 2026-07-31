package board

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/version"
)

func (m Model) View() string {
	// Overlay menus take priority over all views - which overlay wins is
	// decided in ONE place (overlayLadder), shared with Update's dispatch.
	if ov := m.activeOverlay(); ov != nil {
		return ov.view(m)
	}
	switch m.currentView {
	case viewDetail, viewProjectInfo:
		return m.viewDetail()
	case viewArchive:
		return m.viewArchive()
	case viewFocus:
		return m.viewFocus()
	case viewExecutor:
		return m.viewExecutor()
	}
	return m.viewBoard()
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

// statusGlyphAligned is statusGlyph padded to a fixed two display columns, for
// the places that lay the glyph out in a column (the subtask picker's aligned
// rows, the tracker rollup table). statusGlyph itself stays unpadded because
// its third caller puts it in running text (the detail view's "Parent:"
// header), where a trailing space just reads as a typo.
//
// The width is MEASURED, not assumed, so swapping a glyph cannot silently
// reintroduce ragged columns: the emoji (✅🔀🔨⏳) are East-Asian Wide at 2
// columns, while both "○" and the archived "🗄" are 1 - the second of those is
// why this is a width check rather than a special case for the default branch.
func statusGlyphAligned(status storage.TaskStatus) string {
	g := statusGlyph(status)
	if w := lipgloss.Width(g); w < 2 {
		return g + strings.Repeat(" ", 2-w)
	}
	return g
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
	title = truncateWidth(title, width-2)
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
		brief := truncateWidth(t.Meta.Brief, width*2) // ~2 lines worth
		lines = append(lines, "")
		lines = append(lines, helpStyle.Render(brief))
	}

	return style.Render(strings.Join(lines, "\n"))
}
