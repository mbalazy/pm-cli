package storage

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The `cockpit:` block of the global config (<pm-root>/config.yaml): every
// setting the web cockpit (pm serve + the React SPA, pm-cli-118) reads, in
// ONE place next to `remotes`. Global and not per project for the same reason
// the remotes are: a cutoff hour, a refresh window or a section toggle is a
// fact about how the user works, not about one repo, and a copy per project
// is the copy that drifts.
//
// This file owns the TYPE, the DEFAULTS, the PARSER and the VALIDATION. The
// MEANING of each field belongs to the code that consumes it - the attention
// aggregation reads the thresholds and the section toggles, the change feed
// reads the cutoff, the refresh window and the source toggles, the SPA reads
// the sidebar block. Nothing here interprets a section name beyond checking
// it is one pm knows: a typo'd `sections:` key would otherwise switch off
// nothing and switch on nothing, silently, and the closed list is what makes
// the typo visible (the ValidateJournalName argument, again).
//
// DEFAULTS ARE APPLIED BEFORE DECODING: LoadConfig unmarshals the file OVER a
// DefaultCockpitConfig, so an absent key keeps its default and a present key
// - including an explicit zero such as `cutoff_hour: 0`, midnight - wins.
// This is what lets an int field carry a real zero without pointer types.

// CockpitConfig is the resolved `cockpit:` block.
type CockpitConfig struct {
	// Groups names the project groups (Project.Group). The key is the group
	// slug; a group with no entry displays as its slug. Membership is NOT
	// declared here - each project.yaml names its group, so a group is
	// derived from its members (Store.ProjectGroups) and never lists them
	// twice.
	Groups map[string]GroupConfig `yaml:"groups,omitempty"`

	// Thresholds, in days. DoingIdleDays: a `doing` task with no activity for
	// this long counts as idle. WaitingHighlightDays: a `waiting` task older
	// than this is highlighted. StuckProjectDays: a project with no task change
	// for this long counts as stuck.
	DoingIdleDays        int `yaml:"doing_idle_days"`
	WaitingHighlightDays int `yaml:"waiting_highlight_days"`
	StuckProjectDays     int `yaml:"stuck_project_days"`

	// CutoffHour is the local-time hour (0-23) the "since yesterday" window
	// starts at: before that hour today, the window opens yesterday at
	// CutoffHour; from that hour on, today at CutoffHour.
	CutoffHour int `yaml:"cutoff_hour"`

	// Refresh drives the change feed's scheduler.
	Refresh RefreshConfig `yaml:"refresh"`

	// Sections switches the home screen's sections on and off. Every key is
	// one of CockpitSections; an absent key keeps its default.
	Sections map[string]bool `yaml:"sections,omitempty"`

	// Sources switches the change feed's sources on and off. Every key is one
	// of CockpitSources; an absent key keeps its default.
	Sources map[string]bool `yaml:"sources,omitempty"`

	// Sidebar shapes the SPA's project sidebar.
	Sidebar SidebarConfig `yaml:"sidebar"`

	// Git tunes the change feed's git source.
	Git GitConfig `yaml:"git"`

	// Report tunes the LLM report over the feed (pm-cli-118-20); the
	// switch itself is Sources["report"].
	Report ReportConfig `yaml:"report"`
}

// ReportConfig tunes the LLM report: which model writes it and in what
// language. Off by default (Sources["report"]) because it costs tokens.
type ReportConfig struct {
	// Model is the claude model alias or name; a summary, not reasoning,
	// so the default is the cheap one.
	Model string `yaml:"model"`
	// Language is the prose language of the report ("pl", "en", ...).
	Language string `yaml:"language"`
}

// GroupConfig is one entry of CockpitConfig.Groups.
type GroupConfig struct {
	// Name is the display name; empty displays the slug.
	Name string `yaml:"name,omitempty"`
	// Order is the group's place in the sidebar's `manual` sort: lower first,
	// 0 = unplaced (after every placed group, by slug). It lives here and not
	// in the map's key order because a YAML map's order is not a fact the
	// JSON on the wire can carry - the settings screen needs a number.
	Order int `yaml:"order,omitempty"`
}

// GitConfig tunes the feed's git source.
type GitConfig struct {
	// AllBranches widens the commit scan from the branches pm tasks name to
	// every ref of the checkout. Off by default: the v1 scope is "branches
	// pinned to tasks + open PRs", not the whole repo's activity.
	AllBranches bool `yaml:"all_branches"`
}

// RefreshConfig is the change feed's schedule.
type RefreshConfig struct {
	// Every is the tick interval inside the window.
	Every Duration `yaml:"every"`
	// Window is "HH:MM-HH:MM", local time; outside it nothing refreshes on
	// its own (a manual refresh always works). See WindowBounds.
	Window string `yaml:"window"`
}

// SidebarConfig shapes the SPA's sidebar.
type SidebarConfig struct {
	// Variant is one of SidebarVariants.
	Variant string `yaml:"variant"`
	// ShowRepos lists a group's member repos under the group.
	ShowRepos bool `yaml:"show_repos"`
	// Sort is one of SidebarSorts.
	Sort string `yaml:"sort"`
	// Width is the sidebar width in px; 0 = the SPA's own default.
	Width int `yaml:"width"`
}

// CockpitSections are the home screen's sections, in display order. The
// first seven are on by default; the last two are opt-in.
var CockpitSections = []string{
	"needs_me", "landed_no_pr", "focus", "in_progress", "waiting", "changes",
	"stuck_projects", "new_since_cutoff", "recent",
}

// CockpitSources are the change feed's sources. pm reads pm's own files; git
// and github run `git`/`gh` in the project's checkout; slack and report are
// integrations that ship later and default to off.
var CockpitSources = []string{"pm", "git", "github", "slack", "report"}

// SidebarVariants are the legal SidebarConfig.Variant values.
var SidebarVariants = []string{"columns", "plain", "rail"}

// SidebarSorts are the legal SidebarConfig.Sort values: by the group's worst
// state, by its last activity, or in the order the config lists the groups.
var SidebarSorts = []string{"worst", "last_activity", "manual"}

// DefaultCockpitConfig is the configuration with no file at all - the v1
// thresholds the user chose on 2026-09-05 ("we will change them over time",
// hence all in the file and none in the code that reads them).
func DefaultCockpitConfig() CockpitConfig {
	sections := make(map[string]bool, len(CockpitSections))
	for i, name := range CockpitSections {
		sections[name] = i < 7
	}
	return CockpitConfig{
		DoingIdleDays:        7,
		WaitingHighlightDays: 5,
		StuckProjectDays:     14,
		CutoffHour:           18,
		Refresh: RefreshConfig{
			Every:  Duration(30 * time.Minute),
			Window: "07:00-20:00",
		},
		Sections: sections,
		Sources:  map[string]bool{"pm": true, "git": true, "github": true, "slack": false, "report": false},
		Sidebar: SidebarConfig{
			Variant:   "columns",
			ShowRepos: true,
			Sort:      "worst",
		},
		Report: ReportConfig{Model: "haiku", Language: "pl"},
	}
}

// GroupOrder returns the configured group slugs in the sidebar's manual
// order: placed groups (Order > 0) by Order then slug, then the unplaced by
// slug. A group with no entry at all is not listed - the caller appends
// those in whatever order it has them.
func (c *CockpitConfig) GroupOrder() []string {
	if c == nil {
		return nil
	}
	slugs := make([]string, 0, len(c.Groups))
	for slug := range c.Groups {
		slugs = append(slugs, slug)
	}
	sort.Slice(slugs, func(i, j int) bool {
		a, b := c.Groups[slugs[i]].Order, c.Groups[slugs[j]].Order
		if a != b {
			if a == 0 {
				return false
			}
			if b == 0 {
				return true
			}
			return a < b
		}
		return slugs[i] < slugs[j]
	})
	return slugs
}

// GroupName returns the display name of a group: the configured name, or the
// slug when the group has no entry or an entry with no name.
func (c *CockpitConfig) GroupName(slug string) string {
	if c != nil {
		if g, ok := c.Groups[slug]; ok && g.Name != "" {
			return g.Name
		}
	}
	return slug
}

// SectionEnabled answers whether a home section is on. An unknown name is
// off - validate rejects it at load, so this only covers a caller's typo.
func (c *CockpitConfig) SectionEnabled(name string) bool {
	return c != nil && c.Sections[name]
}

// SourceEnabled answers whether a feed source is on.
func (c *CockpitConfig) SourceEnabled(name string) bool {
	return c != nil && c.Sources[name]
}

// validate rejects a block that cannot be acted on. Every message names the
// key, for the same reason PMConfig.validate names the remote: the point of
// failing loudly is that the user can find the line.
func (c *CockpitConfig) validate(where string) error {
	for slug, g := range c.Groups {
		if err := ValidateSlug(slug); err != nil {
			return fmt.Errorf("%s: cockpit.groups: %w", where, err)
		}
		if g.Order < 0 {
			return fmt.Errorf("%s: cockpit.groups.%s.order is %d, must be >= 0", where, slug, g.Order)
		}
	}
	for _, f := range []struct {
		key string
		val int
	}{
		{"doing_idle_days", c.DoingIdleDays},
		{"waiting_highlight_days", c.WaitingHighlightDays},
		{"stuck_project_days", c.StuckProjectDays},
	} {
		if f.val < 0 {
			return fmt.Errorf("%s: cockpit.%s is %d, must be >= 0", where, f.key, f.val)
		}
	}
	if c.CutoffHour < 0 || c.CutoffHour > 23 {
		return fmt.Errorf("%s: cockpit.cutoff_hour is %d, must be an hour 0-23", where, c.CutoffHour)
	}
	if c.Refresh.Every < 0 {
		return fmt.Errorf("%s: cockpit.refresh.every is %s, must not be negative", where, c.Refresh.Every)
	}
	if _, _, err := c.Refresh.WindowBounds(); err != nil {
		return fmt.Errorf("%s: cockpit.refresh.window: %w", where, err)
	}
	if err := knownKeys(c.Sections, CockpitSections); err != nil {
		return fmt.Errorf("%s: cockpit.sections: %w", where, err)
	}
	if err := knownKeys(c.Sources, CockpitSources); err != nil {
		return fmt.Errorf("%s: cockpit.sources: %w", where, err)
	}
	if !contains(SidebarVariants, c.Sidebar.Variant) {
		return fmt.Errorf("%s: cockpit.sidebar.variant %q is not one of %s", where, c.Sidebar.Variant, strings.Join(SidebarVariants, ", "))
	}
	if !contains(SidebarSorts, c.Sidebar.Sort) {
		return fmt.Errorf("%s: cockpit.sidebar.sort %q is not one of %s", where, c.Sidebar.Sort, strings.Join(SidebarSorts, ", "))
	}
	if c.Sidebar.Width < 0 {
		return fmt.Errorf("%s: cockpit.sidebar.width is %d, must be >= 0", where, c.Sidebar.Width)
	}
	if strings.TrimSpace(c.Report.Model) == "" {
		return fmt.Errorf("%s: cockpit.report.model is empty", where)
	}
	if strings.ContainsAny(c.Report.Model, " \t\n") {
		return fmt.Errorf("%s: cockpit.report.model %q must be one word (a claude model alias or name)", where, c.Report.Model)
	}
	return nil
}

// knownKeys rejects a toggle map key that is not in the closed list, naming
// the list so the typo is a one-line fix.
func knownKeys(m map[string]bool, known []string) error {
	var bad []string
	for k := range m {
		if !contains(known, k) {
			bad = append(bad, k)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("unknown %s (known: %s)", strings.Join(bad, ", "), strings.Join(known, ", "))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// WindowBounds parses Window ("HH:MM-HH:MM") into minutes since midnight.
// A window that ends before it starts is rejected rather than read as
// wrapping past midnight - the feed is a daytime thing, and a reversed pair
// is far more likely a typo than an overnight schedule.
func (r RefreshConfig) WindowBounds() (start, end int, err error) {
	parts := strings.SplitN(r.Window, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%q is not HH:MM-HH:MM", r.Window)
	}
	if start, err = parseClock(strings.TrimSpace(parts[0])); err != nil {
		return 0, 0, fmt.Errorf("%q: %w", r.Window, err)
	}
	if end, err = parseClock(strings.TrimSpace(parts[1])); err != nil {
		return 0, 0, fmt.Errorf("%q: %w", r.Window, err)
	}
	if end <= start {
		return 0, 0, fmt.Errorf("%q ends before it starts", r.Window)
	}
	return start, end, nil
}

// InWindow reports whether a local wall-clock instant falls inside the
// refresh window (start inclusive, end exclusive). The clock is a parameter
// so a scheduler test never waits for 07:00.
func (r RefreshConfig) InWindow(now time.Time) bool {
	start, end, err := r.WindowBounds()
	if err != nil {
		return false
	}
	m := now.Hour()*60 + now.Minute()
	return m >= start && m < end
}

func parseClock(s string) (int, error) {
	hm := strings.SplitN(s, ":", 2)
	if len(hm) != 2 {
		return 0, fmt.Errorf("%q is not HH:MM", s)
	}
	h, err := strconv.Atoi(hm[0])
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("%q: hour must be 0-23", s)
	}
	m, err := strconv.Atoi(hm[1])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("%q: minute must be 0-59", s)
	}
	return h*60 + m, nil
}
