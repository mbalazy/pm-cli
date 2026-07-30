package board

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSelectModeKeysWithAllColumnsHidden: same panic class as
// TestBoardKeysWithAllColumnsHidden (update_board_test.go) but for select
// mode - hiding every column leaves m.statuses and m.cursors empty with
// activeCol 0, and updateSelectMode indexed m.cursors[m.activeCol]
// unguarded on j/k/g.
func TestSelectModeKeysWithAllColumnsHidden(t *testing.T) {
	m := Model{
		statuses:      nil,
		cursors:       []int{},
		scrollOffsets: []int{},
		selecting:     true,
		selected:      make(map[string]bool),
		width:         80, height: 24,
	}

	msgs := []tea.KeyMsg{
		keyRunes('g'),
		keyRunes('G'),
		keyRunes('j'),
		keyRunes('k'),
		keyRunes('h'),
		keyRunes('l'),
	}
	for _, msg := range msgs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("key %q panicked: %v", msg.String(), r)
				}
			}()
			m.updateSelectMode(msg)
		}()
	}
}
