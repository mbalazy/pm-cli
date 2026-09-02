package board

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mattn/go-runewidth"
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
func relativeTime(dateStr string) string {
	t, ok := storage.ParseStamp(dateStr)
	if !ok {
		return dateStr
	}
	days := int(time.Since(t).Hours() / 24)
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "1d ago"
	default:
		return fmt.Sprintf("%dd ago", days)
	}
}
