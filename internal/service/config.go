package service

import (
	"sort"

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
	groups := make([]ConfigGroup, 0, len(c.Groups))
	for slug := range c.Groups {
		groups = append(groups, ConfigGroup{Slug: slug, Name: c.GroupName(slug)})
	}
	// A YAML map has no order, so "manual" sidebar order cannot come from
	// here; sorted by slug so the wire bytes are stable between calls.
	sort.Slice(groups, func(i, j int) bool { return groups[i].Slug < groups[j].Slug })
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
		},
	}
}
