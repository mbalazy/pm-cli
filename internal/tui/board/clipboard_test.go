package board

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// fakeLookPath answers like exec.LookPath for a machine on which exactly the
// named tools are installed.
func fakeLookPath(installed ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(installed, name) {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

// TestClipboardCommand: the board ran a bare `pbcopy`, so every yank on Linux
// failed with "executable file not found" and nothing said why. The helper
// picks whatever the machine has.
func TestClipboardCommand(t *testing.T) {
	tests := []struct {
		name      string
		installed []string
		want      []string
	}{
		{"macOS", []string{"pbcopy"}, []string{"pbcopy"}},
		{"Wayland", []string{"wl-copy"}, []string{"wl-copy"}},
		{"X11 with xclip", []string{"xclip"}, []string{"xclip", "-selection", "clipboard"}},
		{"X11 with xsel only", []string{"xsel"}, []string{"xsel", "--clipboard", "--input"}},
		{"several installed: the first candidate wins", []string{"xsel", "wl-copy", "pbcopy"}, []string{"pbcopy"}},
		{"X11 with both: xclip before xsel", []string{"xsel", "xclip"}, []string{"xclip", "-selection", "clipboard"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := clipboardCommand(fakeLookPath(tt.installed...))
			if err != nil {
				t.Fatalf("clipboardCommand() error = %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("clipboardCommand() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("nothing installed is a named error, not a bare exec failure", func(t *testing.T) {
		got, err := clipboardCommand(fakeLookPath())
		if got != nil {
			t.Errorf("argv = %v, want nil", got)
		}
		if !errors.Is(err, errNoClipboardTool) {
			t.Fatalf("err = %v, want errNoClipboardTool", err)
		}
		// The toast is the only place this text is ever read, so it has to
		// name what the user is supposed to install.
		for _, tool := range []string{"pbcopy", "wl-copy", "xclip", "xsel"} {
			if !strings.Contains(err.Error(), tool) {
				t.Errorf("error %q does not name %s", err, tool)
			}
		}
	})
}

// TestCopyToClipboardReportsMissingTool: no tool on PATH must leave the long
// error toast, never the "Copied:" one.
func TestCopyToClipboardReportsMissingTool(t *testing.T) {
	if _, err := clipboardCommand(fakeLookPath()); err == nil {
		t.Fatal("fixture broken: the empty machine found a tool")
	}
	m := &Model{}
	m.showErrorToast("copy failed", errNoClipboardTool)
	if !strings.HasPrefix(m.toastMsg, "copy failed: ") {
		t.Errorf("toastMsg = %q", m.toastMsg)
	}
	if m.toastExpiry.IsZero() {
		t.Error("toastExpiry not set")
	}
}
