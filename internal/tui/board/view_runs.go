package board

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mattn/go-runewidth"
)

// The Runs view's render: the `pm runs` table, plus a state line saying where
// the remote half stands and a footer with the keys.
//
// THE HEIGHT IS EXACT. Like viewBoard, this view must render precisely m.height
// visual lines - bubbletea cuts the TOP off anything taller, so an overshoot
// costs the title, not the last row. The budget is one constant (runsChromeLines)
// shared by the renderer and by fixRunsCursor's scrolling, so the two cannot
// disagree about how many rows fit.
//
// The cell TEXT comes from storage (RunCell.String / AcceptCell.String), never
// from a second formatter here: the words a user reads for a run's state must be
// the same in the table and on this screen.

// runsChromeLines is everything the rows do not get: the title, the state line,
// a blank, the column header (4) and the footer (1).
const runsChromeLines = 5

// Column budgets. Each is a clamp, not a fixed width: the columns size to their
// content so a narrow terminal spends its width on what is actually there, and
// TITLE - the one cell that is prose - absorbs whatever is left.
const (
	runsProjectMin, runsProjectMax = 7, 24
	runsTrackerMin, runsTrackerMax = 7, 18
	runsRunMin, runsRunMax         = 3, 14
	runsAcceptMin, runsAcceptMax   = 10, 38
	runsTitleMin                   = 8
	// runsGaps is the leading indent (2) plus the four two-space column gaps.
	runsGaps = 2 + 4*2
)

// runsRowBudget is how many lines the row area gets.
func (m Model) runsRowBudget() int {
	b := m.height - runsChromeLines
	if b < 1 {
		b = 1
	}
	return b
}

func (m Model) viewRuns() string {
	budget := m.runsRowBudget()
	widths := runsColumnWidths(m.runsRows, m.width)

	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("pm runs"),
		m.runsStateLine(),
		"",
		m.runsHeaderLine(widths),
	}
	lines = append(lines, m.runsBodyLines(widths, budget)...)
	// Truncated BEFORE styling, like every other line here: cutting afterwards
	// slices an ANSI sequence. Without the clamp a narrow terminal drops the tail
	// of the key list with nothing to say it did - and the footer is where the
	// way out of this view is written.
	footer := helpStyle.Render(truncateWidth(
		"↑/↓ navigate · enter agent-view · F report · f fetch remote · r refresh · esc back", m.width))
	lines = append(lines, footer)

	// Exactness, enforced rather than assumed. Overshoot is only possible in a
	// terminal too short for the chrome itself, and there the ROWS give way -
	// the footer holds the keys and the fetch state, so it is the last thing to
	// drop.
	if h := m.height; h > 0 && len(lines) > h {
		keep := h - 1
		lines = append(lines[:keep:keep], footer)
	}
	return m.applyToast(strings.Join(lines, "\n"))
}

// runsBodyLines renders exactly budget lines of rows, scroll indicators and
// padding - the guarantee the height contract rests on.
func (m Model) runsBodyLines(w runsWidths, budget int) []string {
	var body []string
	pad := func() []string {
		for len(body) < budget {
			body = append(body, "")
		}
		return body[:budget]
	}

	if len(m.runsRows) == 0 {
		body = append(body,
			helpStyle.Render(truncateWidth("  No trackers found.", m.width)),
			helpStyle.Render(truncateWidth(
				"  A row appears once a project has a tracker (a task with subtasks).", m.width)),
		)
		return pad()
	}

	rows := m.runsRows
	start := m.runsScroll
	if start > len(rows)-1 {
		start = len(rows) - 1
	}
	if start < 0 {
		start = 0
	}
	avail := budget
	if start > 0 {
		body = append(body, helpStyle.Render(fmt.Sprintf("  ▲ %d more", start)))
		avail--
	}
	end := start + avail
	if end < len(rows) {
		// Reserve the "more below" line out of the same budget.
		avail--
		end = start + avail
	}
	if end > len(rows) {
		end = len(rows)
	}
	for i := start; i < end; i++ {
		body = append(body, m.runsRowLine(rows[i], i == m.runsCursor, w))
	}
	if end < len(rows) {
		body = append(body, helpStyle.Render(fmt.Sprintf("  ▼ %d more", len(rows)-end)))
	}
	return pad()
}

// runsStateLine says how many rows are on screen and where the remote half
// stands. The remote state is spelled out rather than left implicit: rows from a
// machine that was last asked twenty minutes ago are not wrong, but reading them
// as live would be.
func (m Model) runsStateLine() string {
	local, remote := 0, 0
	for _, r := range m.runsRows {
		if r.Remote == "" {
			local++
		} else {
			remote++
		}
	}
	var remoteState string
	switch {
	case m.runsFetching:
		remoteState = "remote: fetching…"
	case m.runsFetchErr != "":
		remoteState = "remote: fetch failed (f retries) - " + firstLine(m.runsFetchErr)
	case !m.runsFetchAt.IsZero():
		remoteState = fmt.Sprintf("remote: %d row(s), fetched %s ago (f refetch)",
			remote, shortDur(time.Since(m.runsFetchAt)))
	default:
		remoteState = "remote: not fetched (f)"
	}
	line := fmt.Sprintf("  %d local · %s", local, remoteState)
	return helpStyle.Render(truncateWidth(line, m.width))
}

// runsWidths is one row's column layout, in display cells.
type runsWidths struct {
	project, tracker, title, run, accept int
}

// runsColumnWidths sizes the columns to the rows actually being shown.
func runsColumnWidths(rows []storage.RunRow, total int) runsWidths {
	w := runsWidths{
		project: runsProjectMin,
		tracker: runsTrackerMin,
		run:     runsRunMin,
		accept:  runsAcceptMin,
	}
	grow := func(cur *int, s string, max int) {
		if n := runewidth.StringWidth(s); n > *cur {
			*cur = min(n, max)
		}
	}
	grow(&w.project, "PROJECT", runsProjectMax)
	grow(&w.tracker, "TRACKER", runsTrackerMax)
	grow(&w.run, "RUN", runsRunMax)
	grow(&w.accept, "ACCEPTANCE", runsAcceptMax)
	for _, r := range rows {
		grow(&w.project, runsProjectCell(r), runsProjectMax)
		grow(&w.tracker, r.Tracker, runsTrackerMax)
		grow(&w.run, r.Run.String(), runsRunMax)
		grow(&w.accept, r.Accept.String(), runsAcceptMax)
	}
	w.title = total - runsGaps - w.project - w.tracker - w.run - w.accept
	if w.title < runsTitleMin {
		w.title = runsTitleMin
	}
	return w
}

// runsProjectCell qualifies a remote project with its machine, the same way the
// CLI table does: the same tracker id can exist on two machines (a VPS batch
// gets ids out of that machine's pm), so a bare slug would make the two rows
// indistinguishable.
func runsProjectCell(r storage.RunRow) string {
	switch {
	case r.Remote != "" && r.Project != "":
		return r.Remote + "/" + r.Project
	case r.Remote != "":
		return r.Remote
	}
	return r.Project
}

func (m Model) runsHeaderLine(w runsWidths) string {
	line := "  " + strings.Join([]string{
		padCell("PROJECT", w.project),
		padCell("TRACKER", w.tracker),
		padCell("TITLE", w.title),
		padCell("RUN", w.run),
		"ACCEPTANCE",
	}, "  ")
	return lipgloss.NewStyle().Bold(true).Foreground(subtle).Render(truncateWidth(line, m.width))
}

// runsRowLine renders one row as a SINGLE line: the cells are assembled as plain
// text, truncated, and only then styled as a whole. Styling per cell and cutting
// afterwards slices through an ANSI sequence and bleeds escape codes across the
// rest of the table (the same trap viewProjectPicker documents).
func (m Model) runsRowLine(r storage.RunRow, selected bool, w runsWidths) string {
	marker := "  "
	if selected {
		marker = "▸ "
	}
	var line string
	if r.Note != "" {
		// A placeholder row for a machine that did not answer: the note takes the
		// title's place and the run/acceptance cells stay empty, because nothing
		// was learned about them.
		line = marker + strings.Join([]string{
			padCell(runsProjectCell(r), w.project),
			padCell("-", w.tracker),
			padCell(oneLine(r.Note), w.title),
			padCell("-", w.run),
			"-",
		}, "  ")
	} else {
		line = marker + strings.Join([]string{
			padCell(runsProjectCell(r), w.project),
			padCell(r.Tracker, w.tracker),
			padCell(oneLine(r.Title), w.title),
			padCell(r.Run.String(), w.run),
			oneLine(r.Accept.String()),
		}, "  ")
	}
	line = truncateWidth(line, m.width)
	switch {
	case selected:
		return lipgloss.NewStyle().Bold(true).Foreground(special).Render(line)
	case r.Note != "":
		return helpStyle.Render(line)
	}
	return line
}

// padCell truncates s to width display cells and pads it out to exactly that
// many, so the columns line up whatever is in them (Polish titles, emoji).
func padCell(s string, width int) string {
	if width < 1 {
		width = 1
	}
	s = truncateWidth(s, width)
	if pad := width - runewidth.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// oneLine flattens whatever a cell holds onto one line. Titles are single-line
// by construction, but a note is an ssh diagnostic relayed as JSON from another
// machine - and one stray newline in it would shift every row below it.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\n\r") {
		return s
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\r", "\n")), " ")
}
