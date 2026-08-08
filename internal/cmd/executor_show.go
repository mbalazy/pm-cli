package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newExecutorShowCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "show [project]",
		Short: "Print the project's resolved executor profile (phases, slots, runtimes, handoff)",
		Long: "Prints the executor profile as RESOLVED, not as written: worktree slot paths expanded and " +
			"their env merged (executor-level overlaid by per-slot), context repos, phase bindings, and the " +
			"handoff contract with the runtime skill's scripts derived from disk.\n\n" +
			"This is the single call an acceptance session makes instead of hardcoding a playbook " +
			"path or copying simulator UDIDs and ports out of project.yaml by hand.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, proj, err := resolveProjectArg(store, args)
			if err != nil {
				return err
			}
			fmt.Print(renderExecutorProfile(slug, proj))
			return nil
		},
	}
}

// resolveProjectSlugArg resolves the optional [project] argument shared by the
// commands that take one: explicit arg, else cwd detection.
func resolveProjectSlugArg(store storage.TaskStore, args []string) (string, error) {
	var slug string
	if len(args) == 1 {
		s, err := store.ResolveProject(args[0])
		if err != nil {
			return "", err
		}
		slug = s
	} else {
		slug = detectProjectFromCwd(store)
	}
	if slug == "" {
		return "", fmt.Errorf("no project (pass a project or run inside a project dir)")
	}
	return slug, nil
}

// resolveProjectArg additionally loads the project, for the commands that need
// the project struct. Commands that only need the slug must call
// resolveProjectSlugArg instead: an unreadable project.yaml is fatal here, and
// inheriting that failure would break commands that work fine without it.
func resolveProjectArg(store storage.TaskStore, args []string) (string, *storage.Project, error) {
	slug, err := resolveProjectSlugArg(store, args)
	if err != nil {
		return "", nil, err
	}
	proj, err := store.GetProject(slug)
	if err != nil {
		return "", nil, fmt.Errorf("load project %s: %w", slug, err)
	}
	return slug, proj, nil
}

// renderExecutorProfile renders the resolved profile. Pure (given the project
// struct + what it reads off disk) so it is table-testable.
func renderExecutorProfile(slug string, proj *storage.Project) string {
	var b strings.Builder
	e := proj.GetExecutor()

	fmt.Fprintf(&b, "# executor profile: %s\n", slug)
	if proj.Path != "" {
		fmt.Fprintf(&b, "repo: %s\n", proj.Path)
	}
	if !proj.HasExecutor() {
		b.WriteString("\nNo `executor` block in project.yaml - every phase resolves to the built-in\n")
		b.WriteString("generic and no worktree slots exist. Draft one with `pm executor init`.\n")
		return b.String()
	}
	if !e.Enabled {
		b.WriteString("\n!! executor disabled (enabled: false)\n")
	}

	b.WriteString("\n## Phases\n")
	for _, p := range storage.ExecutorPhases {
		fmt.Fprintf(&b, "  %-10s %s\n", p, describeBinding(e.Phase(p)))
	}

	b.WriteString("\n## Run config\n")
	fmt.Fprintf(&b, "  base_branch:  %s\n", orUnset(e.BaseBranch))
	fmt.Fprintf(&b, "  prepare:      %s\n", orUnset(e.Prepare))
	fmt.Fprintf(&b, "  baseline:     %s\n", orUnset(e.Baseline))
	// Two done statuses because the two epic modes do different things with a
	// green sub: integration merges it into the epic branch, independent only
	// pushes its branch. Printed side by side so the difference is visible
	// without reading the code that resolves it.
	indepDone, indepExplicit := e.IndependentDoneStatus()
	indepSuffix := " (default)"
	if indepExplicit {
		indepSuffix = ""
	}
	fmt.Fprintf(&b, "  statuses:     start=%s wip=%s done=%s\n", e.StartStatus, e.WipStatus, e.DoneStatus)
	fmt.Fprintf(&b, "                done (independent/batch)=%s%s\n", indepDone, indepSuffix)
	fmt.Fprintf(&b, "  fix_rounds:   %d\n", e.FixRounds)
	if len(e.SeedExclude) > 0 {
		fmt.Fprintf(&b, "  seed_exclude: %s\n", strings.Join(e.SeedExclude, ", "))
	}

	slots := e.ResolveWorktrees(proj.Path)
	if len(slots) == 0 {
		b.WriteString("\n## Worktree slots\n  (none - `--additional` runs are not available)\n")
	} else {
		fmt.Fprintf(&b, "\n## Worktree slots (%d)  [claim with --additional, pin with --slot N]\n", len(slots))
		for i, s := range slots {
			fmt.Fprintf(&b, "  slot %d  %s\n", i+1, s.Path)
			for _, kv := range s.Env {
				fmt.Fprintf(&b, "          %s\n", kv)
			}
			if len(s.Env) == 0 {
				b.WriteString("          (no env)\n")
			}
		}
		b.WriteString("  ^ these are the authoritative runtime identifiers (ports, device ids).\n")
		b.WriteString("    Read them from here; never copy them into a doc that can drift.\n")
	}

	if len(e.ContextRepos) > 0 {
		b.WriteString("\n## Reference repos (READ-ONLY)\n")
		for _, name := range sortedKeys(e.ContextRepos) {
			fmt.Fprintf(&b, "  %-10s %s\n", name, e.ContextRepos[name])
		}
	}

	b.WriteString("\n## Handoff (acceptance)\n")
	b.WriteString(renderHandoff(e.ResolveHandoff(proj.Path)))

	if e.Notes != "" {
		fmt.Fprintf(&b, "\n## Notes\n%s\n", strings.TrimRight(e.Notes, "\n"))
	}
	return b.String()
}

func renderHandoff(h storage.ResolvedHandoff) string {
	var b strings.Builder
	if !h.Declared {
		b.WriteString("  (no handoff block - an acceptance session has no declared playbook or\n")
		b.WriteString("   runtime skill here and will have to guess. See `pm executor doctor`.)\n")
		return b.String()
	}
	if h.PlaybookPath == "" {
		b.WriteString("  playbook:      (unset)\n")
	} else {
		state := "MISSING"
		if h.PlaybookExists {
			state = "ok"
		}
		fmt.Fprintf(&b, "  playbook:      %s  [%s]\n", h.PlaybookPath, state)
	}
	if h.RuntimeSkill == "" {
		b.WriteString("  runtime skill: (unset)\n")
		return b.String()
	}
	loc := "NOT FOUND"
	if h.SkillPath != "" {
		loc = h.SkillPath
	}
	fmt.Fprintf(&b, "  runtime skill: /%s\n", h.RuntimeSkill)
	fmt.Fprintf(&b, "                 %s\n", loc)
	if len(h.Scripts) > 0 {
		fmt.Fprintf(&b, "  scripts:       %s\n", h.ScriptsDir)
		for _, s := range h.Scripts {
			fmt.Fprintf(&b, "                 %s\n", s)
		}
		b.WriteString("  ^ what each script is FOR and when to reach for it: see the playbook.\n")
	}
	return b.String()
}

// describeBinding renders a phase binding the way the worker prompt resolves it.
func describeBinding(pb storage.PhaseBinding) string {
	switch pb.Kind() {
	case storage.BindSkill:
		return "skill: " + pb.Skill
	case storage.BindCmd:
		return "cmd:   " + pb.Cmd
	case storage.BindSkip:
		return "skip"
	default:
		return "generic (built-in)"
	}
}

func orUnset(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unset)"
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
