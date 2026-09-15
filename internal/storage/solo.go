package storage

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Solo shifts (pm-cli-136): the /solo skill (~/.claude/skills/solo) keeps one
// state file per shift under <projectDir>/.shift/<YYYY-MM-DD>-<shift-id>.md
// and writes the closing report next to it as <...>-report.md. The format is
// the skill's prose, not a pm contract, so the reader is lenient: a header
// line "# solo <id>" (older shifts: "# night-shift <id>"), a
// "## Status: closed <stamp> ..." line, and a "## Queue" section of
// "<task-id> · <title> · <status> ..." lines. Whatever does not parse stays
// empty, never guessed.

// ShiftDir is the per-project directory the skill writes shift files into.
const ShiftDir = ".shift"

// Shift is one solo shift as read from its state file.
type Shift struct {
	Project string `json:"project"`
	// ID is the file name after the date - unique per project, the URL's id.
	ID string `json:"id"`
	// Kind is the header word: "solo" or "night-shift".
	Kind string `json:"kind"`
	// Date is the YYYY-MM-DD the file name starts with.
	Date string `json:"date"`
	Open bool   `json:"open"`
	// Closed is the closing stamp (RFC3339) off the status line, or the state
	// file's mtime when that stamp does not parse; empty while open.
	Closed     string      `json:"closed,omitempty"`
	StatusLine string      `json:"status_line"`
	Tasks      []ShiftTask `json:"tasks"`
	// File and Report are absolute paths; Report is empty when there is none.
	File   string `json:"file"`
	Report string `json:"report,omitempty"`
	// Summary is the outcome at a glance (solo_report.go): off the report
	// when there is one, else off the Progress lines; nil when neither says.
	Summary *ShiftSummary `json:"summary,omitempty"`
}

// ShiftTask is one line of a shift's queue.
type ShiftTask struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Status is the queue line's - the task's status when the shift STARTED.
	Status string `json:"status,omitempty"`
	// Outcome and Branch come off the task's "## Progress" line: done,
	// parked, untouched or doing, and the branch the work is on.
	Outcome string `json:"outcome,omitempty"`
	Branch  string `json:"branch,omitempty"`
}

// ReadShifts reads every shift of one project, newest first. A missing
// directory or an unreadable file yields no shift, never an error: the
// shifts are another tool's notes, not pm's store.
func ReadShifts(projectDir, slug string) []Shift {
	dir := filepath.Join(projectDir, ShiftDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Shift
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || strings.HasSuffix(name, "-report.md") {
			continue
		}
		if sh, ok := parseShift(filepath.Join(dir, name), slug); ok {
			out = append(out, sh)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := shiftTime(out[i]), shiftTime(out[j])
		if !a.Equal(b) {
			return a.After(b)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func parseShift(path, slug string) (Shift, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Shift{}, false
	}
	base := strings.TrimSuffix(filepath.Base(path), ".md")
	sh := Shift{Project: slug, File: path, Tasks: []ShiftTask{}}
	if len(base) >= 10 {
		if _, err := time.Parse("2006-01-02", base[:10]); err == nil {
			sh.Date = base[:10]
			sh.ID = strings.TrimPrefix(base[10:], "-")
		}
	}
	if sh.ID == "" {
		sh.ID = base
	}
	if report := filepath.Join(filepath.Dir(path), base+"-report.md"); fileExists(report) {
		sh.Report = report
	}

	inQueue, inProgress := false, false
	type progress struct{ outcome, branch string }
	progressOf := map[string]progress{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "# ") && sh.Kind == "":
			if f := strings.Fields(line[2:]); len(f) > 0 {
				sh.Kind = f[0]
			}
		case strings.HasPrefix(line, "## Status:"):
			sh.StatusLine = strings.TrimSpace(strings.TrimPrefix(line, "## Status:"))
			inQueue, inProgress = false, false
		case strings.HasPrefix(line, "## "):
			inQueue = strings.TrimSpace(line) == "## Queue"
			inProgress = strings.HasPrefix(line, "## Progress") // "## Progress (cont.)" too
		case inQueue:
			if t, ok := parseShiftTask(line); ok {
				sh.Tasks = append(sh.Tasks, t)
			}
		case inProgress:
			if id, outcome, branch, ok := parseProgressLine(line); ok {
				progressOf[id] = progress{outcome, branch}
			}
		}
	}
	for i := range sh.Tasks {
		if p, ok := progressOf[sh.Tasks[i].ID]; ok {
			sh.Tasks[i].Outcome, sh.Tasks[i].Branch = p.outcome, p.branch
		}
	}
	var rep *ShiftReport
	if sh.Report != "" {
		if data, err := os.ReadFile(sh.Report); err == nil {
			rep = ParseShiftReport(string(data))
		}
	}
	sh.Summary = summarizeShift(sh, rep)

	lower := strings.ToLower(sh.StatusLine)
	if !strings.HasPrefix(lower, "closed") {
		sh.Open = true
		return sh, true
	}
	stamp := strings.TrimSpace(sh.StatusLine[len("closed"):])
	if i := strings.Index(stamp, " ("); i >= 0 {
		stamp = stamp[:i]
	}
	if at, ok := parseShiftStamp(stamp); ok {
		sh.Closed = at.Format(time.RFC3339)
	} else if fi, err := os.Stat(path); err == nil {
		sh.Closed = fi.ModTime().Format(time.RFC3339)
	}
	return sh, true
}

// parseShiftTask reads "<id> · <title> · <status> · ..." (a leading "- " is
// tolerated); a line whose first part is not an id (a note in parentheses,
// prose) is not a task.
func parseShiftTask(line string) (ShiftTask, bool) {
	line = strings.TrimPrefix(strings.TrimSpace(line), "- ")
	parts := strings.Split(line, " · ")
	if len(parts) < 2 {
		return ShiftTask{}, false
	}
	id := strings.TrimSpace(parts[0])
	if id == "" || strings.ContainsAny(id, " ()") {
		return ShiftTask{}, false
	}
	t := ShiftTask{ID: id, Title: strings.TrimSpace(parts[1])}
	if len(parts) > 2 {
		t.Status = strings.TrimSpace(parts[2])
	}
	return t, true
}

var (
	progressSep = regexp.MustCompile(` [·*] `)
	branchAtSHA = regexp.MustCompile(`^([A-Za-z0-9._/-]+)@[0-9a-f]{7,40}\b`)
)

// parseProgressLine reads "<task-id> · ... · <done|parked: why|untouched:
// ...|doing> ... · <branch>@<sha> · ..." in the spellings the skill wrote:
// " · " or " * " between fields, a leading "- ", the word in any case and
// with a note after it ("done (doing in pm)", "DONE (local, not pushed)").
func parseProgressLine(line string) (id, outcome, branch string, ok bool) {
	parts := progressSep.Split(strings.TrimPrefix(strings.TrimSpace(line), "- "), -1)
	if len(parts) < 2 {
		return "", "", "", false
	}
	id = strings.TrimSpace(parts[0])
	if id == "" || strings.ContainsAny(id, " ()") {
		return "", "", "", false
	}
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		if outcome == "" {
			low := strings.ToLower(p)
			for _, w := range []string{OutcomeDone, OutcomeParked, OutcomeUntouched, OutcomeDoing} {
				if strings.HasPrefix(low, w) {
					outcome = w
					break
				}
			}
		}
		if m := branchAtSHA.FindStringSubmatch(p); branch == "" && m != nil {
			branch = m[1]
		}
	}
	return id, outcome, branch, true
}

// shiftStampLayouts are the closing-stamp spellings the skill has written so
// far (2026-09-09..14): RFC3339 with or without seconds, a colon-less
// offset, a space-separated local time with or without a zone name.
var shiftStampLayouts = []struct {
	layout string
	local  bool
}{
	{time.RFC3339, false},
	{"2006-01-02T15:04Z07:00", false},
	{"2006-01-02T15:04:05-0700", false},
	{"2006-01-02T15:04-0700", false},
	{"2006-01-02 15:04 MST", false},
	{"2006-01-02 15:04:05", true},
	{"2006-01-02 15:04", true},
	{"2006-01-02T15:04", true},
}

func parseShiftStamp(s string) (time.Time, bool) {
	for _, l := range shiftStampLayouts {
		var at time.Time
		var err error
		if l.local {
			at, err = time.ParseInLocation(l.layout, s, time.Local)
		} else {
			at, err = time.Parse(l.layout, s)
		}
		if err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

// shiftTime is what shifts are ordered by: the closing stamp, else the date.
func shiftTime(sh Shift) time.Time {
	if at, ok := ParseStamp(sh.Closed); ok {
		return at
	}
	if at, err := time.ParseInLocation("2006-01-02", sh.Date, time.Local); err == nil {
		return at
	}
	return time.Time{}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
