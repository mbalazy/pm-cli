package storage

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/fs"
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

// Executor run kinds (RunState.Kind). The kind is not decoration: it decides
// WHICH FILE the run-state lives in (see WriteRunState). An epic run and the
// acceptance of that same epic carry the SAME task id - the tracker's - so
// without the split the acceptance would overwrite the run-state of the very
// run it is accepting.
// Nothing validates Kind, and the routing fails TOWARDS the run's own file:
// any spelling other than RunKindFinish exactly (a typo, "Finish", a kind added
// later) writes over the run-state of the run it means to accept, which is the
// collision this exists to prevent. So an acceptance writer must carry
// RunKindFinish verbatim, not a literal of its own.
const (
	RunKindWork   = "work"
	RunKindEpic   = "run-epic"
	RunKindFinish = "finish"
)

// RunState is the live state of an executor run (`pm work` / `pm run-epic` /
// the acceptance of one), written by the executor process and read by the TUI
// for observability. One file per top-level run, keyed by the task (work) or
// tracker (run-epic) id, under the project's `.executor/` dir - so a tracker
// that has been accepted has TWO files there, the run's and its acceptance's
// (see runStatePath). The executor owns the file; the TUI only reads it.
type RunState struct {
	TaskID string `json:"task_id"` // task (work) or tracker (run-epic) id
	// RunID identifies this physical run (see NewRunID). Carried here so an
	// observer that journals on the manager's behalf - the board's killRun,
	// which never shares the manager's process - can stamp the same id the
	// manager wrote on its own journal lines.
	RunID    string `json:"run_id,omitempty"`
	Project  string `json:"project"`
	Kind     string `json:"kind"`   // RunKindWork | RunKindEpic | RunKindFinish
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
	ID string `json:"id"`
	// pending | running | merged | pushed | verified | blocked | failed |
	// conflict | skipped | manual | aborted, plus done | partial for an
	// acceptance run (RunKindFinish), whose one entry carries the verdict on
	// the tracker it accepted.
	// The three green words differ by what the RUN did with the work: merged
	// into the epic branch, pushed to origin (independent), or verified only
	// (standalone `pm work`, which ends in a draft PR).
	Status  string   `json:"status"`
	Session string   `json:"session,omitempty"`
	Note    string   `json:"note,omitempty"`
	Commits []string `json:"commits,omitempty"`
	// Worker run stats from the claude envelope (0 for skips/manual).
	Turns   int     `json:"turns,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Review is what the worker's review phase actually did - spawns, rounds and
	// model, collected by the worker guard hook. Nil when nothing was observed
	// (a skip, a sub that spawned nothing, or a run from before this existed).
	Review *ReviewTelemetry `json:"review,omitempty"`
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

// finishRunInfix separates an acceptance run's files from the run's own. The
// run keeps <taskID>.json / <taskID>.log unchanged - no file on disk needs
// migrating - and the acceptance gets <taskID>.finish.json / .finish.log.
//
// The encoding is not injective, and cannot be made so without renaming files
// that already exist: a task id may contain a dot, so the run of a task named
// "x.finish" and the acceptance of a task named "x" resolve to ONE path and
// would overwrite each other. Reads survive it (the state's own task_id says
// which one it is - see readRunStatesDir and ReadFinishRunState); two such
// tasks existing in one project at once does not, and nothing here prevents it.
const finishRunInfix = ".finish"

// FinishRunPath is the run-state JSON path for the acceptance (odbiór) of the
// run on taskID. Separate from ExecutorRunPath because the two runs share a
// task id and would otherwise be one file.
func FinishRunPath(projectDir, taskID string) string {
	return filepath.Join(executorRunDir(projectDir), taskID+finishRunInfix+".json")
}

// FinishRunLogPath is the combined stdout/stderr log path for a background
// acceptance run.
func FinishRunLogPath(projectDir, taskID string) string {
	return filepath.Join(executorRunDir(projectDir), taskID+finishRunInfix+".log")
}

// FinishReportPath is where an acceptance run's final markdown report is
// written - the artifact a human reads in the morning, beside the run-state it
// belongs to. Deliberately NOT a `.json`, so readRunStatesDir never has to
// consider it at all.
func FinishReportPath(projectDir, taskID string) string {
	return filepath.Join(executorRunDir(projectDir), taskID+finishRunInfix+".md")
}

// runStatePath is the ONE place that decides which file a run-state belongs in.
// Keeping the decision here (rather than in a second write function callers
// must remember to pick) is why WriteRunState/RunWriter need no finish-aware
// variant: an acceptance writer just carries Kind: RunKindFinish.
//
// Writes route by KIND while reads classify by the file a state's TASK ID
// resolves to (see readRunStatesDir), so the two agree only while a state's
// kind matches the file it came out of. A hand-edited `<id>.json` carrying kind
// "finish" is therefore read as that task's run and written back as its
// acceptance: the original file keeps its last contents forever and a phantom
// acceptance appears beside it. Nothing in pm can produce that state - only an
// editor can - so it is documented rather than guarded.
func runStatePath(projectDir, kind, taskID string) string {
	if kind == RunKindFinish {
		return FinishRunPath(projectDir, taskID)
	}
	return ExecutorRunPath(projectDir, taskID)
}

// WriteRunState atomically writes the run-state for st.TaskID under projectDir,
// stamping Updated. The target file follows st.Kind: an acceptance run
// (RunKindFinish) writes beside the run of the same task, never over it (the
// one id where "beside" is still "over" is in finishRunInfix).
func WriteRunState(projectDir string, st *RunState) error {
	if err := os.MkdirAll(executorRunDir(projectDir), 0755); err != nil {
		return err
	}
	st.Updated = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	path := runStatePath(projectDir, st.Kind, st.TaskID)
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
	return readRunStateFile(ExecutorRunPath(projectDir, taskID))
}

// ReadFinishRunState reads the acceptance run-state for taskID under
// projectDir. Same contract as ReadRunState - an error (never a panic, never a
// zero value) when no acceptance has run - plus one check ReadRunState cannot
// make: the state found at that path must say it belongs to taskID. A task id
// may contain a dot (ValidateTaskID permits it), so the run of a task literally
// named "x.finish" occupies the file where the acceptance of "x" would live,
// and returning it would report one task's run as another's acceptance. A
// mismatch reads as fs.ErrNotExist, because for the caller that is what it is:
// no acceptance of this task is recorded here.
func ReadFinishRunState(projectDir, taskID string) (*RunState, error) {
	st, err := readRunStateFile(FinishRunPath(projectDir, taskID))
	if err != nil {
		return nil, err
	}
	if st.TaskID != taskID {
		return nil, fmt.Errorf("%s: run-state belongs to %q, not to %q: %w",
			FinishRunPath(projectDir, taskID), st.TaskID, taskID, fs.ErrNotExist)
	}
	return st, nil
}

func readRunStateFile(path string) (*RunState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st RunState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// ReadRunStates returns all RUN run-states under projectDir, keyed by task id -
// acceptance run-states are deliberately excluded. Every consumer of this map
// (the board's dashboard, killRun, the run badges) means runs, and an
// acceptance shares its run's task id, so an unfiltered read would let one
// evict the other in the map whichever way the directory happened to sort.
// Missing/unreadable dir -> empty map (no error), so callers can poll cheaply.
func ReadRunStates(projectDir string) map[string]*RunState {
	return readRunStatesDir(projectDir, false)
}

// ReadFinishRunStates returns all ACCEPTANCE run-states under projectDir, keyed
// by the task id of the run each one accepts. The mirror image of
// ReadRunStates: the two never return the same file.
func ReadFinishRunStates(projectDir string) map[string]*RunState {
	return readRunStatesDir(projectDir, true)
}

// readRunStatesDir collects either the run or the acceptance run-states. A file
// belongs to the requested set when it is the file the state INSIDE IT would be
// written to - that one test classifies every file totally and unambiguously,
// because a given name can be the run path of at most one task id and the
// acceptance path of at most one other. Matching on the name alone would not:
// a task id may contain a dot (ValidateTaskID permits it), so the run of a task
// literally named "x.finish" lives at `x.finish.json`, and a name-only rule
// would have to either hand that run to the acceptance map under the wrong task
// or drop a live run the board still needs to show. Its own `task_id` settles
// it. A file whose id matches neither path (empty, hand-edited, a legacy name
// nothing would write today) is skipped rather than keyed on a guess.
//
// Kind is deliberately not consulted: the file name is what made the two runs
// collide, and a state whose kind and path disagree can only come from an
// editor (see runStatePath).
func readRunStatesDir(projectDir string, finish bool) map[string]*RunState {
	dir := executorRunDir(projectDir)
	out := map[string]*RunState{}
	entries, err := os.ReadDir(dir)
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
		st, err := readRunStateFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		want := ExecutorRunPath(projectDir, st.TaskID)
		if finish {
			want = FinishRunPath(projectDir, st.TaskID)
		}
		if filepath.Base(want) != n {
			continue
		}
		out[st.TaskID] = st
	}
	return out
}

// IsLive reports whether the run is marked running AND its process is still
// alive. A crashed executor leaves status=running with a dead PID; this lets the
// TUI distinguish "running" from "stopped (stale)". The run's own Started stamp
// is the second criterion, so a run-state left behind by a crash stops reading
// as live the moment its pid is handed to somebody else.
func (st *RunState) IsLive() bool {
	if st == nil || st.Status != RunStatusRunning || st.PID <= 0 {
		return false
	}
	return ProcessAliveSinceStamp(st.PID, st.Started)
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
// Nothing is signalled until the run's pid is PROVEN to still be the run's own
// process. A run-state is a file that outlives its process - a `kill -9`, an OOM
// or a reboot leaves `status: running` behind with a pid nobody cleaned up, and
// after a reboot the numbers get handed out again from the bottom. Signalling
// that number sends SIGTERM, and two seconds later SIGKILL, to a GROUP of
// processes that have nothing to do with pm. Identity is the run's own Started
// stamp against the process start time (see ProcessAliveSince).
func (st *RunState) Kill(sig syscall.Signal) error {
	if st == nil || st.PID <= 0 {
		return fmt.Errorf("no pid to signal")
	}
	if !ProcessAliveSinceStamp(st.PID, st.Started) {
		return &StaleRunError{TaskID: st.TaskID, PID: st.PID}
	}
	// The worker group is signalled only because the MANAGER just proved to be
	// alive: a live manager clears WorkerPGID the moment its worker returns, so
	// the field names the worker in flight right now. (Its own start time cannot
	// be the test - a worker starts DURING the run, i.e. after the stamp.) The
	// liveness check is the cheap guard against the manager having died between
	// the proof and here.
	if st.WorkerPGID > 0 && st.WorkerPGID != st.PID && ProcessAlive(st.WorkerPGID) {
		_ = killSignal(-st.WorkerPGID, sig)
	}
	// Negative pid targets the process group (pgid == manager pid for a detached
	// run). Fall back to the bare pid if the process isn't a group leader.
	if err := killSignal(-st.PID, sig); err == nil {
		return nil
	}
	return killSignal(st.PID, sig)
}

// killSignal is syscall.Kill behind a seam so tests can assert WHAT a kill would
// signal without a test process ever signalling a real group.
var killSignal = syscall.Kill

// StaleRunError is returned by Kill when the run-state's pid can no longer be
// shown to belong to the run it describes: the process is gone, or it is a
// different one wearing a recycled number. It carries the pid so the caller can
// say so rather than reporting a silent no-op.
type StaleRunError struct {
	TaskID string
	PID    int
}

func (e *StaleRunError) Error() string {
	return fmt.Sprintf("run %s is no longer running (pid %d is gone or belongs to another process) - nothing was signalled", e.TaskID, e.PID)
}

// NewSessionID returns a random UUIDv4 used to pin a worker's session id up
// front (passed via `claude --session-id`) so its transcript path is known
// before the worker emits anything.
func NewSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail in practice on any supported platform.
		panic("storage: crypto/rand.Read failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
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
