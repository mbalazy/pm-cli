// Package runctl is run control for a caller that is not the board: the
// cockpit's `pm serve` (pm-cli-118-21). It re-states, ONCE and without
// bubbletea, the rules the board's launch_executor.go and run_control.go
// follow - which argv a launch runs, where its log and run-state live, what
// a kill signals and reconciles, how an acceptance claim is taken and kept
// alive - so that an HTTP endpoint and the TUI cannot drift into starting
// different processes for the same button. internal/cmd cannot be imported
// here (it imports internal/server, which imports this), so the spawn is a
// detached `pm <args>` like the board's, never a call into the manager.
//
// This is the first place HTTP starts processes. What keeps it honest: every
// spawn is a Plan first (the preview the dialog shows IS the argv that runs),
// never `--sim` (nobody may be at the screen), the pm binary is the one
// serving (os.Executable, the chainFinish rule - "pm" on PATH may be another
// version), and every manager started from here carries PM_LAUNCH_SOURCE so
// its journal start line says `source: cockpit` and `pm executor stats` can
// tell the web's runs from the board's.
package runctl

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// Action is one of the run-control actions an attention row may carry
// (storage.ActionClaim etc.); the API decides which a row has, this package
// decides what each does.
type Action string

const (
	ActionClaim        Action = storage.ActionClaim
	ActionReleaseClaim Action = storage.ActionReleaseClaim
	ActionRerunFinish  Action = storage.ActionRerunFinish
	ActionResumeRun    Action = storage.ActionResumeRun
	ActionKill         Action = storage.ActionKill
)

// Actions lists every action this package performs, in the order a UI may
// offer them.
var Actions = []Action{ActionClaim, ActionReleaseClaim, ActionRerunFinish, ActionResumeRun, ActionKill}

// ParseAction accepts the wire name of an action.
func ParseAction(s string) (Action, bool) {
	for _, a := range Actions {
		if string(a) == s {
			return a, true
		}
	}
	return "", false
}

// Flags are the launch toggles the dialog offers. Sim is deliberately not
// one of them: a detached acceptance keeps its hands off a shared runtime
// (see the board's resolveFinishSim), and no web request may change that.
type Flags struct {
	// Yolo bypasses the worker's permission prompts (`--yolo`); only
	// resume_run reads it - an acceptance is yolo by default.
	Yolo bool `json:"yolo"`
	// Additional runs in an isolated worktree slot (`--additional`); only
	// honoured when the project has slots configured (Plan.AdditionalAvail).
	Additional bool `json:"additional"`
}

// LaunchSourceEnv is the environment variable a spawned manager reads to
// stamp its journal start line (storage.JournalEntry.Source).
const LaunchSourceEnv = storage.LaunchSourceEnv

// Source is the value this package stamps: the cockpit.
const Source = "cockpit"

// Controller performs the actions over one store. Zero value is not usable;
// build it with New.
type Controller struct {
	store storage.TaskStore
	// exe is the pm binary to spawn; resolved from os.Executable when empty.
	exe string
	// session is the claim identity (`cockpit-<host>`), the one string a
	// later Release or heartbeat matches against.
	session string
	now     func() time.Time
	// after schedules the SIGKILL escalation; tests replace it to run at
	// once instead of waiting two seconds.
	after func(d time.Duration, fn func())

	mu   sync.Mutex
	held map[string]*heldClaim // project/tracker -> the claim + its heartbeat
}

// Options tunes a Controller; the zero value is production.
type Options struct {
	// Exe overrides the pm binary (tests hand in a fake script).
	Exe string
	// Session overrides the claim identity (default `cockpit-<hostname>`).
	Session string
	// Now is the clock (default time.Now).
	Now func() time.Time
	// After schedules a delayed call (default time.AfterFunc).
	After func(d time.Duration, fn func())
}

// New builds a Controller over store.
func New(store storage.TaskStore, opts Options) *Controller {
	c := &Controller{store: store, exe: opts.Exe, session: opts.Session, now: opts.Now, after: opts.After, held: map[string]*heldClaim{}}
	if c.session == "" {
		c.session = Source + "-" + storage.Hostname()
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.after == nil {
		c.after = func(d time.Duration, fn func()) { time.AfterFunc(d, fn) }
	}
	return c
}

// Session is the claim identity this controller writes.
func (c *Controller) Session() string { return c.session }

// executable resolves the pm binary: the override, else the binary that is
// serving - os.Executable and not "pm" on PATH, the chainFinish rule (a
// manager on another machine, or an older pm earlier on PATH, would be a
// different program than the one whose API was called).
func (c *Controller) executable() (string, error) {
	if c.exe != "" {
		return c.exe, nil
	}
	return os.Executable()
}

// --- errors ---

// BusyError says the acceptance claim is held by somebody else. Transports
// map it to 409.
type BusyError struct {
	Holder *storage.FinishClaim
}

func (e *BusyError) Error() string {
	h := e.Holder
	return fmt.Sprintf("acceptance of %s is already claimed by %s (pid %d, session %q) since %s",
		h.TrackerID, h.Host, h.PID, h.Session, storage.FinishClaimAge(h.Started)+" ago")
}

// NotFoundError says the project or task does not exist; transports map it
// to 404.
type NotFoundError struct{ What string }

func (e *NotFoundError) Error() string { return e.What + " not found" }

// InputError is a caller's mistake (an unknown action, a flag the project
// cannot honour); transports map it to 400.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// NoRunError says there is no run to act on (kill with nothing live, release
// with nothing held). Transports map it to 409, like a stale run - the
// caller's picture of the world is out of date, not wrong in form.
type NoRunError struct{ Msg string }

func (e *NoRunError) Error() string { return e.Msg }

// --- plan ---

// Plan is what an action would do, resolved from the store: the argv and
// where it runs, the log it writes, and the warnings the board would show.
// The dialog renders it; Spawn executes exactly it.
type Plan struct {
	Action  Action `json:"action"`
	Project string `json:"project"`
	TaskID  string `json:"task_id"`
	// Kind is the run-state kind the action touches: run-epic, work or finish.
	Kind string `json:"kind"`
	// Argv is the full command, `pm` first; empty for claim/release.
	Argv []string `json:"argv,omitempty"`
	// Cwd is the checkout the command runs in (the project's path).
	Cwd string `json:"cwd,omitempty"`
	// Log is the file the detached run writes.
	Log string `json:"log,omitempty"`
	// Warnings are the board's launch warnings (a live run of the same kind,
	// a dirty tree where it matters) - shown, never blocking.
	Warnings []string `json:"warnings,omitempty"`
	// AdditionalAvail says whether the project has worktree slots, i.e.
	// whether the `additional` flag means anything here.
	AdditionalAvail bool `json:"additional_avail"`
	// Session is the claim identity claim/release use.
	Session string `json:"session,omitempty"`
	// Target describes what kill would signal: "<kind> pid N" - or says the
	// run is not live.
	Target string `json:"target,omitempty"`
	// Live is the pid kill would signal, 0 when nothing is live.
	PID int `json:"pid,omitempty"`
}

// target is the resolved subject of an action.
type target struct {
	task      *storage.Task
	proj      *storage.Project
	stateDir  string
	isTracker bool
	slots     bool
	run       *storage.RunState // the run's own state (nil = none)
	accept    *storage.RunState // the acceptance's state (nil = none)
}

func (c *Controller) resolve(project, taskID string) (*target, error) {
	slug, err := c.store.ResolveProject(project)
	if err != nil {
		return nil, &NotFoundError{What: "project " + project}
	}
	task, err := c.store.FindTaskExact(slug, taskID)
	if err != nil {
		return nil, &NotFoundError{What: "task " + taskID}
	}
	proj, err := c.store.GetProject(slug)
	if err != nil {
		return nil, err
	}
	tasks, err := c.store.GetTasks(slug)
	if err != nil {
		return nil, err
	}
	t := &target{task: task, proj: proj, stateDir: c.store.ProjectDir(slug)}
	for _, other := range tasks {
		if other.Meta.Parent == task.Meta.ID {
			t.isTracker = true
			break
		}
	}
	t.slots = len(proj.GetExecutor().ResolveWorktrees(proj.Path)) > 0
	t.run, _ = storage.ReadRunState(t.stateDir, task.Meta.ID)
	t.accept, _ = storage.ReadFinishRunState(t.stateDir, task.Meta.ID)
	return t, nil
}

// Plan resolves what action would do for task taskID of project. It reads
// only local files and starts nothing.
func (c *Controller) Plan(project, taskID string, action Action, f Flags) (*Plan, error) {
	t, err := c.resolve(project, taskID)
	if err != nil {
		return nil, err
	}
	p := &Plan{Action: action, Project: t.task.Project, TaskID: t.task.Meta.ID, AdditionalAvail: t.slots}
	additional := f.Additional && t.slots
	switch action {
	case ActionClaim, ActionReleaseClaim:
		p.Kind = storage.RunKindFinish
		p.Session = c.session
		if h := storage.LiveFinishClaimHolder(t.stateDir, t.task.Meta.ID); h != nil {
			if action == ActionClaim && !(h.Host == storage.Hostname() && h.Session == c.session) {
				p.Warnings = append(p.Warnings, (&BusyError{Holder: h}).Error())
			}
			p.Target = fmt.Sprintf("claim held by %s (session %s) since %s ago", h.Host, h.Session, storage.FinishClaimAge(h.Started))
		} else if action == ActionReleaseClaim {
			p.Target = "no live claim"
		}
	case ActionRerunFinish:
		p.Kind = storage.RunKindFinish
		p.Argv = finishArgs(t.task, additional)
		p.Cwd, p.Log = t.proj.Path, storage.FinishRunLogPath(t.stateDir, t.task.Meta.ID)
		if t.accept.IsLive() {
			p.Warnings = append(p.Warnings, "an acceptance of this tracker is already running; the new one will refuse on the claim")
		}
		if h := storage.LiveFinishClaimHolder(t.stateDir, t.task.Meta.ID); h != nil {
			p.Warnings = append(p.Warnings, (&BusyError{Holder: h}).Error()+" - `pm finish` will refuse")
		}
	case ActionResumeRun:
		p.Kind = storage.RunKindWork
		if t.isTracker {
			p.Kind = storage.RunKindEpic
		}
		p.Argv = executorArgs(t.task, t.isTracker, f.Yolo, additional)
		p.Cwd, p.Log = t.proj.Path, storage.ExecutorLogPath(t.stateDir, t.task.Meta.ID)
		if t.run.IsLive() {
			p.Warnings = append(p.Warnings, "a run of this task is already in flight; nothing stops two managers from working the same branch")
		}
		if !additional {
			if reason := gitPreflight(t.proj.Path, true); reason != "" {
				p.Warnings = append(p.Warnings, reason)
			}
		}
	case ActionKill:
		st, kind := pickRun(t)
		if st == nil {
			p.Target = "nothing is running"
			p.Kind = storage.RunKindEpic
		} else {
			p.Kind, p.PID = kind, st.PID
			p.Target = fmt.Sprintf("%s pid %d (started %s)", kind, st.PID, st.Started)
		}
	default:
		return nil, &InputError{Msg: fmt.Sprintf("unknown action %q", action)}
	}
	if t.proj.Path == "" && (action == ActionRerunFinish || action == ActionResumeRun) {
		p.Warnings = append(p.Warnings, "the project has no path in project.yaml - the launch will fail")
	}
	return p, nil
}

// pickRun chooses which of a task's two run-states an action means, the
// board's pickRunForTask rule: liveness first and the run wins a tie (it is
// the one spending a worker on code); when neither is live, the run's own
// record. Returns the state and its kind, nil when there is none.
func pickRun(t *target) (*storage.RunState, string) {
	if t.run.IsLive() {
		return t.run, t.run.Kind
	}
	if t.accept.IsLive() {
		return t.accept, storage.RunKindFinish
	}
	if t.run != nil {
		return t.run, t.run.Kind
	}
	if t.accept != nil {
		return t.accept, storage.RunKindFinish
	}
	return nil, ""
}

// executorArgs is the board's executorPMArgs without the dry-run and
// then-finish toggles the dialog does not offer: `work` for a leaf task,
// `run-epic` for a tracker.
func executorArgs(t *storage.Task, isTracker, yolo, additional bool) []string {
	sub := storage.RunKindWork
	if isTracker {
		sub = storage.RunKindEpic
	}
	args := []string{sub, t.Project, t.Meta.ID}
	if yolo {
		args = append(args, "--yolo")
	}
	if additional {
		args = append(args, "--additional")
	}
	return args
}

// finishArgs is the board's finishPMArgs with sim resolved to NEVER: a
// detached acceptance may not touch a shared runtime, and there is no tmux
// variant here for a human to sit in front of.
func finishArgs(t *storage.Task, additional bool) []string {
	args := []string{storage.RunKindFinish, t.Meta.ID, "--project", t.Project, "--no-sim"}
	if additional {
		args = append(args, "--additional")
	}
	return args
}

// gitPreflight is the board's boardGitPreflight as a warning: not a git
// checkout, or a dirty tree when the run would use the main checkout.
func gitPreflight(dir string, requireClean bool) string {
	if dir == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return "not a git repo: " + dir
	}
	if requireClean {
		st, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
		if err == nil && strings.TrimSpace(string(st)) != "" {
			return "working tree dirty - the run will refuse unless it uses an additional worktree"
		}
	}
	return ""
}

// --- spawn ---

// Started is what a spawn reports back.
type Started struct {
	PID  int      `json:"pid"`
	Log  string   `json:"log"`
	Argv []string `json:"argv"`
	Kind string   `json:"kind"`
	// Warnings are the plan's, repeated so the toast can carry them.
	Warnings []string `json:"warnings,omitempty"`
}

// Spawn starts the plan's command detached (its own session, output to the
// plan's log), seeds the run-state so the queue shows the run at once, and
// returns the pid. The board's "bg"/"finish" branch: a log truncated for a
// fresh run and APPENDED when a run of the same kind is live (truncating
// would blank the live run's record under it), the manager overwrites the
// seed with its own progress.
func (c *Controller) Spawn(p *Plan) (*Started, error) {
	if p == nil || len(p.Argv) == 0 {
		return nil, &InputError{Msg: "nothing to start"}
	}
	if p.Cwd == "" {
		return nil, &InputError{Msg: "the project has no path in project.yaml - set it first"}
	}
	if info, err := os.Stat(p.Cwd); err != nil || !info.IsDir() {
		return nil, &InputError{Msg: "the project path is not a directory: " + p.Cwd}
	}
	exe, err := c.executable()
	if err != nil {
		return nil, fmt.Errorf("resolve the pm binary: %w", err)
	}
	stateDir := c.store.ProjectDir(p.Project)
	if err := os.MkdirAll(filepath.Dir(p.Log), 0o755); err != nil {
		return nil, err
	}
	live := false
	if p.Kind == storage.RunKindFinish {
		st, _ := storage.ReadFinishRunState(stateDir, p.TaskID)
		live = st.IsLive()
	} else {
		st, _ := storage.ReadRunState(stateDir, p.TaskID)
		live = st.IsLive()
	}
	var logf *os.File
	if live {
		logf, err = os.OpenFile(p.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	} else {
		logf, err = os.Create(p.Log)
	}
	if err != nil {
		return nil, fmt.Errorf("open the run log: %w", err)
	}
	cmd := exec.Command(exe, p.Argv...)
	cmd.Dir = p.Cwd
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Env = append(os.Environ(), LaunchSourceEnv+"="+Source)
	// Detached: survives `pm serve` stopping, no controlling terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	logf.Close() // the child holds its own fd
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", strings.Join(p.Argv, " "), err)
	}
	// Seed the run-state so the queue and the board show the run at once;
	// the manager overwrites it with real progress. Best-effort, like the
	// board's seed.
	_ = storage.WriteRunState(stateDir, &storage.RunState{
		TaskID:   p.TaskID,
		Project:  p.Project,
		Kind:     p.Argv[0],
		Status:   storage.RunStatusRunning,
		PID:      cmd.Process.Pid,
		RepoPath: p.Cwd,
		LogPath:  p.Log,
		Started:  c.now().UTC().Format(time.RFC3339),
	})
	// The process is detached; nothing here waits on it. Reap it in the
	// background so a finished manager does not linger as a zombie of
	// pm serve for the life of the server.
	go func() { _ = cmd.Wait() }()
	return &Started{PID: cmd.Process.Pid, Log: p.Log, Argv: append([]string{"pm"}, p.Argv...), Kind: p.Argv[0], Warnings: p.Warnings}, nil
}

// --- claim ---

// ClaimResult is what Claim / Release answer.
type ClaimResult struct {
	Claim *storage.FinishClaim `json:"claim,omitempty"`
	// Refreshed says an existing claim of ours was refreshed rather than a
	// new one taken.
	Refreshed bool `json:"refreshed,omitempty"`
	// Released says Release removed a claim; false when there was none.
	Released bool `json:"released,omitempty"`
	// Heartbeat is how often the server refreshes the claim, and MaxHold how
	// long it keeps doing so before letting the TTL run out.
	HeartbeatSeconds int `json:"heartbeat_seconds,omitempty"`
	MaxHoldSeconds   int `json:"max_hold_seconds,omitempty"`
}

var (
	// ClaimHeartbeat is how often a held claim is refreshed.
	ClaimHeartbeat = 3 * time.Minute
	// ClaimMaxHold is how long the server keeps a claim alive on the user's
	// behalf; past it the heartbeat stops and the claim lapses on its TTL,
	// so a claim taken and forgotten never blocks a run for a whole day.
	ClaimMaxHold = time.Hour
)

type heldClaim struct {
	claim *storage.FinishClaim
	stop  chan struct{}
}

// Claim takes the acceptance claim of taskID for the cockpit
// (`pm finish claim <tracker> --session cockpit-<host>`), and keeps it alive
// with a heartbeat until Release, an error, or ClaimMaxHold. A claim already
// held by this controller is refreshed instead. Somebody else's live claim is
// a *BusyError.
func (c *Controller) Claim(project, taskID string) (*ClaimResult, error) {
	t, err := c.resolve(project, taskID)
	if err != nil {
		return nil, err
	}
	key := t.task.Project + "/" + t.task.Meta.ID
	claim, err := storage.AcquireFinishClaim(t.stateDir, t.task.Meta.ID, c.session)
	if err != nil {
		var busy *storage.FinishClaimBusyError
		if errors.As(err, &busy) {
			return nil, &BusyError{Holder: busy.Holder}
		}
		return nil, err
	}
	res := &ClaimResult{Claim: claim, Refreshed: claim.Refreshed != claim.Started,
		HeartbeatSeconds: int(ClaimHeartbeat.Seconds()), MaxHoldSeconds: int(ClaimMaxHold.Seconds())}
	// The heartbeat refreshes ITS OWN copy: RefreshFinishClaim moves the
	// stamp on the struct it is handed, and the one in the result belongs
	// to the caller now.
	own := *claim
	c.mu.Lock()
	defer c.mu.Unlock()
	if h, ok := c.held[key]; ok {
		h.claim = &own
		return res, nil
	}
	h := &heldClaim{claim: &own, stop: make(chan struct{})}
	c.held[key] = h
	go c.heartbeat(key, t.stateDir, t.task.Meta.ID, h)
	return res, nil
}

// heartbeat refreshes the claim every ClaimHeartbeat until stopped, a
// refresh fails (the claim moved on, or lapsed), or ClaimMaxHold passes.
func (c *Controller) heartbeat(key, stateDir, tracker string, h *heldClaim) {
	deadline := time.NewTimer(ClaimMaxHold)
	defer deadline.Stop()
	tick := time.NewTicker(ClaimHeartbeat)
	defer tick.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-deadline.C:
			c.forget(key)
			return
		case <-tick.C:
			c.mu.Lock()
			claim := h.claim
			c.mu.Unlock()
			if err := storage.RefreshFinishClaim(stateDir, tracker, claim); err != nil {
				c.forget(key)
				return
			}
		}
	}
}

func (c *Controller) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.held, key)
}

// Release drops the cockpit's claim on taskID: the one this controller holds,
// or - after a restart - any live claim carrying this controller's session on
// this host. Somebody else's claim is a *BusyError; no claim at all is a
// no-op (Released false).
func (c *Controller) Release(project, taskID string) (*ClaimResult, error) {
	t, err := c.resolve(project, taskID)
	if err != nil {
		return nil, err
	}
	key := t.task.Project + "/" + t.task.Meta.ID
	c.mu.Lock()
	h, ok := c.held[key]
	if ok {
		close(h.stop)
		delete(c.held, key)
	}
	c.mu.Unlock()
	current, err := storage.ReadFinishClaim(t.stateDir, t.task.Meta.ID)
	if err != nil && !storage.IsCorruptFinishClaim(err) {
		return nil, err
	}
	if current == nil || current.Expired(c.now()) {
		return &ClaimResult{Released: false}, nil
	}
	if current.Host != storage.Hostname() || current.Session != c.session {
		return nil, &BusyError{Holder: current}
	}
	if err := storage.ReleaseFinishClaim(t.stateDir, t.task.Meta.ID, current); err != nil {
		return nil, err
	}
	return &ClaimResult{Released: true, Claim: current}, nil
}

// Held lists the claims this controller is keeping alive (project/tracker).
func (c *Controller) Held() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.held))
	for k := range c.held {
		out = append(out, k)
	}
	return out
}

// --- kill ---

// KillResult is what Kill answers.
type KillResult struct {
	Kind string `json:"kind"`
	PID  int    `json:"pid"`
	// Parked is the in-flight sub moved to waiting (a run's kill), empty
	// for an acceptance or when nothing was in flight.
	Parked string `json:"parked,omitempty"`
	// ClaimReleased says the killed acceptance's own claim was dropped.
	ClaimReleased bool   `json:"claim_released,omitempty"`
	Note          string `json:"note"`
}

// KillEscalation is how long after the SIGTERM the run is SIGKILLed if it
// is still alive - the board's two seconds.
var KillEscalation = 2 * time.Second

// Kill stops the run the board's K would stop for taskID (pickRun), the
// board's killRun step for step: re-read the state by kind, prove the pid is
// the run's (a stale state is a *storage.StaleRunError - reported, nothing
// signalled), SIGTERM the group, stamp the state failed, journal the kill
// with the subs so far, release the worktree lock and - for an acceptance -
// its own claim, park the in-flight sub on waiting, and schedule a SIGKILL
// after KillEscalation unless the process is gone by then.
func (c *Controller) Kill(project, taskID string) (*KillResult, error) {
	t, err := c.resolve(project, taskID)
	if err != nil {
		return nil, err
	}
	st, kind := pickRun(t)
	if st == nil {
		return nil, &NoRunError{Msg: "nothing to kill: " + t.task.Meta.ID + " has no run"}
	}
	// A run that already ENDED is not a kill target - the board refuses it
	// ("no live executor run to stop") and so does this. Without the check a
	// late kill (the row went stale while the dialog was open) would fall
	// through to the stale branch below and rewrite a done run as failed,
	// journal a kill that never happened and park the tracker on waiting.
	// Only a state that still SAYS running gets reconciled: that is the
	// crashed-manager case, where the honest outcome is worth writing.
	if st.Status != storage.RunStatusRunning {
		return nil, &NoRunError{Msg: fmt.Sprintf("nothing to kill: the last %s run of %s already ended (%s), nothing was signalled", kind, t.task.Meta.ID, st.Status)}
	}
	st.Kind = kind
	acceptance := kind == storage.RunKindFinish
	pid := st.PID
	var stale *storage.StaleRunError
	if err := st.Kill(syscall.SIGTERM); err != nil {
		if errors.As(err, &stale) {
			// The honest outcome is still written: the state says running
			// while the process is gone, which is what the queue reports as
			// crashed - the caller asked for a stop, and gets told it was
			// already stopped.
			c.reconcile(t, st, acceptance, true)
			return nil, stale
		}
		// A signal error is not a stale state: the pid was proven live and
		// the kill failed (EPERM, or the process left between the proof and
		// the signal). Reconcile and report.
		c.reconcile(t, st, acceptance, false)
		return nil, fmt.Errorf("signal %s (pid %d): %w", kind, pid, err)
	}
	parked, claimReleased := c.reconcile(t, st, acceptance, false)
	res := &KillResult{Kind: kind, PID: pid, Parked: parked, ClaimReleased: claimReleased, Note: "stopped by user"}
	stateDir := t.stateDir
	id := t.task.Meta.ID
	c.after(KillEscalation, func() {
		// Re-read rather than trust: two seconds is ample for the manager
		// to exit and its number to be reused; a state whose pid moved on
		// is no proof, which is no signal.
		var fresh *storage.RunState
		if acceptance {
			fresh, _ = storage.ReadFinishRunState(stateDir, id)
		} else {
			fresh, _ = storage.ReadRunState(stateDir, id)
		}
		if fresh != nil && fresh.PID == pid {
			fresh.Kind = kind
			_ = fresh.Kill(syscall.SIGKILL)
		}
	})
	return res, nil
}

// reconcile is the bookkeeping after a kill (or a stale one): the failed
// stamp, the journal line, the lock and claim releases, the park. Returns
// the parked sub and whether the claim was released.
func (c *Controller) reconcile(t *target, st *storage.RunState, acceptance, alreadyGone bool) (string, bool) {
	stateDir := t.stateDir
	stopNote := "stopped by user (cockpit)"
	if alreadyGone {
		stopNote = "stopped by user (cockpit) - the run was already gone, nothing was signalled"
	}
	st.Status = storage.RunStatusFailed
	if st.Error == "" {
		st.Error = stopNote
	}
	inFlight := st.CurrentSub
	if inFlight == "" {
		inFlight = st.TaskID
	}
	stoppedSub := "blocked"
	if acceptance {
		stoppedSub = storage.RunStatusFailed
	}
	for i := range st.Subs {
		if st.Subs[i].ID == inFlight && (st.Subs[i].Status == storage.RunStatusRunning || st.Subs[i].Status == "pending") {
			st.Subs[i].Status = stoppedSub
			if st.Subs[i].Note == "" {
				st.Subs[i].Note = stopNote
			}
		}
	}
	_ = storage.WriteRunState(stateDir, st)

	subs := make([]storage.JournalSub, 0, len(st.Subs))
	for _, sr := range st.Subs {
		subs = append(subs, storage.JournalSub{ID: sr.ID, Result: sr.Status, Note: sr.Note, Session: sr.Session, Turns: sr.Turns, CostUSD: sr.CostUSD})
	}
	durationS := 0
	if started, err := time.Parse(time.RFC3339, st.Started); err == nil {
		durationS = int(c.now().Sub(started).Seconds())
	}
	_ = storage.AppendJournal(stateDir, &storage.JournalEntry{
		Event: storage.JournalEventKilled, Kind: st.Kind, Project: t.task.Project, TaskID: st.TaskID, RunID: st.RunID,
		PID: st.PID, Status: storage.RunStatusFailed, Error: stopNote, Subs: subs, DurationS: durationS, Source: Source,
	})
	if st.RepoPath != "" {
		_ = storage.ReleaseWorktreeLock(st.RepoPath, st.PID)
	}
	claimReleased := false
	if acceptance {
		claimReleased = releaseKilledClaim(stateDir, st.TaskID, st)
	}
	parked := ""
	if inFlight != "" && !acceptance {
		if task, err := c.store.FindTaskExact(t.task.Project, inFlight); err == nil && task.Meta.Status != storage.StatusWaiting {
			if c.store.MoveTask(task, storage.StatusWaiting) == nil {
				parked = inFlight
			}
		}
	}
	return parked, claimReleased
}

// releaseKilledClaim is the board's releaseKilledFinishClaim: drop the claim
// the killed acceptance itself took - proven by the SESSION `pm finish`
// stamped on both the claim and its run-state - and nobody else's.
func releaseKilledClaim(stateDir, tracker string, st *storage.RunState) bool {
	claim, err := storage.ReadFinishClaim(stateDir, tracker)
	if err != nil || claim == nil || claim.Session == "" {
		return false
	}
	if claim.Host != storage.Hostname() || !runHasSession(st, claim.Session) {
		return false
	}
	return storage.ReleaseFinishClaim(stateDir, tracker, claim) == nil
}

func runHasSession(st *storage.RunState, session string) bool {
	if st == nil || session == "" {
		return false
	}
	if st.CurrentSession == session {
		return true
	}
	for _, s := range st.Subs {
		if s.Session == session {
			return true
		}
	}
	return false
}
