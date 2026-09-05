package service

import (
	"os"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func TestConfig(t *testing.T) {
	t.Run("no file = the defaults, resolved", func(t *testing.T) {
		store := newTestStore(t)
		res, err := Config(store)
		if err != nil {
			t.Fatal(err)
		}
		c := res.Cockpit
		if c.Sidebar.Variant != "columns" || !c.Sidebar.ShowRepos || c.Sidebar.Sort != "worst" {
			t.Fatalf("sidebar = %+v", c.Sidebar)
		}
		if c.Refresh.EverySeconds != 1800 || c.Refresh.Window != "07:00-20:00" {
			t.Fatalf("refresh = %+v", c.Refresh)
		}
		if !c.Sections["needs_me"] || c.Sections["recent"] {
			t.Fatalf("sections = %v", c.Sections)
		}
		if len(c.Sections) != len(storage.CockpitSections) {
			t.Fatalf("every known section must be present: %v", c.Sections)
		}
		if c.Groups == nil || len(c.Groups) != 0 {
			t.Fatalf("groups must be an empty array, got %#v", c.Groups)
		}
	})
	t.Run("file values win and groups resolve their names", func(t *testing.T) {
		store := newTestStore(t)
		yaml := "cockpit:\n  sidebar: {variant: rail, show_repos: false}\n  refresh: {every: 10m}\n  groups:\n    acme: {name: ACME}\n    orbit: {}\n"
		if err := os.WriteFile(store.ConfigPath(), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := Config(store)
		if err != nil {
			t.Fatal(err)
		}
		c := res.Cockpit
		if c.Sidebar.Variant != "rail" || c.Sidebar.ShowRepos || c.Sidebar.Sort != "worst" {
			t.Fatalf("sidebar = %+v", c.Sidebar)
		}
		if c.Refresh.EverySeconds != 600 {
			t.Fatalf("refresh = %+v", c.Refresh)
		}
		if len(c.Groups) != 2 || c.Groups[0].Slug != "atlas" || c.Groups[0].Name != "atlas" || c.Groups[1].Name != "ACME" {
			t.Fatalf("groups = %+v", c.Groups)
		}
	})
	t.Run("a broken file is an error", func(t *testing.T) {
		store := newTestStore(t)
		if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  sidebar: {variant: bogus}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Config(store); err == nil {
			t.Fatal("expected an error")
		}
	})
}
