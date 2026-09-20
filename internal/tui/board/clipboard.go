package board

import (
	"errors"
	"os/exec"
	"strings"
	"time"
)

// clipboardTools is the write-to-clipboard command of each platform pm runs
// on, in the order they are tried: macOS first (where pm is developed), then
// Wayland, then the two X11 tools. The first one on PATH wins, so a machine
// with several installed does not need configuring.
var clipboardTools = [][]string{
	{"pbcopy"},
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
}

// errNoClipboardTool is what clipboardCommand returns when none of them is
// installed. It names the candidates, because "copy failed" on a Linux box
// with no xclip is otherwise indistinguishable from a broken board.
var errNoClipboardTool = errors.New("no clipboard tool on PATH (tried pbcopy, wl-copy, xclip, xsel)")

// clipboardCommand returns the argv of the first clipboard tool lookPath
// finds. lookPath is a parameter so the test can drive every platform from
// one machine; callers pass exec.LookPath.
func clipboardCommand(lookPath func(string) (string, error)) ([]string, error) {
	for _, argv := range clipboardTools {
		if _, err := lookPath(argv[0]); err == nil {
			return argv, nil
		}
	}
	return nil, errNoClipboardTool
}

// copyToClipboard writes value to the system clipboard and reports what
// happened in a toast - a short one on success, the 15-second error one when
// no tool is installed or the tool itself fails.
func (m *Model) copyToClipboard(value string) {
	argv, err := clipboardCommand(exec.LookPath)
	if err != nil {
		m.showErrorToast("copy failed", err)
		return
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Stdin = strings.NewReader(value)
	if err := c.Run(); err != nil {
		m.showErrorToast("copy failed", err)
		return
	}
	display := truncateWidth(value, 40)
	m.toastMsg = "Copied: " + display
	m.toastExpiry = time.Now().Add(2 * time.Second)
}
