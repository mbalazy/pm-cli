package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

func runProjectsAddCmd(store storage.TaskStore, args ...string) error {
	cmd := newProjectsAddCmd(store)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return cmd.Execute()
}

// TestProjectsAddRejectsUnsafeSlug: `pm projects add` never ran ValidateSlug
// (only pm_create_project did), and CreateProject MkdirAll's whatever it is
// handed. Two reproduced consequences, one test each:
//
//   - "../outside" wrote a project.yaml OUTSIDE the pm root;
//   - "MyProj" created a project `pm projects` lists but NO command can
//     address (ResolveProject lowercases the query, never the candidates) -
//     and there is no `pm projects rm` to undo it.
func TestProjectsAddRejectsUnsafeSlug(t *testing.T) {
	store, _ := tempStore(t)
	root := store.RootDir()

	for _, slug := range []string{"../outside", "MyProj", "sub/dir", ".."} {
		err := runProjectsAddCmd(store, slug)
		if err == nil {
			t.Fatalf("pm projects add %q was accepted", slug)
		}
		if !strings.Contains(err.Error(), "invalid slug") {
			t.Errorf("slug %q: want a ValidateSlug error, got: %v", slug, err)
		}
		if _, statErr := os.Stat(store.ProjectYAML(slug)); !os.IsNotExist(statErr) {
			t.Errorf("slug %q: project.yaml created at %s (stat err = %v)", slug, store.ProjectYAML(slug), statErr)
		}
	}

	// Nothing new next to or above the pm root either.
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "outside")); !os.IsNotExist(err) {
		t.Errorf("project dir escaped above the pm root (stat err = %v)", err)
	}
	projects, err := store.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0] != "proj" {
		t.Fatalf("rejected adds changed the project list: %v", projects)
	}

	// The valid case still works - this is hardening, not a lockout.
	if err := runProjectsAddCmd(store, "my-proj"); err != nil {
		t.Fatalf("valid slug rejected: %v", err)
	}
	if _, err := store.GetProject("my-proj"); err != nil {
		t.Fatalf("valid slug not created: %v", err)
	}
}
