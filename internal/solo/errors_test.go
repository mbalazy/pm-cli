package solo

import "errors"

// Thin aliases so the tests read like the code under test.
func errorsAs(err error, target any) bool { return errors.As(err, target) }
func errorsIs(err, target error) bool     { return errors.Is(err, target) }
