package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureStderr swaps os.Stderr for a pipe for the duration of fn and returns
// what fn wrote to it, draining concurrently so a write larger than the OS
// pipe buffer can't deadlock the test. Restore + close go through a deferred
// sync.Once (mirrors internal/cmd's capturePipe) so a t.Fatal or panic inside
// fn cannot leave os.Stderr pointing at a dead pipe for every later test in
// the package.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	drained := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		r.Close()
		drained <- string(out)
	}()
	var once sync.Once
	restore := func() {
		once.Do(func() {
			os.Stderr = old
			w.Close()
		})
	}
	os.Stderr = w
	defer restore()
	fn()
	restore()
	return <-drained
}

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

	// The read-failed branch: the whole project dir is gone, so GetTasks
	// itself errors. Nothing may be re-created - not the task file, and not
	// the project dir either (taking the lock would MkdirAll it back).
	t.Run("project gone", func(t *testing.T) {
		s := setupLockTestStore(t)
		stale, err := s.FindTask("app", "app-1")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(s.ProjectDir("app")); err != nil {
			t.Fatal(err)
		}

		err = s.MoveTask(stale, StatusDone)
		if err == nil {
			t.Fatal("MoveTask into a vanished project returned nil")
		}
		// Pin the BRANCH, not just the failure: without this the subtest would
		// silently slide into the not-found branch (already covered above) if
		// GetTasks ever stopped erroring on a missing dir.
		if !strings.HasPrefix(err.Error(), "read project ") {
			t.Fatalf("expected the read-failed branch, got: %v", err)
		}
		if _, err := os.Stat(stale.FilePath); !os.IsNotExist(err) {
			t.Fatalf("task file resurrected at %s (stat err = %v)", stale.FilePath, err)
		}
		if _, err := os.Stat(s.ProjectDir("app")); !os.IsNotExist(err) {
			t.Fatalf("project dir resurrected at %s (stat err = %v)", s.ProjectDir("app"), err)
		}
	})
}

// TestProjectWritesDoNotCreateProjects: the project lock file lives INSIDE the
// project dir, so a lock taken on an unknown slug would MkdirAll it into
// existence - a typo'd slug must stay an error, not become a new project.
func TestProjectWritesDoNotCreateProjects(t *testing.T) {
	s := setupLockTestStore(t)

	if err := s.UpdateProject("typo", &Project{Name: "Typo"}); err == nil {
		t.Fatal("UpdateProject on an unknown slug returned nil")
	}
	if _, err := s.MutateProject("typo", func(*Project) error { return nil }); err == nil {
		t.Fatal("MutateProject on an unknown slug returned nil")
	}
	if _, err := os.Stat(s.ProjectDir("typo")); !os.IsNotExist(err) {
		t.Fatalf("project dir created for an unknown slug (stat err = %v)", err)
	}
}

// TestUpdateProjectTakesTheLock: UpdateProject's own lock is what makes a
// full-struct write mutually exclusive with a MutateProject critical section -
// without it a write lands inside another process's read->patch->write window
// and is clobbered. Asserted directly (the write must not land while the lock
// is held elsewhere), since a lost-update test cannot distinguish it from
// last-write-wins.
func TestUpdateProjectTakesTheLock(t *testing.T) {
	s := setupLockTestStore(t)

	release, err := s.LockProject("app")
	if err != nil {
		t.Fatal(err)
	}
	defer release() // idempotent; keeps the flock from leaking if we fail early

	done := make(chan error, 1)
	go func() { done <- s.UpdateProject("app", &Project{Name: "Updated"}) }()

	select {
	case err := <-done:
		t.Fatalf("UpdateProject returned while the project lock was held (err = %v)", err)
	case <-time.After(100 * time.Millisecond):
	}
	// Assert the WRITE, not just the call: an implementation that wrote first
	// and locked afterwards would also still be blocked here.
	if held, err := s.GetProject("app"); err != nil || held.Name != "App" {
		t.Fatalf("project.yaml was written while the lock was held: %+v (err = %v)", held, err)
	}
	release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("update: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("UpdateProject never completed after the lock was released")
	}
	final, err := s.GetProject("app")
	if err != nil {
		t.Fatal(err)
	}
	if final.Name != "Updated" {
		t.Fatalf("name = %q", final.Name)
	}
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

// TestUpdateProjectConcurrentWrites CHARACTERIZES concurrent full-project
// writes (it does not guard the lock - tmp+rename already rules out a torn
// file, so it passes unlocked too): they are last-write-wins by design, and
// because every writer merges into the file it read, an unknown hand-written
// key survives all of them. The lock's own teeth are in
// TestUpdateProjectTakesTheLock and TestMutateProjectSerializesLostUpdate.
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

// TestLockDegradeWarnsLoudly: when LockProject fails, the degrade-to-
// unlocked-write sites must still complete the write (the mitigating context
// is that this is about observability, not about changing the
// degrade-vs-fail decision) AND say so on stderr, naming the operation, the
// project slug and the lock error - a silent degrade used to hide that a
// concurrent session's edit may get clobbered. Exercises MoveTask and
// UpdateProject: both route through the shared warnLockDegrade helper that
// CreateProject and MutateProject also use, so this covers the helper's
// wording contract without re-deriving a broken lock four times over.
func TestLockDegradeWarnsLoudly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the file-permission check this test relies on")
	}
	s := setupLockTestStore(t)

	// Force a genuine LockProject failure without touching the project dir's
	// own writability (the task/project writes themselves need that): make the
	// lock file unwritable, which fails LockProject's O_RDWR open but leaves
	// the directory free for tmp+rename. setupLockTestStore's CreateProject
	// already took the lock once, so the file exists (0644) - chmod it
	// directly rather than os.WriteFile, whose perm arg is a no-op on an
	// existing file.
	lockPath := filepath.Join(s.ProjectDir("app"), ".pm.lock")
	if err := os.Chmod(lockPath, 0444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(lockPath, 0644) })

	_, lockErr := s.LockProject("app")
	if lockErr == nil {
		t.Fatal("expected LockProject to fail against a read-only lock file")
	}
	errText := lockErr.Error()

	task, err := s.FindTask("app", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	var moveErr error
	moveOut := captureStderr(t, func() {
		moveErr = s.MoveTask(task, StatusDone)
	})
	if moveErr != nil {
		t.Fatalf("MoveTask degraded to unlocked but still failed: %v", moveErr)
	}
	if final, err := s.FindTask("app", "app-1"); err != nil || final.Meta.Status != StatusDone {
		t.Fatalf("MoveTask did not persist despite degrading: task=%+v err=%v", final, err)
	}
	wantMove := fmt.Sprintf("pm: project lock unavailable for %q (%v) - moving task app-1 without it\n", "app", errText)
	if moveOut != wantMove {
		t.Fatalf("MoveTask warning = %q, want %q", moveOut, wantMove)
	}

	var updateErr error
	updateOut := captureStderr(t, func() {
		updateErr = s.UpdateProject("app", &Project{Name: "Updated"})
	})
	if updateErr != nil {
		t.Fatalf("UpdateProject degraded to unlocked but still failed: %v", updateErr)
	}
	if p, err := s.GetProject("app"); err != nil || p.Name != "Updated" {
		t.Fatalf("UpdateProject did not persist despite degrading: project=%+v err=%v", p, err)
	}
	wantUpdate := fmt.Sprintf("pm: project lock unavailable for %q (%v) - updating project without it\n", "app", errText)
	if updateOut != wantUpdate {
		t.Fatalf("UpdateProject warning = %q, want %q", updateOut, wantUpdate)
	}
}
