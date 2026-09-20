package board

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// fakeClipboardTool puts an executable named `name` on PATH that records the
// argv and the stdin it was handed, and points clipboardTools at it. Same
// idea as the fake `claude` binary the executor tests use: only a real exec
// proves the argv and the stdin wiring, which a stubbed runner would hide.
func fakeClipboardTool(t *testing.T, name string, args []string, exit int) (argsFile, stdinFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	stdinFile = filepath.Join(dir, "stdin")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\ncat > %q\nexit %d\n",
		argsFile, stdinFile, exit)
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	old := clipboardTools
	clipboardTools = [][]string{append([]string{name}, args...)}
	t.Cleanup(func() { clipboardTools = old })
	return argsFile, stdinFile
}

// TestCopyToClipboardRunsTheTool: the argv after the tool name and the value
// on stdin are the whole job. A test that stubs the runner cannot see either
// - drop `argv[1:]...` and an X11 yank silently writes the PRIMARY selection
// instead of the clipboard, drop the Stdin line and the clipboard is wiped -
// so this one execs the real command.
func TestCopyToClipboardRunsTheTool(t *testing.T) {
	argsFile, stdinFile := fakeClipboardTool(t, "pm-fake-clip", []string{"-selection", "clipboard"}, 0)

	m := &Model{}
	m.copyToClipboard("hello board")

	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the tool was never run: %v", err)
	}
	if want := "-selection\nclipboard\n"; string(gotArgs) != want {
		t.Errorf("argv after the tool name = %q, want %q", gotArgs, want)
	}
	gotStdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotStdin) != "hello board" {
		t.Errorf("stdin = %q, want %q", gotStdin, "hello board")
	}
	if m.toastMsg != "Copied: hello board" {
		t.Errorf("toastMsg = %q", m.toastMsg)
	}
	if m.toastExpiry.IsZero() {
		t.Error("toastExpiry not set")
	}
}

// TestCopyToClipboardReportsFailures: both ways of failing have to reach the
// user, because a yank that quietly does nothing looks like a broken board.
func TestCopyToClipboardReportsFailures(t *testing.T) {
	t.Run("the tool is there but exits non-zero", func(t *testing.T) {
		fakeClipboardTool(t, "pm-fake-clip-bad", nil, 3)
		m := &Model{}
		m.copyToClipboard("x")
		if !strings.HasPrefix(m.toastMsg, "copy failed: ") {
			t.Errorf("toastMsg = %q, want a copy-failed toast", m.toastMsg)
		}
		if strings.HasPrefix(m.toastMsg, "Copied") {
			t.Errorf("a failed copy reported success: %q", m.toastMsg)
		}
	})

	t.Run("no tool installed names what to install", func(t *testing.T) {
		old := clipboardTools
		clipboardTools = [][]string{{"pm-no-such-clipboard-tool"}}
		t.Cleanup(func() { clipboardTools = old })

		m := &Model{}
		m.copyToClipboard("x")
		if !strings.Contains(m.toastMsg, errNoClipboardTool.Error()) {
			t.Errorf("toastMsg = %q, want it to carry %q", m.toastMsg, errNoClipboardTool)
		}
		for _, tool := range []string{"pbcopy", "wl-copy", "xclip", "xsel"} {
			if !strings.Contains(m.toastMsg, tool) {
				t.Errorf("toast %q does not name %s", m.toastMsg, tool)
			}
		}
	})
}
