package storage

import (
	"fmt"
	"os"
	"strings"
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

// TestMoveTaskVanishedTask: the fresh re-read is a PRECONDITION. A move from a
// copy whose file is gone (a parallel session deleted it; the board still holds
// the card pointer from its last reload) must fail loudly instead of writing
// the stale copy back and resurrecting the task.
func TestMoveTaskVanishedTask(t *testing.T) {
	t.Run("deleted task", func(t *testing.T) {
		s := setupLockTestStore(t)
		stale, err := s.FindTask("app", "app-1")
		if err != nil {
			t.Fatal(err)
		}
		path := stale.FilePath
		if err := s.DeleteTask(stale); err != nil {
			t.Fatal(err)
		}

		if err := s.MoveTask(stale, StatusDone); err == nil {
			t.Fatal("MoveTask on a deleted task returned nil")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("task file resurrected at %s (stat err = %v)", path, err)
		}
		tasks, err := s.GetTasks("app")
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != 0 {
			t.Fatalf("project still holds %d task(s) after the delete+move", len(tasks))
		}
	})

	t.Run("project gone", func(t *testing.T) {
		s := setupLockTestStore(t)
		stale, err := s.FindTask("app", "app-1")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(s.ProjectDir("app")); err != nil {
			t.Fatal(err)
		}

		if err := s.MoveTask(stale, StatusDone); err == nil {
			t.Fatal("MoveTask into a vanished project returned nil")
		}
		if _, err := os.Stat(stale.FilePath); !os.IsNotExist(err) {
			t.Fatalf("task file resurrected at %s (stat err = %v)", stale.FilePath, err)
		}
	})
}

// TestMutateProjectSerializesLostUpdate: project.yaml gets the same lost-update
// protection as task files. N writers each add their own link + tag through a
// read-modify-write; without serialization (and a re-read INSIDE the critical
// section) the later writers overwrite the earlier ones' fields.
func TestMutateProjectSerializesLostUpdate(t *testing.T) {
	s := setupLockTestStore(t)

	const writers = 8
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", i)
			if _, err := s.MutateProject("app", func(p *Project) error {
				if p.Links == nil {
					p.Links = make(map[string]string)
				}
				p.Links[key] = "https://example.test/" + key
				p.Tags = append(p.Tags, key)
				return nil
			}); err != nil {
				t.Errorf("mutate %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	final, err := s.GetProject("app")
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Links) != writers {
		t.Fatalf("lost updates: %d/%d links survived (%v)", len(final.Links), writers, final.Links)
	}
	if len(final.Tags) != writers {
		t.Fatalf("lost updates: %d/%d tags survived (%v)", len(final.Tags), writers, final.Tags)
	}
	if final.Name != "App" {
		t.Fatalf("untouched field clobbered: name = %q", final.Name)
	}
}

// TestUpdateProjectConcurrentWrites: concurrent full-project writes are
// last-write-wins by design, but each one merges into the file it reads - so
// they must never interleave into a torn or unparseable project.yaml, and an
// unknown hand-written key must survive every one of them.
func TestUpdateProjectConcurrentWrites(t *testing.T) {
	s := setupLockTestStore(t)
	path := s.ProjectYAML("app")
	if err := os.WriteFile(path, []byte("name: App\ncustom_key: keep-me\n"), 0644); err != nil {
		t.Fatal(err)
	}

	const writers = 8
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.UpdateProject("app", &Project{
				Name:  "App",
				Stack: fmt.Sprintf("stack-%d", i),
			}); err != nil {
				t.Errorf("update %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	final, err := s.GetProject("app")
	if err != nil {
		t.Fatalf("project.yaml unreadable after concurrent writes: %v", err)
	}
	if final.Name != "App" || !strings.HasPrefix(final.Stack, "stack-") {
		t.Fatalf("torn write: name=%q stack=%q", final.Name, final.Stack)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "custom_key: keep-me") {
		t.Fatalf("unknown key lost under concurrent writes:\n%s", raw)
	}
}
