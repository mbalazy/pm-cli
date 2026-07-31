package storage

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// Executor run statuses (top-level run + per-sub). Kept as plain strings (not
// TaskStatus) - they describe a run, not a board column.
const (
	RunStatusRunning = "running"
	RunStatusDone    = "done"
	RunStatusFailed  = "failed"
)

// RunState is the live state of an executor run (`pm work` / `pm run-epic`),
// written by the executor process and read by the TUI for observability. One
// file per top-level run, keyed by the task (work) or tracker (run-epic) id,
// under the project's `.executor/` dir. The executor owns the file; the TUI
// only reads it.
type RunState struct {
	TaskID string `json:"task_id"` // task (work) or tracker (run-epic) id
	// RunID identifies this physical run (see NewRunID). Carried here so an
	// observer that journals on the manager's behalf - the board's killRun,
	// which never shares the manager's process - can stamp the same id the
	// manager wrote on its own journal lines.
	RunID    string `json:"run_id,omitempty"`
	Project  string `json:"project"`
	Kind     string `json:"kind"`   // "work" | "run-epic"
	Status   string `json:"status"` // running | done | failed
	PID      int    `json:"pid"`
	RepoPath string `json:"repo_path,omitempty"` // git repo (worker cwd) - to resolve transcript .jsonl
	LogPath  string `json:"log_path,omitempty"`
	Started  string `json:"started"` // RFC3339
	Updated  string `json:"updated"` // RFC3339

	// The worker being driven right now: the sub (epic) or the task itself.
	CurrentSub     string `json:"current_sub,omitempty"`
	CurrentSession string `json:"current_session,omitempty"` // worker session id -> transcript .jsonl
	Phase          string `json:"phase,omitempty"`           // free-form ("running", "implement", ...)
	// WorkerPGID is the process-group id of the in-flight `claude -p` worker
	// (cmd.groupCmd gives it its own group so ITS descendants are killable), set
	// while a worker is live and cleared when it returns. The worker is NOT in
	// the manager's group, so this is the only handle an outside observer has on
	// it: the manager forwards catchable signals itself, but a manager that is
	// SIGKILLed - or killed before its forwarding finishes - would otherwise
	// leave an unreachable worker running for the rest of its timeout. Kill
	// signals it alongside the manager's own group.
	WorkerPGID int `json:"worker_pgid,omitempty"`

	Subs  []SubRun `json:"subs,omitempty"` // per-sub progress (run-epic); single entry for work
	Error string   `json:"error,omitempty"`
}

// SubRun is the per-sub progress/outcome within a run.
type SubRun struct {
	ID      string   `json:"id"`
	Status  string   `json:"status"` // pending | running | merged | blocked | failed | conflict | skipped
	Session string   `json:"session,omitempty"`
	Note    string   `json:"note,omitempty"`
	Commits []string `json:"commits,omitempty"`
	// Worker run stats from the claude envelope (0 for skips/manual).
	Turns   int     `json:"turns,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
}

func executorRunDir(projectDir string) string {
	return filepath.Join(projectDir, ".executor")
}

// ExecutorRunPath is the run-state JSON path for a top-level run.
func ExecutorRunPath(projectDir, taskID string) string {
	return filepath.Join(executorRunDir(projectDir), taskID+".json")
}

// ExecutorLogPath is the combined stdout/stderr log path for a background run.
func ExecutorLogPath(projectDir, taskID string) string {
	return filepath.Join(executorRunDir(projectDir), taskID+".log")
}

// WriteRunState atomically writes the run-state for st.TaskID under projectDir,
// stamping Updated.
func WriteRunState(projectDir string, st *RunState) error {
	if err := os.MkdirAll(executorRunDir(projectDir), 0755); err != nil {
		return err
	}
	st.Updated = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	path := ExecutorRunPath(projectDir, st.TaskID)
	// The tmp name carries the writer's pid so two PROCESSES writing the same
	// run-state cannot land in one another's os.WriteFile (truncate+write is
	// not atomic) and rename a torn mixture of both. Real pairs: the board's
	// launch seed or killRun against the manager, which since the heartbeat
	// (pm-cli-40) writes every 30s rather than once per sub. Only the rename
	// publishes, and rename is atomic, so a reader sees one whole version or
	// the other.
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		// Per-pid names no longer overwrite each other, so a failed write would
		// otherwise accumulate one stray tmp per failure instead of reusing the
		// single fixed name.
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// HeartbeatInterval is how often a live run re-stamps its run-state while a
// worker is in flight. Deliberately fixed (no flag, no project.yaml knob): the
// signal exists so an observer can tell a long sub from a stopped run, and
// that only works if it is always on. Short enough that a stalled stamp shows
// up well inside the 60m worker timeout, long enough that the writes are noise.
// Note what it proves: the stamp comes from the MANAGER process, so a fresh
// beat means "the manager is alive and inside a worker" - a worker wedged on a
// network read still beats. Catching THAT would need transcript-tick stamping.
const HeartbeatInterval = 30 * time.Second

// RunWriter is the single serialization point for every write of ONE run-state
// within a process. The file write itself is atomic (tmp+rename), but the
// hazard is the STRUCT: the main goroutine mutates run fields (CurrentSub,
// Phase, Subs) right before writing, while the heartbeat goroutine writes the
// same pointer on a ticker. Both go through the writer's mutex, so there is no
// race on the fields - a mutex inside WriteRunState alone would not be enough.
//
// The zero value is not usable; construct with NewRunWriter. A nil *RunWriter
// is a no-op on every method, so callers that have no run-state (an epic sub's
// worker invoked outside a manager) need no branching.
type RunWriter struct {
	mu  sync.Mutex
	dir string
	st  *RunState
}

// NewRunWriter binds a writer to st under projectDir. After this call, st must
// only be read/mutated through the writer.
func NewRunWriter(projectDir string, st *RunState) *RunWriter {
	return &RunWriter{dir: projectDir, st: st}
}

// Update applies fn to the run-state under the lock and persists the result.
// A nil fn just re-stamps and writes (that is what the heartbeat does - it
// never guesses a phase or invents subs, it only proves the run is alive).
func (w *RunWriter) Update(fn func(*RunState)) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if fn != nil {
		fn(w.st)
	}
	return WriteRunState(w.dir, w.st)
}

// Read runs fn against the run-state under the lock, without writing.
func (w *RunWriter) Read(fn func(*RunState)) {
	if w == nil || fn == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	fn(w.st)
}

// Heartbeat starts a goroutine that re-stamps the run-state every interval and
// returns its stop function. The stop function is idempotent and WAITS for the
// goroutine to exit, so once it returns nothing can touch the file again - the
// heartbeat can never outlive the worker it was started for. A nil writer or a
// non-positive interval yields a no-op stop.
func (w *RunWriter) Heartbeat(interval time.Duration) func() {
	if w == nil || interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				// Best-effort, same contract as every other run-state write: a
				// failed stamp only costs a stale dashboard.
				_ = w.Update(nil)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		<-exited
	}
}

// ReadRunState reads the run-state for taskID under projectDir. Returns an error
// (os.IsNotExist) when no run exists.
func ReadRunState(projectDir, taskID string) (*RunState, error) {
	data, err := os.ReadFile(ExecutorRunPath(projectDir, taskID))
	if err != nil {
		return nil, err
	}
	var st RunState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// ReadRunStates returns all run-states under projectDir, keyed by task id.
// Missing/unreadable dir -> empty map (no error), so callers can poll cheaply.
func ReadRunStates(projectDir string) map[string]*RunState {
	out := map[string]*RunState{}
	entries, err := os.ReadDir(executorRunDir(projectDir))
	if err != nil {
		return out
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(executorRunDir(projectDir), n))
		if err != nil {
			continue
		}
		var st RunState
		if err := json.Unmarshal(data, &st); err != nil {
			continue
		}
		out[st.TaskID] = &st
	}
	return out
}

// IsLive reports whether the run is marked running AND its process is still
// alive. A crashed executor leaves status=running with a dead PID; this lets the
// TUI distinguish "running" from "stopped (stale)".
func (st *RunState) IsLive() bool {
	if st == nil || st.Status != RunStatusRunning || st.PID <= 0 {
		return false
	}
	return ProcessAlive(st.PID)
}

// Kill signals the run's whole process group, falling back to the bare pid, AND
// the in-flight worker's group. Background runs are started detached (Setsid),
// so the manager leads its own process group - but the `claude -p` worker is NOT
// in it: the worker gets its own group (cmd.groupCmd) so that ITS descendants
// are killable. A live manager forwards catchable signals to the worker itself,
// which is why WorkerPGID is signalled here too rather than instead: it is the
// backstop for the manager dying before it can forward (this call escalating to
// SIGKILL, an OOM kill, a crash), which would otherwise leave a headless worker
// running for the rest of its timeout in a worktree the caller is about to
// unlock. Signalling both is safe - the worker group send is best-effort and its
// error is intentionally not reported. Returns an error when there is no manager
// pid to signal.
func (st *RunState) Kill(sig syscall.Signal) error {
	if st == nil || st.PID <= 0 {
		return fmt.Errorf("no pid to signal")
	}
	if st.WorkerPGID > 0 && st.WorkerPGID != st.PID {
		_ = syscall.Kill(-st.WorkerPGID, sig)
	}
	// Negative pid targets the process group (pgid == manager pid for a detached
	// run). Fall back to the bare pid if the process isn't a group leader.
	if err := syscall.Kill(-st.PID, sig); err == nil {
		return nil
	}
	return syscall.Kill(st.PID, sig)
}

// ProcessAlive reports whether pid refers to a live process (Unix: signal 0).
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// NewSessionID returns a random UUID used to pin a worker's session id up front
// (passed via `claude --session-id`) so its transcript path is known before the
// worker emits anything.
func NewSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// NewRunID returns a random identifier for ONE physical executor run, stamped
// on the run-state and on every journal line that run appends. It exists
// because {kind, task_id, pid} does NOT identify a run: pids are recycled, so
// two runs of the same task can share a key, and `pm executor stats` then has
// to guess whether a second terminal line is a kill race (one run) or a
// distinct run whose `start` append was lost - guessing wrong either drops a
// run's subs/turns/cost or double-counts them. Same shape as a session id; the
// two are independent (a run drives many workers, each with its own session).
func NewRunID() string { return NewSessionID() }
