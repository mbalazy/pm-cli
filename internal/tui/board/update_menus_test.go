package board

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// TestAddTaskShowsErrorToast covers pm-cli-67-1: AddTask validates status and
// does an O_EXCL claim on the task file, so a duplicate ID FAILS - the board
// used to just reload and show nothing, silently discarding the user's input.
func TestAddTaskShowsErrorToast(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	if err := store.CreateProject("p", &storage.Project{Name: "P"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTask("p", &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Existing", Status: storage.StatusTodo}}); err != nil {
		t.Fatal(err)
	}

	m := Model{
		store:         store,
		projects:      []string{"all", "p"},
		activeProject: 1,
		menuState:     menuState{hiddenStatuses: make(map[storage.TaskStatus]bool)},
		width:         80, height: 24,
	}
	m.reload()

	// Drive the two-step add flow: same title (so the id+slug filename
	// collides) then an ID that collides with p-1.
	m.adding = true
	m.addStep = 0
	m.addInput.SetValue("Existing")
	result, _ := m.updateAdd(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(Model)
	if m.addStep != 1 {
		t.Fatalf("addStep = %d, want 1 after the title step", m.addStep)
	}
	m.addInput.SetValue("p-1")
	result, _ = m.updateAdd(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(Model)

	if !strings.Contains(m.toastMsg, "add failed") || !strings.Contains(m.toastMsg, "already exists") {
		t.Errorf("toastMsg = %q, want it to mention the add failure and the reason", m.toastMsg)
	}
	if m.adding {
		t.Error("add mode should close after the attempt, success or failure")
	}

	fresh, err := store.FindTask("p", "p-1")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Meta.Title != "Existing" {
		t.Errorf("existing task should be untouched by the failed add, got title %q", fresh.Meta.Title)
	}
}

// TestAddTaskStampsStatusChanged pins the board's add path to the same
// creation rule as storage.NewTask: the board builds its TaskMeta by hand (it
// carries its own id and the column's status), so the stamp is easy to lose
// here - and a task born unstamped reports its status age as unknown forever.
func TestAddTaskStampsStatusChanged(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	if err := store.CreateProject("p", &storage.Project{Name: "P"}); err != nil {
		t.Fatal(err)
	}
	m := Model{
		store:         store,
		projects:      []string{"all", "p"},
		activeProject: 1,
		menuState:     menuState{hiddenStatuses: make(map[storage.TaskStatus]bool)},
		width:         80, height: 24,
	}
	m.reload()

	m.adding = true
	m.addStep = 0
	m.addInput.SetValue("Fresh task")
	result, _ := m.updateAdd(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(Model)
	m.addInput.SetValue("p-7")
	result, _ = m.updateAdd(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(Model)
	if strings.Contains(m.toastMsg, "add failed") {
		t.Fatalf("add failed: %s", m.toastMsg)
	}

	added, err := store.FindTask("p", "p-7")
	if err != nil {
		t.Fatal(err)
	}
	if storage.StampDate(added.Meta.StatusChanged) != storage.Today() {
		t.Errorf("status_changed = %q, want today's stamp", added.Meta.StatusChanged)
	}
	// One clock read for both, so a new task never claims its status moved
	// after its last edit.
	if added.Meta.StatusChanged != added.Meta.Updated {
		t.Errorf("status_changed = %q, want it identical to updated %q", added.Meta.StatusChanged, added.Meta.Updated)
	}
}
