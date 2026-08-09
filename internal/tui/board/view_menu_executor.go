package board

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mbalazy/pm/internal/storage"
)

// The executor launch overlay.
//
// It renders four toggles and up to six launches, and the flat list it used to
// share with the Claude/Codex menu could not carry that: nothing said which
// toggle touched which launch, so every line explained itself in prose
// ("--then-finish; off = tracker's finish_mode decides") and the whole overlay
// had to be read top to bottom before pressing anything. Three things replace
// the prose.
//
//  1. The launches are GROUPED - run, accept, preview - because "start the
//     work" and "judge work that already happened" are different decisions that
//     happened to share a keyboard.
//  2. The toggles are one row of chips, and a chip that does not affect the
//     HIGHLIGHTED launch is dimmed. That is the question the prose was trying
//     to answer, asked the other way round: not "what does this flag mean" but
//     "does it apply to the thing I am about to press".
//  3. The exact argv is previewed. A flag's effect stops being something the
//     label has to describe once the command is on screen - and the tri-state
//     ones (an absent `--then-finish` is not the same as `--then-finish=false`)
//     read correctly for the first time, because absence is what is shown.

// execToggleChip is one of the overlay's per-launch choices.
type execToggleChip struct {
	key   string
	label string
	on    bool
	// applies is whether this chip changes the HIGHLIGHTED launch. A chip that
	// does not is dimmed rather than hidden: it still holds state, and hiding it
	// on cursor movement would make the row jump.
	applies bool
	note    string
}

// executorSectionOf groups a launch kind. Deliberately derived from the kind
// rather than stored on the item, so the ordering of claudeMenuItems - which
// the cursor and the shortcut keys both depend on - stays the one source of
// order and this stays presentation.
func executorSectionOf(kind string) string {
	switch kind {
	case "bg", "here", "tmux":
		return "run"
	case "finish", "finish-tmux":
		return "accept"
	default:
		return "preview"
	}
}

// executorItemHint is the dim half-sentence after a launch's name: what makes
// this variant different from the one above it.
func (m Model) executorItemHint(kind string) string {
	switch kind {
	case "bg":
		return "detached · watch it with W"
	case "here":
		return "takes over this terminal"
	case "tmux":
		return "new tmux window"
	case "finish":
		return "never touches a runtime · watch it with W"
	case "finish-tmux":
		if m.executorSimAvail {
			return "watchable · the only one that can drive the simulator"
		}
		return "watchable"
	case "dry-run":
		return "prints the plan, spends nothing"
	}
	return ""
}

// executorToggleChips builds the toggle row for the launch under the cursor.
func (m Model) executorToggleChips(compact bool) []execToggleChip {
	kind := ""
	if m.claudeMenuCursor < len(m.claudeMenuItems) {
		kind = m.claudeMenuItems[m.claudeMenuCursor].kind
	}
	isFinish := isFinishKind(kind)

	chips := []execToggleChip{{
		key:   "!",
		label: "yolo",
		on:    m.claudeMenuSkipPerms,
		// The acceptance is yolo by definition (see finishPMArgs), so this
		// toggle is not merely ignored there - it would be a lie.
		applies: !isFinish,
	}}
	if m.executorAdditionalAvail {
		chips = append(chips, execToggleChip{
			key: "#", label: chipLabel(compact, "own worktree slot", "slot"),
			on: m.claudeMenuAdditional, applies: true,
		})
	}
	if m.executorIsTracker {
		note := ""
		if !m.claudeMenuThenFinish && m.menuTask() != nil && m.menuTask().Meta.FinishMode == storage.FinishModeAuto {
			// The one thing the argv preview cannot show: an absent
			// --then-finish leaves the decision to the tracker, and this
			// tracker's answer is yes.
			note = "the tracker's finish_mode already chains one"
		}
		chips = append(chips, execToggleChip{
			key: "&", label: chipLabel(compact, "chain accept after", "chain"),
			on: m.claudeMenuThenFinish, applies: !isFinish, note: note,
		})
	}
	if m.executorIsTracker && m.executorSimAvail {
		note := ""
		applies := kind == "finish-tmux"
		if !m.menuHasKind("finish-tmux") {
			note = "needs tmux - a detached acceptance never touches a runtime"
			applies = false
		}
		chips = append(chips, execToggleChip{
			key: "$", label: chipLabel(compact, "simulator", "sim"),
			on: m.claudeMenuSim, applies: applies, note: note,
		})
	}
	return chips
}

// viewExecutorMenu renders the overlay, retrying COMPACT when the wide layout
// would not fit the terminal. Which layout fits is decided by measuring the
// rendered lines rather than by a width threshold: the content varies with the
// project (slots, a runtime skill) and with the noun, so a constant would be
// wrong for somebody. Nothing that decides anything is dropped in compact -
// only the hints and the long halves of the chip labels, which are teaching
// aids for a first read.
func (m Model) viewExecutorMenu() string {
	if body, ok := m.executorMenuBody(false); ok {
		return m.placeMenuBox(body)
	}
	body, _ := m.executorMenuBody(true)
	return m.placeMenuBox(body)
}

// placeMenuBox is the shared chrome: one rounded box, centred.
func (m Model) placeMenuBox(lines []string) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// executorMenuBoxChrome is what the border and padding cost horizontally.
const executorMenuBoxChrome = 2 + 2*3

// executorMenuBody builds the overlay's lines and reports whether they fit the
// terminal's width.
func (m Model) executorMenuBody(compact bool) ([]string, bool) {
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(highlight)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})
	onStyle := lipgloss.NewStyle().Bold(true).Foreground(special)
	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))

	t := m.menuTask()
	noun := "task"
	cmd := "pm work"
	if m.executorIsTracker {
		noun = trackerRunNoun(t)
		cmd = "pm run-epic"
	}

	id := ""
	if t != nil {
		id = t.Meta.ID
	}
	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Run "+noun)+
		"  "+dimStyle.Render(id))
	lines = append(lines, "")

	// The names are padded to one column so the hints line up: the hints are
	// what the reader scans to choose, and ragged ones read as prose.
	nameW := 0
	for _, item := range m.claudeMenuItems {
		if w := lipgloss.Width(item.label); w > nameW {
			nameW = w
		}
	}

	// --- the launches, grouped ---
	sectionTitle := map[string]string{
		"run":    strings.ToUpper(noun) + "  " + "(" + cmd + ")",
		"accept": "ACCEPT  (pm finish - starts no " + noun + "; fine before, during or after one)",
	}
	if compact {
		sectionTitle["accept"] = "ACCEPT  (pm finish - starts no " + noun + ")"
	}
	lastSection := ""
	for i, item := range m.claudeMenuItems {
		sec := executorSectionOf(item.kind)
		if sec != lastSection {
			if lastSection != "" {
				lines = append(lines, "")
			}
			if title := sectionTitle[sec]; title != "" {
				lines = append(lines, "  "+helpStyle.Render(title))
			}
			lastSection = sec
		}
		prefix := "    "
		labelStyle := dimStyle
		if i == m.claudeMenuCursor {
			prefix = "  > "
			labelStyle = onStyle
		}
		row := prefix + keyStyle.Render(item.shortcut) + "  " + labelStyle.Render(padRight(item.label, nameW))
		if hint := m.executorItemHint(item.kind); hint != "" && !compact {
			row += "   " + helpStyle.Render(hint)
		}
		lines = append(lines, row)
	}
	lines = append(lines, "")

	// --- the toggles, as one row of chips ---
	chips := m.executorToggleChips(compact)
	var parts []string
	var notes []string
	for _, c := range chips {
		box := "[ ]"
		if c.on {
			box = "[x]"
		}
		text := box + " " + c.key + " " + c.label
		switch {
		case !c.applies:
			// Dimmest: the state is still true, it just does not reach the
			// launch under the cursor.
			parts = append(parts, helpStyle.Render(text))
		case c.on:
			parts = append(parts, onStyle.Render(text))
		default:
			parts = append(parts, dimStyle.Render(text))
		}
		if c.note != "" {
			notes = append(notes, c.key+" "+c.note)
		}
	}
	lines = append(lines, "  "+strings.Join(parts, "   "))
	for _, n := range notes {
		lines = append(lines, "      "+helpStyle.Render(n))
	}

	// The slot table only when a slot is actually being claimed. Which slot a
	// run gets is worth three lines at the moment you ask for one, and noise
	// every other time.
	if m.claudeMenuAdditional && m.executorAdditionalAvail {
		freeStyle := lipgloss.NewStyle().Foreground(special)
		allBusy := true
		for i, s := range m.executorSlots {
			name := filepath.Base(s.path)
			if s.holder == nil {
				allBusy = false
				lines = append(lines, dimStyle.Render(fmt.Sprintf("      slot %d  %s  ", i+1, name))+freeStyle.Render("○ free"))
			} else {
				lines = append(lines, dimStyle.Render(fmt.Sprintf("      slot %d  %s  ", i+1, name))+
					warnStyle.Render(fmt.Sprintf("● busy: %s (pid %d)", s.holder.TaskID, s.holder.PID)))
			}
		}
		if allBusy {
			lines = append(lines, "      "+warnStyle.Bold(true).Render("all slots busy - this launch will fail; wait or kill a run (K)"))
		}
	}

	// --- what pressing the highlighted key actually runs ---
	if t != nil && m.claudeMenuCursor < len(m.claudeMenuItems) {
		args := m.executorLaunchArgs(t, m.executorIsTracker, m.claudeMenuItems[m.claudeMenuCursor].kind)
		lines = append(lines, "")
		lines = append(lines, "  "+dimStyle.Render("→ pm "+strings.Join(args, " ")))
	}

	lines = append(lines, "")
	help := "  ↑↓ move · enter or its key runs it · @ switch to claude/codex · esc back"
	if compact {
		help = "  ↑↓ move · enter runs · @ agent · esc back"
	}
	lines = append(lines, helpStyle.Render(help))

	widest := 0
	for _, l := range lines {
		if w := lipgloss.Width(l); w > widest {
			widest = w
		}
	}
	return lines, m.width <= 0 || widest+executorMenuBoxChrome <= m.width
}

// padRight pads s to w display cells (never truncates - a name longer than the
// column widens the column, it does not lose characters).
func padRight(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// chipLabel picks a toggle's long or short name.
func chipLabel(compact bool, long, short string) string {
	if compact {
		return short
	}
	return long
}
