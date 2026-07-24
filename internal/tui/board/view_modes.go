package board

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/mbalazy/pm/internal/storage"
)

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
		prompt := "  press x again to delete"
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
