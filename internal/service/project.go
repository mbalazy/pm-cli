package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// CreateProjectInput is the argument set of pm_create_project.
type CreateProjectInput struct {
	Slug     string            `json:"slug" jsonschema:"Project slug (directory name, lowercase, no spaces)"`
	Name     string            `json:"name,omitempty" jsonschema:"Project display name (defaults to slug)"`
	Path     string            `json:"path,omitempty" jsonschema:"Local filesystem path"`
	Repo     string            `json:"repo,omitempty" jsonschema:"Repository URL"`
	Stack    string            `json:"stack,omitempty" jsonschema:"Tech stack description"`
	Notes    string            `json:"notes,omitempty" jsonschema:"Project notes"`
	Prefix   string            `json:"prefix,omitempty" jsonschema:"Task ID prefix (defaults to slug)"`
	Links    map[string]string `json:"links,omitempty" jsonschema:"Links as key=url pairs"`
	Tags     []string          `json:"tags,omitempty" jsonschema:"Tags"`
	Statuses []string          `json:"statuses,omitempty" jsonschema:"Custom statuses (default: todo, doing, waiting, done)"`
	Group    string            `json:"group,omitempty" jsonschema:"Cockpit group slug - several repos that are one product/client share it (e.g. acme); empty = the project is its own group"`
}

// UpdateProjectInput is the argument set of pm_update_project.
type UpdateProjectInput struct {
	Project  string            `json:"project" jsonschema:"Project slug or prefix"`
	Name     string            `json:"name,omitempty" jsonschema:"Project display name"`
	Path     string            `json:"path,omitempty" jsonschema:"Local filesystem path"`
	Repo     string            `json:"repo,omitempty" jsonschema:"Repository URL"`
	Stack    string            `json:"stack,omitempty" jsonschema:"Tech stack description"`
	Notes    string            `json:"notes,omitempty" jsonschema:"Project notes"`
	Prefix   string            `json:"prefix,omitempty" jsonschema:"Task ID prefix"`
	Links    map[string]string `json:"links,omitempty" jsonschema:"Links to merge (existing links are preserved)"`
	Tags     []string          `json:"tags,omitempty" jsonschema:"Replace tags (omit to keep current)"`
	Statuses []string          `json:"statuses,omitempty" jsonschema:"Replace statuses (omit to keep current)"`
	Archived *bool             `json:"archived,omitempty" jsonschema:"Archive or unarchive the project"`
	Group    *string           `json:"group,omitempty" jsonschema:"Set the cockpit group slug (several repos that are one product/client share it). Omit to keep current; pass an empty string to leave the group"`
	Slack    *SlackMapping     `json:"slack,omitempty" jsonschema:"Replace the project's Slack mapping for the cockpit's change feed (workspace label + channel names). Omit to keep current; an empty workspace with no channels removes the mapping"`
}

// ListProjects lists every project with per-status task counts. A project
// whose project.yaml or task dir fails to read is skipped and named in the
// result's note instead of silently vanishing.
func ListProjects(store storage.TaskStore) (*ListProjectsResult, error) {
	projects, err := store.ListProjects()
	if err != nil {
		return nil, err
	}

	var warnings []string
	// Group display names come from the global config. This listing already
	// skips-and-names a broken project rather than failing, so a broken
	// config gets the same treatment: named in the note, names fall back
	// to slugs. ListGroups (the cockpit's own read) stays loud.
	cfg, err := store.LoadConfig()
	if err != nil {
		warnings = append(warnings, err.Error())
		cfg = &storage.PMConfig{Cockpit: storage.DefaultCockpitConfig()}
	}
	result := []ProjectInfo{}
	for _, slug := range projects {
		proj, err := store.GetProject(slug)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("project %q: failed to read project.yaml: %v", slug, err))
			continue
		}
		group := proj.GroupSlug(slug)
		pi := ProjectInfo{
			Slug:       slug,
			Name:       proj.Name,
			Stack:      proj.Stack,
			Archived:   proj.Archived,
			Group:      group,
			GroupName:  cfg.Cockpit.GroupName(group),
			TaskCounts: make(map[string]int),
		}
		tasks, err := store.GetTasks(slug)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("project %q: failed to read tasks: %v", slug, err))
			continue
		}
		for _, t := range tasks {
			pi.TaskCounts[string(t.Meta.Status)]++
		}
		result = append(result, pi)
	}
	return &ListProjectsResult{Projects: result, Note: strings.Join(warnings, "; ")}, nil
}

// ListGroups lists the cockpit's project groups (storage.ProjectGroups):
// active projects only, names from the global config. Unlike ListProjects
// this fails on an unreadable config - the sidebar must never show a group
// under the wrong name as if that were the setting.
func ListGroups(store storage.TaskStore) (*ListGroupsResult, error) {
	groups, err := store.ProjectGroups()
	if err != nil {
		return nil, err
	}
	if groups == nil {
		groups = []storage.ProjectGroup{}
	}
	return &ListGroupsResult{Groups: groups}, nil
}

// CreateProject creates the project directory and project.yaml. An empty,
// unsafe or already-taken slug (case-insensitively), or an unsafe group, is
// a *ValidationError.
func CreateProject(store storage.TaskStore, in CreateProjectInput) (*ProjectResult, error) {
	if in.Slug == "" {
		return nil, validation(fmt.Errorf("slug is required"))
	}
	// The slug becomes a directory name via filepath.Join(ProjectDir/
	// ProjectYAML) - reject anything that could escape the pm root
	// ("../foo") or isn't canonical (uppercase, spaces).
	if err := validation(storage.ValidateSlug(in.Slug)); err != nil {
		return nil, err
	}
	if err := validation(storage.ValidateGroup(in.Group)); err != nil {
		return nil, err
	}

	// Check if project already exists. Case-insensitive: on the default
	// case-insensitive-but-preserving macOS filesystem, ProjectDir("foo")
	// and ProjectDir("Foo") are the SAME directory - an exact-case-only
	// check would miss the collision, MkdirAll would silently succeed
	// against the existing dir, and writeProject's merge-into-existing-file
	// logic would splice this project's fields into the other one's
	// project.yaml. ValidateSlug already forces new slugs to be lowercase,
	// but an existing slug (e.g. CLI-created, which has no slug validation)
	// may not be.
	existing, _ := store.ListProjects()
	for _, s := range existing {
		if strings.EqualFold(s, in.Slug) {
			return nil, validation(fmt.Errorf("project %q already exists (case-insensitive match with %q - this filesystem does not distinguish case)", in.Slug, s))
		}
	}

	name := in.Slug
	if in.Name != "" {
		name = in.Name
	}

	p := &storage.Project{
		Name:     name,
		Prefix:   in.Prefix,
		Path:     in.Path,
		Repo:     in.Repo,
		Stack:    in.Stack,
		Notes:    in.Notes,
		Links:    in.Links,
		Tags:     in.Tags,
		Statuses: in.Statuses,
		Group:    in.Group,
	}

	if err := store.CreateProject(in.Slug, p); err != nil {
		return nil, err
	}
	return projectResult(in.Slug, p), nil
}

// UpdateProject patches only the fields the caller passed, under the
// project lock with the project re-read FRESH inside it. Links merge, tags
// and statuses replace; a statuses replacement that would orphan tasks and
// an unsafe prefix are *ValidationError and write nothing.
func UpdateProject(store storage.TaskStore, in UpdateProjectInput) (*ProjectResult, error) {
	slug, err := store.ResolveProject(in.Project)
	if err != nil {
		return nil, err
	}
	// The prefix is the ID source for every auto-minted task
	// ("<prefix>-<n>"), and AddTask validates the result - so an unsafe
	// prefix accepted here would lock the project out of task creation,
	// with every later add failing about an ID nobody typed. Checked
	// BEFORE the mutation so a rejected call writes nothing.
	if err := validation(storage.ValidateProjectPrefix(in.Prefix)); err != nil {
		return nil, err
	}
	// Same for the group: checked before the mutation so a rejected call
	// writes nothing (MutateProject would refuse it too, but as a storage
	// error, not the caller's-mistake kind).
	if in.Group != nil {
		if err := validation(storage.ValidateGroup(*in.Group)); err != nil {
			return nil, err
		}
	}
	// The whole read -> patch -> write runs under the project lock, with
	// the project re-read FRESH inside it: this only sets the fields the
	// caller passed, so a copy read before the lock would silently revert
	// whatever a parallel session changed meanwhile.
	proj, err := store.MutateProject(slug, func(proj *storage.Project) error {
		if in.Name != "" {
			proj.Name = in.Name
		}
		if in.Path != "" {
			proj.Path = in.Path
		}
		if in.Repo != "" {
			proj.Repo = in.Repo
		}
		if in.Stack != "" {
			proj.Stack = in.Stack
		}
		if in.Notes != "" {
			proj.Notes = in.Notes
		}
		if in.Prefix != "" {
			proj.Prefix = in.Prefix
		}

		// Links: merge, never remove
		if len(in.Links) > 0 {
			if proj.Links == nil {
				proj.Links = make(map[string]string)
			}
			for k, v := range in.Links {
				proj.Links[k] = v
			}
		}

		// Tags: replace if provided
		if in.Tags != nil {
			proj.Tags = in.Tags
		}

		// Statuses: replace if provided. Validated HERE, inside the locked
		// critical section (GetTasks itself takes no lock, so this doesn't
		// nest LockProject): a statuses replacement that drops a status
		// current tasks sit on orphans them - the task vanishes from every
		// board column, the same bug class 0.29.2 closed for
		// MoveTask/UpdateTask. Reading tasks here, under the same lock the
		// task mutations hold around their own writes, closes the TOCTOU
		// window a pre-lock check would leave open. Enforced here (service
		// level), not in storage - CLI/manual project.yaml edits stay
		// lenient.
		if in.Statuses != nil {
			if err := validateStatusesKeepTasks(store, slug, in.Statuses); err != nil {
				return err
			}
			proj.Statuses = in.Statuses
		}

		// Archived: set if provided
		if in.Archived != nil {
			proj.Archived = *in.Archived
		}
		// Group: tri-state (nil = keep, "" = leave the group)
		if in.Group != nil {
			proj.Group = *in.Group
		}
		// Slack: replace whole (nil = keep, empty = remove)
		if in.Slack != nil {
			proj.Slack = slackConfig(in.Slack)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return projectResult(slug, proj), nil
}

func projectResult(slug string, p *storage.Project) *ProjectResult {
	return &ProjectResult{
		Slug:     slug,
		Name:     p.Name,
		Path:     p.Path,
		Repo:     p.Repo,
		Stack:    p.Stack,
		Notes:    p.Notes,
		Prefix:   p.Prefix,
		Links:    p.Links,
		Tags:     p.Tags,
		Statuses: p.Statuses,
		Archived: p.Archived,
		Group:    p.Group,
		Slack:    SlackOf(p),
	}
}

// SlackOf renders a project's Slack mapping for the wire; nil when the
// project has none. Channels is never null so a client can append to it.
func SlackOf(p *storage.Project) *SlackMapping {
	if p == nil || p.Slack == nil {
		return nil
	}
	out := &SlackMapping{Workspace: p.Slack.Workspace, Channels: []string{}}
	out.Channels = append(out.Channels, p.Slack.Channels...)
	return out
}

// slackConfig normalises a mapping from the wire: channels trimmed, blanks
// dropped, a leading '#' kept as typed (the source strips it); an empty
// mapping is nil, so the key leaves project.yaml instead of lingering as
// `slack: {}`.
func slackConfig(m *SlackMapping) *storage.SlackConfig {
	if m == nil {
		return nil
	}
	out := &storage.SlackConfig{Workspace: strings.TrimSpace(m.Workspace)}
	for _, ch := range m.Channels {
		if ch = strings.TrimSpace(ch); ch != "" {
			out.Channels = append(out.Channels, ch)
		}
	}
	if out.Workspace == "" && len(out.Channels) == 0 {
		return nil
	}
	return out
}

// validateStatusesKeepTasks rejects an UpdateProject statuses replacement
// that would drop a status current tasks sit on. Compares against the
// EFFECTIVE post-update status set, not the raw param: an empty slice is the
// only way this schema exposes to reset a project back to defaults, and
// Project.GetStatuses() falls back to DefaultStatuses in that case - without
// mirroring that fallback here, resetting to [] would falsely flag every task
// sitting on a plain default status (e.g. "doing") as orphaned. Archived is
// system-level (never a project status) and excluded. Best-effort: a
// task-read failure does not block the update. The failure is a
// *ValidationError.
func validateStatusesKeepTasks(store storage.TaskStore, slug string, newStatuses []string) error {
	effective := newStatuses
	if len(effective) == 0 {
		effective = make([]string, len(storage.DefaultStatuses))
		for i, s := range storage.DefaultStatuses {
			effective[i] = string(s)
		}
	}
	allowed := make(map[string]bool, len(effective))
	for _, s := range effective {
		allowed[strings.ToLower(s)] = true
	}
	tasks, err := store.GetTasks(slug)
	if err != nil {
		return nil
	}
	counts := make(map[string]int)
	for _, t := range tasks {
		st := string(t.Meta.Status)
		if st == string(storage.StatusArchived) {
			continue
		}
		if !allowed[st] {
			counts[st]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	parts := make([]string, 0, len(counts))
	for st, n := range counts {
		parts = append(parts, fmt.Sprintf("%s (%d task(s))", st, n))
	}
	sort.Strings(parts)
	return validation(fmt.Errorf("new statuses would orphan tasks on: %s", strings.Join(parts, ", ")))
}
