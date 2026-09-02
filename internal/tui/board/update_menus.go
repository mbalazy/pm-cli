package board

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/common"
)

// updateHelp owns the keyboard while the help overlay is up (overlayLadder's
// last entry). helpSearch is a mode INSIDE the overlay - like detailSearching
// inside the detail view - not a ladder entry of its own.
func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.helpSearch {
		return m.updateHelpSearch(msg)
	}
	switch {
	case key.Matches(msg, common.Keys.Search):
		m.helpSearch = true
		m.helpInput.SetValue(m.helpFilter)
		m.helpInput.Focus()
		return m, m.helpInput.Cursor.BlinkCmd()
	case key.Matches(msg, common.Keys.Escape):
		if m.helpFilter != "" {
			m.helpFilter = ""
			return m, nil
		}
		m.showHelp = false
		return m, nil
	default:
		m.showHelp = false
		m.helpFilter = ""
		return m, nil
	}
}

func (m Model) updateHelpSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Enter):
		m.helpSearch = false
		m.helpFilter = m.helpInput.Value()
		m.helpInput.Blur()
	case key.Matches(msg, common.Keys.Escape):
		m.helpSearch = false
		m.helpFilter = ""
		m.helpInput.SetValue("")
		m.helpInput.Blur()
	default:
		var cmd tea.Cmd
		m.helpInput, cmd = m.helpInput.Update(msg)
		m.helpFilter = m.helpInput.Value()
		return m, cmd
	}
	return m, nil
}

func (m Model) updateAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape):
		m.adding = false
		m.addInput.Blur()
	case key.Matches(msg, common.Keys.Enter):
		val := strings.TrimSpace(m.addInput.Value())
		if m.addStep == 0 {
			if val == "" {
				return m, nil
			}
			m.addTitle = val
			m.addStep = 1
			m.addInput.Prompt = "ID (enter for auto): "
			m.addInput.SetValue("")
			return m, nil
		}
		// step 1: create task
		//
		// applyColumnVisibility can empty m.statuses (V menu) between opening
		// add mode and this Enter press - guarded here the same way as the
		// Add keybinding itself (update.go), which is the normal path but not
		// the only one this state can be reached from.
		if len(m.statuses) == 0 {
			m.adding = false
			m.addInput.Blur()
			m.toastMsg = "no visible columns to add to"
			m.toastExpiry = time.Now().Add(3 * time.Second)
			return m, nil
		}
		id := val
		if id == "" {
			id = fmt.Sprintf("%d", time.Now().Unix()%100000)
		}
		// Same rules as storage.NewTask, which this literal cannot use (it
		// carries the board's own id and column status): a task's status age
		// starts at creation, so one added here and left on todo reports a
		// real age instead of "unknown" until its first move - and both stamps
		// come from ONE clock read, so status_changed can never land a second
		// after updated.
		now := storage.Now()
		t := &storage.Task{
			Meta: storage.TaskMeta{
				ID:            id,
				Title:         m.addTitle,
				Status:        m.statuses[0],
				Created:       storage.Today(),
				Updated:       now,
				StatusChanged: now,
			},
		}
		slug := m.projects[m.activeProject]
		if err := m.store.AddTask(slug, t); err != nil {
			m.showErrorToast("add failed", err)
		}
		m.adding = false
		m.addInput.Blur()
		m.reload()
	default:
		var cmd tea.Cmd
		m.addInput, cmd = m.addInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateYankMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.yankMenu = false
	case key.Matches(msg, common.Keys.Up):
		if m.yankCursor > 0 {
			m.yankCursor--
		}
	case key.Matches(msg, common.Keys.Down):
		if m.yankCursor < len(m.yankItems)-1 {
			m.yankCursor++
		}
	case key.Matches(msg, common.Keys.Enter):
		if m.yankCursor < len(m.yankItems) {
			m.copyToClipboard(m.yankItems[m.yankCursor].value)
		}
		m.yankMenu = false
	}
	return m, nil
}

func (m Model) updateLinksMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.linksMenu = false
	case key.Matches(msg, common.Keys.Up):
		if m.linksCursor > 0 {
			m.linksCursor--
		}
	case key.Matches(msg, common.Keys.Down):
		if m.linksCursor < len(m.linkItems)-1 {
			m.linksCursor++
		}
	case key.Matches(msg, common.Keys.Enter):
		if m.linksCursor < len(m.linkItems) {
			// Start(), not Run(): Run() blocks the bubbletea event loop until
			// LaunchServices returns, which is a visible stall. A background
			// Wait() still reaps the child once it exits so it never lingers as
			// a zombie - Start() alone only avoids blocking, not reaping.
			c := exec.Command("open", m.linkItems[m.linksCursor].url)
			if err := c.Start(); err != nil {
				m.showErrorToast("open link failed", err)
			} else {
				go func() { _ = c.Wait() }()
			}
		}
		if len(m.linkItems) <= 1 {
			m.linksMenu = false
		}
	case msg.String() == "y":
		if m.linksCursor < len(m.linkItems) {
			m.copyToClipboard(m.linkItems[m.linksCursor].url)
			m.linksMenu = false
		}
	}
	return m, nil
}

func (m *Model) openSessionMenu(t *storage.Task) {
	m.sessionMenuItems = nil
	m.sessionCursor = 0

	// Resolve project dir + config dir for CC metadata lookup
	var projDir string
	if proj, err := m.store.GetProject(t.Project); err == nil && proj.Path != "" {
		projDir = proj.Path
	}
	configDir := m.claudeConfigDir(t.Project)

	enrichItem := func(sid string, isLatest bool) sessionMenuItem {
		item := sessionMenuItem{sessionID: sid, isLatest: isLatest}
		if meta := loadSessionMeta(projDir, sid, configDir); meta != nil {
			item.summary = meta.Summary
			item.msgCount = meta.MessageCount
			item.branch = meta.GitBranch
			if meta.Modified != "" {
				if t, err := time.Parse(time.RFC3339, meta.Modified); err == nil {
					item.modified = t.Local().Format("Jan 2, 15:04")
				}
			}
		}
		return item
	}

	// Migrate legacy cc-session if present
	if old := t.Meta.Links["cc-session"]; old != "" {
		found := false
		for _, s := range t.Meta.Sessions {
			if s == old {
				found = true
				break
			}
		}
		if !found {
			m.sessionMenuItems = append(m.sessionMenuItems, enrichItem(old, false))
		}
	}

	// Add sessions in reverse order (latest first)
	for i := len(t.Meta.Sessions) - 1; i >= 0; i-- {
		sid := t.Meta.Sessions[i]
		isLatest := i == len(t.Meta.Sessions)-1
		m.sessionMenuItems = append(m.sessionMenuItems, enrichItem(sid, isLatest))
	}

	if len(m.sessionMenuItems) > 0 {
		m.sessionMenu = true
	}
}

func (m Model) updateSessionMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.sessionMenu = false
		m.confirmAction = ""
	case key.Matches(msg, common.Keys.Up):
		if m.sessionCursor > 0 {
			m.sessionCursor--
			m.confirmAction = ""
		}
	case key.Matches(msg, common.Keys.Down):
		if m.sessionCursor < len(m.sessionMenuItems)-1 {
			m.sessionCursor++
			m.confirmAction = ""
		}
	// NOTE: every task lookup below MUST be menuTask(), not selectedTask().
	// The session menu opens from the DETAIL view on m.detailTask, which after
	// a relation jump (p: subtask <-> parent) or a stale board cursor is a
	// different task than the one under the board cursor - selectedTask() here
	// resumed/yanked/DELETED sessions on that other task.
	case key.Matches(msg, common.Keys.Enter), msg.String() == "r":
		if m.sessionCursor < len(m.sessionMenuItems) {
			m.resumeSessionID = m.sessionMenuItems[m.sessionCursor].sessionID
			m.sessionMenu = false
			m.resumeOnly = true
			m.forkMode = false
			t := m.menuTask()
			if t != nil {
				m.openClaudeMenu(t)
			}
			return m, nil
		}
	case msg.String() == "f":
		if m.sessionCursor < len(m.sessionMenuItems) {
			m.resumeSessionID = m.sessionMenuItems[m.sessionCursor].sessionID
			m.sessionMenu = false
			m.resumeOnly = true
			m.forkMode = true
			t := m.menuTask()
			if t != nil {
				m.openClaudeMenu(t)
			}
			return m, nil
		}
	case msg.String() == "y":
		if m.sessionCursor < len(m.sessionMenuItems) {
			sid := m.sessionMenuItems[m.sessionCursor].sessionID
			m.yankItems = nil
			m.yankCursor = 0
			m.yankItems = append(m.yankItems, yankItem{"id", sid})
			t := m.menuTask()
			if t != nil {
				var projDir string
				if proj, err := m.store.GetProject(t.Project); err == nil && proj.Path != "" {
					projDir = proj.Path
				}
				if p := resolveSessionPath(projDir, sid, m.claudeConfigDir(t.Project)); p != "" {
					m.yankItems = append(m.yankItems, yankItem{"path", p})
				}
			}
			m.yankMenu = true
		}
	case msg.String() == "x":
		if m.sessionCursor >= len(m.sessionMenuItems) {
			break
		}
		if m.confirmAction == "delete-session" {
			// Second press - confirmed
			m.confirmAction = ""
			t := m.menuTask()
			if t == nil {
				break
			}
			sid := m.sessionMenuItems[m.sessionCursor].sessionID
			newSessions := make([]string, 0, len(t.Meta.Sessions))
			for _, s := range t.Meta.Sessions {
				if s != sid {
					newSessions = append(newSessions, s)
				}
			}
			t.Meta.Sessions = newSessions
			if t.Meta.Links["cc-session"] == sid {
				delete(t.Meta.Links, "cc-session")
				if len(t.Meta.Links) == 0 {
					t.Meta.Links = nil
				}
			}
			t.Meta.Updated = storage.Now()
			if err := m.store.WriteTask(t); err != nil {
				m.showErrorToast("delete session failed", err)
			} else {
				m.toastMsg = "Deleted session " + sid[:8] + "..."
				m.toastExpiry = time.Now().Add(2 * time.Second)
			}
			m.openSessionMenu(t)
			if len(m.sessionMenuItems) == 0 {
				m.sessionMenu = false
			} else if m.sessionCursor >= len(m.sessionMenuItems) {
				m.sessionCursor = len(m.sessionMenuItems) - 1
			}
			content := m.renderTaskDetail(t)
			m.detailViewport.SetContent(content)
			m.detailPlainContent = stripANSI(content)
		} else {
			// First press - ask for confirmation
			m.confirmAction = "delete-session"
		}
	}
	return m, nil
}

func (m Model) updateColVisMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left), key.Matches(msg, common.Keys.ColumnVis):
		m.colVisMenu = false
		m.reload()
	case key.Matches(msg, common.Keys.Up):
		if m.colVisCursor > 0 {
			m.colVisCursor--
		}
	case key.Matches(msg, common.Keys.Down):
		if m.colVisCursor < len(m.colVisItems)-1 {
			m.colVisCursor++
		}
	case key.Matches(msg, common.Keys.Space):
		if m.colVisCursor < len(m.colVisItems) {
			item := &m.colVisItems[m.colVisCursor]
			item.visible = !item.visible
			// Mark as explicitly set by user
			m.hiddenStatuses[item.status] = !item.visible
		}
	}
	return m, nil
}

func (m *Model) copyToClipboard(value string) {
	c := exec.Command("pbcopy")
	c.Stdin = strings.NewReader(value)
	if err := c.Run(); err != nil {
		m.showErrorToast("copy failed", err)
		return
	}
	display := truncateWidth(value, 40)
	m.toastMsg = "Copied: " + display
	m.toastExpiry = time.Now().Add(2 * time.Second)
}
