package board

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// truncateWidth shortens s to at most max display cells, appending "…" when it
// cuts. It measures display width (runewidth), never byte length: a byte slice
// like s[:n] can split a UTF-8 sequence (Polish diacritics, emoji) into
// mojibake, and double-width emoji make byte counts overshoot columns. A
// too-small max clamps instead of producing a negative slice bound.
func truncateWidth(s string, max int) string {
	if max < 2 {
		max = 2
	}
	if runewidth.StringWidth(s) <= max {
		return s
	}
	return runewidth.Truncate(s, max, "…")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func openEditor(path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nvim"
	}
	c := exec.Command(editor, path)
	return tea.ExecProcess(c, editorFinished)
}

// editorFinished is openEditor's tea.ExecProcess callback, named rather than
// inline so the error can actually be asserted on: tea.ExecProcess hands back
// an unexported message type, so an inline closure is unreachable from a test
// and a regression to the old `return reloadMsg{}` passes the whole suite.
func editorFinished(err error) tea.Msg {
	return editorResultMsg{err: err}
}

func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// relativeTime renders a task stamp as "today" / "Nd ago". It goes through
// storage.ParseStamp because `updated` may be either shape: an RFC3339 stamp
// on anything written since the switch, a bare date on everything older.
//
// The difference is counted in CALENDAR DAYS, both sides truncated to local
// midnight. Elapsed-hours/24 would read the two shapes differently - a task
// touched yesterday at 23:00 is 11 hours old ("today") while a legacy bare
// date for that same yesterday is 34 hours old ("1d ago") - so two tasks last
// touched on the same day would render differently in one list.
func relativeTime(dateStr string) string {
	t, ok := storage.ParseStamp(dateStr)
	if !ok {
		return dateStr
	}
	days := calendarDaysAgo(t)
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "1d ago"
	default:
		return fmt.Sprintf("%dd ago", days)
	}
}

// nowFn is the clock calendarDaysAgo reads, a test seam (like killSignal in
// run_control.go): a test that has to pin "what day is it" otherwise fails
// exactly once, on the run that crosses midnight.
var nowFn = time.Now

// calendarDaysAgo counts whole local calendar days between at and now. Both
// days are rebuilt in UTC purely as arithmetic: a local day is 23 or 25 hours
// long across a DST switch, which would round the wrong way.
func calendarDaysAgo(at time.Time) int {
	at = at.In(time.Local)
	stampDay := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	n := nowFn().In(time.Local)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	return int(today.Sub(stampDay).Hours() / 24)
}
