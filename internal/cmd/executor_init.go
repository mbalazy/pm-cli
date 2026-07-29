package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode"

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
	cmd.AddCommand(newExecutorShowCmd(store))
	cmd.AddCommand(newExecutorDoctorCmd(store))
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
			"binding. A skill named after a runtime (simulator/device/browser) becomes `handoff.runtime_skill`, " +
			"and `handoff.playbook` is scaffolded from what pm can derive - the script inventory of that skill, " +
			"the context repos, the baseline command - with an explicit TODO slot everywhere the answer is a " +
			"judgement rather than a fact on disk.\n\n" +
			"If the project has NO executor block yet, the draft is written as-is. If it already has one, the " +
			"detected fields (`phases`, `baseline`) are refreshed, `handoff` is filled in ONLY when that block " +
			"is empty (its playbook is hand-written prose, never regenerated), and every other hand-set field " +
			"(enabled, additional_worktree, worktree_path, worktrees, base_branch, env, seed_exclude, prepare, " +
			"context_repos, start_status, wip_status, done_status, fix_rounds, gate, notes) is left untouched; " +
			"the output lists which fields were preserved. An existing playbook file is never rewritten.",
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
			result, mergeNotes := mergeExecutor(proj.Executor, draft)
			notes = append(notes, mergeNotes...)

			fmt.Printf("# drafted executor profile for %s (%s)\n", slug, proj.Path)
			for _, n := range notes {
				fmt.Printf("# %s\n", n)
			}
			fmt.Println(marshalExecutorBlock(result))

			plan := planPlaybook(proj.Path, slug, result)
			switch {
			case plan == nil:
			case plan.Exists:
				fmt.Printf("# playbook: %s already exists - left untouched\n", plan.Path)
			default:
				fmt.Printf("# playbook: %s will be scaffolded (fill in every %s slot)\n", plan.Path, playbookTODO)
			}

			if dryRun {
				fmt.Println("# dry-run: nothing written. Re-run without --dry-run to save.")
				return nil
			}

			proj.Executor = result
			if err := store.UpdateProject(slug, proj); err != nil {
				return fmt.Errorf("write project.yaml: %w", err)
			}
			fmt.Printf("# saved to %s\n", store.ProjectYAML(slug))

			// After project.yaml, and never fatal: the profile is the command's
			// job, the scaffold is a convenience. A project.yaml that points at
			// a playbook nobody could create is a `doctor` finding, not a
			// reason to fail a write that already succeeded.
			if err := plan.write(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not scaffold %s: %v\n", plan.Path, err)
			} else if plan != nil && plan.Content != "" {
				fmt.Printf("# scaffolded %s\n", plan.Path)
			}
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
		e.Baseline = verify
		notes = append(notes, "baseline -> cmd: "+verify)
	} else {
		notes = append(notes, "verify -> generic (no stack verify command detected)")
		notes = append(notes, "baseline -> unset (no stack verify command detected)")
	}

	if len(phases) > 0 {
		e.Phases = phases
	}
	for _, p := range storage.ExecutorPhases {
		if _, ok := phases[p]; !ok {
			notes = append(notes, p+" -> generic (built-in)")
		}
	}

	// The odbiór half of the profile. The playbook path is set unconditionally
	// (its evidence map and verification command are worth having even when no
	// runtime skill exists) and `init` scaffolds the file, so a declared path is
	// never a dangling one - `pm executor doctor` treats that as a hard error.
	e.Handoff.Playbook = defaultPlaybookPath
	notes = append(notes, "handoff.playbook -> "+defaultPlaybookPath)
	runtimeSkill, runtimeNote := detectRuntimeSkill(projectPath)
	e.Handoff.RuntimeSkill = runtimeSkill
	notes = append(notes, runtimeNote)

	return &e, notes
}

// mergeExecutor combines a freshly drafted profile with the project's existing
// executor block, if any. A project with no existing block gets the draft as-is
// (first `pm executor init`).
//
// Three categories, not two:
//
//   - phases, baseline - DETECTED. The draft always wins, so a re-run keeps
//     re-scanning the project.
//   - handoff - FILLED IF EMPTY. Detection can propose the path and the runtime
//     skill, but the playbook's actual content is hand-written prose about what
//     each tool is FOR, and re-running init must never imply that prose is
//     regenerable. So: proposed into an empty block, never over a set one.
//   - everything else - HAND-SET. Preserved untouched.
func mergeExecutor(existing *storage.Executor, draft *storage.Executor) (*storage.Executor, []string) {
	if existing == nil {
		return draft, nil
	}
	merged := *existing
	merged.Phases = draft.Phases
	merged.Baseline = draft.Baseline

	notes := preservedFieldNotes(existing)
	if existing.Handoff.IsZero() && !draft.Handoff.IsZero() {
		merged.Handoff = draft.Handoff
		notes = append(notes, "handoff -> filled in (the block was empty)")
	}
	return &merged, notes
}

// preservedFieldNotes lists the existing executor fields (besides phases/baseline,
// which are always refreshed from the draft) that carry a non-default value and
// were therefore kept as-is rather than overwritten.
func preservedFieldNotes(existing *storage.Executor) []string {
	base := (&storage.Project{}).GetExecutor()
	var notes []string
	note := func(field string) { notes = append(notes, "preserved existing "+field) }

	if existing.Enabled != base.Enabled {
		note("enabled")
	}
	if existing.AdditionalWorktree != base.AdditionalWorktree {
		note("additional_worktree")
	}
	if existing.WorktreePath != "" {
		note("worktree_path")
	}
	if len(existing.Worktrees) > 0 {
		note("worktrees")
	}
	if existing.BaseBranch != "" {
		note("base_branch")
	}
	if len(existing.Env) > 0 {
		note("env")
	}
	if len(existing.SeedExclude) > 0 {
		note("seed_exclude")
	}
	if existing.Prepare != "" {
		note("prepare")
	}
	if len(existing.ContextRepos) > 0 {
		note("context_repos")
	}
	if !existing.Handoff.IsZero() {
		note("handoff")
	}
	if existing.StartStatus != base.StartStatus {
		note("start_status")
	}
	if existing.WipStatus != base.WipStatus {
		note("wip_status")
	}
	if existing.DoneStatus != base.DoneStatus {
		note("done_status")
	}
	if existing.FixRounds != base.FixRounds {
		note("fix_rounds")
	}
	if !reflect.DeepEqual(existing.Gate, base.Gate) {
		note("gate")
	}
	if existing.Notes != "" {
		note("notes")
	}
	return notes
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
//
// The test is "does <name>/SKILL.md resolve", deliberately NOT "is <name> a
// directory": os.ReadDir reports a SYMLINK to a directory as a non-directory,
// so an IsDir() gate here made every linked-in skill invisible. Skills shared
// across repos are commonly installed as links to one checkout (that is how
// mobile-claude-toolkit distributes them), and those must be detected like any
// other. os.Stat follows the link, so the SKILL.md probe is the whole check -
// a plain file called `foo` cannot pass it.
func skillNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
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

// runtimeSkillTiers ranks the name segments that mark a skill as the project's
// RUNTIME verification skill: the one that drives a real simulator, device or
// browser, and therefore the one an odbiór (acceptance) session has to read in
// full rather than by its one-line description.
//
// Tiers rather than a flat list, because the signals are not equally strong.
// "simulator" says what the skill is; "verify" appears in plenty of names that
// have nothing to do with a runtime, so it only wins when nothing better exists.
//
// Matching is on name SEGMENTS (split on any non-alphanumeric), never on raw
// substrings - a substring rule makes the segment "sim" match "simplify". Long
// keywords still match inside a segment so "simulatorverify" is not missed.
var runtimeSkillTiers = [][]string{
	{"simulator", "sim"},
	{"device", "emulator"},
	{"runtime", "browser", "e2e"},
	{"verify"},
}

// runtimeSkillTier returns the best (lowest) tier the name matches, or -1.
func runtimeSkillTier(name string) int {
	segments := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for tier, keywords := range runtimeSkillTiers {
		for _, kw := range keywords {
			for _, seg := range segments {
				if seg == kw || (len(kw) >= 6 && strings.Contains(seg, kw)) {
					return tier
				}
			}
		}
	}
	return -1
}

// detectRuntimeSkill picks the project's runtime verification skill by name.
//
// Skills are scanned before commands because only a skill directory can carry
// a scripts/ dir, and those scripts are the concrete diagnostic tools the
// handoff contract exists to surface. A name that phaseForName() already
// recognises is skipped outright: an inner-loop phase skill is not a runtime
// driver, and letting one land in both places would bind the same skill to two
// unrelated jobs.
func detectRuntimeSkill(projectPath string) (string, string) {
	skillsDir := filepath.Join(projectPath, ".claude", "skills")

	name, tier, scripts := "", -1, false
	// Lower tier wins; within a tier, a skill shipping scripts/ wins; otherwise
	// the first (alphabetical) match stands.
	better := func(t int, s bool) bool {
		switch {
		case tier < 0:
			return true
		case t != tier:
			return t < tier
		case s != scripts:
			return s
		default:
			return false
		}
	}

	for _, n := range skillNames(skillsDir) {
		t := runtimeSkillTier(n)
		if t < 0 || phaseForName(n) != "" {
			continue
		}
		s := dirExists(filepath.Join(skillsDir, n, "scripts"))
		if better(t, s) {
			name, tier, scripts = n, t, s
		}
	}
	if name == "" {
		for _, n := range commandNames(filepath.Join(projectPath, ".claude", "commands")) {
			t := runtimeSkillTier(n)
			if t < 0 || phaseForName(n) != "" {
				continue
			}
			if better(t, false) {
				name, tier = n, t
			}
		}
	}

	if name == "" {
		return "", "handoff.runtime_skill -> none detected (no skill named after a simulator/device/browser runtime)"
	}
	note := "handoff.runtime_skill -> " + name
	if scripts {
		note += " (ships scripts/)"
	}
	return name, note
}

// phaseForName maps a skill/command name to a phase, or "" if it is not a
// recognised inner-loop phase. Order matters: review is checked before pr so
// "review-pr" binds to review.
//
// Deliberately does NOT know `verify`: the verify phase is bound from the
// stack's own command by detectVerifyCmd, and the word is claimed by
// runtimeSkillTiers instead - a skill called "simulator-verify" is a runtime
// driver, not the inner-loop verify step.
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

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
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
