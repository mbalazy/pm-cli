package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCockpitDefaultsWithoutFileOrBlock(t *testing.T) {
	want := DefaultCockpitConfig()
	check := func(t *testing.T, got CockpitConfig) {
		t.Helper()
		if got.DoingIdleDays != 7 || got.WaitingHighlightDays != 5 || got.StuckProjectDays != 14 || got.CutoffHour != 18 {
			t.Errorf("thresholds = %+v", got)
		}
		if got.Refresh.Every.Duration() != 30*time.Minute || got.Refresh.Window != "07:00-20:00" {
			t.Errorf("refresh = %+v", got.Refresh)
		}
		if got.Sidebar.Variant != "columns" || !got.Sidebar.ShowRepos || got.Sidebar.Sort != "worst" {
			t.Errorf("sidebar = %+v", got.Sidebar)
		}
		for _, name := range CockpitSections {
			want := name != "new_since_cutoff" && name != "recent"
			if got.SectionEnabled(name) != want {
				t.Errorf("section %s enabled = %v, want %v", name, got.SectionEnabled(name), want)
			}
		}
		if got.ShowExecutor {
			t.Error("show_executor must default to off")
		}
		if !got.SourceEnabled("pm") || got.SourceEnabled("slack") || got.SourceEnabled("nosuch") {
			t.Errorf("sources = %v", got.Sources)
		}
		if len(got.Groups) != 0 || got.GroupName("acme") != "acme" {
			t.Errorf("groups = %v", got.Groups)
		}
		if len(got.Sections) != len(want.Sections) {
			t.Errorf("sections = %v", got.Sections)
		}
	}

	t.Run("no file", func(t *testing.T) {
		store := &Store{Root: t.TempDir()}
		cfg, err := store.LoadConfig()
		if err != nil {
			t.Fatal(err)
		}
		check(t, cfg.Cockpit)
	})

	t.Run("file without the block", func(t *testing.T) {
		store := &Store{Root: t.TempDir()}
		writeConfig(t, store, sampleConfig)
		cfg, err := store.LoadConfig()
		if err != nil {
			t.Fatal(err)
		}
		check(t, cfg.Cockpit)
		if len(cfg.Remotes) != 1 {
			t.Errorf("remotes lost: %+v", cfg.Remotes)
		}
	})
}

// A present key wins, an absent key keeps its default - including an explicit
// zero, which is a legal cutoff hour (midnight) and must not be read as unset.
func TestCockpitDecodesOverDefaults(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	writeConfig(t, store, `cockpit:
  groups:
    acme: {name: "Acme"}
    orbit: {}
  cutoff_hour: 0
  waiting_highlight_days: 9
  refresh:
    every: 1h
  sections:
    focus: false
    recent: true
  sources:
    github: false
  sidebar:
    show_repos: false
    width: 300
`)
	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Cockpit
	if c.CutoffHour != 0 {
		t.Errorf("cutoff_hour = %d, want explicit 0", c.CutoffHour)
	}
	if c.WaitingHighlightDays != 9 || c.DoingIdleDays != 7 || c.StuckProjectDays != 14 {
		t.Errorf("thresholds = %+v", c)
	}
	if c.Refresh.Every.Duration() != time.Hour || c.Refresh.Window != "07:00-20:00" {
		t.Errorf("refresh = %+v (window must keep its default)", c.Refresh)
	}
	if c.SectionEnabled("focus") || !c.SectionEnabled("recent") || !c.SectionEnabled("needs_me") || c.SectionEnabled("new_since_cutoff") {
		t.Errorf("sections = %v", c.Sections)
	}
	if c.SourceEnabled("github") || !c.SourceEnabled("pm") || !c.SourceEnabled("git") {
		t.Errorf("sources = %v", c.Sources)
	}
	if c.Sidebar.ShowRepos || c.Sidebar.Width != 300 || c.Sidebar.Variant != "columns" {
		t.Errorf("sidebar = %+v", c.Sidebar)
	}
	if c.GroupName("acme") != "Acme" || c.GroupName("atlas") != "atlas" || c.GroupName("other") != "other" {
		t.Errorf("group names: acme=%q orbit=%q", c.GroupName("acme"), c.GroupName("atlas"))
	}
}

func TestCockpitRejectsBadValues(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"unknown section", "cockpit:\n  sections:\n    focsu: true\n", []string{"cockpit.sections", "focsu", "needs_me"}},
		{"unknown source", "cockpit:\n  sources:\n    jira: true\n", []string{"cockpit.sources", "jira"}},
		{"cutoff hour out of range", "cockpit:\n  cutoff_hour: 24\n", []string{"cockpit.cutoff_hour", "0-23"}},
		{"negative threshold", "cockpit:\n  doing_idle_days: -1\n", []string{"cockpit.doing_idle_days"}},
		{"window not a range", "cockpit:\n  refresh:\n    window: 9-17\n", []string{"cockpit.refresh.window"}},
		{"window reversed", "cockpit:\n  refresh:\n    window: 20:00-07:00\n", []string{"ends before it starts"}},
		{"bare number duration", "cockpit:\n  refresh:\n    every: 30\n", []string{"duration"}},
		{"bad sidebar variant", "cockpit:\n  sidebar:\n    variant: wide\n", []string{"cockpit.sidebar.variant", "columns, plain, rail"}},
		{"bad sidebar sort", "cockpit:\n  sidebar:\n    sort: name\n", []string{"cockpit.sidebar.sort"}},
		{"group slug not a slug", "cockpit:\n  groups:\n    Acme: {name: x}\n", []string{"cockpit.groups", "Acme"}},
		{"unparsable block", "cockpit:\n  sections:\n   - focus\n", []string{"parse"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &Store{Root: t.TempDir()}
			writeConfig(t, store, tc.body)
			_, err := store.LoadConfig()
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
			if !strings.Contains(err.Error(), store.ConfigPath()) {
				t.Errorf("error %q does not name the file", err)
			}
		})
	}
}

func TestRefreshWindow(t *testing.T) {
	r := RefreshConfig{Window: "07:00-20:00"}
	start, end, err := r.WindowBounds()
	if err != nil || start != 7*60 || end != 20*60 {
		t.Fatalf("bounds = %d,%d,%v", start, end, err)
	}
	day := func(h, m int) time.Time { return time.Date(2026, 9, 5, h, m, 0, 0, time.Local) }
	cases := map[time.Time]bool{
		day(6, 59):  false,
		day(7, 0):   true,
		day(13, 30): true,
		day(19, 59): true,
		day(20, 0):  false,
		day(23, 0):  false,
	}
	for at, want := range cases {
		if got := r.InWindow(at); got != want {
			t.Errorf("InWindow(%s) = %v, want %v", at.Format("15:04"), got, want)
		}
	}
	if (RefreshConfig{Window: "junk"}).InWindow(day(12, 0)) {
		t.Error("an unparsable window must never be inside")
	}
}

// SaveConfig keeps comments and unknown keys, writes the resolved cockpit
// block, and what it wrote reads back as the same config.
func TestSaveConfigPreservesHandTunedYAML(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	writeConfig(t, store, `# Global pm configuration - remote runners.
remotes:
  - name: runner
    # the VPS
    ssh: runner
    pm: /home/runner/go/bin/pm
    root: /home/runner/.claude/pm
my_future_key: keep-me
cockpit:
  # night owl
  cutoff_hour: 20
`)
	cfg, err := store.MutateConfig(func(c *PMConfig) error {
		c.Cockpit.Groups = map[string]GroupConfig{"acme": {Name: "Acme"}}
		c.Cockpit.Sections["recent"] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Exists {
		t.Error("Exists must be true after a save")
	}

	raw, _ := os.ReadFile(store.ConfigPath())
	text := string(raw)
	for _, want := range []string{
		"# Global pm configuration - remote runners.",
		"# the VPS",
		"my_future_key: keep-me",
		"# night owl",
		"cutoff_hour: 20",
		"name: Acme",
		"every: 30m0s",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("written file lacks %q:\n%s", want, text)
		}
	}

	again, err := store.LoadConfig()
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if again.Cockpit.CutoffHour != 20 || again.Cockpit.GroupName("acme") != "Acme" || !again.Cockpit.SectionEnabled("recent") || again.Cockpit.SectionEnabled("new_since_cutoff") {
		t.Errorf("re-read = %+v", again.Cockpit)
	}
	if len(again.Remotes) != 1 || again.Remotes[0].SSH != "runner" {
		t.Errorf("remotes = %+v", again.Remotes)
	}

	// A second, unchanged save is byte-identical - the merge keeps the old
	// nodes whenever the decoded value is unchanged.
	if err := store.SaveConfig(again); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(store.ConfigPath())
	if string(raw2) != text {
		t.Errorf("no-op save changed the file:\n--- before\n%s\n--- after\n%s", text, raw2)
	}
}

func TestSaveConfigCreatesAndValidates(t *testing.T) {
	store := &Store{Root: t.TempDir()}

	t.Run("creates a missing file", func(t *testing.T) {
		cfg := &PMConfig{Cockpit: DefaultCockpitConfig()}
		if err := store.SaveConfig(cfg); err != nil {
			t.Fatal(err)
		}
		again, err := store.LoadConfig()
		if err != nil || !again.Exists || again.Cockpit.CutoffHour != 18 {
			t.Fatalf("re-read = %+v, %v", again, err)
		}
	})

	t.Run("refuses a bad value before writing", func(t *testing.T) {
		before, _ := os.ReadFile(store.ConfigPath())
		_, err := store.MutateConfig(func(c *PMConfig) error {
			c.Cockpit.Sidebar.Variant = "huge"
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "cockpit.sidebar.variant") {
			t.Fatalf("err = %v", err)
		}
		after, _ := os.ReadFile(store.ConfigPath())
		if string(before) != string(after) {
			t.Error("a rejected save touched the file")
		}
	})
}

func TestGroupOrderAndSlackMapping(t *testing.T) {
	t.Run("placed groups by order then slug, unplaced after by slug", func(t *testing.T) {
		c := CockpitConfig{Groups: map[string]GroupConfig{
			"zeta": {Order: 1}, "alpha": {}, "mid": {Order: 2}, "beta": {}, "also1": {Order: 1},
		}}
		got := strings.Join(c.GroupOrder(), " ")
		if got != "also1 zeta mid alpha beta" {
			t.Fatalf("GroupOrder = %q", got)
		}
		if (&CockpitConfig{}).GroupOrder() != nil && len((&CockpitConfig{}).GroupOrder()) != 0 {
			t.Fatal("no groups = empty order")
		}
	})
	t.Run("a negative order is rejected at load", func(t *testing.T) {
		store := &Store{Root: t.TempDir()}
		if err := os.WriteFile(store.ConfigPath(), []byte("cockpit:\n  groups:\n    acme: {order: -1}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := store.LoadConfig()
		if err == nil || !strings.Contains(err.Error(), "cockpit.groups.acme.order") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("git.all_branches decodes over its default and round-trips", func(t *testing.T) {
		store := &Store{Root: t.TempDir()}
		cfg, err := store.LoadConfig()
		if err != nil || cfg.Cockpit.Git.AllBranches {
			t.Fatalf("default all_branches must be off: %v %v", cfg.Cockpit.Git, err)
		}
		if _, err := store.MutateConfig(func(c *PMConfig) error { c.Cockpit.Git.AllBranches = true; return nil }); err != nil {
			t.Fatal(err)
		}
		cfg, err = store.LoadConfig()
		if err != nil || !cfg.Cockpit.Git.AllBranches {
			t.Fatalf("all_branches lost on the round trip: %v %v", cfg.Cockpit.Git, err)
		}
	})
	t.Run("project slack mapping round-trips and survives a comment", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "project.yaml")
		if err := os.WriteFile(path, []byte("name: P\n# keep me\nnotes: hi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		p := &Project{Name: "P", Notes: "hi", Slack: &SlackConfig{Workspace: "acme", Channels: []string{"#acme-api-dev"}}}
		if err := WriteProject(path, p); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(path)
		if !strings.Contains(string(raw), "# keep me") || !strings.Contains(string(raw), "workspace: acme") {
			t.Fatalf("written:\n%s", raw)
		}
		got, err := ReadProject(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.Slack == nil || got.Slack.Workspace != "acme" || len(got.Slack.Channels) != 1 || got.Slack.Channels[0] != "#acme-api-dev" {
			t.Fatalf("slack = %+v", got.Slack)
		}
	})
}

// TestCockpitNullToggleMapsKeepDefaults: `sections:` / `sources:` present
// with a null value (children commented out) is the default, not every
// switch off.
func TestCockpitNullToggleMapsKeepDefaults(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	writeConfig(t, store, "cockpit:\n  sections:\n  sources:\n")
	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	def := DefaultCockpitConfig()
	for name, on := range def.Sections {
		if cfg.Cockpit.SectionEnabled(name) != on {
			t.Errorf("section %s = %v, want the default %v", name, cfg.Cockpit.SectionEnabled(name), on)
		}
	}
	for name, on := range def.Sources {
		if cfg.Cockpit.SourceEnabled(name) != on {
			t.Errorf("source %s = %v, want the default %v", name, cfg.Cockpit.SourceEnabled(name), on)
		}
	}
}
