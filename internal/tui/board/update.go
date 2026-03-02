package board

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
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
		inTmux := os.Getenv("TMUX") != ""
		hasSession := t.Meta.Links["cc-session"] != ""
		m.claudeMenuItems = nil
		m.claudeMenuCursor = 0
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
		m.claudeMenu = true

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
	if m.yankMenu {
		return m.updateYankMenu(msg)
	}
	if m.linksMenu {
		return m.updateLinksMenu(msg)
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

func (m Model) updateClaudeMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Check mnemonic shortcut keys first
	typed := msg.String()
	for _, item := range m.claudeMenuItems {
		if typed == item.shortcut {
			m.claudeMenu = false
			return m.launchClaude(item.kind)
		}
	}

	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.claudeMenu = false
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
			return m.launchClaude(item.kind)
		}
	}
	return m, nil
}

func (m Model) launchClaude(kind string) (tea.Model, tea.Cmd) {
	t := m.selectedTask()
	if t == nil {
		return m, nil
	}

	prompt := buildClaudePrompt(t, m.store)

	switch kind {
	case "here":
		sessionID := generateSessionID()
		m.saveSessionLink(t, sessionID)
		c := exec.Command("claude", "--session-id", sessionID, prompt)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			return reloadMsg{}
		})

	case "tmux":
		sessionID := generateSessionID()
		m.saveSessionLink(t, sessionID)
		shellCmd := fmt.Sprintf("claude --session-id %s %s", sessionID, shellQuote(prompt))
		exec.Command("tmux", "new-window", "-n", "cc:"+t.Meta.ID, "sh", "-c", shellCmd).Start()
		m.toastMsg = "Launched in tmux: cc:" + t.Meta.ID
		m.toastExpiry = time.Now().Add(3 * time.Second)

	case "worktree":
		sessionID := generateSessionID()
		m.saveSessionLink(t, sessionID)
		c := exec.Command("claude", "-w", t.Meta.ID, "--session-id", sessionID, prompt)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			return reloadMsg{}
		})

	case "worktree-tmux":
		sessionID := generateSessionID()
		m.saveSessionLink(t, sessionID)
		shellCmd := fmt.Sprintf("claude -w %s --session-id %s %s", shellQuote(t.Meta.ID), sessionID, shellQuote(prompt))
		exec.Command("tmux", "new-window", "-n", "wt:"+t.Meta.ID, "sh", "-c", shellCmd).Start()
		m.toastMsg = "Launched worktree in tmux: wt:" + t.Meta.ID
		m.toastExpiry = time.Now().Add(3 * time.Second)

	case "resume":
		sessionID := t.Meta.Links["cc-session"]
		c := exec.Command("claude", "--resume", sessionID, prompt)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			return reloadMsg{}
		})

	case "resume-tmux":
		sessionID := t.Meta.Links["cc-session"]
		shellCmd := fmt.Sprintf("claude --resume %s %s", sessionID, shellQuote(prompt))
		exec.Command("tmux", "new-window", "-n", "cc:"+t.Meta.ID, "sh", "-c", shellCmd).Start()
		m.toastMsg = "Resumed in tmux: cc:" + t.Meta.ID
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

	// Add project path for context
	if proj, err := store.GetProject(t.Project); err == nil && proj.Path != "" {
		fmt.Fprintf(&sb, "Path: %s\n", proj.Path)
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

func (m *Model) saveSessionLink(t *storage.Task, sessionID string) {
	if t.Meta.Links == nil {
		t.Meta.Links = make(map[string]string)
	}
	t.Meta.Links["cc-session"] = sessionID
	t.Meta.Updated = storage.Today()
	storage.WriteTask(t)
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
