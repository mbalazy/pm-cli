package board

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// The executor overlay's layout contract. What is asserted here is not wording
// but the three things that make a dense menu readable: launches are grouped by
// what they DO, each one previews the command it would run, and a toggle says
// whether it reaches the launch under the cursor.

// The argv preview is the overlay's answer to "what will this actually do", so
// it has to track the cursor and every toggle - a preview that lags either is
// worse than none.
func TestThePreviewFollowsTheCursorAndTheToggles(t *testing.T) {
	os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	defer os.Unsetenv("TMUX")
	m := trackerModel(t)
	m.openExecutorMenu(m.taskByID("p-9"))

	preview := func(m *Model) string { return menuPreview(m) }

	if got := preview(m); got != "pm run-epic p p-9" {
		t.Errorf("preview on the run = %q", got)
	}

	// A toggle changes it in place, without moving the cursor.
	next, _ := m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	v := next.(Model)
	if got := preview(&v); got != "pm run-epic p p-9 --yolo" {
		t.Errorf("preview after ! = %q", got)
	}
	next, _ = v.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("&")})
	v = next.(Model)
	if got := preview(&v); !strings.HasSuffix(got, "--then-finish") {
		t.Errorf("preview after & = %q", got)
	}

	// Moving onto an acceptance shows a different COMMAND, which is the whole
	// reason the two are in separate groups.
	for i, item := range v.claudeMenuItems {
		if item.kind == "finish" {
			v.claudeMenuCursor = i
		}
	}
	if got := preview(&v); got != "pm finish p-9 --project p --no-sim" {
		t.Errorf("preview on the acceptance = %q", got)
	}
}

// A toggle that cannot reach the highlighted launch is dimmed rather than
// hidden - it still holds state, and hiding it would make the row jump as the
// cursor moves. Dimmed means "rendered through helpStyle", which is the one
// thing separating it from an armable chip, so the test reads the escape codes
// rather than the text.
func TestAToggleThatDoesNotApplyIsDimmed(t *testing.T) {
	os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	defer os.Unsetenv("TMUX")
	m := trackerModel(t)
	m.openExecutorMenu(m.taskByID("p-9"))

	chipApplies := func(m *Model, key string) bool {
		for _, c := range m.executorToggleChips(false) {
			if c.key == key {
				return c.applies
			}
		}
		t.Fatalf("no %q chip on the menu", key)
		return false
	}

	// Cursor on a run: yolo and the chain toggle both reach it.
	if !chipApplies(m, "!") || !chipApplies(m, "&") {
		t.Error("on a run launch, ! and & both apply")
	}
	// Cursor on the acceptance: neither does. `pm finish` is yolo by definition
	// and chains nothing, so an armed chip there would be a lie about the argv.
	for i, item := range m.claudeMenuItems {
		if item.kind == "finish" {
			m.claudeMenuCursor = i
		}
	}
	if chipApplies(m, "!") || chipApplies(m, "&") {
		t.Error("on an acceptance, neither ! nor & reaches the command")
	}
	// And it is visibly different, not just different in the model.
	out := m.viewClaudeMenu()
	dim := strings.Contains(out, helpStyle.Render("[ ] ! yolo"))
	if !dim {
		t.Errorf("the inapplicable chip is not rendered dim:\n%s", stripANSI(out))
	}
}

// The launches are grouped, and the acceptance group carries what used to be
// repeated on both of its items: it starts no run, and it may be run at any
// point relative to one.
func TestTheLaunchesAreGroupedByWhatTheyDo(t *testing.T) {
	os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	defer os.Unsetenv("TMUX")
	m := trackerModel(t)
	m.openExecutorMenu(m.taskByID("p-9"))
	out := stripANSI(m.viewClaudeMenu())

	runHdr := strings.Index(out, "EPIC  (pm run-epic)")
	accHdr := strings.Index(out, "ACCEPT  (pm finish")
	if runHdr < 0 || accHdr < 0 {
		t.Fatalf("the two groups are not both headed:\n%s", out)
	}
	if runHdr > accHdr {
		t.Error("the run group comes first - it is what x is normally pressed for")
	}
	// Every launch of the run group sits between the two headers.
	for _, name := range []string{"background", "here", "tmux window"} {
		if i := strings.Index(out, name); i < runHdr || i > accHdr {
			t.Errorf("%q is not inside the run group:\n%s", name, out)
		}
	}
	for _, want := range []string{"starts no epic", "before, during or after"} {
		if !strings.Contains(out, want) {
			t.Errorf("the acceptance group is missing %q:\n%s", want, out)
		}
	}
}

// A narrow terminal gets a layout that FITS. What is dropped is the teaching
// half - the hints and the long chip names - never a key, a group or the
// preview: an overlay whose border runs off the right edge is the state this
// whole pass was meant to end.
func TestANarrowTerminalGetsTheCompactLayout(t *testing.T) {
	os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	defer os.Unsetenv("TMUX")
	m := trackerModel(t)
	m.openExecutorMenu(m.taskByID("p-9"))
	m.width, m.height = 80, 24

	out := stripANSI(m.viewClaudeMenu())
	for _, line := range strings.Split(out, "\n") {
		if w := len([]rune(strings.TrimRight(line, " "))); w > m.width {
			t.Fatalf("a %d-cell line in an %d-column terminal:\n%s", w, m.width, out)
		}
	}
	// Everything you can PRESS survives, and so does the preview.
	for _, want := range []string{"b  background", "a  now, detached", "A  now, in a tmux", "d  dry-run",
		"ACCEPT", "! yolo", "& chain", "→ pm run-epic"} {
		if !strings.Contains(out, want) {
			t.Errorf("compact dropped %q, which is not a hint:\n%s", want, out)
		}
	}
	if strings.Contains(out, "takes over this terminal") {
		t.Errorf("compact must drop the hints - they are what does not fit:\n%s", out)
	}
}

// The slot table is three lines that answer "which slot will this get". Worth
// the space at the moment a slot is being claimed, noise every other time.
func TestTheSlotTableAppearsOnlyWhenASlotIsBeingClaimed(t *testing.T) {
	m := trackerModel(t)
	m.openExecutorMenu(m.taskByID("p-9"))
	m.executorAdditionalAvail = true
	m.executorSlots = []executorSlotStatus{{path: "/tmp/wt1"}, {path: "/tmp/wt2"}}

	if out := stripANSI(m.viewClaudeMenu()); strings.Contains(out, "slot 1") {
		t.Errorf("the slot table is up before a slot was asked for:\n%s", out)
	}
	m.claudeMenuAdditional = true
	out := stripANSI(m.viewClaudeMenu())
	for _, want := range []string{"slot 1", "slot 2", "○ free"} {
		if !strings.Contains(out, want) {
			t.Errorf("the slot table is missing %q:\n%s", want, out)
		}
	}
}

// The one fact the argv preview cannot show: an ABSENT --then-finish is not
// "no acceptance", it is "the tracker decides" - and this tracker says yes.
func TestAnAutoChainingTrackerSaysSoWhereTheToggleIsOff(t *testing.T) {
	m := newBoardModel(t,
		&storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Tracker", Status: storage.StatusDoing, FinishMode: storage.FinishModeAuto}},
		&storage.Task{Meta: storage.TaskMeta{ID: "p-9-1", Title: "Sub", Status: storage.StatusTodo, Parent: "p-9"}},
	)
	m.currentView = viewDetail
	m.openDetailTask(m.taskByID("p-9"))
	m.width, m.height = 130, 40
	m.openExecutorMenu(m.taskByID("p-9"))

	out := stripANSI(m.viewClaudeMenu())
	if strings.Contains(out, "--then-finish") {
		t.Errorf("the flag is not being passed, so it must not be in the preview:\n%s", out)
	}
	if !strings.Contains(out, "finish_mode already chains one") {
		t.Errorf("an auto-chaining tracker must say so where the toggle reads off:\n%s", out)
	}

	// Armed, the note goes away and the flag appears - the two never both show.
	next, _ := m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("&")})
	out = stripANSI(next.(Model).viewClaudeMenu())
	if !strings.Contains(out, "--then-finish") || strings.Contains(out, "already chains one") {
		t.Errorf("armed, the preview carries the flag and the note is gone:\n%s", out)
	}
}

// A leaf task runs `pm work` and has no acceptance at all, so it gets neither
// the accept group nor the toggles that only exist for a tracker.
func TestALeafTaskGetsNoAcceptanceGroup(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Leaf", Status: storage.StatusTodo}})
	m.currentView = viewDetail
	m.openDetailTask(m.taskByID("p-1"))
	m.width, m.height = 130, 40
	m.openExecutorMenu(m.taskByID("p-1"))

	out := stripANSI(m.viewClaudeMenu())
	for _, unwanted := range []string{"ACCEPT", "chain accept", "$ simulator"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a leaf task must not be offered %q:\n%s", unwanted, out)
		}
	}
	for _, want := range []string{"TASK  (pm work)", "→ pm work p p-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the leaf menu is missing %q:\n%s", want, out)
		}
	}
}
