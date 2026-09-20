package board

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/mbalazy/pm-cli/internal/tui/common"
)

// updateDetail owns the keyboard in the task-detail / project-info view.
// Overlays opened from here (subtask picker, launch menu, yank, links, session
// menu) intercept input before Update ever dispatches here - the precedence
// lives in overlayLadder (overlay.go), not in this function.
func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Detail search input mode
	if m.detailSearching {
		return m.updateDetailSearch(msg)
	}

	// Project info view: close, scroll, yank, links
	if m.currentView == viewProjectInfo {
		switch {
		case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
			m.currentView = m.previousView
			m.reload()
			return m, nil
		case key.Matches(msg, common.Keys.Yank):
			if m.infoSlug != "" {
				m.copyToClipboard(m.infoSlug)
			}
			return m, nil
		case key.Matches(msg, common.Keys.YankMenu):
			if m.infoProject != nil {
				m.yankItems = nil
				m.yankCursor = 0
				m.yankItems = append(m.yankItems, yankItem{"slug", m.infoSlug})
				if m.infoProject.Path != "" {
					m.yankItems = append(m.yankItems, yankItem{"path", m.infoProject.Path})
				}
				if m.infoProject.Repo != "" {
					m.yankItems = append(m.yankItems, yankItem{"repo", m.infoProject.Repo})
				}
				for name, url := range m.infoProject.Links {
					m.yankItems = append(m.yankItems, yankItem{"link: " + name, url})
				}
				if len(m.yankItems) > 0 {
					m.yankMenu = true
				}
			}
			return m, nil
		case key.Matches(msg, common.Keys.Links):
			if m.infoProject != nil && len(m.infoProject.Links) > 0 {
				m.linkItems = nil
				m.linksCursor = 0
				for name, url := range m.infoProject.Links {
					m.linkItems = append(m.linkItems, linkItem{name, url})
				}
				m.linksMenu = true
			}
			return m, nil
		case key.Matches(msg, common.Keys.Claude):
			if m.infoSlug != "" {
				m.openProjectClaudeMenu(m.infoSlug)
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.detailViewport, cmd = m.detailViewport.Update(msg)
		return m, cmd
	}

	t := m.detailTask
	switch {
	case key.Matches(msg, common.Keys.Escape):
		if m.detailSearchQuery != "" {
			m.detailSearchQuery = ""
			m.detailSearchMatches = nil
			// Restore original content (remove highlights)
			if t != nil {
				content := m.renderTaskDetail(t)
				m.detailViewport.SetContent(content)
			}
			return m, nil
		}
		m.currentView = m.previousView
		m.reload()
		return m, nil

	case key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		m.currentView = m.previousView
		m.reload()
		return m, nil

	case key.Matches(msg, common.Keys.Search):
		m.detailSearching = true
		m.detailSearchInput.SetValue(m.detailSearchQuery)
		m.detailSearchInput.Focus()
		return m, m.detailSearchInput.Cursor.BlinkCmd()

	case msg.String() == "n":
		if len(m.detailSearchMatches) > 0 {
			m.detailSearchIdx = (m.detailSearchIdx + 1) % len(m.detailSearchMatches)
			m.applySearchHighlights()
			m.detailViewport.SetYOffset(m.detailSearchMatches[m.detailSearchIdx])
		}
		return m, nil

	case msg.String() == "N":
		if len(m.detailSearchMatches) > 0 {
			m.detailSearchIdx = (m.detailSearchIdx - 1 + len(m.detailSearchMatches)) % len(m.detailSearchMatches)
			m.applySearchHighlights()
			m.detailViewport.SetYOffset(m.detailSearchMatches[m.detailSearchIdx])
		}
		return m, nil

	case t != nil && msg.String() == "p":
		// context-aware relation jump: subtask -> parent; tracker -> subtask picker
		if parent := m.taskParent(t); parent != nil {
			m.openDetailTask(parent)
		} else if kids := m.taskChildren(t); len(kids) > 0 {
			m.openSubtaskPicker(kids)
		}
		return m, nil

	case key.Matches(msg, common.Keys.Yank):
		if t != nil && t.Meta.ID != "" {
			m.copyToClipboard(t.Meta.ID)
		}

	case key.Matches(msg, common.Keys.YankMenu):
		if t != nil {
			m.yankItems = nil
			m.yankCursor = 0
			if t.Meta.Branch != "" {
				m.yankItems = append(m.yankItems, yankItem{"branch", t.Meta.Branch})
			}
			m.yankItems = append(m.yankItems, yankItem{"id", t.Meta.ID})
			m.yankItems = append(m.yankItems, yankItem{"title", t.Meta.Title})
			m.yankItems = append(m.yankItems, yankItem{"path", t.FilePath})
			if sid := lastSession(t); sid != "" {
				m.yankItems = append(m.yankItems, yankItem{"session", sid})
			}
			if t.Meta.Brief != "" {
				m.yankItems = append(m.yankItems, yankItem{"brief", t.Meta.Brief})
			}
			if t.Meta.AC != "" {
				m.yankItems = append(m.yankItems, yankItem{"ac", t.Meta.AC})
			}
			if t.Body != "" {
				m.yankItems = append(m.yankItems, yankItem{"body", t.Body})
			}
			for name, url := range t.Meta.Links {
				m.yankItems = append(m.yankItems, yankItem{"link: " + name, url})
			}
			if len(m.yankItems) > 0 {
				m.yankMenu = true
			}
		}

	case key.Matches(msg, common.Keys.Edit):
		if t != nil {
			return m, openEditor(t.FilePath)
		}

	case key.Matches(msg, common.Keys.Links):
		if t != nil && len(t.Meta.Links) > 0 {
			m.linkItems = nil
			m.linksCursor = 0
			for name, url := range t.Meta.Links {
				m.linkItems = append(m.linkItems, linkItem{name, url})
			}
			m.linksMenu = true
		}

	case key.Matches(msg, common.Keys.Move):
		if t != nil {
			m.doMoveForward(t)
			m.currentView = m.previousView
			return m, nil
		}

	case key.Matches(msg, common.Keys.MoveBack):
		if t != nil {
			m.doMoveBack(t)
			m.currentView = m.previousView
			return m, nil
		}

	case key.Matches(msg, common.Keys.Done):
		if t != nil {
			if m.confirmAction == "done" && m.confirmTaskID == t.Meta.ID {
				m.doDone(t)
				m.confirmAction = ""
				m.confirmTaskID = ""
				m.currentView = m.previousView
				return m, nil
			}
			m.confirmAction = "done"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Waiting):
		if t != nil {
			if m.confirmAction == "waiting" && m.confirmTaskID == t.Meta.ID {
				m.doWaiting(t)
				m.confirmAction = ""
				m.confirmTaskID = ""
				m.currentView = m.previousView
				return m, nil
			}
			m.confirmAction = "waiting"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Archive):
		if t != nil {
			if m.confirmAction == "archive" && m.confirmTaskID == t.Meta.ID {
				m.doArchive(t)
				m.confirmAction = ""
				m.confirmTaskID = ""
				m.currentView = m.previousView
				return m, nil
			}
			m.confirmAction = "archive"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Claude):
		if t != nil {
			m.openClaudeMenu(t)
		}

	case key.Matches(msg, common.Keys.Executor):
		if t != nil {
			m.openExecutorMenu(t)
		}

	case key.Matches(msg, common.Keys.WatchExecutor):
		if t != nil && !m.openExecutorView(t) {
			m.toastMsg = "no executor run for this task (launch with x)"
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}

	case key.Matches(msg, common.Keys.FinishReport):
		// The report of THIS task, never of the tracker above it: a sub has no
		// acceptance of its own, so F on one says there is no report rather
		// than opening a document about a different unit of work.
		if t != nil {
			m.openFinishReport(t.Project, t.Meta.ID)
		}

	case key.Matches(msg, common.Keys.KillRun):
		if t != nil {
			st, finish := m.pickRunForTask(t)
			if st == nil || !st.IsLive() {
				m.toastMsg = "no live executor run to stop"
				m.toastExpiry = time.Now().Add(3 * time.Second)
				break
			}
			// Keyed on the kind too - see the same branch in update.go.
			if m.confirmAction == "kill-run" && m.confirmTaskID == st.TaskID && m.confirmRunFinish == finish {
				m.confirmAction = ""
				m.confirmTaskID = ""
				return m, m.killRun(st)
			}
			m.confirmAction = "kill-run"
			m.confirmTaskID = st.TaskID
			m.confirmRunFinish = finish
		}

	case msg.String() == "s":
		if t != nil {
			m.openSessionMenu(t)
		}

	case msg.String() == "r":
		if t != nil {
			// reload the whole task set first so the subtask table (rendered from
			// m.tasks via taskChildren) reflects fresh child statuses, not just the
			// parent's own fields.
			m.reload()
			reloaded, err := m.store.FindTask(t.Project, t.Meta.ID)
			if err == nil {
				off := m.detailViewport.YOffset
				m.detailTask = reloaded
				content := m.renderTaskDetail(reloaded)
				m.detailViewport.SetContent(content)
				m.detailPlainContent = stripANSI(content)
				m.detailViewport.SetYOffset(off)
				m.toastMsg = "Refreshed"
				m.toastExpiry = time.Now().Add(2 * time.Second)
			}
		}

	default:
		var cmd tea.Cmd
		m.detailViewport, cmd = m.detailViewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateDetailSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Enter):
		m.detailSearching = false
		m.detailSearchQuery = m.detailSearchInput.Value()
		m.detailSearchInput.Blur()
		m.computeDetailSearchMatches()
		if len(m.detailSearchMatches) > 0 {
			m.detailSearchIdx = 0
			m.detailViewport.SetYOffset(m.detailSearchMatches[0])
		}
	case key.Matches(msg, common.Keys.Escape):
		m.detailSearching = false
		m.detailSearchQuery = ""
		m.detailSearchMatches = nil
		m.detailSearchInput.SetValue("")
		m.detailSearchInput.Blur()
		// Restore original content (remove highlights)
		if m.detailTask != nil {
			content := m.renderTaskDetail(m.detailTask)
			m.detailViewport.SetContent(content)
		}
	default:
		var cmd tea.Cmd
		m.detailSearchInput, cmd = m.detailSearchInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) computeDetailSearchMatches() {
	m.detailSearchMatches = nil
	m.detailSearchIdx = 0
	q := strings.ToLower(m.detailSearchQuery)
	if q == "" {
		// Restore original content (remove highlights)
		if m.detailTask != nil {
			content := m.renderTaskDetail(m.detailTask)
			m.detailViewport.SetContent(content)
		}
		return
	}
	plainLines := strings.Split(m.detailPlainContent, "\n")
	for i, line := range plainLines {
		if strings.Contains(strings.ToLower(line), q) {
			m.detailSearchMatches = append(m.detailSearchMatches, i)
		}
	}

	// Apply highlighting to rendered content
	m.applySearchHighlights()
}

// applySearchHighlights re-renders the viewport content with search highlights.
// The active match line gets a distinct color (cyan), others get yellow.
func (m *Model) applySearchHighlights() {
	if m.detailTask == nil || m.detailSearchQuery == "" {
		return
	}
	original := m.renderTaskDetail(m.detailTask)
	renderedLines := strings.Split(original, "\n")
	q := strings.ToLower(m.detailSearchQuery)

	// Determine which line is the active match
	activeLine := -1
	if len(m.detailSearchMatches) > 0 && m.detailSearchIdx < len(m.detailSearchMatches) {
		activeLine = m.detailSearchMatches[m.detailSearchIdx]
	}

	var highlighted []string
	for i, line := range renderedLines {
		highlighted = append(highlighted, highlightSearchTerm(line, q, i == activeLine))
	}
	m.detailViewport.SetContent(strings.Join(highlighted, "\n"))
}

// highlightSearchTerm highlights occurrences of query in a line that may contain ANSI codes.
// active=true uses cyan highlight for the current match, false uses yellow for others.
func highlightSearchTerm(line, query string, active bool) string {
	plain := stripANSI(line)
	lowerPlain := strings.ToLower(plain)
	if !strings.Contains(lowerPlain, query) {
		return line
	}

	// Build a mapping from plain-text index to original-string index
	plainToOrig := make([]int, len(plain))
	pi := 0
	for i := 0; i < len(line); {
		if line[i] == '\x1b' && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && !((line[j] >= 'A' && line[j] <= 'Z') || (line[j] >= 'a' && line[j] <= 'z')) {
				j++
			}
			if j < len(line) {
				j++
			}
			i = j
			continue
		}
		if pi < len(plain) {
			plainToOrig[pi] = i
			pi++
		}
		i++
	}

	// Find all match positions in plain text
	type match struct{ start, end int }
	var matches []match
	pos := 0
	for {
		idx := strings.Index(lowerPlain[pos:], query)
		if idx < 0 {
			break
		}
		start := pos + idx
		matches = append(matches, match{start, start + len(query)})
		pos = start + len(query)
	}
	if len(matches) == 0 {
		return line
	}

	hlOn := "\x1b[30;43m" // black on yellow (inactive)
	if active {
		hlOn = "\x1b[30;46m" // black on cyan (active)
	}
	hlOff := "\x1b[0m"
	var result strings.Builder
	lastOrig := 0
	for _, m := range matches {
		origStart := plainToOrig[m.start]
		origEnd := len(line)
		if m.end < len(plainToOrig) {
			origEnd = plainToOrig[m.end]
		}
		result.WriteString(line[lastOrig:origStart])
		result.WriteString(hlOn)
		result.WriteString(line[origStart:origEnd])
		result.WriteString(hlOff)
		lastOrig = origEnd
	}
	result.WriteString(line[lastOrig:])
	return result.String()
}

// openDetailTask loads a task into the detail viewport (used by Enter and by
// parent<->subtask navigation). Leaves previousView untouched so Esc returns to
// wherever the detail flow started.
func (m *Model) openDetailTask(t *storage.Task) {
	m.detailTask = t
	m.detailViewport = viewport.New(m.width, m.height-2)
	content := m.renderTaskDetail(t)
	m.detailViewport.SetContent(content)
	m.detailPlainContent = stripANSI(content)
	m.detailSearchQuery = ""
	m.detailSearchMatches = nil
	m.detailSearchIdx = 0
	m.detailSearching = false
}
