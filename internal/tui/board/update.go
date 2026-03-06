package board

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/common"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.detailViewport.Width = msg.Width
		m.detailViewport.Height = msg.Height - 2
		return m, nil

	case tickMsg:
		m.reload()
		if m.currentView == viewArchive {
			m.fixArchiveCursor()
		}
		return m, doTick()

	case reloadMsg:
		m.reload()
		return m, doTick()

	case tea.KeyMsg:
		if m.currentView == viewDetail || m.currentView == viewProjectInfo {
			return m.updateDetail(msg)
		}
		if m.colVisMenu {
			return m.updateColVisMenu(msg)
		}
		if m.yankMenu {
			return m.updateYankMenu(msg)
		}
		if m.linksMenu {
			return m.updateLinksMenu(msg)
		}
		if m.claudeMenu {
			return m.updateClaudeMenu(msg)
		}
		if m.currentView == viewArchive {
			return m.updateArchive(msg)
		}
		if m.showHelp {
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
		if m.adding {
			return m.updateAdd(msg)
		}
		if m.searching {
			return m.updateSearch(msg)
		}
		return m.updateBoard(msg)
	}
	return m, nil
}

func (m Model) updateBoard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Confirmation pattern: any key other than the confirming key resets
	if m.confirmAction != "" {
		isConfirmKey := false
		switch m.confirmAction {
		case "done":
			isConfirmKey = key.Matches(msg, common.Keys.Done)
		case "waiting":
			isConfirmKey = key.Matches(msg, common.Keys.Waiting)
		case "delete":
			isConfirmKey = key.Matches(msg, common.Keys.Delete)
		case "archive":
			isConfirmKey = key.Matches(msg, common.Keys.Archive)
		case "quit":
			isConfirmKey = key.Matches(msg, common.Keys.Quit)
		}
		if !isConfirmKey {
			m.confirmAction = ""
			m.confirmTaskID = ""
			// fall through to normal handling
		}
	}

	switch {
	case key.Matches(msg, common.Keys.Quit):
		if m.confirmAction == "quit" {
			return m, tea.Quit
		}
		m.confirmAction = "quit"

	case key.Matches(msg, common.Keys.Left):
		if len(m.statuses) > 0 {
			m.activeCol = (m.activeCol - 1 + len(m.statuses)) % len(m.statuses)
		}

	case key.Matches(msg, common.Keys.Right):
		if len(m.statuses) > 0 {
			m.activeCol = (m.activeCol + 1) % len(m.statuses)
		}

	case key.Matches(msg, common.Keys.Up):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 && m.cursors[m.activeCol] > 0 {
			m.cursors[m.activeCol]--
			m.fixScrollOffsets()
		}

	case key.Matches(msg, common.Keys.Down):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 && m.cursors[m.activeCol] < len(tasks)-1 {
			m.cursors[m.activeCol]++
			m.fixScrollOffsets()
		}

	case key.Matches(msg, common.Keys.JumpTop):
		m.cursors[m.activeCol] = 0
		m.fixScrollOffsets()

	case key.Matches(msg, common.Keys.JumpBottom):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 {
			m.cursors[m.activeCol] = len(tasks) - 1
			m.fixScrollOffsets()
		}

	case key.Matches(msg, common.Keys.HalfDown):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 {
			m.cursors[m.activeCol] = min(m.cursors[m.activeCol]+5, len(tasks)-1)
			m.fixScrollOffsets()
		}

	case key.Matches(msg, common.Keys.HalfUp):
		m.cursors[m.activeCol] = max(m.cursors[m.activeCol]-5, 0)
		m.fixScrollOffsets()

	case msg.String() == "r":
		m.refreshProjects()
		m.reload()
		m.toastMsg = "Refreshed"
		m.toastExpiry = time.Now().Add(2 * time.Second)

	case key.Matches(msg, common.Keys.Tab):
		m.activeProject = (m.activeProject + 1) % len(m.projects)
		m.reload()

	case key.Matches(msg, common.Keys.ShiftTab):
		m.activeProject = (m.activeProject - 1 + len(m.projects)) % len(m.projects)
		m.reload()

	case key.Matches(msg, common.Keys.MoveBack):
		if t := m.selectedTask(); t != nil {
			m.doMoveBack(t)
		}

	case key.Matches(msg, common.Keys.Move):
		if t := m.selectedTask(); t != nil {
			m.doMoveForward(t)
		}

	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		t := m.selectedTask()
		if t != nil {
			m.previousView = viewBoard
			m.currentView = viewDetail
			m.detailTask = t
			m.detailViewport = viewport.New(m.width, m.height-2)
			m.detailViewport.SetContent(renderTaskDetail(t, m.width))
		}

	case key.Matches(msg, common.Keys.Edit):
		t := m.selectedTask()
		if t != nil {
			return m, openEditor(t.FilePath)
		}

	case key.Matches(msg, common.Keys.Done):
		t := m.selectedTask()
		if t == nil {
			break
		}
		if m.confirmAction == "done" && m.confirmTaskID == t.Meta.ID {
			m.doDone(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
		} else {
			m.confirmAction = "done"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Waiting):
		t := m.selectedTask()
		if t == nil {
			break
		}
		if m.confirmAction == "waiting" && m.confirmTaskID == t.Meta.ID {
			m.doWaiting(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
		} else {
			m.confirmAction = "waiting"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Delete):
		t := m.selectedTask()
		if t == nil {
			break
		}
		if m.confirmAction == "delete" && m.confirmTaskID == t.Meta.ID {
			// confirmed
			m.lastUndo = &undoAction{kind: "delete", task: snapshotTask(t)}
			m.store.DeleteTask(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
			m.reload()
		} else {
			m.confirmAction = "delete"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Yank):
		t := m.selectedTask()
		if t == nil {
			break
		}
		if t.Meta.ID != "" {
			m.copyToClipboard(t.Meta.ID)
		} else {
			m.toastMsg = "no id set"
			m.toastExpiry = time.Now().Add(2 * time.Second)
		}

	case key.Matches(msg, common.Keys.YankMenu):
		t := m.selectedTask()
		if t == nil {
			break
		}
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
		for name, url := range t.Meta.Links {
			m.yankItems = append(m.yankItems, yankItem{"link: " + name, url})
		}
		if len(m.yankItems) > 0 {
			m.yankMenu = true
		}

	case key.Matches(msg, common.Keys.Links):
		t := m.selectedTask()
		if t == nil || len(t.Meta.Links) == 0 {
			break
		}
		m.linkItems = nil
		m.linksCursor = 0
		for name, url := range t.Meta.Links {
			m.linkItems = append(m.linkItems, linkItem{name, url})
		}
		m.linksMenu = true

	case key.Matches(msg, common.Keys.Archive):
		t := m.selectedTask()
		if t == nil {
			break
		}
		if m.confirmAction == "archive" && m.confirmTaskID == t.Meta.ID {
			m.doArchive(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
		} else {
			m.confirmAction = "archive"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.ToggleArchive):
		m.currentView = viewArchive
		m.archiveCursor = 0
		m.confirmAction = ""
		m.confirmTaskID = ""

	case key.Matches(msg, common.Keys.ProjectInfo):
		var slug string
		if m.activeProject == 0 {
			t := m.selectedTask()
			if t == nil {
				break
			}
			slug = t.Project
		} else {
			slug = m.projects[m.activeProject]
		}
		proj, _ := m.store.GetProject(slug)
		if proj != nil {
			m.previousView = viewBoard
			m.currentView = viewProjectInfo
			m.infoProject = proj
			m.infoSlug = slug
			m.detailViewport = viewport.New(m.width, m.height-2)
			m.detailViewport.SetContent(renderProjectInfo(proj, slug, m.width))
		}

	case key.Matches(msg, common.Keys.Undo):
		m.doUndo()

	case key.Matches(msg, common.Keys.ColumnVis):
		var allStatuses []storage.TaskStatus
		if m.activeProject == 0 {
			allStatuses = m.store.GetAllStatuses()
		} else {
			slug := m.projects[m.activeProject]
			allStatuses = m.store.GetProjectStatuses(slug)
		}
		visibleSet := make(map[storage.TaskStatus]bool)
		for _, s := range m.statuses {
			visibleSet[s] = true
		}
		m.colVisItems = nil
		m.colVisCursor = 0
		for _, s := range allStatuses {
			m.colVisItems = append(m.colVisItems, colVisItem{status: s, visible: visibleSet[s]})
		}
		m.colVisMenu = true

	case key.Matches(msg, common.Keys.ReorderDown):
		m.doReorder(1)

	case key.Matches(msg, common.Keys.ReorderUp):
		m.doReorder(-1)

	case key.Matches(msg, common.Keys.Zoom):
		m.zoomed = !m.zoomed

	case key.Matches(msg, common.Keys.Claude):
		t := m.selectedTask()
		if t == nil {
			break
		}
		m.openClaudeMenu(t)

	case key.Matches(msg, common.Keys.Help):
		m.showHelp = true

	case key.Matches(msg, common.Keys.Add):
		if m.activeProject == 0 {
			if len(m.projects) > 1 {
				m.activeProject = 1
				m.reload()
				m.activeCol = 0
			} else {
				return m, nil
			}
		}
		m.adding = true
		m.addStep = 0
		m.addInput.Prompt = "Title: "
		m.addInput.SetValue("")
		m.addInput.Focus()
		return m, m.addInput.Cursor.BlinkCmd()

	case key.Matches(msg, common.Keys.Search):
		m.searching = true
		m.searchInput.SetValue(m.searchQuery)
		m.searchInput.Focus()
		return m, m.searchInput.Cursor.BlinkCmd()
	}
	return m, nil
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Enter):
		m.searching = false
		m.searchQuery = m.searchInput.Value()
		m.searchInput.Blur()
		m.fixCursors()
	case key.Matches(msg, common.Keys.Escape):
		m.searching = false
		m.searchQuery = ""
		m.searchInput.SetValue("")
		m.searchInput.Blur()
		m.fixCursors()
	default:
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		m.searchQuery = m.searchInput.Value()
		m.fixCursors()
		return m, cmd
	}
	return m, nil
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
		id := val
		if id == "" {
			id = fmt.Sprintf("%d", time.Now().Unix()%100000)
		}
		now := storage.Today()
		t := &storage.Task{
			Meta: storage.TaskMeta{
				ID:      id,
				Title:   m.addTitle,
				Status:  m.statuses[0],
				Created: now,
				Updated: now,
			},
		}
		slug := m.projects[m.activeProject]
		m.store.AddTask(slug, t)
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

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Overlay menus intercept input first
	if m.claudeMenu {
		return m.updateClaudeMenu(msg)
	}
	if m.yankMenu {
		return m.updateYankMenu(msg)
	}
	if m.linksMenu {
		return m.updateLinksMenu(msg)
	}
	if m.sessionMenu {
		return m.updateSessionMenu(msg)
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
		}
		var cmd tea.Cmd
		m.detailViewport, cmd = m.detailViewport.Update(msg)
		return m, cmd
	}

	t := m.detailTask
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		m.currentView = m.previousView
		m.reload()
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

	case msg.String() == "s":
		if t != nil {
			m.openSessionMenu(t)
		}

	case msg.String() == "r":
		if t != nil {
			reloaded, err := m.store.FindTask(t.Project, t.Meta.ID)
			if err == nil {
				m.detailTask = reloaded
				content := renderTaskDetail(reloaded, m.width)
				m.detailViewport.SetContent(content)
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

func (m Model) updateArchive(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Confirmation pattern for delete in archive
	if m.confirmAction != "" {
		isConfirmKey := false
		if m.confirmAction == "delete" {
			isConfirmKey = key.Matches(msg, common.Keys.Delete)
		}
		if !isConfirmKey {
			m.confirmAction = ""
			m.confirmTaskID = ""
		}
	}

	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.ToggleArchive):
		m.currentView = viewBoard
		m.confirmAction = ""
		m.confirmTaskID = ""
		return m, nil

	case key.Matches(msg, common.Keys.Quit):
		m.currentView = viewBoard
		m.reload()
		return m, nil

	case key.Matches(msg, common.Keys.Up):
		if m.archiveCursor > 0 {
			m.archiveCursor--
		}

	case key.Matches(msg, common.Keys.Down):
		tasks := m.archivedTasks()
		if m.archiveCursor < len(tasks)-1 {
			m.archiveCursor++
		}

	case key.Matches(msg, common.Keys.JumpTop):
		m.archiveCursor = 0

	case key.Matches(msg, common.Keys.JumpBottom):
		tasks := m.archivedTasks()
		if len(tasks) > 0 {
			m.archiveCursor = len(tasks) - 1
		}

	case key.Matches(msg, common.Keys.Restore):
		t := m.selectedArchiveTask()
		if t != nil {
			m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
			statuses := m.store.GetProjectStatuses(t.Project)
			m.store.MoveTask(t, statuses[0])
			m.reload()
			m.fixArchiveCursor()
		}

	case key.Matches(msg, common.Keys.Delete):
		t := m.selectedArchiveTask()
		if t == nil {
			break
		}
		if m.confirmAction == "delete" && m.confirmTaskID == t.Meta.ID {
			m.lastUndo = &undoAction{kind: "delete", task: snapshotTask(t)}
			m.store.DeleteTask(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
			m.reload()
			m.fixArchiveCursor()
		} else {
			m.confirmAction = "delete"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		t := m.selectedArchiveTask()
		if t != nil {
			m.previousView = viewArchive
			m.currentView = viewDetail
			m.detailTask = t
			m.detailViewport = viewport.New(m.width, m.height-2)
			m.detailViewport.SetContent(renderTaskDetail(t, m.width))
		}

	case key.Matches(msg, common.Keys.Tab):
		m.activeProject = (m.activeProject + 1) % len(m.projects)
		m.reload()
		m.archiveCursor = 0

	case key.Matches(msg, common.Keys.ShiftTab):
		m.activeProject = (m.activeProject - 1 + len(m.projects)) % len(m.projects)
		m.reload()
		m.archiveCursor = 0

	case key.Matches(msg, common.Keys.Undo):
		m.doUndo()
		m.fixArchiveCursor()

	case key.Matches(msg, common.Keys.Search):
		m.searching = true
		m.searchInput.SetValue(m.searchQuery)
		m.searchInput.Focus()
		return m, m.searchInput.Cursor.BlinkCmd()
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
			exec.Command("open", m.linkItems[m.linksCursor].url).Start()
		}
		m.linksMenu = false
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

	// Resolve project dir for CC metadata lookup
	var projDir string
	if proj, err := m.store.GetProject(t.Project); err == nil && proj.Path != "" {
		projDir = proj.Path
	}

	enrichItem := func(sid string, isLatest bool) sessionMenuItem {
		item := sessionMenuItem{sessionID: sid, isLatest: isLatest}
		if meta := loadSessionMeta(projDir, sid); meta != nil {
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
	case key.Matches(msg, common.Keys.Enter), msg.String() == "r":
		if m.sessionCursor < len(m.sessionMenuItems) {
			m.resumeSessionID = m.sessionMenuItems[m.sessionCursor].sessionID
			m.sessionMenu = false
			m.resumeOnly = true
			m.forkMode = false
			t := m.selectedTask()
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
			t := m.selectedTask()
			if t != nil {
				m.openClaudeMenu(t)
			}
			return m, nil
		}
	case msg.String() == "y":
		if m.sessionCursor < len(m.sessionMenuItems) {
			m.copyToClipboard(m.sessionMenuItems[m.sessionCursor].sessionID)
			m.sessionMenu = false
		}
	case msg.String() == "x":
		if m.sessionCursor >= len(m.sessionMenuItems) {
			break
		}
		if m.confirmAction == "delete-session" {
			// Second press - confirmed
			m.confirmAction = ""
			t := m.selectedTask()
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
			t.Meta.Updated = storage.Today()
			storage.WriteTask(t)
			m.openSessionMenu(t)
			if len(m.sessionMenuItems) == 0 {
				m.sessionMenu = false
			} else if m.sessionCursor >= len(m.sessionMenuItems) {
				m.sessionCursor = len(m.sessionMenuItems) - 1
			}
			m.toastMsg = "Deleted session " + sid[:8] + "..."
			m.toastExpiry = time.Now().Add(2 * time.Second)
			m.detailViewport.SetContent(renderTaskDetail(t, m.width))
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
	c.Start()
	display := value
	if len(display) > 40 {
		display = display[:37] + "..."
	}
	m.toastMsg = "Copied: " + display
	m.toastExpiry = time.Now().Add(2 * time.Second)
}

func (m Model) statusIndex(s storage.TaskStatus) int {
	for i, st := range m.statuses {
		if st == s {
			return i
		}
	}
	return 0
}

func (m *Model) fixCursors() {
	for i := range m.statuses {
		if i >= len(m.cursors) {
			continue
		}
		tasks := m.columnTasks(i)
		if len(tasks) == 0 {
			m.cursors[i] = 0
		} else if m.cursors[i] >= len(tasks) {
			m.cursors[i] = len(tasks) - 1
		}
	}
	m.fixScrollOffsets()
}

// cardHeight returns the rendered height of a single task card (border + content + margin).
func cardHeight(t *storage.Task) int {
	lines := 2 // title + project
	if len(t.Meta.Tags) > 0 {
		lines++
	}
	return lines + 3 // +2 border, +1 margin bottom
}

// fixScrollOffsets ensures the cursor is visible within the column viewport.
// It renders cards to measure actual heights (accounting for text wrapping).
func (m *Model) fixScrollOffsets() {
	overhead := 11
	if m.adding || m.searching || m.searchQuery != "" {
		overhead++
	}
	maxCardHeight := m.height - overhead
	if maxCardHeight < 5 {
		maxCardHeight = 5
	}
	// Conservative budget: subtract 2 for column header + possible scroll indicator
	cardBudget := maxCardHeight - 2
	if cardBudget < 3 {
		cardBudget = 3
	}

	numCols := len(m.statuses)
	if numCols == 0 {
		numCols = 1
	}
	colWidth := (m.width - 8) / numCols
	if m.zoomed {
		colWidth = m.width - 8
	}
	if colWidth < 20 {
		colWidth = 20
	}
	cardW := colWidth - 6

	for i := range m.statuses {
		if i >= len(m.cursors) || i >= len(m.scrollOffsets) {
			continue
		}
		tasks := m.columnTasks(i)
		if len(tasks) == 0 {
			m.scrollOffsets[i] = 0
			continue
		}
		cursor := m.cursors[i]
		if cursor >= len(tasks) {
			cursor = len(tasks) - 1
		}

		measure := func(j int) int {
			var card string
			if m.zoomed {
				card = renderZoomCard(tasks[j], cardW, false)
			} else {
				card = renderCard(tasks[j], cardW, false)
			}
			return strings.Count(card, "\n") + 1
		}

		// Scroll up if cursor is above the visible window
		if cursor < m.scrollOffsets[i] {
			m.scrollOffsets[i] = cursor
		}

		// Scroll down if cursor is below the visible window
		usedH := 0
		lastVisible := m.scrollOffsets[i]
		for j := m.scrollOffsets[i]; j < len(tasks); j++ {
			h := measure(j)
			if usedH+h > cardBudget && j > m.scrollOffsets[i] {
				break
			}
			usedH += h
			lastVisible = j
		}
		if cursor > lastVisible {
			// Scroll down: find new offset so cursor fits at bottom
			usedH = 0
			start := cursor
			for start >= 0 {
				h := measure(start)
				if usedH+h > cardBudget && start < cursor {
					start++
					break
				}
				usedH += h
				if start == 0 {
					break
				}
				start--
			}
			m.scrollOffsets[i] = start
		}
	}
}

// --- Task action helpers (shared between board/detail/archive views) ---

// fixArchiveCursor clamps archive cursor to valid range after archive list changes.
func (m *Model) fixArchiveCursor() {
	tasks := m.archivedTasks()
	if m.archiveCursor >= len(tasks) && len(tasks) > 0 {
		m.archiveCursor = len(tasks) - 1
	}
}

// doMoveForward cycles task to next status column.
func (m *Model) doMoveForward(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
	idx := m.statusIndex(t.Meta.Status)
	m.store.MoveTask(t, m.statuses[(idx+1)%len(m.statuses)])
	m.reload()
}

// doMoveBack cycles task to previous status column.
func (m *Model) doMoveBack(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
	idx := m.statusIndex(t.Meta.Status)
	var newStatus storage.TaskStatus
	if idx > 0 {
		newStatus = m.statuses[idx-1]
	} else {
		newStatus = m.statuses[len(m.statuses)-1]
	}
	m.store.MoveTask(t, newStatus)
	m.reload()
}

// doDone marks task with the last status (typically "done").
func (m *Model) doDone(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "done", task: snapshotTask(t)}
	m.store.MoveTask(t, m.statuses[len(m.statuses)-1])
	m.reload()
}

// doWaiting marks task as waiting.
func (m *Model) doWaiting(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
	m.store.MoveTask(t, storage.StatusWaiting)
	m.reload()
}

// doArchive archives the task.
func (m *Model) doArchive(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "archive", task: snapshotTask(t)}
	m.store.MoveTask(t, storage.StatusArchived)
	m.reload()
}

// doUndo restores the last undone action.
func (m *Model) doUndo() {
	if m.lastUndo == nil {
		m.toastMsg = "nothing to undo"
		m.toastExpiry = time.Now().Add(2 * time.Second)
		return
	}
	u := m.lastUndo
	storage.WriteTask(u.task)
	m.lastUndo = nil
	m.toastMsg = "undone: " + u.kind
	m.toastExpiry = time.Now().Add(2 * time.Second)
	m.reload()
}

// doReorder swaps the selected task with a neighbor in the column.
// direction: +1 = move down, -1 = move up.
func (m *Model) doReorder(direction int) {
	tasks := m.columnTasks(m.activeCol)
	if len(tasks) < 2 {
		return
	}
	cursor := m.cursors[m.activeCol]
	target := cursor + direction
	if target < 0 || target >= len(tasks) {
		return
	}

	a := tasks[cursor]
	b := tasks[target]

	// If all tasks have Order==0, assign sequential orders to all tasks in column
	allZero := true
	for _, t := range tasks {
		if t.Meta.Order != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		for i, t := range tasks {
			t.Meta.Order = (i + 1) * 10
			t.Meta.Updated = storage.Today()
			storage.WriteTask(t)
		}
	}

	// Swap orders
	a.Meta.Order, b.Meta.Order = b.Meta.Order, a.Meta.Order
	a.Meta.Updated = storage.Today()
	b.Meta.Updated = storage.Today()
	storage.WriteTask(a)
	storage.WriteTask(b)

	m.cursors[m.activeCol] = target
	m.reload()
}

func (m *Model) openClaudeMenu(t *storage.Task) {
	inTmux := os.Getenv("TMUX") != ""
	hasSession := lastSession(t) != ""
	m.claudeMenuItems = nil
	m.claudeMenuCursor = 0
	m.claudeMenuSkipPerms = false

	if m.resumeOnly && m.forkMode {
		// Fork sub-menu: fork from selected session
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Fork here", "fork", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Fork in tmux", "fork-tmux", "t"})
			m.claudeMenuCursor = 1 // default to tmux
		}
	} else if m.resumeOnly {
		// Resume sub-menu: only show resume options
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume here", "resume", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume in tmux", "resume-tmux", "t"})
			m.claudeMenuCursor = 1 // default to tmux
		}
	} else {
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Here (takes over terminal)", "here", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Tmux window", "tmux", "t"})
		}
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Worktree (here)", "worktree", "w"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Worktree + tmux", "worktree-tmux", "W"})
		}
		if hasSession {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume session", "resume", "r"})
			if inTmux {
				m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume in tmux", "resume-tmux", "R"})
			}
		}
		// Smart default: tmux if available, else here
		if inTmux {
			for i, item := range m.claudeMenuItems {
				if item.kind == "tmux" {
					m.claudeMenuCursor = i
					break
				}
			}
		}
	}
	m.claudeMenu = true
}

func (m Model) updateClaudeMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Toggle skip-permissions
	typed := msg.String()
	if typed == "!" {
		m.claudeMenuSkipPerms = !m.claudeMenuSkipPerms
		return m, nil
	}

	// Check mnemonic shortcut keys first
	for _, item := range m.claudeMenuItems {
		if typed == item.shortcut {
			m.claudeMenu = false
			m.resumeOnly = false
			m.forkMode = false
			return m.launchClaude(item.kind)
		}
	}

	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.claudeMenu = false
		m.resumeOnly = false
		m.forkMode = false
		m.resumeSessionID = ""
	case key.Matches(msg, common.Keys.Up):
		if m.claudeMenuCursor > 0 {
			m.claudeMenuCursor--
		}
	case key.Matches(msg, common.Keys.Down):
		if m.claudeMenuCursor < len(m.claudeMenuItems)-1 {
			m.claudeMenuCursor++
		}
	case key.Matches(msg, common.Keys.Enter):
		if m.claudeMenuCursor < len(m.claudeMenuItems) {
			item := m.claudeMenuItems[m.claudeMenuCursor]
			m.claudeMenu = false
			m.resumeOnly = false
			m.forkMode = false
			return m.launchClaude(item.kind)
		}
	}
	return m, nil
}

// worktreeName returns a semantic name for the worktree branch.
// Uses task branch if set, otherwise slugifies the task title.
func worktreeName(t *storage.Task) string {
	if t.Meta.Branch != "" {
		return t.Meta.Branch
	}
	return storage.Slugify(t.Meta.Title)
}

// copyWorktreeFiles pre-creates a git worktree (if needed) and copies all
// untracked files from the main repo into the worktree.
func copyWorktreeFiles(projDir, wtName string) {
	wtPath := filepath.Join(projDir, ".claude", "worktrees", wtName)

	// Pre-create worktree if it doesn't exist yet
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		cmd := exec.Command("git", "worktree", "add", wtPath)
		cmd.Dir = projDir
		if err := cmd.Run(); err != nil {
			return
		}
	}

	// Find all untracked files (both ignored and non-ignored)
	cmd := exec.Command("git", "ls-files", "--others")
	cmd.Dir = projDir
	out, err := cmd.Output()
	if err != nil {
		return
	}

	for _, rel := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if rel == "" {
			continue
		}
		src := filepath.Join(projDir, rel)
		dst := filepath.Join(wtPath, rel)

		// Skip if destination already exists
		if _, err := os.Stat(dst); err == nil {
			continue
		}

		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		info, _ := os.Stat(src)
		os.MkdirAll(filepath.Dir(dst), 0755)
		os.WriteFile(dst, data, info.Mode())
	}
}

func (m Model) launchClaude(kind string) (tea.Model, tea.Cmd) {
	t := m.selectedTask()
	if t == nil {
		return m, nil
	}

	prompt := buildClaudePrompt(t, m.store)
	skipFlag := ""
	if m.claudeMenuSkipPerms {
		skipFlag = "--dangerously-skip-permissions"
	}

	// Resolve project working directory
	var projDir string
	if proj, err := m.store.GetProject(t.Project); err == nil && proj.Path != "" {
		if info, err := os.Stat(proj.Path); err == nil && info.IsDir() {
			projDir = proj.Path
		}
	}

	// Check if the session lives in a worktree. Claude Code stores conversations
	// in ~/.claude/projects/<path-hash>/ where path-hash is derived from cwd.
	// When a session was created inside a worktree, we need to resume from that
	// worktree dir so CC finds the conversation.
	var worktreeDir string
	if projDir != "" && (kind == "resume" || kind == "resume-tmux") {
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		worktreeDir = findWorktreeForSession(projDir, sessionID)
	}

	setDir := func(c *exec.Cmd) {
		if projDir != "" {
			c.Dir = projDir
		}
	}

	setResumeDir := func(c *exec.Cmd) {
		if worktreeDir != "" {
			c.Dir = worktreeDir
		} else if projDir != "" {
			c.Dir = projDir
		}
	}

	withCd := func(cmd string) string {
		if projDir != "" {
			return fmt.Sprintf("cd %s && %s", shellQuote(projDir), cmd)
		}
		return cmd
	}

	withResumeCd := func(cmd string) string {
		if worktreeDir != "" {
			return fmt.Sprintf("cd %s && %s", shellQuote(worktreeDir), cmd)
		}
		return withCd(cmd)
	}

	switch kind {
	case "here":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		args := []string{"--session-id", sessionID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		args = append(args, prompt)
		c := exec.Command("claude", args...)
		setDir(c)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			return reloadMsg{}
		})

	case "tmux":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		shellCmd := withCd(fmt.Sprintf("claude --session-id %s %s %s", sessionID, skipFlag, shellQuote(prompt)))
		exec.Command("tmux", "new-window", "-n", "cc:"+t.Meta.ID, "sh", "-c", shellCmd).Start()
		m.toastMsg = "Launched in tmux: cc:" + t.Meta.ID
		m.toastExpiry = time.Now().Add(3 * time.Second)

	case "worktree":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		wtName := worktreeName(t)
		if projDir != "" {
			copyWorktreeFiles(projDir, wtName)
		}
		args := []string{"-w", wtName, "--session-id", sessionID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		args = append(args, prompt)
		c := exec.Command("claude", args...)
		setDir(c)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			return reloadMsg{}
		})

	case "worktree-tmux":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		wtName := worktreeName(t)
		if projDir != "" {
			copyWorktreeFiles(projDir, wtName)
		}
		shellCmd := withCd(fmt.Sprintf("claude -w %s --session-id %s %s %s", shellQuote(wtName), sessionID, skipFlag, shellQuote(prompt)))
		exec.Command("tmux", "new-window", "-n", "wt:"+t.Meta.ID, "sh", "-c", shellCmd).Start()
		m.toastMsg = "Launched worktree in tmux: wt:" + t.Meta.ID
		m.toastExpiry = time.Now().Add(3 * time.Second)

	case "resume":
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		m.resumeSessionID = ""
		args := []string{"--resume", sessionID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		c := exec.Command("claude", args...)
		setResumeDir(c)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			return reloadMsg{}
		})

	case "resume-tmux":
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		m.resumeSessionID = ""
		shellCmd := withResumeCd(fmt.Sprintf("claude --resume %s %s", sessionID, skipFlag))
		exec.Command("tmux", "new-window", "-n", "cc:"+t.Meta.ID, "sh", "-c", shellCmd).Start()
		m.toastMsg = "Resumed in tmux: cc:" + t.Meta.ID
		m.toastExpiry = time.Now().Add(3 * time.Second)

	case "fork":
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		m.resumeSessionID = ""
		m.forkMode = false
		args := []string{"--resume", sessionID, "--fork-session"}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		c := exec.Command("claude", args...)
		setResumeDir(c)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			return reloadMsg{}
		})

	case "fork-tmux":
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		m.resumeSessionID = ""
		m.forkMode = false
		shellCmd := withResumeCd(fmt.Sprintf("claude --resume %s --fork-session %s", sessionID, skipFlag))
		exec.Command("tmux", "new-window", "-n", "cc:"+t.Meta.ID, "sh", "-c", shellCmd).Start()
		m.toastMsg = "Forked in tmux: cc:" + t.Meta.ID
		m.toastExpiry = time.Now().Add(3 * time.Second)
	}

	return m, nil
}

func buildClaudePrompt(t *storage.Task, store *storage.Store) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Working on: #%s %s [%s]\n", t.Meta.ID, t.Meta.Title, t.Meta.Status)
	fmt.Fprintf(&sb, "Project: %s\n", t.Project)
	if t.Meta.Branch != "" {
		fmt.Fprintf(&sb, "Branch: %s\n", t.Meta.Branch)
	}

	if proj, err := store.GetProject(t.Project); err == nil {
		if proj.Path != "" {
			fmt.Fprintf(&sb, "Path: %s\n", proj.Path)
		}
		if proj.Stack != "" {
			fmt.Fprintf(&sb, "Stack: %s\n", proj.Stack)
		}
		if proj.Repo != "" {
			fmt.Fprintf(&sb, "Repo: %s\n", proj.Repo)
		}
		if proj.Notes != "" {
			fmt.Fprintf(&sb, "Notes: %s\n", proj.Notes)
		}
	}

	if t.Meta.Brief != "" {
		fmt.Fprintf(&sb, "\n%s\n", t.Meta.Brief)
	}

	sb.WriteString("\nUse pm MCP (pm_get_task) for full task details.")
	return sb.String()
}

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func lastSession(t *storage.Task) string {
	if len(t.Meta.Sessions) > 0 {
		return t.Meta.Sessions[len(t.Meta.Sessions)-1]
	}
	return t.Meta.Links["cc-session"] // legacy fallback
}

func (m *Model) saveSession(t *storage.Task, sessionID string) {
	// Migrate legacy cc-session link
	if old := t.Meta.Links["cc-session"]; old != "" {
		if len(t.Meta.Sessions) == 0 || t.Meta.Sessions[len(t.Meta.Sessions)-1] != old {
			t.Meta.Sessions = append(t.Meta.Sessions, old)
		}
		delete(t.Meta.Links, "cc-session")
		if len(t.Meta.Links) == 0 {
			t.Meta.Links = nil
		}
	}
	t.Meta.Sessions = append(t.Meta.Sessions, sessionID)
	t.Meta.Updated = storage.Today()
	storage.WriteTask(t)
}

type sessionIndexEntry struct {
	SessionID    string `json:"sessionId"`
	Summary      string `json:"summary"`
	MessageCount int    `json:"messageCount"`
	Modified     string `json:"modified"`
	GitBranch    string `json:"gitBranch"`
}

type sessionsIndex struct {
	Entries []sessionIndexEntry `json:"entries"`
}

// ccProjectDirs returns CC project directories to search for session data.
// Includes main project dir and all worktree dirs.
func ccProjectDirs(projDir string) []string {
	homeDir, _ := os.UserHomeDir()
	ccProjectsDir := filepath.Join(homeDir, ".claude", "projects")

	var dirs []string
	dirs = append(dirs, filepath.Join(ccProjectsDir, pathToCCProject(projDir)))
	wtBase := filepath.Join(projDir, ".claude", "worktrees")
	if entries, err := os.ReadDir(wtBase); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				wtPath := filepath.Join(wtBase, e.Name())
				dirs = append(dirs, filepath.Join(ccProjectsDir, pathToCCProject(wtPath)))
			}
		}
	}
	return dirs
}

// loadSessionMeta looks up session metadata from Claude Code's sessions-index.json.
// Falls back to parsing the JSONL file directly if not found in index.
func loadSessionMeta(projDir, sessionID string) *sessionIndexEntry {
	if projDir == "" || sessionID == "" {
		return nil
	}

	ccDirs := ccProjectDirs(projDir)

	// Try sessions-index.json first (fast path)
	for _, dir := range ccDirs {
		indexPath := filepath.Join(dir, "sessions-index.json")
		data, err := os.ReadFile(indexPath)
		if err != nil {
			continue
		}
		var idx sessionsIndex
		if err := json.Unmarshal(data, &idx); err != nil {
			continue
		}
		for _, e := range idx.Entries {
			if e.SessionID == sessionID {
				return &e
			}
		}
	}

	// Fallback: parse JSONL file directly
	for _, dir := range ccDirs {
		jsonlPath := filepath.Join(dir, sessionID+".jsonl")
		info, err := os.Stat(jsonlPath)
		if err != nil {
			continue
		}
		entry := &sessionIndexEntry{
			SessionID: sessionID,
			Modified:  info.ModTime().UTC().Format(time.RFC3339),
		}
		data, err := os.ReadFile(jsonlPath)
		if err != nil {
			return entry
		}
		msgCount := 0
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			var rec struct {
				Type    string `json:"type"`
				Summary string `json:"summary"`
			}
			if json.Unmarshal([]byte(line), &rec) != nil {
				continue
			}
			if rec.Type == "user" || rec.Type == "assistant" {
				msgCount++
			}
			if rec.Type == "summary" && rec.Summary != "" {
				entry.Summary = rec.Summary
			}
		}
		entry.MessageCount = msgCount
		return entry
	}

	return nil
}

// findWorktreeForSession scans worktree subdirs under projDir and checks
// if Claude Code has a conversation file for the given session ID stored
// under a project directory corresponding to that worktree path.
// Returns the worktree absolute path if found, empty string otherwise.
func findWorktreeForSession(projDir, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	wtBase := filepath.Join(projDir, ".claude", "worktrees")
	entries, err := os.ReadDir(wtBase)
	if err != nil {
		return ""
	}
	homeDir, _ := os.UserHomeDir()
	ccProjectsDir := filepath.Join(homeDir, ".claude", "projects")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wtPath := filepath.Join(wtBase, e.Name())
		// Claude Code maps absolute path to project dir name by replacing
		// "/" with "-" and stripping "." from directory names.
		ccDirName := pathToCCProject(wtPath)
		sessionFile := filepath.Join(ccProjectsDir, ccDirName, sessionID+".jsonl")
		if _, err := os.Stat(sessionFile); err == nil {
			return wtPath
		}
	}
	return ""
}

// pathToCCProject converts an absolute path to the Claude Code project
// directory name format: replace "/" and "." with "-".
func pathToCCProject(absPath string) string {
	s := strings.ReplaceAll(absPath, "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func openEditor(path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nvim"
	}
	c := exec.Command(editor, path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return reloadMsg{}
	})
}
