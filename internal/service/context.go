package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// ContextInput is the argument set of pm_context.
type ContextInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project slug or prefix"`
	Cwd     string `json:"cwd,omitempty" jsonschema:"Current working directory for auto-detection"`
}

// Context is the session-start rollup: project-scoped when in.Project is
// given or in.Cwd matches a configured project path, cross-project
// otherwise (a cwd miss is reported in the cross-project note). The result
// is a *ProjectContextResult or a *CrossProjectContextResult.
func Context(store storage.TaskStore, in ContextInput) (any, error) {
	projectSlug := ""
	cwdMissNote := ""
	if in.Project != "" {
		slug, err := store.ResolveProject(in.Project)
		if err != nil {
			return nil, err
		}
		projectSlug = slug
	} else if in.Cwd != "" {
		slug, err := storage.ResolveProjectFromCwd(store, in.Cwd)
		if err != nil {
			cwdMissNote = fmt.Sprintf("cwd %q matches no configured project - showing all projects; pass project explicitly or register the path in project.yaml", in.Cwd)
		}
		projectSlug = slug
	}

	if projectSlug != "" {
		return ProjectContext(store, projectSlug)
	}
	return CrossProjectContext(store, cwdMissNote)
}

// ProjectContext builds the project-scoped rollup: doing tasks with FULL
// briefs and bodies capped at ContextBodyLimit, the generated tracker
// rollup, counts, and pointer-sized extras (executor profile, journal
// COUNTS, today's focus tasks).
func ProjectContext(store storage.TaskStore, slug string) (*ProjectContextResult, error) {
	proj, err := store.GetProject(slug)
	if err != nil {
		return nil, fmt.Errorf("project %q: failed to read project.yaml: %v", slug, err)
	}

	pm := ProjectMeta{
		Slug:  slug,
		Name:  proj.Name,
		Repo:  proj.Repo,
		Stack: proj.Stack,
		Notes: proj.Notes,
		Links: proj.Links,
	}
	for _, s := range proj.GetStatuses() {
		pm.Statuses = append(pm.Statuses, string(s))
	}

	tasks, err := store.GetTasks(slug)
	if err != nil {
		return nil, fmt.Errorf("project %q: failed to read tasks: %v", slug, err)
	}
	trackers, suppressed := storage.BuildTrackers(tasks, store.GetLandingStatuses(slug)...)
	counts := make(map[string]int)
	doing := []TaskDetail{}
	for _, t := range tasks {
		counts[string(t.Meta.Status)]++
		if t.Meta.Status == storage.StatusDoing && !suppressed[t.Meta.ID] {
			d := ToDetail(t)
			d.Body = TruncateBody(d.Body, ContextBodyLimit)
			doing = append(doing, d)
		}
	}

	sort.Slice(doing, func(i, j int) bool {
		return doing[i].Updated > doing[j].Updated
	})

	result := &ProjectContextResult{
		Project:    pm,
		DoingTasks: doing,
		TaskCounts: counts,
		Trackers:   trackers,
	}

	// A POINTER, not the profile. Context is the session-start call, so its
	// output sits in every session's window - the executor/handoff profile is
	// only worth its size to the session that actually runs or accepts work,
	// and `pm executor show` charges it to that session alone.
	if proj.HasExecutor() {
		result.ExecutorProfile = "run `pm executor show " + slug +
			"` for phase bindings, worktree slot runtimes (ports/device ids), context repos and the handoff playbook"
	}

	// Journals: counts only, for the same reason as the profile above. A
	// journal only ever grows, so putting its content here would put an
	// unbounded, permanently growing payload in every session's window - and
	// its readers are the sessions about to touch that subsystem, not all of
	// them. The counts are what makes those sessions ask.
	if jc, err := storage.JournalCounts(store.ProjectDir(slug), proj); err == nil && len(jc) > 0 {
		result.Journals = jc
		result.JournalsNote = "subsystems this project keeps a running incident record for. Call pm_journal_list with the name BEFORE touching one of them; record what bit you with pm_journal_add."
	}

	// The timeline's default read, unlike the journal's counts: its reader is
	// every session that starts on the project, and the delta after a state
	// stays small because the stale signal asks for a new state at 10 entries.
	result.Timeline = contextTimeline(store.ProjectDir(slug), time.Now())

	result.FocusTasks = FocusTaskSummaries(store)
	result.Attention, result.AttentionNote = contextAttention(store, AttentionInput{Project: slug})
	return result, nil
}

// CrossProjectContext builds the every-active-project rollup, briefs
// compressed to one line; note (a cwd miss) and per-project read warnings
// land in the result's note.
func CrossProjectContext(store storage.TaskStore, note string) (*CrossProjectContextResult, error) {
	projects, err := store.ListActiveProjects()
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %v", err)
	}

	var warnings []string
	result := []ProjectSummary{}
	for _, slug := range projects {
		proj, err := store.GetProject(slug)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("project %q: failed to read project.yaml: %v", slug, err))
			continue
		}
		ps := ProjectSummary{
			Slug:       slug,
			Name:       proj.Name,
			Repo:       proj.Repo,
			TaskCounts: make(map[string]int),
		}
		tasks, err := store.GetTasks(slug)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("project %q: failed to read tasks: %v", slug, err))
			continue
		}
		trackers, suppressed := storage.BuildTrackers(tasks, store.GetLandingStatuses(slug)...)
		ps.Trackers = trackers
		for _, t := range tasks {
			ps.TaskCounts[string(t.Meta.Status)]++
			if t.Meta.Status == storage.StatusDoing && !suppressed[t.Meta.ID] {
				s := ToSummary(t)
				// Cross-project view = a scan across every active project, so
				// briefs compress like any other listing. The project-scoped
				// branch keeps them whole (see ProjectContext).
				s.Brief = storage.BriefLine(s.Brief)
				ps.DoingTasks = append(ps.DoingTasks, s)
			}
		}
		sort.Slice(ps.DoingTasks, func(i, j int) bool {
			return ps.DoingTasks[i].Updated > ps.DoingTasks[j].Updated
		})
		ps.TimelineState = timelineStatePointer(store.ProjectDir(slug), time.Now())
		result = append(result, ps)
	}

	output := &CrossProjectContextResult{
		Projects:   result,
		FocusTasks: FocusTaskSummaries(store),
	}
	noteParts := []string{}
	if note != "" {
		noteParts = append(noteParts, note)
	}
	var attentionNote string
	output.Attention, attentionNote = contextAttention(store, AttentionInput{})
	if attentionNote != "" {
		noteParts = append(noteParts, attentionNote)
	}
	noteParts = append(noteParts, warnings...)
	if len(noteParts) > 0 {
		output.Note = strings.Join(noteParts, "; ")
	}
	return output, nil
}
