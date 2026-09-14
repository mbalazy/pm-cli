// Package report is the LLM layer over the change feed (pm-cli-118-20): one
// prose report per cutoff period, written by a headless `claude -p` from
// the RAW feed (the cache since the cutoff), the attention queue (no task
// bodies) and the group list - and a short list of suggestions the person
// accepts or dismisses one by one. The raw feed stays the source of truth
// and works without this; the report is a reading of it, and it costs
// tokens, so it is off by default (cockpit.sources.report).
//
// The contract is the executor's: pm builds the prompt, runs claude, reads
// the envelope and WRITES the result itself under <pm-root>/.cockpit/
// reports/ - a headless claude cannot write under ~/.claude, and must not
// need to. internal/cmd's runner is out of reach (it imports the server,
// which imports this), so the run is re-stated here in its smallest form:
// the worker's environment rule (no API key - the subscription pays; the
// PM_HEADLESS marker), its own process group, a hard timeout, the CLI's
// two output shapes.
package report

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mbalazy/pm/internal/feed"
	"github.com/mbalazy/pm/internal/storage"
)

// Timeout bounds one report: a summary of a day's events, not reasoning.
var Timeout = 3 * time.Minute

// Suggestion is one thing the report proposes doing, mapped onto an
// existing cockpit action so "do it" is the ordinary confirmation dialog.
type Suggestion struct {
	// ID is stable for the period + line, so a dismissal survives a re-read.
	ID string `json:"id"`
	// Project + TaskID name the subject; Project is empty when the task
	// could not be placed (the UI then offers only dismiss).
	Project string `json:"project,omitempty"`
	TaskID  string `json:"task_id"`
	// Action is one of Actions; empty when the model named an unknown one.
	Action string `json:"action,omitempty"`
	Text   string `json:"text"`
}

// Actions are the actions a suggestion may name - the cockpit's own
// mutation kinds with a task or project subject, nothing new.
var Actions = []string{
	storage.ActionFocusToggle, storage.ActionSetWaitingFor, storage.ActionBackToTodo,
	storage.ActionClaim, storage.ActionRerunFinish, storage.ActionResumeRun, storage.ActionSleepProject,
}

// Report is one period's report as stored.
type Report struct {
	Period    string `json:"period"`
	Cutoff    string `json:"cutoff"`
	Generated string `json:"generated"`
	Model     string `json:"model"`
	// Tokens is the envelope's usage - the cost, in the unit the user
	// watches. Nil when the run produced no envelope.
	Tokens      *storage.TokenUsage `json:"tokens,omitempty"`
	DurationS   int                 `json:"duration_s"`
	Text        string              `json:"text,omitempty"`
	Suggestions []Suggestion        `json:"suggestions"`
	Dismissed   []string            `json:"dismissed,omitempty"`
	// Error is the run's failure text; a report with an error has no text.
	Error string `json:"error,omitempty"`
	// Events / Rows say how much the prompt carried.
	Events int `json:"events"`
	Rows   int `json:"rows"`
}

// PeriodKey names a period by its cutoff, in the local zone: one report per
// "since yesterday 18:00" window.
func PeriodKey(cutoff time.Time) string {
	return cutoff.In(time.Local).Format("2006-01-02T15")
}

// --- prompt ---

// Input is what a report is written from.
type Input struct {
	Cutoff    time.Time
	Now       time.Time
	Events    []feed.Event
	Attention *storage.Attention
	Groups    []storage.ProjectGroup
	Language  string
}

// BuildPrompt renders the input as the prompt: the facts in fixed sections,
// then the instructions (what to write, how long, the suggestion line
// format with the allowed actions). Deterministic, so a test can pin it.
func BuildPrompt(in Input) string {
	var b strings.Builder
	lang := in.Language
	if lang == "" {
		lang = "pl"
	}
	fmt.Fprintf(&b, "You are writing the morning report of a one-person software consultancy's control panel (pm). Window: %s to %s.\n\n",
		in.Cutoff.Format("2006-01-02 15:04"), in.Now.Format("2006-01-02 15:04"))
	b.WriteString("## Project groups\n")
	for _, g := range in.Groups {
		fmt.Fprintf(&b, "- %s (%s): repos %s\n", g.Name, g.Slug, strings.Join(g.Projects, ", "))
	}
	if len(in.Groups) == 0 {
		b.WriteString("(none)\n")
	}
	fmt.Fprintf(&b, "\n## Changes since the cutoff (%d events, newest first)\n", len(in.Events))
	for _, e := range in.Events {
		line := fmt.Sprintf("- [%s] %s %s", shortTS(e.TS), e.Source, e.Project)
		if e.TaskID != "" {
			line += " " + e.TaskID
		}
		line += ": " + e.Title
		if e.Detail != "" {
			line += " - " + e.Detail
		}
		if e.Severity == storage.SeverityCrit || e.Severity == storage.SeverityWarn {
			line += " [" + e.Severity + "]"
		}
		b.WriteString(line + "\n")
	}
	if len(in.Events) == 0 {
		b.WriteString("(nothing)\n")
	}
	b.WriteString("\n## What needs the person now (the attention queue)\n")
	rows := 0
	if in.Attention != nil {
		for _, sec := range in.Attention.Sections {
			if sec.Name == storage.SectionChanges || len(sec.Rows) == 0 {
				continue
			}
			fmt.Fprintf(&b, "### %s (%d)\n", sec.Name, sec.Total)
			for _, r := range sec.Rows {
				rows++
				line := "- " + r.Project
				if r.TaskID != "" {
					line += " " + r.TaskID
				}
				line += ": " + r.Title + " - " + r.Reason
				if r.Status != "" {
					line += " (status " + r.Status + ")"
				}
				if len(r.Flags) > 0 {
					line += " [" + strings.Join(r.Flags, ",") + "]"
				}
				b.WriteString(line + "\n")
			}
		}
	}
	if rows == 0 {
		b.WriteString("(nothing)\n")
	}
	fmt.Fprintf(&b, `
## Instructions
Write the report in the language with code "%s". Markdown, at most 250 words: one short paragraph per project group that had anything happen (bold the group name), then a paragraph for what needs a decision. Facts only - every sentence must come from the lists above; never invent a state. Skip groups with nothing.
Then, on separate lines at the very end, up to 5 suggestions in EXACTLY this format, one per line, or none:
- SUGGEST <task_id> <action>: <one sentence why>
where <action> is one of: %s. Use a task id exactly as listed above. Do not wrap the suggestions in a heading.
`, lang, strings.Join(Actions, ", "))
	return b.String()
}

func shortTS(ts string) string {
	if t, ok := storage.ParseStamp(ts); ok {
		return t.In(time.Local).Format("Mon 15:04")
	}
	return ts
}

// --- suggestions ---

var suggestLine = regexp.MustCompile(`(?m)^\s*[-*]?\s*SUGGEST\s+(\S+)\s+([a-z_]+)\s*:\s*(.+?)\s*$`)

// ParseSuggestions splits the model's text into the prose and the SUGGEST
// lines. placeOf resolves a task id to its project (empty = unknown); the
// period seeds the ids so a dismissal is stable across re-reads.
func ParseSuggestions(period, text string, placeOf func(taskID string) string) (string, []Suggestion) {
	var out []Suggestion
	prose := suggestLine.ReplaceAllStringFunc(text, func(line string) string {
		m := suggestLine.FindStringSubmatch(line)
		s := Suggestion{TaskID: m[1], Action: m[2], Text: strings.TrimSpace(m[3])}
		if !contains(Actions, s.Action) {
			s.Action = ""
		}
		if placeOf != nil {
			s.Project = placeOf(s.TaskID)
		}
		sum := sha1.Sum([]byte(period + "\x00" + s.TaskID + "\x00" + s.Action + "\x00" + s.Text))
		s.ID = hex.EncodeToString(sum[:6])
		out = append(out, s)
		return ""
	})
	return strings.TrimSpace(prose), out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// --- the claude run ---

// Writer runs claude. Exe is the binary ("claude" on PATH by default; tests
// hand in a fake script). Env, when set, replaces the environment builder.
type Writer struct {
	Exe string
	Env func() []string
}

// Outcome is what one run returned.
type Outcome struct {
	Text      string
	Tokens    *storage.TokenUsage
	SessionID string
}

// envelope is the CLI's result object; only what the report reads.
type envelope struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	Usage     *struct {
		Input         int `json:"input_tokens"`
		CacheCreation int `json:"cache_creation_input_tokens"`
		CacheRead     int `json:"cache_read_input_tokens"`
		Output        int `json:"output_tokens"`
	} `json:"usage"`
}

// Run invokes `claude -p <prompt>` with the model and reads the text back.
// One turn, no tools: a summary needs neither. The process runs in its own
// group and is killed as a group on the timeout.
func (w *Writer) Run(ctx context.Context, prompt, model string) (*Outcome, error) {
	exe := w.Exe
	if exe == "" {
		exe = "claude"
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	args := []string{"-p", prompt, "--output-format", "json", "--model", model, "--max-turns", "1", "--tools", "", "--strict-mcp-config"}
	c := exec.CommandContext(ctx, exe, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	c.WaitDelay = 2 * time.Second
	if w.Env != nil {
		c.Env = w.Env()
	} else {
		c.Env = Environ()
	}
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("claude timed out after %s", Timeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("claude exited %d: %s", exitErr.ExitCode(), firstLine(stderr.String(), stdout.String()))
		}
		return nil, fmt.Errorf("claude: %w", err)
	}
	return decode(stdout.Bytes())
}

// Decode reads the CLI's `--output-format json` output (an object, or a
// message array whose last result message counts) into an Outcome; the
// review runner (internal/review) reads its stdout file with it.
func Decode(data []byte) (*Outcome, error) { return decode(data) }

// Environ is the worker's environment rule: ANTHROPIC_API_KEY /
// ANTHROPIC_AUTH_TOKEN stripped so the subscription pays, PM_HEADLESS=1 so
// project hooks can tell a headless run apart.
func Environ() []string {
	src := os.Environ()
	out := make([]string, 0, len(src)+1)
	for _, kv := range src {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") || strings.HasPrefix(kv, "PM_HEADLESS=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PM_HEADLESS=1")
}

func firstLine(parts ...string) string {
	for _, p := range parts {
		for _, l := range strings.Split(strings.TrimSpace(p), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				return l
			}
		}
	}
	return "no output"
}

// decode reads the CLI's stdout in its two shapes: a result object, or a
// message array whose result message is the last (the executor's
// decodeClaudeOutput rule, restated).
func decode(data []byte) (*Outcome, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("claude wrote no output")
	}
	var raw json.RawMessage
	switch trimmed[0] {
	case '[':
		var msgs []json.RawMessage
		if err := json.Unmarshal(trimmed, &msgs); err != nil {
			return nil, fmt.Errorf("claude output is not a well-formed message array: %w", err)
		}
		for _, m := range msgs {
			var head struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(m, &head) == nil && head.Type == "result" {
				raw = m
			}
		}
		if raw == nil {
			return nil, fmt.Errorf("claude output is a %d-message array with no result message", len(msgs))
		}
	case '{':
		raw = trimmed
	default:
		return nil, fmt.Errorf("claude output is neither a JSON object nor an array")
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("claude result envelope: %w", err)
	}
	if env.IsError {
		return nil, fmt.Errorf("claude reported an error (%s): %s", env.Subtype, firstLine(env.Result))
	}
	out := &Outcome{Text: strings.TrimSpace(env.Result), SessionID: env.SessionID}
	if env.Usage != nil {
		out.Tokens = &storage.TokenUsage{Input: env.Usage.Input, CacheCreation: env.Usage.CacheCreation, CacheRead: env.Usage.CacheRead, Output: env.Usage.Output}
	}
	if out.Text == "" {
		return nil, fmt.Errorf("claude returned an empty result")
	}
	return out, nil
}

// --- storage ---

// Store keeps the reports under <pm-root>/.cockpit/reports/: <period>.json
// (the Report) and <period>.md (the prose, for a person opening the dir).
type Store struct{ Root string }

func (s Store) dir() string { return filepath.Join(s.Root, ".cockpit", "reports") }

func (s Store) path(period string) string { return filepath.Join(s.dir(), period+".json") }

// Read returns the period's report, or nil when there is none.
func (s Store) Read(period string) (*Report, error) {
	data, err := os.ReadFile(s.path(period))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path(period), err)
	}
	if r.Suggestions == nil {
		r.Suggestions = []Suggestion{}
	}
	return &r, nil
}

// storeMu makes Write and Dismiss (a read-modify-write) mutually exclusive
// within the process: pm serve runs the background write and a dismiss
// click on the same period, and without this a dismiss could read the old
// report and write it back over the fresh one.
var storeMu sync.Mutex

// Write stores the report (both files, atomically each).
func (s Store) Write(r *Report) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	return s.write(r)
}

func (s Store) write(r *Report) error {
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return err
	}
	if r.Suggestions == nil {
		r.Suggestions = []Suggestion{}
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(s.path(r.Period), data); err != nil {
		return err
	}
	md := r.Text
	if r.Error != "" {
		md = "(report failed: " + r.Error + ")\n"
	}
	for _, sg := range r.Suggestions {
		md += fmt.Sprintf("\n- SUGGEST %s %s: %s", sg.TaskID, sg.Action, sg.Text)
	}
	return atomicWrite(filepath.Join(s.dir(), r.Period+".md"), []byte(strings.TrimSpace(md)+"\n"))
}

// Dismiss records a suggestion as dismissed; unknown ids are an error.
func (s Store) Dismiss(period, id string) (*Report, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	r, err := s.Read(period)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("no report for period %s", period)
	}
	known := false
	for _, sg := range r.Suggestions {
		if sg.ID == id {
			known = true
		}
	}
	if !known {
		return nil, fmt.Errorf("no suggestion %q in the report of %s", id, period)
	}
	for _, d := range r.Dismissed {
		if d == id {
			return r, nil
		}
	}
	r.Dismissed = append(r.Dismissed, id)
	sort.Strings(r.Dismissed)
	return r, s.write(r)
}

// atomicWrite writes through a unique temp file in the target's directory
// and renames it over. Unique per call (os.CreateTemp), not per process: two
// goroutines writing the same path would otherwise truncate and rename each
// other's temp file.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// --- the whole job ---

// Write builds the prompt, runs claude and stores the outcome - success or
// failure - as the period's report. The failure is stored too: a report
// that failed at 07:30 must say so at 09:00 instead of looking never
// attempted, and the scheduler must not retry it every tick.
func (w *Writer) Write(ctx context.Context, store Store, in Input, model string) (*Report, error) {
	period := PeriodKey(in.Cutoff)
	place := map[string]string{}
	for _, e := range in.Events {
		if e.TaskID != "" {
			place[e.TaskID] = e.Project
		}
	}
	if in.Attention != nil {
		for _, sec := range in.Attention.Sections {
			for _, r := range sec.Rows {
				if r.TaskID != "" {
					place[r.TaskID] = r.Project
				}
			}
		}
	}
	rows := 0
	if in.Attention != nil {
		for _, sec := range in.Attention.Sections {
			rows += len(sec.Rows)
		}
	}
	r := &Report{Period: period, Cutoff: in.Cutoff.Format(time.RFC3339), Model: model, Events: len(in.Events), Rows: rows, Suggestions: []Suggestion{}}
	started := time.Now()
	out, err := w.Run(ctx, BuildPrompt(in), model)
	r.Generated = time.Now().Format(time.RFC3339)
	r.DurationS = int(time.Since(started).Seconds())
	if err != nil {
		r.Error = err.Error()
	} else {
		r.Tokens = out.Tokens
		r.Text, r.Suggestions = ParseSuggestions(period, out.Text, func(id string) string { return place[id] })
	}
	if werr := store.Write(r); werr != nil {
		return r, werr
	}
	return r, err
}
