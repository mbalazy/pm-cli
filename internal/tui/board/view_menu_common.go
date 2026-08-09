package board

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// What the two launch overlays share: the box, the fit rules, and the one thing
// that turned out to matter most on screen - a STABLE WIDTH.
//
// The command preview is the only line whose length nobody controls: a worktree
// name is a slugified task title, so moving the cursor from "here" to the
// worktree row could double the longest line and the whole modal jumped wider
// under the hand. A menu that resizes as the cursor moves is harder to read
// than one with no preview at all, because the eye has to re-find every row.
//
// So the box is measured from the STRUCTURAL lines only - title, groups, items,
// chips, help, none of which change with the cursor - and the preview is
// wrapped into whatever width that gives, with its continuation lines indented
// under the command. The block is then padded to the tallest preview any item
// would produce, so neither dimension moves while navigating.

// menuBoxChrome is what the rounded border and its padding cost horizontally.
const menuBoxChrome = 2 + 2*3

// menuContentCap bounds the box regardless of terminal width, matching the
// detail view and project info. A 200-column terminal should not get a
// 200-column modal: prose and key lists stop being scannable long before that.
const menuContentCap = 100

// menuContentWidth is how wide one line inside the box may be.
func menuContentWidth(termWidth int) int {
	if termWidth <= 0 {
		return menuContentCap // no terminal size yet (tests, first frame)
	}
	w := termWidth - menuBoxChrome
	if w > menuContentCap {
		w = menuContentCap
	}
	if w < 24 {
		w = 24
	}
	return w
}

// menuBodyWidth is the width the preview wraps to: the widest structural line,
// so a short menu stays a short box and the preview never widens one.
func menuBodyWidth(structural []string, termWidth int) int {
	w := 0
	for _, l := range structural {
		if x := lipgloss.Width(l); x > w {
			w = x
		}
	}
	if cap := menuContentWidth(termWidth); w > cap {
		w = cap
	}
	if w < 24 {
		w = 24
	}
	return w
}

// previewIndent is "  → ", the arrow gutter continuation lines align under.
const previewIndent = "    "

// previewBlock wraps one command into lines of at most width cells, breaking
// between ARGUMENTS so a flag and its value are never split across lines, and
// indenting the continuations under the command itself. A single token longer
// than the width is left long rather than cut: it is a path or a branch name,
// and half of one is worse than a line that runs on.
func previewBlock(cmd string, width int) []string {
	body := width - lipgloss.Width(previewIndent)
	if body < 16 {
		body = 16
	}
	var out []string
	cur := ""
	for _, tok := range strings.Fields(cmd) {
		switch {
		case cur == "":
			cur = tok
		case lipgloss.Width(cur)+1+lipgloss.Width(tok) <= body:
			cur += " " + tok
		default:
			out = append(out, cur)
			cur = tok
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	if len(out) == 0 {
		return nil
	}
	for i := range out {
		if i == 0 {
			out[i] = "  → " + out[i]
		} else {
			out[i] = previewIndent + "  " + out[i]
		}
	}
	return out
}

// renderPreview styles a preview block and pads it to lines rows, so the box
// keeps its height as the cursor moves between a one-line command and a
// three-line one.
func renderPreview(cmd string, width, lines int, style lipgloss.Style) []string {
	block := previewBlock(cmd, width)
	out := make([]string, 0, lines)
	for _, l := range block {
		out = append(out, style.Render(l))
	}
	for len(out) < lines {
		out = append(out, "")
	}
	return out
}

// tallestPreview is how many rows the preview block must reserve: the most any
// item on this menu would need. Computed over every item rather than the
// selected one, which is the whole point - the reserved height cannot depend on
// where the cursor happens to be.
func tallestPreview(cmds []string, width int) int {
	n := 1
	for _, c := range cmds {
		if h := len(previewBlock(c, width)); h > n {
			n = h
		}
	}
	return n
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

// menuBoxFits reports whether these lines, once boxed, sit inside the terminal
// in BOTH directions. Height matters as much as width: lipgloss.Place centres
// the box, so an overlay two lines too tall loses a line off each end - and the
// bottom one is the help, which is where the way out is written.
func (m Model) menuBoxFits(lines []string) bool {
	if m.width <= 0 || m.height <= 0 {
		return true // no terminal size yet (tests, first frame)
	}
	widest := 0
	for _, l := range lines {
		if w := lipgloss.Width(l); w > widest {
			widest = w
		}
	}
	// The box costs 1 border + 1 padding row at the top and the same below.
	return widest+menuBoxChrome <= m.width && len(lines)+4 <= m.height
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

// joinTitle appends a group's subject to its name, dropping it when the layout
// has no room - the name alone still says which group this is.
func joinTitle(name, subject string, compact bool) string {
	if compact || subject == "" {
		return name
	}
	return name + "   " + subject
}
