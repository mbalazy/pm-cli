package board

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/common"
)

func (m Model) updateSelectMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Confirmation for bulk delete
	if m.confirmAction == "delete-selected" {
		if key.Matches(msg, common.Keys.Delete) {
			tasks := m.markedTasks()
			count := 0
			for _, t := range tasks {
				m.store.DeleteTask(t)
				count++
			}
			m.confirmAction = ""
			m.selecting = false
			m.selected = make(map[string]bool)
			m.reload()
			m.toastMsg = fmt.Sprintf("Deleted %d tasks", count)
			m.toastExpiry = time.Now().Add(2 * time.Second)
			return m, nil
		}
		m.confirmAction = ""
		return m, nil
	}

	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit):
		m.selecting = false
		m.selected = make(map[string]bool)

	// Navigation - same as board
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
	case key.Matches(msg, common.Keys.Left):
		if m.activeCol > 0 {
			m.activeCol--
		}
	case key.Matches(msg, common.Keys.Right):
		if m.activeCol < len(m.statuses)-1 {
			m.activeCol++
		}
	case key.Matches(msg, common.Keys.JumpTop):
		// With every column hidden m.cursors is empty and m.activeCol is 0 -
		// unguarded indexing here panicked select mode the same way it did
		// updateBoard (see update.go).
		if len(m.statuses) > 0 {
			m.cursors[m.activeCol] = 0
			m.fixScrollOffsets()
		}
	case key.Matches(msg, common.Keys.JumpBottom):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 {
			m.cursors[m.activeCol] = len(tasks) - 1
			m.fixScrollOffsets()
		}

	// Toggle selection
	case key.Matches(msg, common.Keys.Space), key.Matches(msg, common.Keys.Select):
		if t := m.selectedTask(); t != nil {
			if m.selected[t.Meta.ID] {
				delete(m.selected, t.Meta.ID)
			} else {
				m.selected[t.Meta.ID] = true
			}
		}

	// Bulk actions
	case key.Matches(msg, common.Keys.Move):
		m.bulkStatusMove(m.markedTasks(), "Moved", "forward", func(t *storage.Task) storage.TaskStatus {
			return m.statuses[(m.statusIndex(t.Meta.Status)+1)%len(m.statuses)]
		})

	case key.Matches(msg, common.Keys.MoveBack):
		m.bulkStatusMove(m.markedTasks(), "Moved", "back", func(t *storage.Task) storage.TaskStatus {
			if idx := m.statusIndex(t.Meta.Status); idx > 0 {
				return m.statuses[idx-1]
			}
			return m.statuses[len(m.statuses)-1]
		})

	case key.Matches(msg, common.Keys.Done):
		m.bulkStatusMove(m.markedTasks(), "Marked", "done", func(*storage.Task) storage.TaskStatus {
			return m.statuses[len(m.statuses)-1]
		})

	case key.Matches(msg, common.Keys.Waiting):
		m.bulkStatusMove(m.markedTasks(), "Marked", "waiting", func(*storage.Task) storage.TaskStatus {
			return storage.StatusWaiting
		})

	case key.Matches(msg, common.Keys.Archive):
		m.bulkStatusMove(m.markedTasks(), "Archived", "", func(*storage.Task) storage.TaskStatus {
			return storage.StatusArchived
		})

	case key.Matches(msg, common.Keys.Delete):
		if len(m.selected) > 0 {
			m.confirmAction = "delete-selected"
		}

	case key.Matches(msg, common.Keys.Yank):
		tasks := m.markedTasks()
		if len(tasks) > 0 {
			var ids []string
			for _, t := range tasks {
				ids = append(ids, t.Meta.ID)
			}
			m.copyToClipboard(strings.Join(ids, "\n"))
		}

	case key.Matches(msg, common.Keys.YankMenu):
		tasks := m.markedTasks()
		if len(tasks) > 0 {
			var ids, titles, idTitles, branches, paths []string
			for _, t := range tasks {
				ids = append(ids, t.Meta.ID)
				titles = append(titles, t.Meta.Title)
				idTitles = append(idTitles, t.Meta.ID+": "+t.Meta.Title)
				if t.Meta.Branch != "" {
					branches = append(branches, t.Meta.Branch)
				}
				paths = append(paths, t.FilePath)
			}
			m.yankItems = nil
			m.yankCursor = 0
			m.yankItems = append(m.yankItems, yankItem{"IDs", strings.Join(ids, "\n")})
			m.yankItems = append(m.yankItems, yankItem{"titles", strings.Join(titles, "\n")})
			m.yankItems = append(m.yankItems, yankItem{"IDs + titles", strings.Join(idTitles, "\n")})
			if len(branches) > 0 {
				m.yankItems = append(m.yankItems, yankItem{"branches", strings.Join(branches, "\n")})
			}
			m.yankItems = append(m.yankItems, yankItem{"file paths", strings.Join(paths, "\n")})
			m.yankMenu = true
		}
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
			if err := m.store.MoveTask(t, statuses[0]); err != nil {
				m.showErrorToast("restore failed", err)
			}
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
			content := m.renderTaskDetail(t)
			m.detailViewport.SetContent(content)
			m.detailPlainContent = stripANSI(content)
			m.detailSearchQuery = ""
			m.detailSearchMatches = nil
			m.detailSearchIdx = 0
			m.detailSearching = false
		}

	case key.Matches(msg, common.Keys.Tab):
		m.activeProject = m.nextVisibleProject(1)
		m.reload()
		m.archiveCursor = 0

	case key.Matches(msg, common.Keys.ShiftTab):
		m.activeProject = m.nextVisibleProject(-1)
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

func (m Model) updateFocus(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.FocusView):
		m.currentView = viewBoard
		return m, nil

	case key.Matches(msg, common.Keys.Quit):
		m.currentView = viewBoard
		return m, nil

	case key.Matches(msg, common.Keys.Up):
		if m.focusCursor > 0 {
			m.focusCursor--
		}

	case key.Matches(msg, common.Keys.Down):
		tasks := m.focusTasks()
		if m.focusCursor < len(tasks)-1 {
			m.focusCursor++
		}

	case key.Matches(msg, common.Keys.JumpTop):
		m.focusCursor = 0

	case key.Matches(msg, common.Keys.JumpBottom):
		tasks := m.focusTasks()
		if len(tasks) > 0 {
			m.focusCursor = len(tasks) - 1
		}

	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		t := m.selectedFocusTask()
		if t != nil {
			m.previousView = viewFocus
			m.currentView = viewDetail
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

	case key.Matches(msg, common.Keys.Focus), key.Matches(msg, common.Keys.Delete):
		t := m.selectedFocusTask()
		if t != nil {
			m.focusPlan.Remove(t.Meta.ID)
			m.focusPlan.Date = storage.Today()
			m.saveFocusPlan()
			m.rebuildFocusSet()
			m.fixFocusCursor()
			m.toastMsg = "Removed from focus"
			m.toastExpiry = time.Now().Add(2 * time.Second)
		}

	case key.Matches(msg, common.Keys.ReorderDown):
		tasks := m.focusTasks()
		if m.focusCursor < len(tasks)-1 {
			m.focusPlan.Swap(m.focusCursor, m.focusCursor+1)
			m.focusCursor++
			m.saveFocusPlan()
		}

	case key.Matches(msg, common.Keys.ReorderUp):
		tasks := m.focusTasks()
		if len(tasks) > 0 && m.focusCursor > 0 {
			m.focusPlan.Swap(m.focusCursor, m.focusCursor-1)
			m.focusCursor--
			m.saveFocusPlan()
		}

	case key.Matches(msg, common.Keys.Move):
		if t := m.selectedFocusTask(); t != nil {
			m.doMoveForward(t)
			m.fixFocusCursor()
		}

	case key.Matches(msg, common.Keys.MoveBack):
		if t := m.selectedFocusTask(); t != nil {
			m.doMoveBack(t)
			m.fixFocusCursor()
		}

	case key.Matches(msg, common.Keys.Done):
		if t := m.selectedFocusTask(); t != nil {
			m.doDone(t)
			m.focusPlan.Remove(t.Meta.ID)
			m.saveFocusPlan()
			m.rebuildFocusSet()
			m.fixFocusCursor()
		}

	case key.Matches(msg, common.Keys.Waiting):
		if t := m.selectedFocusTask(); t != nil {
			m.doWaiting(t)
			m.fixFocusCursor()
		}

	case key.Matches(msg, common.Keys.Undo):
		m.doUndo()
		m.fixFocusCursor()

	case key.Matches(msg, common.Keys.Claude):
		if t := m.selectedFocusTask(); t != nil {
			m.openClaudeMenu(t)
		}

	case key.Matches(msg, common.Keys.Yank):
		if t := m.selectedFocusTask(); t != nil && t.Meta.ID != "" {
			m.copyToClipboard(t.Meta.ID)
		}

	case key.Matches(msg, common.Keys.YankMenu):
		t := m.selectedFocusTask()
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
			for name, url := range t.Meta.Links {
				m.yankItems = append(m.yankItems, yankItem{"link: " + name, url})
			}
			if len(m.yankItems) > 0 {
				m.yankMenu = true
			}
		}

	case key.Matches(msg, common.Keys.Zoom):
		m.zoomed = !m.zoomed
	}
	return m, nil
}
