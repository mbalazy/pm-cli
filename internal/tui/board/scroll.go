package board

import (
	"strings"

	"github.com/mbalazy/pm/internal/storage"
)

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
			// badge/marker live on the existing ID line, so they don't change
			// the line count - pass empty for measurement.
			var card string
			if m.zoomed {
				card = renderZoomCard(tasks[j], cardW, false, "", "")
			} else {
				card = renderCard(tasks[j], cardW, false, "", "")
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
