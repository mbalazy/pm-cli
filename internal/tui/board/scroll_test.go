package board

import (
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// TestColumnGeometry covers pm-cli-72-2 point 4: viewBoard (view.go) and
// fixScrollOffsets (scroll.go) used to compute overhead/maxCardHeight/colWidth
// independently - a drift risk between render and scroll. Both now share
// columnGeometry(); this locks in its numbers directly.
func TestColumnGeometry(t *testing.T) {
	cases := []struct {
		name              string
		width, height     int
		statuses          []storage.TaskStatus
		zoomed            bool
		adding, searching bool
		searchQuery       string
		wantMaxCardHeight int
		wantColWidth      int
	}{
		{
			name:  "baseline: 4 columns, no active filter",
			width: 120, height: 40,
			statuses:          []storage.TaskStatus{storage.StatusTodo, storage.StatusDoing, storage.StatusWaiting, storage.StatusDone},
			wantMaxCardHeight: 29, // 40 - 11
			wantColWidth:      28, // (120-8)/4
		},
		{
			name:  "search query active adds 1 to overhead",
			width: 120, height: 40,
			statuses:          []storage.TaskStatus{storage.StatusTodo, storage.StatusDoing},
			searchQuery:       "foo",
			wantMaxCardHeight: 28, // 40 - 12
			wantColWidth:      56, // (120-8)/2
		},
		{
			name:  "adding mode adds 1 to overhead",
			width: 120, height: 40,
			statuses:          []storage.TaskStatus{storage.StatusTodo},
			adding:            true,
			wantMaxCardHeight: 28,
			wantColWidth:      112, // (120-8)/1
		},
		{
			name:  "zoomed collapses to a single full-width column",
			width: 120, height: 40,
			statuses:          []storage.TaskStatus{storage.StatusTodo, storage.StatusDoing, storage.StatusWaiting, storage.StatusDone},
			zoomed:            true,
			wantMaxCardHeight: 29,
			wantColWidth:      112, // (120-8)/1, NOT /4
		},
		{
			name:  "tiny terminal clamps maxCardHeight to 5",
			width: 120, height: 10,
			statuses:          []storage.TaskStatus{storage.StatusTodo},
			wantMaxCardHeight: 5,
			wantColWidth:      112,
		},
		{
			name:  "narrow terminal clamps colWidth to 20",
			width: 40, height: 40,
			statuses:          []storage.TaskStatus{storage.StatusTodo, storage.StatusDoing, storage.StatusWaiting, storage.StatusDone},
			wantMaxCardHeight: 29,
			wantColWidth:      20,
		},
		{
			name:  "no columns still yields a single-column width",
			width: 120, height: 40,
			statuses:          nil,
			wantMaxCardHeight: 29,
			wantColWidth:      112,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := Model{
				width: c.width, height: c.height,
				statuses:    c.statuses,
				zoomed:      c.zoomed,
				adding:      c.adding,
				searching:   c.searching,
				searchQuery: c.searchQuery,
			}
			gotMaxCardHeight, gotColWidth := m.columnGeometry()
			if gotMaxCardHeight != c.wantMaxCardHeight {
				t.Errorf("maxCardHeight = %d, want %d", gotMaxCardHeight, c.wantMaxCardHeight)
			}
			if gotColWidth != c.wantColWidth {
				t.Errorf("colWidth = %d, want %d", gotColWidth, c.wantColWidth)
			}
		})
	}
}
