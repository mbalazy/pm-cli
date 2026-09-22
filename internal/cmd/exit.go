package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mbalazy/pm-cli/internal/service"
	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/spf13/cobra"
)

// Exit codes of the pm binary (pm-cli-149). A script - a skill's shell
// helper, typically - reads the code before it reads stderr, so the code
// has to tell "you called it wrong" from "the thing is not there" from
// "the state refused it" from "pm broke". Three specific codes and one
// catch-all; nothing finer (no "dependency failed" / "no effect" /
// "partial" - pm has no bulk mutations to describe that way).
//
// The contract, as docs/cli.md states it:
//
//	0  ok
//	1  every other failure (I/O, a broken project.yaml, a worker crash)
//	2  bad usage: unknown command, unknown flag, wrong argument count,
//	   no project given, an ambiguous task query, or a value the store
//	   rejects (a status outside the project's set, an unsafe id, an
//	   unknown --kind, a malformed [verified] marker)
//	3  not found: the task, project or journal named does not exist
//	4  conflict: the store's current state refuses the mutation (a task
//	   file already under that id, a claim another process holds)
//
// Structured output (--json, the executor envelopes) goes to stdout ONLY;
// every diagnostic, the error line included, goes to stderr - a caller
// piping stdout into a JSON parser must never see prose there.
const (
	ExitError    = 1
	ExitUsage    = 2
	ExitNotFound = 3
	ExitConflict = 4
)

// exitError pins an exit code on an error the classifier below could not
// tell from its type alone: a cobra flag/argument error (plain errors),
// "no project given", a journal name outside the declared list, a claim
// another process holds. Error() is the wrapped text unchanged.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func withExit(code int, err error) error {
	if err == nil {
		return nil
	}
	return &exitError{code: code, err: err}
}

func usageErr(err error) error    { return withExit(ExitUsage, err) }
func notFoundErr(err error) error { return withExit(ExitNotFound, err) }
func conflictErr(err error) error { return withExit(ExitConflict, err) }

// ExitCode maps the error a command returned to the process exit code.
// An explicit exitError wins; then the store's typed errors and sentinels;
// the service layer's ValidationError (pm today goes through it) is bad
// usage; anything else is 1.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	var iv *storage.InvalidValueError
	var ce *storage.ConflictError
	var ve *service.ValidationError
	switch {
	case errors.As(err, &iv), errors.As(err, &ve), errors.Is(err, storage.ErrAmbiguousTask):
		return ExitUsage
	case errors.Is(err, storage.ErrTaskNotFound), errors.Is(err, storage.ErrProjectNotFound):
		return ExitNotFound
	case errors.As(err, &ce):
		return ExitConflict
	}
	return ExitError
}

// classifyUsage wires cobra's own failures - which it reports as plain
// errors - into exit 2: flag parsing on every command (the FlagErrorFunc
// is inherited down the tree), argument-count validators on every command
// that has one, and an unknown subcommand on the root (cobra's default
// "legacy" validator would answer that only when root.Args is nil, so the
// root gets an explicit one that says the same thing).
func classifyUsage(root *cobra.Command) {
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageErr(err)
	})
	root.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
		if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
			msg += "\n\nDid you mean this?\n\t" + strings.Join(s, "\n\t") + "\n"
		}
		return usageErr(errors.New(msg))
	}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if v := sub.Args; v != nil {
				sub.Args = func(cmd *cobra.Command, args []string) error {
					return usageErr(v(cmd, args))
				}
			}
			walk(sub)
		}
	}
	walk(root)
}
