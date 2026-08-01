package board

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
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
		store:          store,
		projects:       []string{"all", "p"},
		activeProject:  1,
		hiddenStatuses: make(map[storage.TaskStatus]bool),
		width:          80, height: 24,
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
