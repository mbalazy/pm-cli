package service

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
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
		yaml := "cockpit:\n  sidebar: {variant: rail, show_repos: false}\n  refresh: {every: 10m}\n  groups:\n    acme: {name: Acme}\n    orbit: {}\n"
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
		if len(c.Groups) != 2 || c.Groups[0].Slug != "acme" || c.Groups[0].Name != "Acme" || c.Groups[1].Slug != "orbit" || c.Groups[1].Name != "orbit" {
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

func TestUpdateSettings(t *testing.T) {
	t.Run("a patch changes only what it names and keeps the file's comments", func(t *testing.T) {
		store := newTestStore(t)
		yaml := "# hand-written\ncockpit:\n  # night owl\n  cutoff_hour: 20\n  groups:\n    acme: {name: Acme}\nmy_key: keep\n"
		if err := os.WriteFile(store.ConfigPath(), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		three, variant, off := 3, "rail", false
		res, err := UpdateSettings(store, UpdateSettingsInput{
			DoingIdleDays: &three,
			Refresh:       &RefreshPatch{EverySeconds: &[]int{600}[0]},
			Sections:      map[string]bool{"recent": true, "needs_me": false},
			Sources:       map[string]bool{"git": false},
			Sidebar:       &SidebarPatch{Variant: &variant, ShowRepos: &off},
			Groups: []ConfigGroupPatch{
				{Slug: "orbit", Name: &[]string{"Orbit"}[0], Order: &[]int{1}[0]},
				{Slug: "acme", Order: &[]int{2}[0]},
			},
			Git: &GitPatch{AllBranches: &[]bool{true}[0]},
		})
		if err != nil {
			t.Fatal(err)
		}
		c := res.Cockpit
		if c.DoingIdleDays != 3 || c.WaitingHighlightDays != 5 || c.CutoffHour != 20 {
			t.Fatalf("thresholds/cutoff = %+v", c)
		}
		if c.Refresh.EverySeconds != 600 || c.Refresh.Window != "07:00-20:00" {
			t.Fatalf("refresh = %+v", c.Refresh)
		}
		if !c.Sections["recent"] || c.Sections["needs_me"] || !c.Sections["waiting"] {
			t.Fatalf("sections = %v", c.Sections)
		}
		if c.Sources["git"] || !c.Sources["pm"] {
			t.Fatalf("sources = %v", c.Sources)
		}
		if c.Sidebar.Variant != "rail" || c.Sidebar.ShowRepos || c.Sidebar.Sort != "worst" {
			t.Fatalf("sidebar = %+v", c.Sidebar)
		}
		if len(c.Groups) != 2 || c.Groups[0].Slug != "orbit" || c.Groups[0].Name != "Orbit" || c.Groups[0].Order != 1 ||
			c.Groups[1].Slug != "acme" || c.Groups[1].Name != "Acme" || c.Groups[1].Order != 2 {
			t.Fatalf("groups = %+v", c.Groups)
		}
		if !c.Git.AllBranches {
			t.Fatalf("git = %+v", c.Git)
		}
		raw, _ := os.ReadFile(store.ConfigPath())
		for _, want := range []string{"# hand-written", "# night owl", "cutoff_hour: 20", "name: Acme", "order: 2", "all_branches: true", "my_key: keep"} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("file lacks %q:\n%s", want, raw)
			}
		}
		// The next read sees it - no cache in between.
		again, err := Config(store)
		if err != nil || again.Cockpit.DoingIdleDays != 3 || again.Cockpit.Sidebar.Variant != "rail" {
			t.Fatalf("re-read = %+v %v", again, err)
		}
	})
	t.Run("an empty patch on no file creates the resolved file", func(t *testing.T) {
		store := newTestStore(t)
		res, err := UpdateSettings(store, UpdateSettingsInput{})
		if err != nil || res.Cockpit.CutoffHour != 18 {
			t.Fatalf("res = %+v %v", res, err)
		}
		if _, err := os.Stat(store.ConfigPath()); err != nil {
			t.Fatal("the file must exist after a save")
		}
	})
	t.Run("bad values are ValidationErrors naming the field, and write nothing", func(t *testing.T) {
		store := newTestStore(t)
		bad := []struct {
			name string
			in   UpdateSettingsInput
			want string
		}{
			{"hour", UpdateSettingsInput{CutoffHour: &[]int{24}[0]}, "cutoff_hour"},
			{"threshold zero", UpdateSettingsInput{StuckProjectDays: &[]int{0}[0]}, "stuck_project_days"},
			{"interval too short", UpdateSettingsInput{Refresh: &RefreshPatch{EverySeconds: &[]int{60}[0]}}, "every_seconds"},
			{"window reversed", UpdateSettingsInput{Refresh: &RefreshPatch{Window: &[]string{"20:00-07:00"}[0]}}, "window"},
			{"window garbage", UpdateSettingsInput{Refresh: &RefreshPatch{Window: &[]string{"morning"}[0]}}, "window"},
			{"section typo", UpdateSettingsInput{Sections: map[string]bool{"needsme": true}}, "sections"},
			{"source typo", UpdateSettingsInput{Sources: map[string]bool{"jira": true}}, "sources"},
			{"variant", UpdateSettingsInput{Sidebar: &SidebarPatch{Variant: &[]string{"huge"}[0]}}, "sidebar.variant"},
			{"sort", UpdateSettingsInput{Sidebar: &SidebarPatch{Sort: &[]string{"random"}[0]}}, "sidebar.sort"},
			{"width", UpdateSettingsInput{Sidebar: &SidebarPatch{Width: &[]int{-1}[0]}}, "sidebar.width"},
			{"group slug", UpdateSettingsInput{Groups: []ConfigGroupPatch{{Slug: "Acme"}}}, "groups"},
			{"group order", UpdateSettingsInput{Groups: []ConfigGroupPatch{{Slug: "acme", Order: &[]int{-2}[0]}}}, "order"},
		}
		for _, tc := range bad {
			_, err := UpdateSettings(store, tc.in)
			var ve *ValidationError
			if err == nil || !errors.As(err, &ve) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: err = %v, want a ValidationError mentioning %q", tc.name, err, tc.want)
			}
		}
		if _, err := os.Stat(store.ConfigPath()); err == nil {
			t.Fatal("a rejected patch must write nothing")
		}
		// Off (0) is legal for the interval.
		if _, err := UpdateSettings(store, UpdateSettingsInput{Refresh: &RefreshPatch{EverySeconds: &[]int{0}[0]}}); err != nil {
			t.Fatalf("every_seconds 0 = off must be legal: %v", err)
		}
	})
}
