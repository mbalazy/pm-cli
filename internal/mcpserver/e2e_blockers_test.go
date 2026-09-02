package mcpserver

import (
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// blockerDetail is the slice of taskDetail these tests care about, decoded
// from the handlers' real JSON output.
type blockerDetail struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	WaitingFor    string `json:"waiting_for"`
	StatusChanged string `json:"status_changed"`
}

// ageStatusStamp back-dates a task's status_changed on disk and returns the
// value it planted, so a following handler call can be shown to have
// re-stamped it. Two stamps written in the same second are byte-identical
// (RFC3339 is second-resolution), which would make a naive before/after
// comparison pass whether or not the field was written.
func ageStatusStamp(t *testing.T, store *storage.Store, id string) string {
	t.Helper()
	const aged = "2025-01-01T09:00:00+01:00"
	task, err := store.FindTask("test", id)
	if err != nil {
		t.Fatal(err)
	}
	task.Meta.StatusChanged = aged
	if err := store.WriteTask(task); err != nil {
		t.Fatal(err)
	}
	return aged
}

// TestE2EWaitingForAndStatusChanged drives the blocker pair through the REAL
// registered handlers (startMCP): waiting_for is tri-state on update - the
// brief/ac contract - and status_changed is stamped by pm itself, never a
// param, on both mutation paths that can change a status.
func TestE2EWaitingForAndStatusChanged(t *testing.T) {
	store, _ := setupMCPTestStore(t)
	sess := startMCP(t, store)

	// add: waiting_for is accepted, status_changed is stamped at creation.
	text, isErr := call(t, sess, "pm_add_task", map[string]any{
		"project": "test", "title": "Blocked on review",
		"status": "waiting", "waiting_for": "review Alex PR #940",
	})
	if isErr {
		t.Fatalf("add_task error: %s", text)
	}
	var added blockerDetail
	mustUnmarshal(t, text, &added)
	if added.WaitingFor != "review Alex PR #940" {
		t.Errorf("waiting_for = %q, want the blocker text", added.WaitingFor)
	}
	if storage.StampDate(added.StatusChanged) != storage.Today() {
		t.Errorf("status_changed = %q, want today's stamp", added.StatusChanged)
	}

	// Tri-state part 1: OMITTING waiting_for keeps it. The stamp is back-dated
	// first so the "a non-status edit leaves it alone" check below is real and
	// not two same-second stamps comparing equal.
	aged := ageStatusStamp(t, store, added.ID)
	text, isErr = call(t, sess, "pm_update_task", map[string]any{
		"project": "test", "task_id": added.ID, "brief": "still blocked",
	})
	if isErr {
		t.Fatalf("update_task error: %s", text)
	}
	var kept blockerDetail
	mustUnmarshal(t, text, &kept)
	if kept.WaitingFor != added.WaitingFor {
		t.Errorf("omitted waiting_for must keep the value, got %q", kept.WaitingFor)
	}
	// The same edit must not touch the status clock - that is the whole reason
	// the field exists next to `updated`.
	if kept.StatusChanged != aged {
		t.Errorf("a non-status edit re-stamped status_changed: %q -> %q", aged, kept.StatusChanged)
	}

	// Tri-state part 2: an EMPTY string clears it (the block lifted).
	text, isErr = call(t, sess, "pm_update_task", map[string]any{
		"project": "test", "task_id": added.ID, "waiting_for": "",
	})
	if isErr {
		t.Fatalf("update_task error: %s", text)
	}
	var cleared blockerDetail
	mustUnmarshal(t, text, &cleared)
	if cleared.WaitingFor != "" {
		t.Errorf("empty waiting_for must clear it, got %q", cleared.WaitingFor)
	}
	onDisk, err := store.FindTask("test", added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Meta.WaitingFor != "" {
		t.Errorf("on disk waiting_for = %q, want cleared", onDisk.Meta.WaitingFor)
	}

	// pm_update_task with a status writes the task itself (no MoveTask), so it
	// is the second path that has to stamp. The stamps pm writes have
	// second resolution and this test runs inside one second, so a re-stamp is
	// proven by planting an OLD value on disk first, not by comparing two
	// same-run stamps.
	agedStamp := ageStatusStamp(t, store, added.ID)
	if _, isErr = call(t, sess, "pm_update_task", map[string]any{
		"project": "test", "task_id": added.ID, "status": "todo",
	}); isErr {
		t.Fatal("update_task with status errored")
	}
	viaUpdate, err := store.FindTask("test", added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if viaUpdate.Meta.StatusChanged == agedStamp {
		t.Error("pm_update_task with a new status must re-stamp status_changed")
	}

	// pm_move_task: the stamp lands on disk and comes back out of pm_get_task.
	agedStamp = ageStatusStamp(t, store, added.ID)
	if _, isErr = call(t, sess, "pm_move_task", map[string]any{
		"project": "test", "task_id": added.ID, "new_status": "waiting",
	}); isErr {
		t.Fatal("move_task errored")
	}
	moved, err := store.FindTask("test", added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Meta.StatusChanged == agedStamp {
		t.Error("pm_move_task must re-stamp status_changed")
	}
	if storage.StampDate(moved.Meta.StatusChanged) != storage.Today() {
		t.Errorf("status_changed = %q, want today's stamp", moved.Meta.StatusChanged)
	}
	text, isErr = call(t, sess, "pm_get_task", map[string]any{"project": "test", "task_id": added.ID})
	if isErr {
		t.Fatalf("get_task error: %s", text)
	}
	var got blockerDetail
	mustUnmarshal(t, text, &got)
	if got.StatusChanged != moved.Meta.StatusChanged {
		t.Errorf("pm_get_task status_changed = %q, want %q", got.StatusChanged, moved.Meta.StatusChanged)
	}

	// A task written before the fields existed reports neither: the JSON keys
	// are omitted, so a reader sees "unknown" rather than a guess.
	text, isErr = call(t, sess, "pm_get_task", map[string]any{"project": "test", "task_id": "t-1"})
	if isErr {
		t.Fatalf("get_task error: %s", text)
	}
	// Match the JSON KEYS, not the words: asserting on the bare strings would
	// fire the day a fixture's body or brief happens to mention either field.
	if strings.Contains(text, `"waiting_for"`) || strings.Contains(text, `"status_changed"`) {
		t.Errorf("legacy task must omit both keys, got: %s", text)
	}
	var legacy blockerDetail
	mustUnmarshal(t, text, &legacy)
	if legacy.WaitingFor != "" || legacy.StatusChanged != "" {
		t.Errorf("legacy task must report neither field, got %+v", legacy)
	}
}
