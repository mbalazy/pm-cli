package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeProjectDir drops a project.yaml for slug under the store's root.
func writeProjectDir(t *testing.T, s *Store, slug string, p *Project) {
	t.Helper()
	if err := os.MkdirAll(s.ProjectDir(slug), 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteProject(s.ProjectYAML(slug), p); err != nil {
		t.Fatal(err)
	}
}

func TestProjectGroups(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	writeProjectDir(t, store, "acme-api", &Project{Name: "AcmeApi", Group: "acme"})
	writeProjectDir(t, store, "acme-zap", &Project{Name: "AcmeZap", Group: "acme"})
	writeProjectDir(t, store, "acme-best", &Project{Name: "Best", Group: "acme", Archived: true})
	writeProjectDir(t, store, "pm-cli", &Project{Name: "pm"})
	writeProjectDir(t, store, "old", &Project{Name: "Old", Archived: true})
	writeConfig(t, store, "cockpit:\n  groups:\n    acme: {name: Acme}\n    ghost: {name: Nobody}\n")

	groups, err := store.ProjectGroups()
	if err != nil {
		t.Fatal(err)
	}
	want := []ProjectGroup{
		{Slug: "acme", Name: "Acme", Projects: []string{"acme-api", "acme-zap"}},
		{Slug: "pm-cli", Name: "pm-cli", Projects: []string{"pm-cli"}},
	}
	if !reflect.DeepEqual(groups, want) {
		t.Errorf("groups = %+v, want %+v", groups, want)
	}
}

func TestProjectGroupsSoloArchivedGroupVanishes(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	writeProjectDir(t, store, "atlas", &Project{Name: "LE", Group: "orbit", Archived: true})
	groups, err := store.ProjectGroups()
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Errorf("a group whose only member sleeps must not be listed: %+v", groups)
	}
}

func TestProjectGroupsBadConfigIsLoud(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	writeProjectDir(t, store, "a", &Project{Name: "A"})
	writeConfig(t, store, "cockpit:\n  groups:\n    Bad Name: {}\n")
	if _, err := store.ProjectGroups(); err == nil {
		t.Fatal("an unparsable config must not degrade to slug-named groups")
	}
}

func TestGroupSlug(t *testing.T) {
	if got := (&Project{Group: "acme"}).GroupSlug("acme-api"); got != "acme" {
		t.Errorf("got %q", got)
	}
	if got := (&Project{}).GroupSlug("acme-api"); got != "acme-api" {
		t.Errorf("got %q", got)
	}
	var nilP *Project
	if got := nilP.GroupSlug("acme-api"); got != "acme-api" {
		t.Errorf("nil project: got %q", got)
	}
}

func TestValidateGroup(t *testing.T) {
	for _, ok := range []string{"", "acme", "little-engine", "a.b_c9"} {
		if err := ValidateGroup(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"Acme", "little engine", "../x", "-x", "a/b"} {
		if err := ValidateGroup(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// The store refuses an unsafe group on create and on a mutation that CHANGES
// it, but keeps a project that already carries a bad one editable elsewhere.
func TestStoreValidatesGroup(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	if err := store.CreateProject("a", &Project{Name: "A", Group: "Bad Group"}); err == nil {
		t.Fatal("create accepted an unsafe group")
	}
	if err := store.CreateProject("a", &Project{Name: "A", Group: "acme"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MutateProject("a", func(p *Project) error { p.Group = "No Way"; return nil }); err == nil {
		t.Fatal("mutate accepted an unsafe group change")
	}
	p, _ := store.GetProject("a")
	if p.Group != "acme" {
		t.Fatalf("rejected group was written: %q", p.Group)
	}

	// A hand-edited bad group must not lock the project: an edit that leaves
	// the group alone goes through.
	raw := "name: A\ngroup: Hand Edited\n"
	if err := os.WriteFile(store.ProjectYAML("a"), []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MutateProject("a", func(p *Project) error { p.Notes = "n"; return nil }); err != nil {
		t.Fatalf("notes edit refused on a project with a bad hand-edited group: %v", err)
	}
	// Clearing it is a change too, and a legal one.
	if _, err := store.MutateProject("a", func(p *Project) error { p.Group = ""; return nil }); err != nil {
		t.Fatalf("clearing the group refused: %v", err)
	}
}

// `group` is a KNOWN key now: writeProject must carry it, and a rewrite that
// does not touch it must leave the rest of a commented file byte-exact.
func TestWriteProjectRoundTripsGroup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.yaml")
	handTuned := `name: AcmeApi
# the Acme content pipeline
path: /repos/acme-api
group: acme # shares the cockpit page with acme-zap
custom: keep
`
	if err := os.WriteFile(path, []byte(handTuned), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := ReadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Group != "acme" {
		t.Fatalf("group = %q", p.Group)
	}
	if err := WriteProject(path, p); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != handTuned {
		t.Errorf("no-op rewrite changed the file:\n--- before\n%s\n--- after\n%s", handTuned, raw)
	}

	p.Notes = "touched"
	if err := WriteProject(path, p); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	text := string(raw)
	for _, want := range []string{"# the Acme content pipeline", "group: acme # shares the cockpit page with acme-zap", "custom: keep", "notes: touched"} {
		if !strings.Contains(text, want) {
			t.Errorf("after an unrelated edit the file lacks %q:\n%s", want, text)
		}
	}
	again, _ := ReadProject(path)
	if again.Group != "acme" {
		t.Errorf("group after rewrite = %q", again.Group)
	}
}
