package board

import (
	"strings"

	"github.com/mbalazy/pm-cli/internal/storage"
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

// columnGeometry computes the two board-layout numbers viewBoard (view.go) and
// fixScrollOffsets both need and used to compute independently (a drift risk:
// render and scroll disagreeing on where the cursor sits). maxCardHeight is
// the vertical budget for a column's cards; colWidth is a single column's
// rendered width (zoom mode collapses to one column at full width).
//
// Non-column overhead: title(2\n) + tabs(2\n) + after-cols(1\n) + confirm(1\n)
// + status bar(1 line) + column border/padding(4\n) = 11 lines. Search/add
// input adds 1 more when active.
func (m Model) columnGeometry() (maxCardHeight, colWidth int) {
	overhead := 11
	if m.adding || m.searching || m.searchQuery != "" {
		overhead++
	}
	maxCardHeight = m.height - overhead
	if maxCardHeight < 5 {
		maxCardHeight = 5
	}

	numCols := len(m.statuses)
	if numCols == 0 || m.zoomed {
		numCols = 1
	}
	colWidth = (m.width - 8) / numCols
	if colWidth < 20 {
		colWidth = 20
	}
	return maxCardHeight, colWidth
}

// fixScrollOffsets ensures the cursor is visible within the column viewport.
// It renders cards to measure actual heights (accounting for text wrapping).
func (m *Model) fixScrollOffsets() {
	maxCardHeight, colWidth := m.columnGeometry()
	// Conservative budget: subtract a CONSTANT 2 lines for column header +
	// possible scroll indicator. viewBoard (view.go) instead subtracts the
	// ACTUAL header line count (1, or 2 once a "N more" indicator is shown) -
	// deliberately different: this is a pre-render estimate that must stay
	// stable across scroll direction changes, while the renderer knows the
	// real number once it decides whether to draw the indicator.
	cardBudget := maxCardHeight - 2
	if cardBudget < 3 {
		cardBudget = 3
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
