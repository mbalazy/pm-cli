package storage

import "fmt"

// The two error KINDS a transport keys on beyond the not-found sentinels
// (ErrTaskNotFound / ErrProjectNotFound / ErrAmbiguousTask in store.go).
// Both carry the wrapped error's text UNCHANGED - the type is the signal,
// never a message prefix, so no caller matching on prose moved. The CLI
// maps them to exit codes (internal/cmd/exit.go: 2 and 4), the service
// layer already folds InvalidValueError into its own *ValidationError
// (400 on HTTP, a tool error over MCP). pm-cli-149.

// InvalidValueError is a value the caller supplied that the store rejects:
// a status outside the project's set, an unsafe task id or slug, an
// unknown mode or timeline kind, a malformed provenance marker. The fix is
// on the caller's side, in the command line or the tool call.
type InvalidValueError struct{ Err error }

func (e *InvalidValueError) Error() string { return e.Err.Error() }
func (e *InvalidValueError) Unwrap() error { return e.Err }

// invalidValue is fmt.Errorf wrapped as an InvalidValueError.
func invalidValue(format string, args ...any) error {
	return &InvalidValueError{Err: fmt.Errorf(format, args...)}
}

// ConflictError is a mutation the store's CURRENT STATE refuses - a task
// file already under that id, a claim another process holds. The input
// was fine; the fix is to look at the state (or retry later), not at the
// command line.
type ConflictError struct{ Err error }

func (e *ConflictError) Error() string { return e.Err.Error() }
func (e *ConflictError) Unwrap() error { return e.Err }

// conflict is fmt.Errorf wrapped as a ConflictError.
func conflict(format string, args ...any) error {
	return &ConflictError{Err: fmt.Errorf(format, args...)}
}
