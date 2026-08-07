package storage

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := &RunState{
		TaskID:         "p-1",
		Project:        "p",
		Kind:           "run-epic",
		Status:         RunStatusRunning,
		PID:            os.Getpid(),
		CurrentSub:     "p-1-2",
		CurrentSession: "sess-abc",
		Subs: []SubRun{
			{ID: "p-1-1", Status: "merged"},
			{ID: "p-1-2", Status: RunStatusRunning, Session: "sess-abc"},
		},
	}
	if err := WriteRunState(dir, st); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}

	got, err := ReadRunState(dir, "p-1")
	if err != nil {
		t.Fatalf("ReadRunState: %v", err)
	}
	if got.Kind != "run-epic" || got.CurrentSub != "p-1-2" || got.CurrentSession != "sess-abc" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if len(got.Subs) != 2 || got.Subs[1].Session != "sess-abc" {
		t.Errorf("subs mismatch: %+v", got.Subs)
	}
	if got.Updated == "" {
		t.Error("WriteRunState should stamp Updated")
	}

	all := ReadRunStates(dir)
	if all["p-1"] == nil {
		t.Error("ReadRunStates should include p-1")
	}
}

// TestWriteRunStateTmpIsPerProcess pins the pm-cli-48 fix: the scratch file
// WriteRunState writes through is named per WRITER, so two processes updating
// the same run-state (the board's seed/killRun against the manager's heartbeat)
// cannot interleave inside one os.WriteFile and rename a torn mixture. A
// foreign process's scratch file is the observable proxy - if this writer still
// used a shared name it would have written straight through it.
func TestWriteRunStateTmpIsPerProcess(t *testing.T) {
	dir := t.TempDir()
	st := &RunState{TaskID: "p-1", Project: "p", Kind: "work", Status: RunStatusRunning}
	if err := WriteRunState(dir, st); err != nil {
		t.Fatalf("seed WriteRunState: %v", err)
	}

	// Stand in for another process mid-write on the same run-state.
	foreign := ExecutorRunPath(dir, "p-1") + ".tmp.999999"
	if err := os.WriteFile(foreign, []byte("half-written by another pid"), 0644); err != nil {
		t.Fatalf("write foreign tmp: %v", err)
	}

	st.Phase = "implement"
	if err := WriteRunState(dir, st); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}

	data, err := os.ReadFile(foreign)
	if err != nil {
		t.Fatalf("foreign tmp should be untouched: %v", err)
	}
	if string(data) != "half-written by another pid" {
		t.Errorf("foreign tmp was written through: %q", data)
	}
	got, err := ReadRunState(dir, "p-1")
	if err != nil || got.Phase != "implement" {
		t.Errorf("own write should still land whole: %+v err=%v", got, err)
	}
}

// TestWriteRunStateCleansUpTmpOnFailure pins the other half of pm-cli-48: with
// per-pid names a failed write no longer overwrites a single fixed scratch
// file, so every failure would leak one unless it is cleaned up. The publish is
// forced to fail by parking a non-empty directory where the run-state file
// goes - os.Rename cannot replace that.
func TestWriteRunStateCleansUpTmpOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := ExecutorRunPath(dir, "p-1")
	if err := os.MkdirAll(filepath.Join(path, "blocker"), 0755); err != nil {
		t.Fatalf("mkdir blocker: %v", err)
	}

	if err := WriteRunState(dir, &RunState{TaskID: "p-1", Project: "p"}); err == nil {
		t.Fatal("WriteRunState should fail when the target cannot be replaced")
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("failed write left a scratch file behind: %s", e.Name())
		}
	}
}

func TestRunStateIsLive(t *testing.T) {
	// running + our own (live) pid -> live
	live := &RunState{Status: RunStatusRunning, PID: os.Getpid()}
	if !live.IsLive() {
		t.Error("running run with the current pid should be live")
	}
	// running but a pid that does not exist -> not live (stale)
	stale := &RunState{Status: RunStatusRunning, PID: 2147483640}
	if stale.IsLive() {
		t.Error("running run with a dead pid should not be live")
	}
	// done -> never live
	done := &RunState{Status: RunStatusDone, PID: os.Getpid()}
	if done.IsLive() {
		t.Error("a done run is never live")
	}
	if (&RunState{}).IsLive() {
		t.Error("zero-value run is not live")
	}
}

func TestRunStateKill(t *testing.T) {
	// No pid -> error, no panic.
	if err := (&RunState{}).Kill(syscall.SIGTERM); err == nil {
		t.Error("Kill with no pid should error")
	}

	// Spawn a detached child (its own process group, like a bg run) and kill it.
	c := exec.Command("sleep", "30")
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := c.Process.Pid
	if !ProcessAlive(pid) {
		t.Fatal("child should be alive right after start")
	}
	st := &RunState{Status: RunStatusRunning, PID: pid}
	if err := st.Kill(syscall.SIGKILL); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	_, _ = c.Process.Wait() // reap so the pid isn't a zombie that reads as alive
	// Give the kernel a beat, then confirm it's gone.
	for i := 0; i < 50 && ProcessAlive(pid); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if ProcessAlive(pid) {
		t.Error("child should be dead after Kill(SIGKILL)")
	}
}

// TestRunStateKillReachesTheWorkerGroup pins the backstop half of Kill: the
// worker is NOT in the manager's process group, so signalling the manager alone
// only takes the worker down while the manager is alive to forward it. Once the
// caller escalates to SIGKILL, that forwarding is gone - WorkerPGID is what
// still reaches the worker tree.
func TestRunStateKillReachesTheWorkerGroup(t *testing.T) {
	spawnGroup := func() *exec.Cmd {
		t.Helper()
		c := exec.Command("sleep", "30")
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // never this test's group
		if err := c.Start(); err != nil {
			t.Fatalf("start sleep: %v", err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _, _ = c.Process.Wait() })
		return c
	}
	manager, worker := spawnGroup(), spawnGroup()

	st := &RunState{Status: RunStatusRunning, PID: manager.Process.Pid, WorkerPGID: worker.Process.Pid}
	if err := st.Kill(syscall.SIGKILL); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	// Liveness is read through Wait, not ProcessAlive: an unreaped kill leaves a
	// zombie that still answers signal 0, so polling would pass either way. A
	// signalled process is reaped in milliseconds; one that was missed sits in
	// its `sleep 30`.
	for _, c := range []*exec.Cmd{manager, worker} {
		reaped := make(chan struct{})
		go func(c *exec.Cmd) { _, _ = c.Process.Wait(); close(reaped) }(c)
		select {
		case <-reaped:
		case <-time.After(5 * time.Second):
			t.Errorf("pid %d survived Kill", c.Process.Pid)
		}
	}
}

func TestReadRunStatesMissingDir(t *testing.T) {
	// No .executor dir -> empty map, no panic.
	if got := ReadRunStates(t.TempDir()); len(got) != 0 {
		t.Errorf("expected empty map for missing dir, got %v", got)
	}
	if got := ReadFinishRunStates(t.TempDir()); len(got) != 0 {
		t.Errorf("expected empty map for missing dir, got %v", got)
	}
}

// TestWriteRunStateKindPicksTheFile pins the routing: WriteRunState is the one
// place that knows which file a run-state belongs in, so a caller only has to
// carry the right Kind. An acceptance must never land on <id>.json - that file
// belongs to the run being accepted.
func TestWriteRunStateKindPicksTheFile(t *testing.T) {
	dir := t.TempDir()
	fin := &RunState{TaskID: "p-1", Project: "p", Kind: RunKindFinish, Status: RunStatusRunning}
	if err := WriteRunState(dir, fin); err != nil {
		t.Fatalf("WriteRunState(finish): %v", err)
	}
	if _, err := os.Stat(FinishRunPath(dir, "p-1")); err != nil {
		t.Fatalf("finish run-state should exist at %s: %v", FinishRunPath(dir, "p-1"), err)
	}
	if _, err := os.Stat(ExecutorRunPath(dir, "p-1")); !os.IsNotExist(err) {
		t.Errorf("finish write must not touch the run's own file (err=%v)", err)
	}

	// The RunWriter goes through WriteRunState, so it routes identically - that
	// is why there is no finish-aware writer constructor.
	if err := NewRunWriter(dir, &RunState{TaskID: "p-2", Project: "p", Kind: RunKindFinish}).Update(nil); err != nil {
		t.Fatalf("RunWriter.Update(finish): %v", err)
	}
	if _, err := os.Stat(FinishRunPath(dir, "p-2")); err != nil {
		t.Errorf("RunWriter should write the finish file: %v", err)
	}
	if _, err := os.Stat(ExecutorRunPath(dir, "p-2")); !os.IsNotExist(err) {
		t.Errorf("RunWriter(finish) must not write the run's own file (err=%v)", err)
	}
}

// TestRunAndFinishCoexist is the point of the split: an epic run and the
// acceptance of that same epic carry the SAME task id. Both live on disk, and
// neither read side may return the other - ReadRunStates keys by task id, so an
// unfiltered read would let whichever sorted later evict the other.
func TestRunAndFinishCoexist(t *testing.T) {
	dir := t.TempDir()
	run := &RunState{TaskID: "p-1", Project: "p", Kind: RunKindEpic, Status: RunStatusRunning, Phase: "epic"}
	fin := &RunState{TaskID: "p-1", Project: "p", Kind: RunKindFinish, Status: RunStatusRunning, Phase: "acceptance"}
	if err := WriteRunState(dir, run); err != nil {
		t.Fatalf("WriteRunState(run): %v", err)
	}
	if err := WriteRunState(dir, fin); err != nil {
		t.Fatalf("WriteRunState(finish): %v", err)
	}

	got, err := ReadRunState(dir, "p-1")
	if err != nil || got.Phase != "epic" {
		t.Errorf("ReadRunState should return the epic run: %+v err=%v", got, err)
	}
	gotFin, err := ReadFinishRunState(dir, "p-1")
	if err != nil || gotFin.Phase != "acceptance" {
		t.Errorf("ReadFinishRunState should return the acceptance: %+v err=%v", gotFin, err)
	}

	runs := ReadRunStates(dir)
	if len(runs) != 1 || runs["p-1"] == nil || runs["p-1"].Phase != "epic" {
		t.Errorf("ReadRunStates should return only the epic run, got %+v", runs)
	}
	fins := ReadFinishRunStates(dir)
	if len(fins) != 1 || fins["p-1"] == nil || fins["p-1"].Phase != "acceptance" {
		t.Errorf("ReadFinishRunStates should return only the acceptance, got %+v", fins)
	}
}

// TestReadFinishRunStateMissing: same contract as ReadRunState on a run that
// never happened - an error to branch on, not a panic and not a zero value.
func TestReadFinishRunStateMissing(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRunState(dir, &RunState{TaskID: "p-1", Project: "p", Kind: RunKindEpic}); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	// A run WITHOUT an acceptance must not resolve to the run's own file.
	got, err := ReadFinishRunState(dir, "p-1")
	if !os.IsNotExist(err) {
		t.Errorf("expected a not-exist error, got %+v err=%v", got, err)
	}
	if got, err := ReadFinishRunState(dir, "nope"); !os.IsNotExist(err) {
		t.Errorf("expected a not-exist error for an unknown task, got %+v err=%v", got, err)
	}
}

// TestReadRunStatesReadsFilesPmDidNotWrite is the risky half of the split: the
// new filter runs over files that are ALREADY on disk, written by an older pm
// (no `kind` at all) or by hand. Going through WriteRunState would prove
// nothing here - it would only show that the writer and the reader agree on the
// same new rule - so these are placed raw.
func TestReadRunStatesReadsFilesPmDidNotWrite(t *testing.T) {
	dir := t.TempDir()
	execDir := filepath.Join(dir, ".executor")
	if err := os.MkdirAll(execDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Names an older pm could have produced: no kind, a dotted id, and the
	// neighbouring artifacts that share the directory.
	raw := map[string]string{
		"p-1.json":     `{"task_id":"p-1","project":"p","status":"running"}`,
		"app-1.2.json": `{"task_id":"app-1.2","project":"p","kind":"work","status":"done"}`,
	}
	for name, body := range raw {
		if err := os.WriteFile(filepath.Join(execDir, name), []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	for _, name := range []string{"p-1.log", "journal.jsonl", "p-1.finish.claim"} {
		if err := os.WriteFile(filepath.Join(execDir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	runs := ReadRunStates(dir)
	if len(runs) != 2 || runs["p-1"] == nil || runs["app-1.2"] == nil {
		t.Fatalf("pre-existing run-states must still be read, got %v", keysOf(runs))
	}
	if got, err := ReadRunState(dir, "p-1"); err != nil || got.TaskID != "p-1" {
		t.Errorf("ReadRunState on a legacy file: %+v err=%v", got, err)
	}
	if fins := ReadFinishRunStates(dir); len(fins) != 0 {
		t.Errorf("nothing here is an acceptance, got %v", keysOf(fins))
	}
}

// TestReadRunStatesSkipsAliasedFile: a task id may contain a dot, so task
// "x.finish" owns the very file name the acceptance of "x" uses. Whichever way
// it is read it belongs to somebody - the one thing it must never do is enter
// the acceptance map under a run's id.
func TestReadRunStatesSkipsAliasedFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRunState(dir, &RunState{TaskID: "x.finish", Project: "p", Kind: RunKindWork}); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	if fins := ReadFinishRunStates(dir); len(fins) != 0 {
		t.Errorf("a run of task \"x.finish\" is not an acceptance, got %v", keysOf(fins))
	}
	if runs := ReadRunStates(dir); len(runs) != 0 {
		t.Errorf("its file name is the acceptance encoding, so it cannot be a run either, got %v", keysOf(runs))
	}
}

func keysOf(m map[string]*RunState) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestIsFinishRunStateFile(t *testing.T) {
	cases := map[string]bool{
		"p-1.finish.json":   true,
		"p-1.finish.log":    true, // the infix sits before the extension in both
		"p-1.json":          false,
		"p-1.log":           false,
		"p-1.finished.json": false,
		"p-1.json.finish":   false,
		"app-1.2.json":      false,
		"journal.jsonl":     false,
		"p-1.finish.claim":  true, // the acceptance claim is an acceptance artifact too
		".finish.json":      true,
		"p-1":               false,
	}
	for name, want := range cases {
		if got := isFinishRunStateFile(name); got != want {
			t.Errorf("isFinishRunStateFile(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestRunStateFilesNeedNoMigration: the run's own paths are byte-for-byte what
// they were before the split, so no file already on disk has to move.
func TestRunStateFilesNeedNoMigration(t *testing.T) {
	dir := t.TempDir()
	for _, kind := range []string{RunKindWork, RunKindEpic, ""} {
		if err := WriteRunState(dir, &RunState{TaskID: "p-9", Project: "p", Kind: kind}); err != nil {
			t.Fatalf("WriteRunState(%q): %v", kind, err)
		}
		want := filepath.Join(dir, ".executor", "p-9.json")
		if _, err := os.Stat(want); err != nil {
			t.Errorf("kind %q should still write %s: %v", kind, want, err)
		}
		if _, err := os.Stat(FinishRunPath(dir, "p-9")); !os.IsNotExist(err) {
			t.Errorf("kind %q must not write the finish file (err=%v)", kind, err)
		}
	}
}

func TestNewSessionIDUnique(t *testing.T) {
	a, b := NewSessionID(), NewSessionID()
	if a == b || a == "" {
		t.Errorf("session ids should be unique and non-empty: %q %q", a, b)
	}
	if len(a) != 36 { // 8-4-4-4-12
		t.Errorf("session id %q is not uuid-shaped", a)
	}
}

func TestNewSessionIDVersionAndVariantBits(t *testing.T) {
	id := NewSessionID()
	// version nibble is the first char of the third group ("xxxx-xxxx-Vxxx-...")
	if id[14] != '4' {
		t.Errorf("session id %q has version nibble %q, want 4 (UUIDv4)", id, string(id[14]))
	}
	// variant bits are the top two bits of the first char of the fourth group,
	// which must render as one of 8/9/a/b (RFC 4122 variant 10xx).
	switch id[19] {
	case '8', '9', 'a', 'b':
	default:
		t.Errorf("session id %q has variant nibble %q, want 8/9/a/b", id, string(id[19]))
	}
}

func TestNewRunIDSameShapeAsSessionID(t *testing.T) {
	id := NewRunID()
	if len(id) != 36 {
		t.Errorf("run id %q is not uuid-shaped", id)
	}
	if id[14] != '4' {
		t.Errorf("run id %q has version nibble %q, want 4 (UUIDv4)", id, string(id[14]))
	}
}
