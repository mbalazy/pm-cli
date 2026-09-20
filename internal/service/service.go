// Package service holds the task-mutation semantics every pm front end
// shares: the MCP server (`pm mcp`) and the HTTP API (`pm serve`) both decode
// their transport's arguments into the input types here and call the same
// functions, so a validation rule, a tri-state field or a lock discipline can
// never diverge between the two. Handlers are meant to stay thin - decode,
// call, encode - and this package knows nothing about MCP or HTTP.
//
// The input types carry `jsonschema` struct tags because the MCP SDK derives
// each tool's schema from the very struct it decodes into; the tags are plain
// Go struct tags, not an SDK dependency, and they are the ONE copy of every
// parameter description an agent ever reads.
package service

import "github.com/mbalazy/pm-cli/internal/storage"

// ValidationError is a failure the CALLER caused - a status outside the
// project's set, a mode/epic_mode/finish_mode value that isn't one of the
// legal words. Transports map it to their "bad request" shape (an MCP tool
// error, HTTP 400); every other error means pm itself could not do the work.
type ValidationError struct {
	Err error
}

func (e *ValidationError) Error() string { return e.Err.Error() }

func (e *ValidationError) Unwrap() error { return e.Err }

// validation wraps err as a *ValidationError; nil stays nil so the storage
// validators can be called in one line.
func validation(err error) error {
	if err == nil {
		return nil
	}
	return &ValidationError{Err: err}
}

// validateStatusForProject rejects a status outside the project's set, with
// archived always legal (system-level, never listed in project.yaml).
func validateStatusForProject(store storage.TaskStore, slug string, st storage.TaskStatus) error {
	if st == storage.StatusArchived {
		return nil
	}
	return validation(storage.ValidateStatus(st, store.GetProjectStatuses(slug)))
}
