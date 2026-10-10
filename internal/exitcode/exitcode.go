// Package exitcode holds pm's process exit codes and the error type that
// pins one of them on an error. internal/cmd's ExitCode reads it back; the
// contract each code stands for is documented there and in docs/cli.md.
package exitcode

// The exit codes of the pm binary.
const (
	Error    = 1
	Usage    = 2
	NotFound = 3
	Conflict = 4
)

// CodedError pins an exit code on an error a classifier could not tell from
// its type alone: a cobra flag/argument error (plain errors), "no project
// given", a journal name outside the declared list, a claim another process
// holds. Error() is the wrapped text unchanged.
type CodedError struct {
	Code int
	Err  error
}

func (e *CodedError) Error() string { return e.Err.Error() }
func (e *CodedError) Unwrap() error { return e.Err }

// With wraps err with code; a nil err stays nil.
func With(code int, err error) error {
	if err == nil {
		return nil
	}
	return &CodedError{Code: code, Err: err}
}
