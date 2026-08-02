package storage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// The executor journal is the append-only, cross-run history of executor
// activity for a project: one JSONL line per event under
// `<project>/.executor/journal.jsonl`. Where RunState is the LIVE state of one
// run (overwritten in place, read by the TUI), the journal is the durable
// record ACROSS runs - the raw material for retrospectives ("which subs fail,
// why, how long do runs take") that feed back into the epic flow.
//
// Event model: every run appends a "start" line when it begins and an "end"
// line when it finishes; a kill from the board appends "killed". A "start"
// with no matching "end"/"killed" line means the run crashed or was killed
// externally (SIGKILL, reboot) - itself a signal worth surfacing in analysis.
// All appends are best-effort observability, same contract as WriteRunState:
// a failed write costs history, never correctness.

// Journal event kinds.
const (
	JournalEventStart  = "start"
	JournalEventEnd    = "end"
	JournalEventKilled = "killed"
)

// JournalSub is one sub's outcome inside an "end" event (a single entry for
// `pm work`; one per sub for `pm run-epic`).
type JournalSub struct {
	ID string `json:"id"`
	// merged | pushed | verified | blocked | failed | conflict | skipped | manual.
	// Lines written before 0.34.0 say "merged" for every green outcome,
	// including batch subs nothing was ever merged for - they are never
	// rewritten, so a consumer reading old history must keep that in mind.
	Result    string  `json:"result"`
	Note      string  `json:"note,omitempty"`
	Branch    string  `json:"branch,omitempty"`     // the branch the sub's work landed on (key artifact in independent mode)
	DurationS int     `json:"duration_s,omitempty"` // wall-clock of the driveSub/worker, 0 for skips
	Session   string  `json:"session,omitempty"`    // worker session id -> transcript .jsonl
	Turns     int     `json:"turns,omitempty"`      // claude envelope num_turns (0 for skips)
	CostUSD   float64 `json:"cost_usd,omitempty"`   // claude envelope total_cost_usd
}

// JournalEntry is one line in the executor journal.
type JournalEntry struct {
	Event   string `json:"event"` // start | end | killed
	TS      string `json:"ts"`    // RFC3339, stamped by AppendJournal if empty
	Kind    string `json:"kind"`  // "work" | "run-epic"
	Project string `json:"project"`
	TaskID  string `json:"task_id"` // task (work) or tracker (run-epic) id
	// RunID ties every line of ONE physical run together (see NewRunID). All
	// three producers stamp it: the manager's own start/end lines and the
	// board's killed line, which lifts it off the run-state. Empty on lines
	// written before 0.27.0 - consumers must keep pairing those the old way,
	// by {kind, task_id, pid}.
	RunID      string `json:"run_id,omitempty"`
	PID        int    `json:"pid,omitempty"`
	Model      string `json:"model,omitempty"`
	Additional bool   `json:"additional,omitempty"`
	Yolo       bool   `json:"yolo,omitempty"`
	// Independent = the run drove an independent (batch) epic: subs on their
	// own branches off the base, pushed, nothing merged. Explicit so retros can
	// segment integration vs batch runs without parsing Branch.
	Independent bool `json:"independent,omitempty"`
	// WorkDir = the worktree slot the run claimed (additional runs only) so
	// retros can tell WHICH slot served a run once there is more than one.
	WorkDir string `json:"work_dir,omitempty"`
	// Baseline = the verification baseline command actually captured and
	// injected into the worker(s) this run, empty if none was used (not
	// configured, or capture degraded to no-baseline). Records USE, not config.
	Baseline  string       `json:"baseline,omitempty"`
	Branch    string       `json:"branch,omitempty"`     // epic integration branch / task branch; "independent:<base>" for batch runs
	Status    string       `json:"status,omitempty"`     // end only: done | failed (run level)
	DurationS int          `json:"duration_s,omitempty"` // end only: whole-run wall-clock
	Subs      []JournalSub `json:"subs,omitempty"`       // end only: per-sub outcomes
	Error     string       `json:"error,omitempty"`
}

// JournalPath is the executor journal path for a project dir.
func JournalPath(projectDir string) string {
	return filepath.Join(executorRunDir(projectDir), "journal.jsonl")
}

// AppendJournal appends one entry to the project's executor journal, stamping
// TS if unset. Append-only: the file is never rewritten or truncated.
func AppendJournal(projectDir string, e *JournalEntry) error {
	if err := os.MkdirAll(executorRunDir(projectDir), 0755); err != nil {
		return err
	}
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(JournalPath(projectDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// ReadJournal returns all journal entries for a project dir, oldest first.
// Missing file -> empty slice (no error); unparseable lines are skipped so one
// corrupt line never hides the rest of the history.
func ReadJournal(projectDir string) ([]JournalEntry, error) {
	f, err := os.Open(JournalPath(projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []JournalEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e JournalEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Event != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}
