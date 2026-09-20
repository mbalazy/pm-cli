package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// The acceptance in a tmux window, and the one flag only it may carry.
//
// Why the gate is where it is: pm's worktree lock and its interactive-session
// lock are separate resources that never collide, and the whole of what makes
// that true is that headless workers never boot a runtime (session_lock.go). An
// acceptance allowed to drive a simulator while detached would break exactly
// that, so --sim is bound to the launch kind, not to the toggle alone.

// simProject gives project p a repo with a runtime skill in it, which is pm's
// only evidence that the project HAS a runtime to drive.
func simProject(t *testing.T, m *Model, withSkill bool) string {
	t.Helper()
	repo := t.TempDir()
	proj, err := m.store.GetProject("p")
	if err != nil {
		t.Fatal(err)
	}
	proj.Path = repo
	if withSkill {
		dir := filepath.Join(repo, ".claude", "skills", "simulator-verify")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# simulator-verify\n"), 0644); err != nil {
			t.Fatal(err)
		}
		proj.Executor = &storage.Executor{Handoff: storage.Handoff{RuntimeSkill: "simulator-verify"}}
	}
	if err := m.store.UpdateProject("p", proj); err != nil {
		t.Fatal(err)
	}
	return repo
}

// trackerModel is a tracker with one sub, which is what puts the acceptance
// items on the menu at all, opened in the DETAIL view.
//
// The view matters: the launch overlay resolves its task through menuTask(),
// and both real openers hand it exactly what menuTask() would return anyway
// (update.go passes selectedTask, update_detail.go passes detailTask). A test
// that opened the menu on a task the board cursor was not sitting on would be
// exercising a state no keypress can produce.
func trackerModel(t *testing.T) *Model {
	t.Helper()
	m := newBoardModel(t,
		&storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}},
		&storage.Task{Meta: storage.TaskMeta{ID: "p-9-1", Title: "Sub one", Status: storage.StatusTodo, Parent: "p-9"}},
	)
	m.currentView = viewDetail
	m.openDetailTask(m.taskByID("p-9"))
	// Wide enough for the full layout: newBoardModel's 80 columns trigger the
	// compact fallback, which drops exactly the hints these tests read.
	m.width, m.height = 130, 40
	return m
}

// The rule itself, as a pure function: the toggle is a request, the launch kind
// decides. Every other kind ignores it - including the DETACHED acceptance,
// which is the one that would do damage.
func TestOnlyTheTmuxAcceptanceMayUseTheSimulator(t *testing.T) {
	for _, kind := range []string{"finish", "bg", "here", "tmux", "dry-run"} {
		if resolveFinishSim(kind, true) {
			t.Errorf("kind %q must never carry --sim", kind)
		}
	}
	if !resolveFinishSim("finish-tmux", true) {
		t.Error("the tmux acceptance is the one launch that may")
	}
	if resolveFinishSim("finish-tmux", false) {
		t.Error("without the toggle even the tmux acceptance runs --no-sim")
	}
}

// Both acceptance kinds run `pm finish`, so everything keyed off the COMMAND
// (argv, log path, run-state routing, the live-run warning) has to see them as
// one - which is the conflation the old `kind == "finish"` test would make.
func TestBothAcceptanceKindsAreFinishRuns(t *testing.T) {
	for _, kind := range []string{"finish", "finish-tmux"} {
		if !isFinishKind(kind) {
			t.Errorf("%q is a finish run", kind)
		}
	}
	for _, kind := range []string{"bg", "here", "tmux", "dry-run"} {
		if isFinishKind(kind) {
			t.Errorf("%q is not a finish run", kind)
		}
	}
}

func TestTheSimToggleIsOfferedOnlyWhereItMeansSomething(t *testing.T) {
	t.Run("no runtime skill, no toggle", func(t *testing.T) {
		os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
		defer os.Unsetenv("TMUX")
		m := trackerModel(t)
		simProject(t, m, false)
		m.openExecutorMenu(m.taskByID("p-9"))

		if m.executorSimAvail {
			t.Error("a project with no runtime skill has no simulator to offer")
		}
		if out := stripANSI(m.viewClaudeMenu()); strings.Contains(out, "simulator") {
			t.Errorf("the menu offers a simulator this project does not have:\n%s", out)
		}
		// And the key does nothing rather than arming an inert flag.
		next, _ := m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("$")})
		if next.(Model).claudeMenuSim {
			t.Error("$ must not arm --sim without a runtime skill")
		}
	})

	t.Run("runtime skill in tmux: toggle and the tmux acceptance", func(t *testing.T) {
		os.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
		defer os.Unsetenv("TMUX")
		m := trackerModel(t)
		simProject(t, m, true)
		m.openExecutorMenu(m.taskByID("p-9"))

		if !m.executorSimAvail {
			t.Fatal("a declared, resolvable runtime skill is what makes the toggle available")
		}
		out := stripANSI(m.viewClaudeMenu())
		for _, want := range []string{"A  now, in a tmux window", "can drive the simulator", "[ ] $ simulator"} {
			if !strings.Contains(out, want) {
				t.Errorf("the menu is missing %q:\n%s", want, out)
			}
		}

		next, _ := m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("$")})
		v := next.(Model)
		if !v.claudeMenuSim {
			t.Fatal("$ did not arm the simulator")
		}
		// Armed: the chip is checked, and - the assertion that matters - the
		// previewed command for the tmux acceptance actually carries the flag.
		v.claudeMenuCursor = len(v.claudeMenuItems) - 2 // the finish-tmux row
		out = stripANSI(v.viewClaudeMenu())
		if !strings.Contains(out, "[x] $ simulator") {
			t.Errorf("the toggle does not read as armed:\n%s", out)
		}
		if !strings.Contains(out, "→ pm finish p-9 --project p --sim") {
			t.Errorf("the previewed command does not carry --sim:\n%s", out)
		}
	})

	t.Run("outside tmux the toggle is shown but will not move", func(t *testing.T) {
		os.Unsetenv("TMUX")
		m := trackerModel(t)
		simProject(t, m, true)
		m.openExecutorMenu(m.taskByID("p-9"))

		out := stripANSI(m.viewClaudeMenu())
		if strings.Contains(out, "Accept in a tmux window") {
			t.Errorf("there is no tmux to accept in:\n%s", out)
		}
		if !strings.Contains(out, "needs tmux") {
			t.Errorf("the disabled toggle must say why it is off:\n%s", out)
		}
		next, _ := m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("$")})
		if next.(Model).claudeMenuSim {
			t.Error("$ armed a flag that no available launch would carry")
		}
	})
}

// The warning the user asked for: a simulator-enabled acceptance claiming a
// slot whose runtime an interactive session is already driving. A warning, not
// a block - both may be wanted, and the slot is only pm's best guess.
func TestASimAcceptanceWarnsAboutASlotHeldByALiveSession(t *testing.T) {
	m := trackerModel(t)
	simProject(t, m, true)
	task := m.taskByID("p-9")
	// Two slots, both free of an EXECUTOR lock, so the launch would take slot 1.
	m.executorSlots = []executorSlotStatus{{path: "/tmp/wt1"}, {path: "/tmp/wt2"}}

	if w := m.simSlotWarning(task, true, true); w != "" {
		t.Errorf("no session holds a slot yet: %q", w)
	}

	// A live interactive session on slot 1 - the pid is this test process, which
	// is alive by construction.
	projDir := m.store.ProjectDir("p")
	lockPath := storage.SessionLockPath(projDir, 1)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte(`{"pid":`+itoa(os.Getpid())+`,"slot":1}`), 0644); err != nil {
		t.Fatal(err)
	}

	w := m.simSlotWarning(task, true, true)
	if !strings.Contains(w, "slot 1") {
		t.Errorf("warning = %q, want it to name the slot", w)
	}

	// Not armed, or not claiming a slot at all: nothing to warn about. Without
	// --additional the acceptance runs in the main checkout, whose runtime pm
	// neither locks nor pretends to.
	if w := m.simSlotWarning(task, false, true); w != "" {
		t.Errorf("no simulator, no warning: %q", w)
	}
	if w := m.simSlotWarning(task, true, false); w != "" {
		t.Errorf("no slot claimed, no warning: %q", w)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
