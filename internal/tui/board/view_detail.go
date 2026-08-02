package board

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mbalazy/pm/internal/storage"
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
		out.WriteString(renderSubtaskTable(kids, contentWidth, m.landing))
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

// glamourRenderers caches one dark-style TermRenderer per wrap width: building
// one costs ~864us, and renderTaskDetail calls glamourRender twice per render -
// every tick while a live run sits in the detail view, and on every n/N search
// step. The TUI's Update/View run on a single goroutine, so no lock is needed.
var glamourRenderers = map[int]*glamour.TermRenderer{}

// glamourRender renders markdown to ANSI at the given wrap width, falling back
// to the raw markdown on error.
func glamourRender(md string, contentWidth int) string {
	r, ok := glamourRenderers[contentWidth]
	if !ok {
		var err error
		r, err = glamour.NewTermRenderer(
			glamour.WithStylePath("dark"),
			glamour.WithWordWrap(contentWidth),
		)
		if err != nil {
			return md
		}
		glamourRenderers[contentWidth] = r
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return out
}

// renderSubtaskTable renders a tracker's children as a bordered table with a
// horizontal rule between every row and titles wrapped to at most two lines.
// landing = the project's executor landing statuses; a child sitting on one of
// them counts towards the header's N/M, same rule as the card badge.
func renderSubtaskTable(kids []*storage.Task, width int, landing []storage.TaskStatus) string {
	done := 0
	for _, k := range kids {
		if k.Meta.Status == storage.StatusDone || slices.Contains(landing, k.Meta.Status) {
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
			statusGlyphAligned(k.Meta.Status)+" "+string(k.Meta.Status),
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

// viewDetail renders the task-detail / project-info body. Overlays opened on
// top of it (subtask picker, yank, links, session menu) are resolved by
// overlayLadder before View ever gets here - see overlay.go.
func (m Model) viewDetail() string {
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
