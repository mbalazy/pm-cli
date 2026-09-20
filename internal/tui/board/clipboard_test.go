package board

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestClipboardToolsOrder pins the candidate list and each tool's flags. The
// flags are the whole point: `xclip` without `-selection clipboard` writes
// the PRIMARY selection, which is not what Ctrl+V pastes.
func TestClipboardToolsOrder(t *testing.T) {
	want := [][]string{
		{"pbcopy"},
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	}
	if len(clipboardTools) != len(want) {
		t.Fatalf("got %v, want %v", clipboardTools, want)
	}
	for i := range want {
		if !slices.Equal(clipboardTools[i], want[i]) {
			t.Errorf("candidate %d = %v, want %v", i, clipboardTools[i], want[i])
		}
	}
}

// fakeTool writes an executable that records the argv and the stdin it was
// handed, then exits with `exit`. Same idea as the fake `claude` binary the
// executor tests use: only a real exec proves the argv and the stdin wiring,
// which a stubbed runner would hide.
type fakeTool struct {
	name      string
	args      []string
	argsFile  string
	stdinFile string
}

func newFakeTool(t *testing.T, dir, name string, args []string, exit int) fakeTool {
	t.Helper()
	f := fakeTool{
		name:      name,
		args:      args,
		argsFile:  filepath.Join(dir, name+".args"),
		stdinFile: filepath.Join(dir, name+".stdin"),
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\ncat > %q\nexit %d\n",
		f.argsFile, f.stdinFile, exit)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fakeTool) ran() bool {
	_, err := os.Stat(f.argsFile)
	return err == nil
}

// useFakeTools puts the tools on PATH and makes them the candidate list.
func useFakeTools(t *testing.T, tools ...fakeTool) {
	t.Helper()
	old := clipboardTools
	var argv [][]string
	for _, f := range tools {
		argv = append(argv, append([]string{f.name}, f.args...))
	}
	clipboardTools = argv
	t.Cleanup(func() { clipboardTools = old })
}

// TestCopyToClipboardRunsTheTool: the argv after the tool name and the value
// on stdin are the whole job, and a stubbed runner can see neither - drop
// `argv[1:]...` and an X11 yank silently writes the PRIMARY selection, hand
// the command the wrong stdin and the clipboard is cleared while the board
// still says "Copied:". So this execs the real command.
func TestCopyToClipboardRunsTheTool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	tool := newFakeTool(t, dir, "pm-fake-clip", []string{"-selection", "clipboard"}, 0)
	useFakeTools(t, tool)

	m := &Model{}
	m.copyToClipboard("hello board")

	gotArgs, err := os.ReadFile(tool.argsFile)
	if err != nil {
		t.Fatalf("the tool was never run: %v", err)
	}
	if want := "-selection\nclipboard\n"; string(gotArgs) != want {
		t.Errorf("argv after the tool name = %q, want %q", gotArgs, want)
	}
	gotStdin, err := os.ReadFile(tool.stdinFile)
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

// TestCopyToClipboardFallsThroughAFailingTool is the wl-copy-on-X11 case:
// the tool is installed, so presence alone would pick it, and it exits
// non-zero because there is no Wayland server. xclip is sitting right there
// and works.
func TestCopyToClipboardFallsThroughAFailingTool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	wayland := newFakeTool(t, dir, "pm-fake-wl", nil, 1)
	x11 := newFakeTool(t, dir, "pm-fake-x11", []string{"-selection", "clipboard"}, 0)
	useFakeTools(t, wayland, x11)

	m := &Model{}
	m.copyToClipboard("still copied")

	if !wayland.ran() {
		t.Error("the first candidate was never tried")
	}
	got, err := os.ReadFile(x11.stdinFile)
	if err != nil {
		t.Fatalf("the second candidate never ran: %v", err)
	}
	if string(got) != "still copied" {
		t.Errorf("stdin = %q, want %q", got, "still copied")
	}
	if m.toastMsg != "Copied: still copied" {
		t.Errorf("toastMsg = %q, want the success toast", m.toastMsg)
	}
}

// TestCopyToClipboardReportsFailures: both ways of failing have to reach the
// user, because a yank that quietly does nothing looks like a broken board.
func TestCopyToClipboardReportsFailures(t *testing.T) {
	t.Run("every installed tool refuses", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		useFakeTools(t,
			newFakeTool(t, dir, "pm-fake-bad1", nil, 1),
			newFakeTool(t, dir, "pm-fake-bad2", nil, 3),
		)

		m := &Model{}
		m.copyToClipboard("x")
		if !strings.HasPrefix(m.toastMsg, "copy failed: ") {
			t.Errorf("toastMsg = %q, want a copy-failed toast", m.toastMsg)
		}
		// Which tools were tried is the only clue the user gets.
		for _, name := range []string{"pm-fake-bad1", "pm-fake-bad2"} {
			if !strings.Contains(m.toastMsg, name) {
				t.Errorf("toast %q does not name %s", m.toastMsg, name)
			}
		}
	})

	t.Run("nothing installed names what to install", func(t *testing.T) {
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

// TestWriteClipboardSkipsWhatIsNotInstalled: the lookup is injected so every
// platform's choice can be driven from one machine.
func TestWriteClipboardSkipsWhatIsNotInstalled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	only := newFakeTool(t, dir, "pm-fake-only", nil, 0)
	useFakeTools(t,
		fakeTool{name: "pm-fake-absent"},
		only,
	)

	installed := func(name string) (string, error) {
		if name == "pm-fake-only" {
			return filepath.Join(dir, name), nil
		}
		return "", errors.New("not found")
	}
	if err := writeClipboard("v", installed); err != nil {
		t.Fatalf("writeClipboard() = %v", err)
	}
	if got, _ := os.ReadFile(only.stdinFile); string(got) != "v" {
		t.Errorf("stdin = %q, want %q", got, "v")
	}
	if _, err := os.Stat(filepath.Join(dir, "pm-fake-absent.args")); err == nil {
		t.Error("a tool the lookup said was absent was run anyway")
	}
}
