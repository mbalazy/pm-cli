// Package projectarg resolves the optional [project] argument the pm commands
// share: an explicit slug, else the project whose path contains the working
// directory.
package projectarg

import (
	"fmt"
	"os"

	"github.com/mbalazy/pm-cli/internal/exitcode"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// Slug resolves the optional [project] argument shared by the commands that
// take one: explicit arg, else cwd detection.
func Slug(store storage.TaskStore, args []string) (string, error) {
	var slug string
	if len(args) == 1 {
		s, err := store.ResolveProject(args[0])
		if err != nil {
			return "", err
		}
		slug = s
	} else {
		slug = FromCwd(store)
	}
	if slug == "" {
		return "", exitcode.With(exitcode.Usage, fmt.Errorf("no project (pass a project or run inside a project dir)"))
	}
	return slug, nil
}

// Load additionally loads the project, for the commands that need the project
// struct. Commands that only need the slug must call Slug instead: an
// unreadable project.yaml is fatal here, and inheriting that failure would
// break commands that work fine without it.
func Load(store storage.TaskStore, args []string) (string, *storage.Project, error) {
	slug, err := Slug(store, args)
	if err != nil {
		return "", nil, err
	}
	proj, err := store.GetProject(slug)
	if err != nil {
		return "", nil, fmt.Errorf("load project %s: %w", slug, err)
	}
	return slug, proj, nil
}

// FromCwd returns the slug of the project whose path contains the working
// directory, or "" when there is none.
func FromCwd(store storage.TaskStore) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	slug, err := storage.ResolveProjectFromCwd(store, cwd)
	if err != nil {
		return ""
	}
	return slug
}
