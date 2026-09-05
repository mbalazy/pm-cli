package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// Config returns the resolved cockpit block of the global config - what the
// SPA reads to shape itself (sidebar variant, section toggles, thresholds,
// refresh schedule). Resolved means defaults applied: a missing file is the
// default block, never an error. An unparsable file IS an error, the
// ListGroups rule: a sidebar drawn on defaults because the file has a typo
// would present a different setting as the user's.
func Config(store storage.TaskStore) (*ConfigResult, error) {
	cfg, err := store.LoadConfig()
	if err != nil {
		return nil, err
	}
	return cockpitResult(&cfg.Cockpit), nil
}

func cockpitResult(c *storage.CockpitConfig) *ConfigResult {
	// In the manual order (placed groups by `order`, then the rest by slug):
	// the one order a JSON object cannot carry, so the array carries it.
	order := c.GroupOrder()
	groups := make([]ConfigGroup, 0, len(order))
	for _, slug := range order {
		groups = append(groups, ConfigGroup{Slug: slug, Name: c.GroupName(slug), Order: c.Groups[slug].Order})
	}
	sections := make(map[string]bool, len(storage.CockpitSections))
	for _, name := range storage.CockpitSections {
		sections[name] = c.SectionEnabled(name)
	}
	sources := make(map[string]bool, len(storage.CockpitSources))
	for _, name := range storage.CockpitSources {
		sources[name] = c.SourceEnabled(name)
	}
	return &ConfigResult{
		Cockpit: CockpitResult{
			Groups:               groups,
			DoingIdleDays:        c.DoingIdleDays,
			WaitingHighlightDays: c.WaitingHighlightDays,
			StuckProjectDays:     c.StuckProjectDays,
			CutoffHour:           c.CutoffHour,
			Refresh: RefreshResult{
				EverySeconds: int(c.Refresh.Every.Duration().Seconds()),
				Window:       c.Refresh.Window,
			},
			Sections: sections,
			Sources:  sources,
			Sidebar: SidebarResult{
				Variant:   c.Sidebar.Variant,
				ShowRepos: c.Sidebar.ShowRepos,
				Sort:      c.Sidebar.Sort,
				Width:     c.Sidebar.Width,
			},
			Git: GitResult{AllBranches: c.Git.AllBranches},
		},
	}
}

// --- settings write (pm-cli-118-18) ---

// UpdateSettingsInput is a PATCH of the cockpit block: every field is
// optional and an absent one keeps the file's value, so the settings
// screen sends only what changed. Toggle maps merge key by key; `groups`
// merges by slug (an entry sets that group's name and order and leaves the
// others). Nothing here can remove a group entry or a remote - the file is
// still hand-editable for that.
type UpdateSettingsInput struct {
	DoingIdleDays        *int               `json:"doing_idle_days,omitempty"`
	WaitingHighlightDays *int               `json:"waiting_highlight_days,omitempty"`
	StuckProjectDays     *int               `json:"stuck_project_days,omitempty"`
	CutoffHour           *int               `json:"cutoff_hour,omitempty"`
	Refresh              *RefreshPatch      `json:"refresh,omitempty"`
	Sections             map[string]bool    `json:"sections,omitempty"`
	Sources              map[string]bool    `json:"sources,omitempty"`
	Sidebar              *SidebarPatch      `json:"sidebar,omitempty"`
	Groups               []ConfigGroupPatch `json:"groups,omitempty"`
	Git                  *GitPatch          `json:"git,omitempty"`
}

// RefreshPatch patches cockpit.refresh. EverySeconds 0 switches the
// automatic refresh off (a manual one always works).
type RefreshPatch struct {
	EverySeconds *int    `json:"every_seconds,omitempty"`
	Window       *string `json:"window,omitempty"`
}

// SidebarPatch patches cockpit.sidebar.
type SidebarPatch struct {
	Variant   *string `json:"variant,omitempty"`
	ShowRepos *bool   `json:"show_repos,omitempty"`
	Sort      *string `json:"sort,omitempty"`
	Width     *int    `json:"width,omitempty"`
}

// ConfigGroupPatch sets one group's name and order (cockpit.groups.<slug>).
// Name nil keeps the current name; "" clears it (the group displays as its
// slug). Order nil keeps; 0 unplaces.
type ConfigGroupPatch struct {
	Slug  string  `json:"slug"`
	Name  *string `json:"name,omitempty"`
	Order *int    `json:"order,omitempty"`
}

// GitPatch patches cockpit.git.
type GitPatch struct {
	AllBranches *bool `json:"all_branches,omitempty"`
}

// MinRefreshEvery is the shortest automatic refresh the settings screen may
// set (0 = off is still legal): every tick runs git and gh in every checkout,
// and a one-minute interval would make the cockpit the busiest process on
// the machine. Hand-edited files are validated by storage alone (>= 0) - a
// person typing `every: 1m` into a file has read the comment above it.
const MinRefreshEvery = 5 * time.Minute

// UpdateSettings applies the patch to the cockpit block and writes it back
// (comments and unknown keys kept - storage.SaveConfig). Validated BEFORE
// the write, as a *ValidationError naming the field, so a bad form value
// never lands where the next LoadConfig would choke on it: hour 0-23,
// window "HH:MM-HH:MM" ending after it starts, interval 0 or >= 5 min,
// thresholds >= 1, sidebar variant/sort from the closed lists, toggle keys
// from the closed lists, group slugs valid. Returns the resolved block as
// Config does.
func UpdateSettings(store storage.TaskStore, in UpdateSettingsInput) (*ConfigResult, error) {
	if err := validation(validateSettingsPatch(in)); err != nil {
		return nil, err
	}
	cfg, err := store.MutateConfig(func(cfg *storage.PMConfig) error {
		c := &cfg.Cockpit
		if in.DoingIdleDays != nil {
			c.DoingIdleDays = *in.DoingIdleDays
		}
		if in.WaitingHighlightDays != nil {
			c.WaitingHighlightDays = *in.WaitingHighlightDays
		}
		if in.StuckProjectDays != nil {
			c.StuckProjectDays = *in.StuckProjectDays
		}
		if in.CutoffHour != nil {
			c.CutoffHour = *in.CutoffHour
		}
		if in.Refresh != nil {
			if in.Refresh.EverySeconds != nil {
				c.Refresh.Every = storage.Duration(time.Duration(*in.Refresh.EverySeconds) * time.Second)
			}
			if in.Refresh.Window != nil {
				c.Refresh.Window = strings.TrimSpace(*in.Refresh.Window)
			}
		}
		if len(in.Sections) > 0 {
			if c.Sections == nil {
				c.Sections = map[string]bool{}
			}
			for k, v := range in.Sections {
				c.Sections[k] = v
			}
		}
		if len(in.Sources) > 0 {
			if c.Sources == nil {
				c.Sources = map[string]bool{}
			}
			for k, v := range in.Sources {
				c.Sources[k] = v
			}
		}
		if in.Sidebar != nil {
			if in.Sidebar.Variant != nil {
				c.Sidebar.Variant = *in.Sidebar.Variant
			}
			if in.Sidebar.ShowRepos != nil {
				c.Sidebar.ShowRepos = *in.Sidebar.ShowRepos
			}
			if in.Sidebar.Sort != nil {
				c.Sidebar.Sort = *in.Sidebar.Sort
			}
			if in.Sidebar.Width != nil {
				c.Sidebar.Width = *in.Sidebar.Width
			}
		}
		if len(in.Groups) > 0 {
			if c.Groups == nil {
				c.Groups = map[string]storage.GroupConfig{}
			}
			for _, g := range in.Groups {
				entry := c.Groups[g.Slug]
				if g.Name != nil {
					entry.Name = strings.TrimSpace(*g.Name)
				}
				if g.Order != nil {
					entry.Order = *g.Order
				}
				c.Groups[g.Slug] = entry
			}
		}
		if in.Git != nil && in.Git.AllBranches != nil {
			c.Git.AllBranches = *in.Git.AllBranches
		}
		// The storage validation runs again inside SaveConfig; a failure
		// there (a cross-field rule this patch check missed) is a
		// caller's mistake too, not a broken file.
		return nil
	})
	if err != nil {
		// storage's own cockpit validation caught what the patch check
		// could not see alone (it names the key): still the caller's value.
		if strings.Contains(err.Error(), ": cockpit.") {
			return nil, validation(err)
		}
		return nil, err
	}
	return cockpitResult(&cfg.Cockpit), nil
}

// validateSettingsPatch checks the patch's own values (the file-level rules
// storage enforces, plus the screen's stricter minimums) and names the
// field in every message so the form can point at it.
func validateSettingsPatch(in UpdateSettingsInput) error {
	for _, f := range []struct {
		key string
		val *int
	}{
		{"doing_idle_days", in.DoingIdleDays},
		{"waiting_highlight_days", in.WaitingHighlightDays},
		{"stuck_project_days", in.StuckProjectDays},
	} {
		if f.val != nil && *f.val < 1 {
			return fmt.Errorf("%s must be >= 1 day, got %d", f.key, *f.val)
		}
	}
	if in.CutoffHour != nil && (*in.CutoffHour < 0 || *in.CutoffHour > 23) {
		return fmt.Errorf("cutoff_hour must be an hour 0-23, got %d", *in.CutoffHour)
	}
	if in.Refresh != nil {
		if e := in.Refresh.EverySeconds; e != nil && *e != 0 && time.Duration(*e)*time.Second < MinRefreshEvery {
			return fmt.Errorf("refresh.every_seconds must be 0 (off) or >= %d (%s), got %d", int(MinRefreshEvery.Seconds()), MinRefreshEvery, *e)
		}
		if in.Refresh.Window != nil {
			r := storage.RefreshConfig{Window: strings.TrimSpace(*in.Refresh.Window)}
			if _, _, err := r.WindowBounds(); err != nil {
				return fmt.Errorf("refresh.window: %w", err)
			}
		}
	}
	if err := knownToggleKeys("sections", in.Sections, storage.CockpitSections); err != nil {
		return err
	}
	if err := knownToggleKeys("sources", in.Sources, storage.CockpitSources); err != nil {
		return err
	}
	if in.Sidebar != nil {
		if v := in.Sidebar.Variant; v != nil && !containsString(storage.SidebarVariants, *v) {
			return fmt.Errorf("sidebar.variant %q is not one of %s", *v, strings.Join(storage.SidebarVariants, ", "))
		}
		if v := in.Sidebar.Sort; v != nil && !containsString(storage.SidebarSorts, *v) {
			return fmt.Errorf("sidebar.sort %q is not one of %s", *v, strings.Join(storage.SidebarSorts, ", "))
		}
		if w := in.Sidebar.Width; w != nil && *w < 0 {
			return fmt.Errorf("sidebar.width must be >= 0, got %d", *w)
		}
	}
	for _, g := range in.Groups {
		if err := storage.ValidateSlug(g.Slug); err != nil {
			return fmt.Errorf("groups: %w", err)
		}
		if g.Order != nil && *g.Order < 0 {
			return fmt.Errorf("groups.%s.order must be >= 0, got %d", g.Slug, *g.Order)
		}
	}
	return nil
}

func knownToggleKeys(field string, m map[string]bool, known []string) error {
	for k := range m {
		if !containsString(known, k) {
			return fmt.Errorf("%s: unknown %s (known: %s)", field, k, strings.Join(known, ", "))
		}
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
