package board

import (
	tea "github.com/charmbracelet/bubbletea"
)

// overlaySpec is one modal layer floating above the base views (board, detail,
// project-info, archive, focus, executor): the flag that opens it, the renderer
// that draws it, and the handler that owns the keyboard while it is up.
type overlaySpec struct {
	name   string
	open   func(Model) bool
	view   func(Model) string
	update func(Model, tea.KeyMsg) (tea.Model, tea.Cmd)
}

// overlayLadder is THE single source of overlay precedence. Both the render
// path (View) and the key-dispatch path (Update) resolve the active overlay
// through it, so an overlay is drawn by exactly the layer that is also eating
// the keys. Before this, View and updateDetail each hand-coded their own
// if-ladder and the two DISAGREED (View checked the launch menu first,
// updateDetail the subtask picker), and adding an overlay meant editing every
// ladder in the right order.
//
// ORDER (first match wins). An overlay that can be opened ON TOP of another
// must outrank it, or the one underneath would keep eating the keys:
//
//  1. launch menu (claude/codex/executor) - opens over every base view. The
//     session menu clears itself before opening it, so it never has to lose
//     to one; this also resolves the old View-vs-updateDetail disagreement in
//     View's favour (what the user sees is what handles the keys).
//  2. yank menu - openable from a LIVE session menu and from the project
//     picker (both stay set underneath), so it MUST outrank them.
//  3. links menu - same class as yank: a transient leaf, nothing stacks on it.
//  4. session menu - sits under yank (2) and opens the launch menu (1).
//  5. subtask picker - detail-scoped leaf, opens no other overlay.
//  6. column-visibility menu, 7. project picker, 8. help - board-scoped leaves.
//
// Overlays always win over the base views. That is not a new rule: every
// overlay is reachable only from a view that already dispatched to it, and the
// ladders this table replaces resolved that way in each reachable combination.
var overlayLadder = []overlaySpec{
	{
		name:   "launch-menu",
		open:   func(m Model) bool { return m.claudeMenu },
		view:   Model.viewClaudeMenu,
		update: Model.updateClaudeMenu,
	},
	{
		name:   "yank-menu",
		open:   func(m Model) bool { return m.yankMenu },
		view:   Model.viewYankMenu,
		update: Model.updateYankMenu,
	},
	{
		name:   "links-menu",
		open:   func(m Model) bool { return m.linksMenu },
		view:   Model.viewLinksMenu,
		update: Model.updateLinksMenu,
	},
	{
		name:   "session-menu",
		open:   func(m Model) bool { return m.sessionMenu },
		view:   Model.viewSessionMenu,
		update: Model.updateSessionMenu,
	},
	{
		name:   "subtask-picker",
		open:   func(m Model) bool { return m.subtaskPicker },
		view:   Model.viewSubtaskPicker,
		update: Model.updateSubtaskPicker,
	},
	{
		name:   "column-visibility",
		open:   func(m Model) bool { return m.colVisMenu },
		view:   Model.viewColVisMenu,
		update: Model.updateColVisMenu,
	},
	{
		name:   "project-picker",
		open:   func(m Model) bool { return m.projectPicker },
		view:   Model.viewProjectPicker,
		update: Model.updateProjectPicker,
	},
	{
		name:   "help",
		open:   func(m Model) bool { return m.showHelp },
		view:   Model.viewHelp,
		update: Model.updateHelp,
	},
}

// activeOverlay returns the highest-precedence open overlay, or nil when the
// base view owns the screen and the keyboard.
func (m Model) activeOverlay() *overlaySpec {
	for i := range overlayLadder {
		if overlayLadder[i].open(m) {
			return &overlayLadder[i]
		}
	}
	return nil
}
