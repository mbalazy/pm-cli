package board

import (
	"os"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// What the detail view TELLS you about the executor. X is the one key for all
// three launches (`pm work`, `pm run-epic`, and the acceptance under [a]), and
// which one a tracker gets is decided by its frontmatter rather than by the
// key - so the view has to name both the key and the mode, or the key is
// reachable only by already knowing it.

func TestSubtaskHintNamesTheRunTheTrackerWouldActuallyGet(t *testing.T) {
	cases := []struct {
		name     string
		epicMode string
		width    int
		want     []string
		notWant  []string
	}{
		{
			name:     "integration tracker",
			epicMode: "",
			width:    100,
			want:     []string{"p: pick & open", "X: run epic", "X→a: accept"},
			notWant:  []string{"run batch"},
		},
		{
			name:     "independent tracker",
			epicMode: storage.EpicModeIndependent,
			width:    100,
			want:     []string{"p: pick & open", "X: run batch", "X→a: accept"},
			notWant:  []string{"run epic"},
		},
		{
			// Narrow: the keys survive, the prose does not - the legend sits
			// one line above a table already clamped to this width.
			name:     "narrow terminal keeps the keys",
			epicMode: storage.EpicModeIndependent,
			width:    narrowHintWidth - 1,
			want:     []string{"p: pick", "X: run batch"},
			notWant:  []string{"pick & open", "X→a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := &storage.Task{Meta: storage.TaskMeta{ID: "p-9", EpicMode: tc.epicMode}}
			got := subtaskHint(parent, tc.width)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("hint %q is missing %q", got, w)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("hint %q must not contain %q", got, w)
				}
			}
		})
	}
}

// The legend and the menu one keypress later must agree. The hint promising
// "X: run batch" over an overlay titled "Run epic" would make the new legend
// worse than none: it would be teaching the wrong word for the thing.
func TestTheHintAndTheLaunchMenuAgreeOnTheWord(t *testing.T) {
	for _, tc := range []struct{ epicMode, noun, other string }{
		{"", "epic", "batch"},
		{storage.EpicModeIndependent, "batch", "epic"},
	} {
		t.Run(tc.noun, func(t *testing.T) {
			m := newBoardModel(t,
				&storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Tracker", Status: storage.StatusDoing, EpicMode: tc.epicMode}},
				&storage.Task{Meta: storage.TaskMeta{ID: "p-9-1", Title: "Sub one", Status: storage.StatusTodo, Parent: "p-9"}},
			)
			tracker := m.taskByID("p-9")
			if got := subtaskHint(tracker, 100); !strings.Contains(got, "X: run "+tc.noun) {
				t.Errorf("hint = %q, want the %s wording", got, tc.noun)
			}
			m.currentView = viewDetail
			m.openDetailTask(tracker)
			m.openExecutorMenu(tracker)
			menu := stripANSI(m.viewClaudeMenu())
			if !strings.Contains(menu, "Run "+tc.noun+" (pm run-epic)") {
				t.Errorf("menu title does not say %q:\n%s", tc.noun, menu)
			}
			if strings.Contains(menu, "Run "+tc.other+" in background") {
				t.Errorf("menu offers the %s wording for a %s tracker:\n%s", tc.other, tc.noun, menu)
			}
			// The acceptance is the same key path in both modes - that is the
			// whole reason the hint spells it X→a rather than a key of its own.
			if !strings.Contains(menu, "[a] Run acceptance in background") {
				t.Errorf("menu is missing the acceptance item:\n%s", menu)
			}
		})
	}
}

// A tracker with no epic_mode at all still gets a legend: empty is a legal
// value meaning integration, not an absence.
func TestSubtaskHintHandlesANilParent(t *testing.T) {
	if got := subtaskHint(nil, 100); !strings.Contains(got, "X: run epic") {
		t.Errorf("hint for an unknown parent = %q, want the integration wording", got)
	}
}

// The rendered table carries the legend, so the plumbing from renderTaskDetail
// through to the header is covered and not just the pure function.
func TestSubtaskTableRendersTheHint(t *testing.T) {
	m := newBoardModel(t,
		&storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing, EpicMode: storage.EpicModeIndependent}},
		&storage.Task{Meta: storage.TaskMeta{ID: "p-9-1", Title: "Sub one", Status: storage.StatusTodo, Parent: "p-9"}},
	)
	tracker := m.taskByID("p-9")
	if tracker == nil {
		t.Fatal("tracker not loaded")
	}
	detail := m.renderTaskDetail(tracker)
	for _, want := range []string{"Subtasks (0/1)", "X: run batch"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail is missing %q:\n%s", want, detail)
		}
	}
}

// The two fields that decide what X launches are printed for a tracker, in both
// their set and their unset spelling - an omitted line would read as "does not
// apply" for a mode that is very much in force.
func TestDetailHeadNamesTheExecutorModes(t *testing.T) {
	cases := []struct {
		name       string
		epicMode   string
		finishMode string
		want       []string
	}{
		{
			name: "defaults are spelled out, not blank",
			// Fragments rather than whole labels: glamour word-wraps the head
			// to the content width, so an assertion spanning the wrap point
			// would be testing the terminal width, not the wording.
			want: []string{"Epic mode:", "epic (integration)", "Finish:", "off (launch"},
		},
		{
			name:       "batch with a chained acceptance",
			epicMode:   storage.EpicModeIndependent,
			finishMode: storage.FinishModeAuto,
			want:       []string{"batch (independent)", "auto (runs"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newBoardModel(t,
				&storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Tracker", Status: storage.StatusDoing, EpicMode: tc.epicMode, FinishMode: tc.finishMode}},
				&storage.Task{Meta: storage.TaskMeta{ID: "p-9-1", Title: "Sub one", Status: storage.StatusTodo, Parent: "p-9"}},
			)
			detail := stripANSI(m.renderTaskDetail(m.taskByID("p-9")))
			for _, w := range tc.want {
				if !strings.Contains(detail, w) {
					t.Errorf("detail head is missing %q:\n%s", w, detail)
				}
			}
		})
	}
}

// A leaf task has no epic_mode to speak of: X on it runs `pm work`, and there
// is no acceptance and no epic mode to report.
func TestDetailHeadOmitsTheModesForALeafTask(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Leaf", Status: storage.StatusTodo}})
	detail := m.renderTaskDetail(m.taskByID("p-1"))
	if strings.Contains(detail, "Epic mode") {
		t.Errorf("a leaf task must not claim an epic mode:\n%s", detail)
	}
}

// The detail help bar has to name the keys this view actually dispatches. X in
// particular has no other trace here - before this it was reachable only from
// the board bar or from memory.
func TestDetailHelpBarNamesTheExecutorKeys(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Leaf", Status: storage.StatusTodo}})
	m.currentView = viewDetail
	m.openDetailTask(m.taskByID("p-1"))
	view := m.viewDetail()
	for _, want := range []string{"c: claude", "X/W/K: exec run/watch/stop"} {
		if !strings.Contains(view, want) {
			t.Errorf("help bar is missing %q:\n%s", want, view)
		}
	}
}

// openRunLog is the half of the double-launch warning that cannot be left to
// the user's judgement: a second launch shares the log PATH, so a truncating
// open would blank the record of the run that is still writing it.
func TestOpenRunLogSparesALiveRunsLog(t *testing.T) {
	path := t.TempDir() + "/run.log"
	if err := os.WriteFile(path, []byte("first run's output\n"), 0644); err != nil {
		t.Fatal(err)
	}

	f, err := openRunLog(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("second run\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "first run's output") {
		t.Errorf("the live run's log was blanked: %q", data)
	}
	if !strings.Contains(string(data), "second run") {
		t.Errorf("the second run's output did not land: %q", data)
	}

	// With nothing live, a launch still starts from an empty log - the file is
	// the record of ONE run, not of every run this task ever had.
	f, err = openRunLog(path, false)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("a fresh launch must truncate, got %q", data)
	}
}

// The warning is per KIND. A run and its acceptance are routinely live at the
// same time by design, so an acceptance launch must not be warned about by the
// run beside it, and vice versa.
func TestLiveRunWarningIsPerKind(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	pid := liveRunPID(t)
	if err := storage.WriteRunState(stateDir, liveRun(t, "p-9", pid)); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()
	task := m.taskByID("p-9")

	if w := m.liveRunForLaunch(task, false); w == "" {
		t.Error("a second run launch over a live run must warn")
	}
	if w := m.liveRunForLaunch(task, true); w != "" {
		t.Errorf("an acceptance must not be warned about by the live run it accepts: %q", w)
	}

	if err := storage.WriteRunState(stateDir, liveFinish(t, "p-9", pid)); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()
	if w := m.liveRunForLaunch(task, true); w == "" {
		t.Error("a second acceptance over a live acceptance must warn")
	}
}
