package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Review telemetry answers three questions about a worker's review phase that
// were previously only answerable by hand-parsing the worker's transcript: how
// many reviewer subagents it spawned, in how many review->fix rounds, and on
// which model. That parsing took an hour in the 2026-08-07 session and got the
// spawn count WRONG on the first pass, which is precisely why two earlier review
// fixes (0.36.2, 0.36.3) could go unrespected for many runs without anyone
// noticing. A regression has to be visible in one command or it is invisible.
//
// The data is collected by pm's own PreToolUse hook (`pm worker-guard`), which
// already runs on every tool call; a spawn of the Agent tool appends one line
// here. The hook is a separate process per tool call, so a file is the only
// state that survives between calls.

// ReviewerAgentType is the subagent type pm defines for reviewer spawns (the
// --agents payload the worker is launched with names it).
const ReviewerAgentType = "pm-reviewer"

// GenericSubagentType reports whether a subagent type is one of the built-in
// generic types that inherit the session's model. They are the only types the
// review harness treats as REVIEWERS - re-modeled, handed the diff packet,
// forced synchronous; a custom type is left completely alone. Compared
// lowercase (the same type has been seen spelled `Explore` and
// `general-purpose` in one transcript). An empty type is NOT generic here -
// callers that want the guard's "no type becomes a reviewer" rule handle the
// empty string themselves, because they apply it at different moments.
func GenericSubagentType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "explore", "general-purpose", "plan", ReviewerAgentType:
		return true
	}
	return false
}

// ReviewSpawn is one observed subagent spawn - one line of the telemetry JSONL.
type ReviewSpawn struct {
	TS           string `json:"ts"`                      // RFC3339Nano; round clustering reads this
	Model        string `json:"model,omitempty"`         // tool_input.model, empty = inherit the worker's model
	SubagentType string `json:"subagent_type,omitempty"` // tool_input.subagent_type
	HasDiff      bool   `json:"has_diff,omitempty"`      // the agent prompt carried the diff instead of making it go find one
	// Nested marks a spawn issued from INSIDE another subagent rather than by
	// the worker itself. The PreToolUse payload carries agent_id/agent_type only
	// for nested calls, which is what makes the distinction observable at all.
	// It is tracked separately because a cap on the worker does not see these:
	// in orbit-106-3 one reviewer spawned its own Explore subagent that
	// burned 6.1M tokens over 65 tool calls.
	Nested    bool   `json:"nested,omitempty"`
	AgentType string `json:"agent_type,omitempty"` // spawning agent's type, nested spawns only
	// Background marks a spawn the worker asked to run in the background - or
	// left to the tool's background default, which is the same outcome. It
	// records what the WORKER asked for, before pm forced the spawn synchronous
	// (forceSyncSpawn): the behavior that lost three reviews in epic pm-cli-100,
	// not pm's correction of it.
	Background bool `json:"background,omitempty"`
	// Denied marks a spawn the guard REFUSED. It is recorded anyway, and kept
	// out of every other count: it never ran, so it must not consume the round's
	// budget or show up as review that happened. A refusal is the single most
	// interesting line this file can carry - it is the evidence the cap did
	// something - so dropping it would make an enforced run look like one where
	// the worker simply behaved.
	Denied bool `json:"denied,omitempty"`
	// Head is the repo's HEAD commit when the spawn was judged, i.e. the tip this
	// reviewer could possibly have seen. It is what makes "the last fix was never
	// reviewed" a MEASURABLE fact rather than a sentence in the worker's prose:
	// compare the last allowed spawn's Head against the sub's final tip and the
	// commits in between are the ones no reviewer looked at. Empty when the head
	// could not be read (no git dir), which degrades to no signal.
	Head string `json:"head,omitempty"`
}

// AgentToolCall is one exploration tool call (Read/Grep/Glob) made from INSIDE
// a subagent - one line of the sidecar calls JSONL. Kept in its OWN file, never
// mixed into the spawns file: any JSON object unmarshals into ReviewSpawn (all
// fields optional), so a foreign line there would silently count as a spawn.
type AgentToolCall struct {
	TS      string `json:"ts"` // RFC3339Nano
	AgentID string `json:"agent_id"`
	Tool    string `json:"tool"`
	// Denied marks a call the per-agent budget refused. Recorded anyway - a
	// refusal is the evidence the budget did something - and kept out of the
	// used-budget count, since it never ran.
	Denied bool `json:"denied,omitempty"`
}

// AgentCallsPath derives the sidecar calls file from the spawn-telemetry path.
func AgentCallsPath(telemetryPath string) string {
	return strings.TrimSuffix(telemetryPath, ".jsonl") + ".calls.jsonl"
}

// AppendAgentToolCall appends one call record - same single O_APPEND write
// assumption as AppendReviewSpawn (concurrent hook processes).
func AppendAgentToolCall(path string, c AgentToolCall) error {
	if c.TS == "" {
		c.TS = time.Now().Format(time.RFC3339Nano)
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// ReadAgentToolCalls reads a calls file, skipping corrupt lines; a missing file
// means no subagent made an exploration call, which is not an error.
func ReadAgentToolCalls(path string) ([]AgentToolCall, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []AgentToolCall
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c AgentToolCall
		if json.Unmarshal([]byte(line), &c) != nil {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// CountAgentToolCalls counts the calls that actually RAN for one agent - the
// number the budget is judged against. Denied calls are evidence, not usage.
func CountAgentToolCalls(calls []AgentToolCall, agentID string) int {
	n := 0
	for _, c := range calls {
		if !c.Denied && c.AgentID == agentID {
			n++
		}
	}
	return n
}

// ReviewTelemetry is the per-sub rollup stored on SubRun and in the journal.
// A pointer field on both, so entries written before this existed stay nil and
// omitempty keeps them byte-identical rather than gaining empty keys.
type ReviewTelemetry struct {
	Spawns   int `json:"spawns"`              // subagent spawns that actually ran
	Rounds   int `json:"rounds,omitempty"`    // derived: clusters of spawns (see ReviewRoundGap)
	Nested   int `json:"nested,omitempty"`    // of Spawns, how many came from inside another subagent
	WithDiff int `json:"with_diff,omitempty"` // of Spawns, how many were handed the diff
	Denied   int `json:"denied,omitempty"`    // spawns the cap refused - these never ran
	// ToolCalls / ToolDenied measure the subagents' exploration appetite: how
	// many Read/Grep/Glob calls their agents made, and how many the per-agent
	// budget refused. What the budget saves is the runaway tail (measured on run
	// pm-cli-100: 2 of 8 reviewers pulled 33+ calls / $3+ where the typical one
	// needed 8-13 turns), so the refusal count is the line a retro looks for.
	ToolCalls  int `json:"tool_calls,omitempty"`
	ToolDenied int `json:"tool_denied,omitempty"`
	// Background: of Spawns, how many the worker asked to run in the background
	// (or left to the tool's background default - same outcome). Measures the
	// behavior forceSyncSpawn corrects, recorded before the correction.
	Background int `json:"background,omitempty"`
	// Models lists the distinct models the spawns asked for, comma-joined and
	// sorted; "inherit" stands for a spawn that named no model (the caller-side
	// model wins over an agent definition's, so "inherit" and an explicit name
	// are genuinely different facts).
	Models string `json:"models,omitempty"`
	// LastReviewedHead is the repo tip the LAST reviewer that actually ran could
	// see (top-level, not denied). UnreviewedCommits is how many commits the sub
	// added after it - filled in by the caller once the worker is done, since only
	// it knows the final tip (see storage.CountUnreviewedCommits).
	//
	// The pair exists to separate two states that both come back as `blocked` with
	// prose in `unresolved`: "findings are still open at the round cap" and "the
	// findings were fixed, but the LAST fix landed after the cap and nothing
	// reviewed it". The second is structural to any capped loop - the round that
	// would check the final fix is the round past the cap - and it was readable
	// only inside a sentence (orbit-114-1, $23.75: "the final mechanism ...
	// landed after the review round cap and has had NO adversarial review").
	// UnreviewedCommits > 0 is now that same fact, in a number.
	//
	// Commits are the unit, so a final fix left UNCOMMITTED does not register.
	// That is the honest limit of a cheap signal: the workers commit as they go
	// (which is why a timed-out sub still has a branch), and the alternative -
	// hashing trees - buys a case nobody has observed.
	LastReviewedHead  string `json:"last_reviewed_head,omitempty"`
	UnreviewedCommits int    `json:"unreviewed_commits,omitempty"`
}

// ReviewRoundGap separates one review round from the next. Measured 2026-08-07
// against a live claude 2.1.224: subagents spawned in parallel within ONE
// assistant turn arrive 0.6-0.9s apart, while a new round costs a full reviewer
// run - the reviewers in epic orbit-106 made 13-75 tool calls each, i.e.
// minutes. Nothing in the hook payload groups a turn's tool calls together
// (session_id and prompt_id are shared by the whole user turn AND by the
// subagents' own calls), so the gap is the available signal.
const ReviewRoundGap = 30 * time.Second

// ReviewTelemetryPath is where the hook writes a worker's spawns. Keyed by the
// worker's session id, which pm mints and pins with `claude --session-id`, so
// the manager knows the path before the worker starts and two concurrent
// workers can never collide.
func ReviewTelemetryPath(projectDir, sessionID string) string {
	return filepath.Join(executorRunDir(projectDir), "review-"+sessionID+".jsonl")
}

// AppendReviewSpawn appends one spawn record. Callers are separate hook
// processes that may run concurrently (parallel spawns in one turn), so the
// line is assembled first and handed to a single O_APPEND write - the same
// assumption the executor journal makes.
func AppendReviewSpawn(path string, s ReviewSpawn) error {
	if s.TS == "" {
		s.TS = time.Now().Format(time.RFC3339Nano)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// WithReviewTelemetryLock runs fn holding an exclusive flock on the telemetry
// file, so a read-decide-append cycle in one hook process cannot interleave with
// another's. The hook processes for a turn's parallel spawns are separate
// processes, and the decision they make (is this spawn within the round's cap?)
// reads what the others have written.
//
// In practice the observed spacing between parallel spawns is 0.6-0.9s against a
// hook that runs in milliseconds, so the window is tiny - but the cost of the
// race is letting a spawn past a cap that exists precisely to be unarguable, and
// a flock is the same mechanism LockProject already uses.
//
// Degrades to running fn unlocked if the lock cannot be taken: a worker must
// never stall because a lock file could not be opened.
func WithReviewTelemetryLock(path string, fn func() error) error {
	if path == "" {
		return fn()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fn()
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fn()
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fn()
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}

// ReadReviewSpawns reads a telemetry file, skipping corrupt lines like
// ReadJournal does. A missing file is not an error: most workers never spawn
// anything, and a run with no review phase must not read as a failure.
func ReadReviewSpawns(path string) ([]ReviewSpawn, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ReviewSpawn
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var s ReviewSpawn
		if json.Unmarshal([]byte(line), &s) != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// AggregateReviewSpawns rolls spawns up into the per-sub telemetry. Pure, so
// the clustering rule is testable without spawning anything.
//
// Always returns a value, including for zero spawns: "this worker spawned
// nobody" is the OUTCOME the whole change is aiming at, and collapsing it into
// the same nil as "nothing was measured" would make success indistinguishable
// from a broken hook. Which of the two a nil means is decided by the file's
// existence, in CollectReviewTelemetry.
//
// Rounds are counted over TOP-LEVEL spawns only: a nested spawn happens while a
// reviewer is already running, so counting it would inflate the round count with
// something that is not a review->fix round at all.
func AggregateReviewSpawns(spawns []ReviewSpawn) *ReviewTelemetry {
	t := &ReviewTelemetry{}
	if len(spawns) == 0 {
		return t
	}
	models := map[string]bool{}
	var topTimes []time.Time
	for _, s := range spawns {
		// A refused spawn never ran: it counts as a refusal and nothing else.
		if s.Denied {
			t.Denied++
			continue
		}
		t.Spawns++
		if s.Nested {
			t.Nested++
		}
		if s.HasDiff {
			t.WithDiff++
		}
		if s.Background {
			t.Background++
		}
		if s.Model == "" {
			models["inherit"] = true
		} else {
			models[s.Model] = true
		}
		if s.Nested {
			continue
		}
		// The spawns file is append-only and written in judgement order, so the
		// last top-level allowed REVIEWER spawn's head is the newest tip any
		// reviewer saw. Only spawns pm treats as reviewers advance it: a
		// generic type (or none, which the guard turns into the reviewer type)
		// is pinned to the review model and handed the current diff, while a
		// custom-type spawn - an explore helper, a project-defined agent - is
		// left untouched by the review harness and says nothing about what a
		// reviewer saw; letting it advance the signal would zero the
		// unreviewed-commits count on a sub whose final fix no reviewer ever
		// read. (A generic-type helper spawned for non-review work still
		// advances it - the harness attached the diff to its prompt, and
		// nothing recordable tells it apart from a reviewer.)
		if s.Head != "" && (s.SubagentType == "" || GenericSubagentType(s.SubagentType)) {
			t.LastReviewedHead = s.Head
		}
		if ts, err := time.Parse(time.RFC3339Nano, s.TS); err == nil {
			topTimes = append(topTimes, ts)
		}
	}
	names := make([]string, 0, len(models))
	for m := range models {
		names = append(names, m)
	}
	sort.Strings(names)
	t.Models = strings.Join(names, ",")

	// An unparseable timestamp drops out of the clustering rather than sorting
	// to one end and inventing a round boundary - the same lesson the journal's
	// ordering rules learned the hard way.
	sort.Slice(topTimes, func(i, j int) bool { return topTimes[i].Before(topTimes[j]) })
	if len(topTimes) > 0 {
		t.Rounds = 1
		for i := 1; i < len(topTimes); i++ {
			if topTimes[i].Sub(topTimes[i-1]) > ReviewRoundGap {
				t.Rounds++
			}
		}
	}
	return t
}

// InitReviewTelemetry creates the (empty) telemetry file before a worker starts.
// It is what makes "spawned nobody" an observable outcome rather than a silence:
// the hook only ever appends, so without this an absent file would mean both
// "zero spawns" and "no telemetry collected at all". Best-effort - a run must
// never fail because observability could not be set up.
func InitReviewTelemetry(path string) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_ = f.Close()
}

// CollectReviewTelemetry reads and aggregates a worker's telemetry file, then
// removes it - the rollup lives on the SubRun and in the journal from here on,
// and leaving per-session files behind would grow .executor without bound.
//
// Returns nil ONLY when there is genuinely nothing to report: no file, i.e. the
// hook was never attached (an older binary, or a build where the settings could
// not be rendered). A file that exists but is empty yields a zero telemetry,
// which reads as "this worker spawned nobody" - a real and desirable result.
//
// Best-effort throughout: telemetry is observability, and losing it must never
// change what a run reports about the work itself.
func CollectReviewTelemetry(projectDir, sessionID string) *ReviewTelemetry {
	if projectDir == "" || sessionID == "" {
		return nil
	}
	path := ReviewTelemetryPath(projectDir, sessionID)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	spawns, err := ReadReviewSpawns(path)
	if err != nil {
		return nil
	}
	t := AggregateReviewSpawns(spawns)
	callsPath := AgentCallsPath(path)
	if calls, err := ReadAgentToolCalls(callsPath); err == nil {
		for _, c := range calls {
			if c.Denied {
				t.ToolDenied++
			} else {
				t.ToolCalls++
			}
		}
	}
	_ = os.Remove(path)
	_ = os.Remove(path + ".lock")
	_ = os.Remove(callsPath)
	_ = os.Remove(callsPath + ".lock")
	return t
}
