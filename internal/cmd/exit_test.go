package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/service"
	"github.com/mbalazy/pm-cli/internal/storage"
)

func TestExitCodeClassifies(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"plain error", errors.New("disk on fire"), ExitError},
		{"explicit usage", usageErr(errors.New("unknown flag")), ExitUsage},
		{"explicit not found", notFoundErr(errors.New("no journal")), ExitNotFound},
		{"explicit conflict", conflictErr(errors.New("claim held")), ExitConflict},
		{"wrapped explicit keeps its code", fmt.Errorf("outer: %w", conflictErr(errors.New("inner"))), ExitConflict},
		{"store invalid value", storage.ValidateStatus("bogus", storage.DefaultStatuses), ExitUsage},
		{"store invalid value wrapped", fmt.Errorf("add: %w", storage.ValidateTaskID("../x")), ExitUsage},
		{"service validation", &service.ValidationError{Err: errors.New("bad")}, ExitUsage},
		{"ambiguous task", fmt.Errorf("%w %q: 2 matches", storage.ErrAmbiguousTask, "x"), ExitUsage},
		{"task not found", fmt.Errorf("%w: %q", storage.ErrTaskNotFound, "x"), ExitNotFound},
		{"project not found", fmt.Errorf("%w: %q", storage.ErrProjectNotFound, "x"), ExitNotFound},
		{"conflict", &storage.ConflictError{Err: errors.New("exists")}, ExitConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExitCode(c.err); got != c.want {
				t.Errorf("ExitCode(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

// exitFixture is a data dir with one project (acme, prefix acme) holding
// acme-1, wired into the root command through PM_DATA_DIR - the whole
// binary's path, cobra dispatch included, is what the exit codes cover.
func exitFixture(t *testing.T) *storage.Store {
	t.Helper()
	root := t.TempDir()
	t.Setenv("PM_DATA_DIR", root)
	store := &storage.Store{Root: root}
	if err := store.CreateProject("acme", &storage.Project{Name: "Acme", Prefix: "acme"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTask("acme", storage.NewTask("acme-1", "First task", "acme")); err != nil {
		t.Fatal(err)
	}
	return store
}

// runRoot executes `pm <args>` the way main does and returns the exit code
// plus what reached stdout (both cobra's writer and the process stdout)
// and cobra's stderr writer.
func runRoot(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	var err error
	captured := captureStdout(t, func() {
		root := NewRootCmd()
		root.SetArgs(args)
		root.SetOut(&out)
		root.SetErr(&errOut)
		err = root.Execute()
	})
	return ExitCode(err), out.String() + captured, errOut.String()
}

func TestExitCodesEndToEnd(t *testing.T) {
	exitFixture(t)
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"unknown command", []string{"bogus"}, ExitUsage},
		{"unknown flag", []string{"add", "--vps"}, ExitUsage},
		{"wrong argument count", []string{"add", "acme"}, ExitUsage},
		{"no project given", []string{"context"}, ExitUsage},
		{"status outside the project's set", []string{"mv", "acme", "acme-1", "bogus"}, ExitUsage},
		{"unsafe id", []string{"add", "acme", "bad id", "--id", "../x"}, ExitUsage},
		{"unknown timeline kind", []string{"timeline", "add", "--project", "acme", "--kind", "bogus", "--text", "x"}, ExitUsage},
		{"project not found", []string{"show", "nope", "acme-1"}, ExitNotFound},
		{"task not found", []string{"show", "acme", "nope"}, ExitNotFound},
		{"journal not declared", []string{"journal", "add", "nope", "--project", "acme"}, ExitNotFound},
		{"reorder parent not found", []string{"reorder", "nope-1", "x"}, ExitNotFound},
		{"duplicate id", []string{"add", "acme", "another title", "--id", "acme-1"}, ExitConflict},
		{"ok", []string{"list", "-p", "acme"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, _ := runRoot(t, c.args...)
			if code != c.want {
				t.Errorf("pm %s: exit %d, want %d", strings.Join(c.args, " "), code, c.want)
			}
			if code != 0 && stdout != "" {
				t.Errorf("pm %s: a failing command wrote to stdout: %q", strings.Join(c.args, " "), stdout)
			}
		})
	}
}

// The --json rule: the structured result is the ONLY thing on stdout, and
// a failure leaves stdout empty (the error goes to stderr, once - cobra's
// own print is silenced so main's is the only one).
func TestJSONCommandsKeepStdoutStructured(t *testing.T) {
	exitFixture(t)
	for _, args := range [][]string{
		{"today", "--json"},
		{"runs", "--json", "--local"},
		{"timeline", "--json", "--project", "acme"},
		{"timeline", "list", "--json", "--project", "acme"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, stdout, stderr := runRoot(t, args...)
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			var v any
			if err := json.Unmarshal([]byte(stdout), &v); err != nil {
				t.Errorf("stdout is not one JSON document: %v\n%s", err, stdout)
			}
		})
	}
	t.Run("failure leaves stdout empty and stderr silent", func(t *testing.T) {
		code, stdout, stderr := runRoot(t, "timeline", "--json", "--project", "nope")
		if code != ExitNotFound {
			t.Errorf("exit %d, want %d", code, ExitNotFound)
		}
		if stdout != "" {
			t.Errorf("stdout must stay empty on failure, got %q", stdout)
		}
		if stderr != "" {
			t.Errorf("cobra must not print the error itself (main does, once), got %q", stderr)
		}
	})
}
