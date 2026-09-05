package storage

import "sort"

// ProjectGroup is one group of projects as the cockpit shows it: the slug
// every member's project.yaml names (or the member's own slug for a project
// that names none), the display name from the global config, and the member
// slugs. Derived on every read from the members - there is no membership
// list to keep in step with the projects.
type ProjectGroup struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	// Projects are the ACTIVE member slugs, sorted. An archived project is
	// asleep and belongs to no group, so a group whose every member sleeps
	// is not listed at all.
	Projects []string `json:"projects"`
}

// ProjectGroups lists the groups of the active projects, sorted by slug.
//
// The config is loaded for the display names, so an unparsable config.yaml
// is an error here too - the same loud failure LoadConfig promises, never a
// list with every group named by its slug as if no names had been set.
// A project whose project.yaml does not read counts as active and grouped
// by itself, as ListActiveProjects treats it.
func (s *Store) ProjectGroups() ([]ProjectGroup, error) {
	cfg, err := s.LoadConfig()
	if err != nil {
		return nil, err
	}
	slugs, err := s.ListProjects()
	if err != nil {
		return nil, err
	}
	members := make(map[string][]string)
	for _, slug := range slugs {
		proj, err := s.GetProject(slug)
		if err == nil && proj.Archived {
			continue
		}
		g := proj.GroupSlug(slug)
		members[g] = append(members[g], slug)
	}
	out := make([]ProjectGroup, 0, len(members))
	for g, ps := range members {
		sort.Strings(ps)
		out = append(out, ProjectGroup{Slug: g, Name: cfg.Cockpit.GroupName(g), Projects: ps})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}
