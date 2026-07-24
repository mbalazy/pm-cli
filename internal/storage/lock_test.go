package storage

import (
	"os"
	"sync"
	"testing"
	"time"
)

// TestLockProjectMutualExclusion: a second acquirer must wait until the first
// releases (flock blocks across fds even in one process).
func TestLockProjectMutualExclusion(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	os.MkdirAll(s.ProjectDir("app"), 0755)

	release1, err := s.LockProject("app")
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		release2, err := s.LockProject("app")
		if err != nil {
			t.Errorf("second lock: %v", err)
			close(acquired)
			return
		}
		close(acquired)
		release2()
	}()

	select {
	case <-acquired:
		t.Fatal("second acquirer got the lock while the first still held it")
	case <-time.After(100 * time.Millisecond):
	}
	release1()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second acquirer never got the lock after release")
	}
}

// TestLockProjectSerializesLostUpdate encodes the lost-update hazard: N
// writers each do lock -> read -> append a session -> write; with the lock
// every append must survive.
func TestLockProjectSerializesLostUpdate(t *testing.T) {
	s := setupLockTestStore(t)

	const writers = 8
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			release, err := s.LockProject("app")
			if err != nil {
				t.Errorf("lock: %v", err)
				return
			}
			defer release()
			task, err := s.FindTask("app", "app-1")
			if err != nil {
				t.Errorf("find: %v", err)
				return
			}
			task.Meta.Sessions = append(task.Meta.Sessions, string(rune('a'+i)))
			if err := s.WriteTask(task); err != nil {
				t.Errorf("write: %v", err)
			}
		}(i)
	}
	wg.Wait()

	final, err := s.FindTask("app", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Meta.Sessions) != writers {
		t.Fatalf("lost updates: %d/%d sessions survived (%v)", len(final.Meta.Sessions), writers, final.Meta.Sessions)
	}
}

func setupLockTestStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{Root: t.TempDir()}
	if err := s.CreateProject("app", &Project{Name: "App"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTask("app", &Task{Meta: TaskMeta{ID: "app-1", Title: "T", Status: StatusTodo}}); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestMoveTaskFreshRead: a move from a stale copy (board loaded minutes ago,
// epic sub read at run start) must not clobber edits made in between.
func TestMoveTaskFreshRead(t *testing.T) {
	s := setupLockTestStore(t)

	stale, err := s.FindTask("app", "app-1")
	if err != nil {
		t.Fatal(err)
	}

	other, _ := s.FindTask("app", "app-1")
	other.Meta.Brief = "fresh brief"
	other.Body = "fresh note"
	if err := s.WriteTask(other); err != nil {
		t.Fatal(err)
	}

	if err := s.MoveTask(stale, StatusDone); err != nil {
		t.Fatal(err)
	}

	final, _ := s.FindTask("app", "app-1")
	if final.Meta.Status != StatusDone {
		t.Fatalf("status = %s", final.Meta.Status)
	}
	if final.Meta.Brief != "fresh brief" || final.Body != "fresh note" {
		t.Fatalf("stale move clobbered mid-time edits: brief=%q body=%q", final.Meta.Brief, final.Body)
	}
	if stale.Meta.Brief != "fresh brief" {
		t.Fatal("caller's copy not refreshed in place")
	}
}
