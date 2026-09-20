package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// staleStamp stands in for a status_changed written long ago: the whole point
// of the field is that it survives edits, so tests assert against a value no
// code under test could have produced today.
const staleStamp = "2025-01-01T09:00:00+01:00"

func TestSetStatusStampsOnlyRealChanges(t *testing.T) {
	t.Run("a changed status stamps status_changed", func(t *testing.T) {
		task := &Task{Meta: TaskMeta{ID: "s-1", Title: "T", Status: StatusTodo, StatusChanged: staleStamp}}
		task.SetStatus(StatusWaiting)

		if task.Meta.Status != StatusWaiting {
			t.Fatalf("status = %q, want %q", task.Meta.Status, StatusWaiting)
		}
		if task.Meta.StatusChanged == staleStamp {
			t.Fatal("status_changed must be re-stamped on a real change")
		}
		if _, err := time.Parse(time.RFC3339, task.Meta.StatusChanged); err != nil {
			t.Errorf("status_changed = %q, want an RFC3339 stamp: %v", task.Meta.StatusChanged, err)
		}
		if got := StampDate(task.Meta.StatusChanged); got != Today() {
			t.Errorf("status_changed date = %q, want %q", got, Today())
		}
	})

	// The no-op guard is the field's whole value: re-stamping a move to the
	// status a task already holds would reset the "stuck since" clock and erase
	// the age the field exists to report.
	t.Run("moving to the status already held leaves the stamp alone", func(t *testing.T) {
		task := &Task{Meta: TaskMeta{ID: "s-2", Title: "T", Status: StatusWaiting, StatusChanged: staleStamp}}
		task.SetStatus(StatusWaiting)

		if task.Meta.StatusChanged != staleStamp {
			t.Errorf("status_changed = %q, want it untouched (%q)", task.Meta.StatusChanged, staleStamp)
		}
	})

	// Files are never migrated: a task written before the field existed has an
	// empty stamp, and it stays empty until a real move - readers are told to
	// treat that as unknown rather than guess it from `updated`.
	t.Run("an unstamped task stays unstamped until it actually moves", func(t *testing.T) {
		task := &Task{Meta: TaskMeta{ID: "s-3", Title: "T", Status: StatusTodo}}
		task.SetStatus(StatusTodo)
		if task.Meta.StatusChanged != "" {
			t.Errorf("status_changed = %q, want it left empty (unknown)", task.Meta.StatusChanged)
		}
		task.SetStatus(StatusDoing)
		if task.Meta.StatusChanged == "" {
			t.Error("status_changed must be stamped once the status really changes")
		}
	})
}

func TestNewTaskStampsStatusChanged(t *testing.T) {
	task := NewTask("s-4", "Fresh", "alpha")
	if _, err := time.Parse(time.RFC3339, task.Meta.StatusChanged); err != nil {
		t.Fatalf("status_changed = %q, want an RFC3339 stamp: %v", task.Meta.StatusChanged, err)
	}
	// A task created and left on todo must report a real age, not "unknown".
	if got := StampDate(task.Meta.StatusChanged); got != task.Meta.Created {
		t.Errorf("status_changed date = %q, want the created date %q", got, task.Meta.Created)
	}
	// Both stamps come from one clock read: two Now() calls can straddle a
	// second, leaving a brand-new task whose status moved AFTER its last edit.
	if task.Meta.StatusChanged != task.Meta.Updated {
		t.Errorf("status_changed = %q, want it identical to updated %q", task.Meta.StatusChanged, task.Meta.Updated)
	}
}

func TestMoveTaskStampsStatusChanged(t *testing.T) {
	t.Run("stamps the field and persists it", func(t *testing.T) {
		store, _ := setupTestStore(t)
		task, _ := store.FindTask("alpha", "a-1") // fixture: no status_changed

		if err := store.MoveTask(task, StatusWaiting); err != nil {
			t.Fatalf("move: %v", err)
		}
		if got := StampDate(task.Meta.StatusChanged); got != Today() {
			t.Errorf("status_changed date = %q, want %q", got, Today())
		}
		reloaded, err := store.FindTask("alpha", "a-1")
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Meta.StatusChanged != task.Meta.StatusChanged {
			t.Errorf("on disk status_changed = %q, want %q", reloaded.Meta.StatusChanged, task.Meta.StatusChanged)
		}
	})

	// MoveTask compares against the FRESH on-disk status, so a redundant move -
	// `pm mv` to where the task already is, an executor parking an already
	// parked sub - must not touch the clock even though the file is rewritten.
	t.Run("a redundant move does not re-stamp", func(t *testing.T) {
		store, dir := setupTestStore(t)
		task := &Task{
			Meta:     TaskMeta{ID: "a-9", Title: "Parked", Status: StatusWaiting, Created: "2025-01-01", Updated: "2025-01-01", StatusChanged: staleStamp},
			FilePath: filepath.Join(dir, "alpha", "a-9-parked.md"),
			Project:  "alpha",
		}
		if err := WriteTask(task); err != nil {
			t.Fatal(err)
		}
		if err := store.MoveTask(task, StatusWaiting); err != nil {
			t.Fatalf("move: %v", err)
		}
		if task.Meta.StatusChanged != staleStamp {
			t.Errorf("status_changed = %q, want it untouched (%q)", task.Meta.StatusChanged, staleStamp)
		}
		if task.Meta.Updated == "2025-01-01" {
			t.Error("updated must still move - only status_changed is conditional")
		}
	})

	// An edit that is not a status change (a brief, a session note) goes
	// through Store.WriteTask, which holds no status logic at all - this pins
	// that down, so a later "just stamp it centrally in writeTask" refactor
	// (which would re-stamp every unrelated edit and destroy the age) fails
	// here rather than in production data.
	t.Run("a non-status edit leaves the stamp alone", func(t *testing.T) {
		store, dir := setupTestStore(t)
		task := &Task{
			Meta:     TaskMeta{ID: "a-8", Title: "Blocked", Status: StatusWaiting, Created: "2025-01-01", Updated: "2025-01-01", StatusChanged: staleStamp},
			FilePath: filepath.Join(dir, "alpha", "a-8-blocked.md"),
			Project:  "alpha",
		}
		if err := WriteTask(task); err != nil {
			t.Fatal(err)
		}
		task.Meta.Brief = "still waiting on the client"
		task.Meta.Updated = Now()
		if err := store.WriteTask(task); err != nil {
			t.Fatal(err)
		}
		reloaded, err := store.FindTask("alpha", "a-8")
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Meta.StatusChanged != staleStamp {
			t.Errorf("status_changed = %q, want it untouched (%q)", reloaded.Meta.StatusChanged, staleStamp)
		}
	})
}

func TestWaitingForRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "w-1-blocked.md")
	blocker := "review by the lead, PR #940"
	if err := WriteTask(&Task{
		Meta:     TaskMeta{ID: "w-1", Title: "Blocked", Status: StatusWaiting, Created: Today(), Updated: Now(), WaitingFor: blocker},
		FilePath: path,
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The value is yaml-quoted (it holds a '#'), so assert the key and the text
	// rather than one exact rendering.
	if !strings.Contains(string(raw), "waiting_for:") || !strings.Contains(string(raw), blocker) {
		t.Errorf("frontmatter must carry the waiting_for key, got:\n%s", raw)
	}

	back, err := ReadTask(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Meta.WaitingFor != blocker {
		t.Errorf("waiting_for = %q, want %q", back.Meta.WaitingFor, blocker)
	}

	// Omitted when unset - a task nobody recorded a blocker for must not grow a
	// blank key in every file.
	if err := WriteTask(&Task{
		Meta:     TaskMeta{ID: "w-2", Title: "Free", Status: StatusTodo, Created: Today(), Updated: Now()},
		FilePath: filepath.Join(dir, "w-2-free.md"),
	}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, "w-2-free.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "waiting_for") || strings.Contains(string(raw), "status_changed") {
		t.Errorf("unset fields must be omitted, got:\n%s", raw)
	}
}
