package board

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
)

// overlayModel builds a Model with the given overlay flag(s) applied, sized so
// every overlay renderer has room, and backed by a real (empty) store because
// some overlay handlers reload on escape.
func overlayModel(t *testing.T, apply func(*Model)) Model {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	if err := store.CreateProject("p", &storage.Project{Name: "P"}); err != nil {
		t.Fatal(err)
	}
	m := Model{
		store:          store,
		projects:       []string{"all", "p"},
		statuses:       []storage.TaskStatus{storage.StatusTodo},
		cursors:        []int{0},
		scrollOffsets:  []int{0},
		hiddenStatuses: make(map[storage.TaskStatus]bool),
		hiddenProjects: make(map[string]bool),
		selected:       make(map[string]bool),
		focusSet:       make(map[string]bool),
		width:          80,
		height:         24,
	}
	apply(&m)
	return m
}

// setOverlay opens the named overlay on m (the flag only - contents are set up
// by the openX helpers in production, which is not what this file tests).
func setOverlay(m *Model, name string) {
	switch name {
	case "launch-menu":
		m.claudeMenu = true
		m.claudeMenuItems = []claudeMenuItem{{"Here", "here", "h"}}
	case "yank-menu":
		m.yankMenu = true
		m.yankItems = []yankItem{{"id", "p-1"}}
	case "links-menu":
		m.linksMenu = true
		m.linkItems = []linkItem{{"pr", "https://example.com"}}
	case "session-menu":
		m.sessionMenu = true
		m.sessionMenuItems = []sessionMenuItem{{sessionID: "abc"}}
	case "subtask-picker":
		m.subtaskPicker = true
		m.subtaskItems = []*storage.Task{{Meta: storage.TaskMeta{ID: "p-2", Title: "kid"}}}
	case "column-visibility":
		m.colVisMenu = true
		m.colVisItems = []colVisItem{{status: storage.StatusTodo, visible: true}}
	case "project-picker":
		m.projectPicker = true
		m.pickerItems = []pickerItem{{slug: "p", name: "P"}}
	case "help":
		m.showHelp = true
	default:
		panic("unknown overlay " + name)
	}
}

// isOpen reports whether the named overlay's flag is still set.
func isOpen(m Model, name string) bool {
	for _, spec := range overlayLadder {
		if spec.name == name {
			return spec.open(m)
		}
	}
	panic("unknown overlay " + name)
}

// TestActiveOverlayResolvesEachFlag: every ladder entry must react to its own
// flag and only its own - a copy-pasted predicate would make two entries fight
// over one flag and the entry below it unreachable.
func TestActiveOverlayResolvesEachFlag(t *testing.T) {
	for _, spec := range overlayLadder {
		t.Run(spec.name, func(t *testing.T) {
			m := overlayModel(t, func(m *Model) { setOverlay(m, spec.name) })
			got := m.activeOverlay()
			if got == nil {
				t.Fatalf("%s is open but activeOverlay returned nil", spec.name)
			}
			if got.name != spec.name {
				t.Errorf("activeOverlay = %q, want %q", got.name, spec.name)
			}
		})
	}
	t.Run("none open", func(t *testing.T) {
		m := overlayModel(t, func(*Model) {})
		if got := m.activeOverlay(); got != nil {
			t.Errorf("activeOverlay = %q with nothing open, want nil", got.name)
		}
	})
}

// TestOverlayPrecedence pins the documented order for the combinations that can
// actually occur: the yank menu opens ON TOP of a live session menu and of the
// project picker (both stay set underneath), and the launch menu wins over the
// subtask picker - the case where View and updateDetail used to disagree.
func TestOverlayPrecedence(t *testing.T) {
	cases := []struct {
		name string
		open []string
		want string
	}{
		{"yank over session menu", []string{"session-menu", "yank-menu"}, "yank-menu"},
		{"yank over project picker", []string{"project-picker", "yank-menu"}, "yank-menu"},
		{"launch menu over subtask picker", []string{"subtask-picker", "launch-menu"}, "launch-menu"},
		{"launch menu over help", []string{"help", "launch-menu"}, "launch-menu"},
		{"links over session menu", []string{"session-menu", "links-menu"}, "links-menu"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := overlayModel(t, func(m *Model) {
				for _, o := range c.open {
					setOverlay(m, o)
				}
			})
			got := m.activeOverlay()
			if got == nil || got.name != c.want {
				t.Fatalf("activeOverlay = %v, want %q", got, c.want)
			}
		})
	}
}

// TestViewRendersActiveOverlay: View must draw the overlay the ladder resolves,
// from every base view - including the detail view, whose renderer used to
// re-encode its own overlay order.
func TestViewRendersActiveOverlay(t *testing.T) {
	views := []view{viewBoard, viewDetail, viewArchive, viewFocus, viewExecutor}
	for _, spec := range overlayLadder {
		for _, v := range views {
			m := overlayModel(t, func(m *Model) {
				setOverlay(m, spec.name)
				m.currentView = v
				m.detailTask = &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "T"}}
			})
			if got, want := m.View(), spec.view(m); got != want {
				t.Errorf("view %d with %s open: View() did not render the overlay", v, spec.name)
			}
		}
	}
}

// TestUpdateDispatchesToActiveOverlay: the layer being drawn is the layer
// eating the keys. Escape closes every overlay, so it is the one key that
// proves which handler received the message.
func TestUpdateDispatchesToActiveOverlay(t *testing.T) {
	for _, spec := range overlayLadder {
		t.Run(spec.name, func(t *testing.T) {
			m := overlayModel(t, func(m *Model) {
				setOverlay(m, spec.name)
				m.currentView = viewDetail
				m.detailTask = &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "T"}}
			})
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			if isOpen(next.(Model), spec.name) {
				t.Errorf("esc did not reach the %s handler (overlay still open)", spec.name)
			}
		})
	}
}

// TestUpdateStackedOverlayKeepsLowerLayer: with the yank menu open on top of
// the project picker, esc must close only the yank menu - the picker stays up.
// updateProjectPicker used to hand-check this itself; now the ladder does.
func TestUpdateStackedOverlayKeepsLowerLayer(t *testing.T) {
	m := overlayModel(t, func(m *Model) {
		setOverlay(m, "project-picker")
		setOverlay(m, "yank-menu")
	})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(Model)
	if got.yankMenu {
		t.Error("esc should close the yank menu")
	}
	if !got.projectPicker {
		t.Error("the project picker underneath must stay open")
	}
}

// TestUpdateWithoutOverlayReachesBaseView: with nothing open the base view
// still owns the keys (the ladder must not swallow input).
func TestUpdateWithoutOverlayReachesBaseView(t *testing.T) {
	m := overlayModel(t, func(m *Model) { m.currentView = viewArchive })
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := next.(Model).currentView; got != viewBoard {
		t.Errorf("esc in the archive view should return to the board, got view %d", got)
	}
}

// TestHelpSearchStaysInsideHelpOverlay: helpSearch is a mode within the help
// overlay, not a ladder entry - "/" must open it and esc must close the mode
// while the overlay stays up.
func TestHelpSearchStaysInsideHelpOverlay(t *testing.T) {
	m := overlayModel(t, func(m *Model) {
		setOverlay(m, "help")
		m.helpInput = textinput.New() // New() does this; a zero textinput panics on Focus
	})
	next, _ := m.Update(keyMsg("/"))
	searching := next.(Model)
	if !searching.helpSearch {
		t.Fatal(`"/" should start the help filter`)
	}
	if !searching.showHelp {
		t.Fatal("the help overlay must stay open while filtering")
	}
	back, _ := searching.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if back.(Model).helpSearch {
		t.Error("esc should leave the help filter mode")
	}
	if !back.(Model).showHelp {
		t.Error("esc in the filter mode must not close the help overlay")
	}
}
