package board

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// viewBoard must render EXACTLY m.height visual lines. One line too many and
// bubbletea cuts the top of the board off; one too few and the status bar
// floats. The budget is split between viewBoard (which lays out the non-column
// chrome) and columnGeometry (which sizes the cards), so a change to either
// side alone breaks it - which is the whole reason the geometry lives in one
// helper.
func TestViewBoardRendersExactlyHeightLines(t *testing.T) {
	var tasks []*storage.Task
	for _, id := range []string{"p-1", "p-2", "p-3", "p-4", "p-5"} {
		tasks = append(tasks, &storage.Task{
			Project: "p",
			Meta: storage.TaskMeta{
				ID:     id,
				Title:  "task " + id + " with a title long enough to wrap in a narrow column",
				Status: storage.StatusTodo,
			},
		})
	}
	m := newBoardModel(t, tasks...)

	check := func(t *testing.T, m *Model) {
		t.Helper()
		m.fixScrollOffsets()
		got := strings.Count(m.viewBoard(), "\n") + 1
		if got != m.height {
			t.Errorf("viewBoard rendered %d lines, want height %d", got, m.height)
		}
	}

	for _, h := range []int{20, 24, 30, 40, 60} {
		for _, w := range []int{80, 120, 200} {
			t.Run(fmt.Sprintf("h=%d w=%d", h, w), func(t *testing.T) {
				m.height, m.width = h, w
				check(t, m)
			})
		}
	}

	t.Run("zoomed", func(t *testing.T) {
		m.height, m.width = 30, 120
		m.zoomed = true
		defer func() { m.zoomed = false }()
		check(t, m)
	})

	t.Run("search active adds one line to the overhead", func(t *testing.T) {
		m.height, m.width = 30, 120
		m.searching, m.searchQuery = true, "task"
		defer func() { m.searching, m.searchQuery = false, "" }()
		check(t, m)
	})
}
