package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newExecutorCmd(store storage.TaskStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "executor",
		Short: "Executor profile tooling",
	}
	cmd.AddCommand(newExecutorInitCmd(store))
	cmd.AddCommand(newExecutorStatsCmd(store))
	return cmd
}

func newExecutorInitCmd(store storage.TaskStore) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "init [project]",
		Short: "Draft an executor profile by scanning the project's skills/commands and stack",
		Long: "Scans <project>/.claude/skills + .claude/commands and detects the stack to draft an " +
			"`executor` block for project.yaml. Detected skills map to phase bindings; the rest stay empty " +
			"(the engine's built-in generic). Phases with no skill but a detectable verify command get a cmd " +
			"binding. Writes/merges into project.yaml without clobbering other config; re-run updates the draft.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var slug string
			if len(args) == 1 {
				s, err := store.ResolveProject(args[0])
				if err != nil {
					return err
				}
				slug = s
			} else {
				slug = detectProjectFromCwd(store)
			}
			if slug == "" {
				return fmt.Errorf("no project (pass a project or run inside a project dir)")
			}

			proj, err := store.GetProject(slug)
			if err != nil {
				return fmt.Errorf("load project %s: %w", slug, err)
			}
			if proj.Path == "" {
				return fmt.Errorf("project %s has no path - set `path` in project.yaml so init can scan it", slug)
			}

			draft, notes := draftExecutor(proj.Path)

			fmt.Printf("# drafted executor profile for %s (%s)\n", slug, proj.Path)
			for _, n := range notes {
				fmt.Printf("# %s\n", n)
			}
			fmt.Println(marshalExecutorBlock(draft))

			if dryRun {
				fmt.Println("# dry-run: not written. Re-run without --dry-run to save to project.yaml.")
				return nil
			}

			proj.Executor = draft
			if err := store.UpdateProject(slug, proj); err != nil {
				return fmt.Errorf("write project.yaml: %w", err)
			}
			fmt.Printf("# saved to %s\n", store.ProjectYAML(slug))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the drafted profile without writing project.yaml")
	return cmd
}

// draftExecutor scans a project dir for skills/commands + stack and returns a
// drafted executor profile plus human-readable detection notes.
func draftExecutor(projectPath string) (*storage.Executor, []string) {
	// Start from the engine defaults (enabled, additional_worktree off, todo/doing/merged,
	// fix_rounds 3, human gates) so the drafted block is explicit and editable.
	e := (&storage.Project{}).GetExecutor()
	var notes []string

	phases, skillNotes := detectSkillPhases(projectPath)
	notes = append(notes, skillNotes...)

	if verify := detectVerifyCmd(projectPath); verify != "" {
		if _, taken := phases[storage.PhaseVerify]; !taken {
			phases[storage.PhaseVerify] = storage.PhaseBinding{Cmd: verify}
			notes = append(notes, "verify -> cmd: "+verify)
		}
	} else {
		notes = append(notes, "verify -> generic (no stack verify command detected)")
	}

	if len(phases) > 0 {
		e.Phases = phases
	}
	for _, p := range storage.ExecutorPhases {
		if _, ok := phases[p]; !ok {
			notes = append(notes, p+" -> generic (built-in)")
		}
	}
	return &e, notes
}

// detectSkillPhases scans .claude/skills + .claude/commands and maps recognised
// names to phase skill bindings.
func detectSkillPhases(projectPath string) (map[string]storage.PhaseBinding, []string) {
	phases := make(map[string]storage.PhaseBinding)
	var notes []string

	add := func(name string) {
		phase := phaseForName(name)
		if phase == "" {
			return
		}
		if _, taken := phases[phase]; taken {
			return // first match wins
		}
		phases[phase] = storage.PhaseBinding{Skill: "/" + name}
		notes = append(notes, fmt.Sprintf("%s -> skill: /%s", phase, name))
	}

	for _, name := range skillNames(filepath.Join(projectPath, ".claude", "skills")) {
		add(name)
	}
	for _, name := range commandNames(filepath.Join(projectPath, ".claude", "commands")) {
		add(name)
	}
	return phases, notes
}

// skillNames returns the skill directory names under dir (skills are dirs with a
// SKILL.md).
func skillNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md")); err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// commandNames returns the command names under dir (commands are <name>.md files).
func commandNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(names)
	return names
}

// phaseForName maps a skill/command name to a phase, or "" if it is not a
// recognised inner-loop phase. Order matters: review is checked before pr so
// "review-pr" binds to review.
func phaseForName(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "implement"):
		return storage.PhaseImplement
	case strings.Contains(n, "review"):
		return storage.PhaseReview
	case strings.Contains(n, "test"):
		return storage.PhaseTest
	case n == "pr" || strings.Contains(n, "pull-request") || strings.Contains(n, "create-pr"):
		return storage.PhasePR
	}
	return ""
}

// detectVerifyCmd composes a verify command from the project's stack, or "" when
// none is recognised (the engine then uses the generic verify).
func detectVerifyCmd(projectPath string) string {
	if fileExists(filepath.Join(projectPath, "go.mod")) {
		return "go test ./... && go vet ./..."
	}
	if pkg := filepath.Join(projectPath, "package.json"); fileExists(pkg) {
		return nodeVerifyCmd(projectPath, pkg)
	}
	if fileExists(filepath.Join(projectPath, "Cargo.toml")) {
		return "cargo test && cargo clippy -- -D warnings"
	}
	return ""
}

func nodeVerifyCmd(projectPath, pkgPath string) string {
	scripts := readPackageScripts(pkgPath)
	if len(scripts) == 0 {
		return ""
	}
	run := nodeRunner(projectPath)
	// Prefer a single aggregate script if the project already has one.
	for _, agg := range []string{"verify", "check", "ci"} {
		if _, ok := scripts[agg]; ok {
			return run(agg)
		}
	}
	var parts []string
	for _, s := range []string{"test", "lint", "typecheck"} {
		if _, ok := scripts[s]; ok {
			parts = append(parts, run(s))
		}
	}
	return strings.Join(parts, " && ")
}

// nodeRunner returns a function that renders "run <script>" for the project's
// package manager.
func nodeRunner(projectPath string) func(string) string {
	switch {
	case fileExists(filepath.Join(projectPath, "yarn.lock")):
		return func(s string) string { return "yarn " + s }
	case fileExists(filepath.Join(projectPath, "pnpm-lock.yaml")):
		return func(s string) string { return "pnpm " + s }
	default:
		return func(s string) string { return "npm run " + s }
	}
}

func readPackageScripts(pkgPath string) map[string]string {
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	return pkg.Scripts
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// marshalExecutorBlock renders just the executor block for previewing.
func marshalExecutorBlock(e *storage.Executor) string {
	wrapper := struct {
		Executor *storage.Executor `yaml:"executor"`
	}{Executor: e}
	data, err := yaml.Marshal(wrapper)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(data), "\n")
}
