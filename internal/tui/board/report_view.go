package board

import (
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/common"
)

// The acceptance report: the markdown a `pm finish` run writes beside its
// run-state, and until now the one artifact of the whole acceptance that the
// board could not reach. What was left was the COUNT - "3 visual claim(s) open"
// - with no way to ask which three, short of knowing the path by heart.
//
// Two ways in, on purpose: F renders it here (no context switch, and it stays
// available while a run is live), e inside hands it to $EDITOR (search, yank,
// and a window that outlives the board).

// openFinishReport opens the acceptance report of taskID in project proj.
// Returns false - with a toast already set saying why - when there is nothing
// to show, which is the common case: a tracker that was never accepted has no
// report, and that is not an error.
func (m *Model) openFinishReport(proj, taskID string) bool {
	if m.store == nil || taskID == "" {
		return false
	}
	path := storage.FinishReportPath(m.store.ProjectDir(proj), taskID)
	body, err := os.ReadFile(path)
	if err != nil {
		// A missing report and an unreadable one are different sentences: the
		// first is the ordinary state of a tracker nobody has accepted yet, the
		// second is something to go and look at.
		if os.IsNotExist(err) {
			m.toastMsg = "no acceptance report for " + taskID + " yet (X→a runs one)"
			m.toastExpiry = time.Now().Add(4 * time.Second)
		} else {
			m.showErrorToast("acceptance report", err)
		}
		return false
	}
	m.reportPrevView = m.currentView
	m.currentView = viewReport
	m.reportPath = path
	m.reportTaskID = taskID
	m.reportViewport = viewport.New(m.width, reportBodyHeight(m.height))
	m.setReportContent(body)
	return true
}

// reloadReport re-reads the file under the open view. The report is rewritten
// whenever the tracker is accepted again, and an acceptance can be running
// while this is on screen.
func (m *Model) reloadReport() {
	body, err := os.ReadFile(m.reportPath)
	if err != nil {
		m.showErrorToast("acceptance report", err)
		return
	}
	off := m.reportViewport.YOffset
	m.setReportContent(body)
	m.reportViewport.SetYOffset(off)
}

// setReportContent renders the markdown into the viewport. An empty report is
// given a sentence of its own: a blank pager is indistinguishable from a
// broken one.
func (m *Model) setReportContent(body []byte) {
	if strings.TrimSpace(string(body)) == "" {
		m.reportViewport.SetContent(helpStyle.Render("\n  The report file is empty."))
		return
	}
	m.reportViewport.SetContent(glamourRender(string(body), reportContentWidth(m.width)))
}

// reportContentWidth caps the wrap at the same 100 columns the detail view and
// the project info use, so prose does not stretch across a wide terminal.
func reportContentWidth(w int) int {
	cw := w - 4
	if cw > 100 {
		cw = 100
	}
	if cw < 20 {
		cw = 20
	}
	return cw
}

func reportBodyHeight(h int) int {
	// header (2) + footer (1)
	body := h - 3
	if body < 3 {
		body = 3
	}
	return body
}

func (m Model) updateReportView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit),
		key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.FinishReport):
		m.currentView = m.reportPrevView
		return m, nil

	case key.Matches(msg, common.Keys.Edit):
		// The second way in, and the reason the first one does not have to grow
		// search, yank and a window of its own.
		return m, openEditor(m.reportPath)

	case key.Matches(msg, common.Keys.Restore): // r
		m.reloadReport()
		return m, nil

	default:
		var cmd tea.Cmd
		m.reportViewport, cmd = m.reportViewport.Update(msg)
		return m, cmd
	}
}

func (m Model) viewReport() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Acceptance report · " + m.reportTaskID)
	// Clamped: an unclamped path is longer than most terminals are wide, and a
	// wrapped second line would put this view one line over its height budget.
	header := title + "\n" + helpStyle.Render(truncateWidth("  "+shortenPath(m.reportPath), max(10, m.width)))
	footer := helpStyle.Render("e: $EDITOR · j/k C-d/C-u scroll · r reload · esc back")
	return m.applyToast(strings.Join([]string{header, m.reportViewport.View(), footer}, "\n"))
}
