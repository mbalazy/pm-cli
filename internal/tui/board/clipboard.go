package board

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// clipboardTools is the write-to-clipboard command of each platform pm runs
// on, in the order they are tried: macOS first (where pm is developed), then
// Wayland, then the two X11 tools.
var clipboardTools = [][]string{
	{"pbcopy"},
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
}

// errNoClipboardTool is what writeClipboard returns when none of them is
// installed. It names the candidates, because "copy failed" on a Linux box
// with no xclip is otherwise indistinguishable from a broken board.
var errNoClipboardTool = errors.New("no clipboard tool on PATH (tried pbcopy, wl-copy, xclip, xsel)")

// writeClipboard pipes value into the first installed tool that accepts it.
//
// Being on PATH is not the same as working: `wl-clipboard` is pulled in by
// several desktop meta-packages, so `wl-copy` is present on plenty of X11
// machines, where it exits non-zero with "Failed to connect to a Wayland
// server". Stopping at the first tool that is merely PRESENT would report a
// failed copy while xclip sat installed and ready, so a non-zero exit moves
// on to the next candidate and only the last word is reported.
//
// lookPath is a parameter so a test can decide which tools the machine has;
// callers pass exec.LookPath.
func writeClipboard(value string, lookPath func(string) (string, error)) error {
	var failed []string
	var lastErr error
	for _, argv := range clipboardTools {
		if _, err := lookPath(argv[0]); err != nil {
			continue
		}
		c := exec.Command(argv[0], argv[1:]...)
		c.Stdin = strings.NewReader(value)
		if err := c.Run(); err != nil {
			failed = append(failed, argv[0])
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("%s did not take it (%v)", strings.Join(failed, ", "), lastErr)
	}
	return errNoClipboardTool
}

// copyToClipboard writes value to the system clipboard and reports what
// happened in a toast - a short one on success, the 15-second error one when
// nothing is installed or every installed tool refused.
func (m *Model) copyToClipboard(value string) {
	if err := writeClipboard(value, exec.LookPath); err != nil {
		m.showErrorToast("copy failed", err)
		return
	}
	display := truncateWidth(value, 40)
	m.toastMsg = "Copied: " + display
	m.toastExpiry = time.Now().Add(2 * time.Second)
}
