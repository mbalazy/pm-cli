package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/spf13/cobra"
)

// checkLevel separates the two kinds of finding the doctor can produce, because
// they deserve different consequences.
//
// levelError is a BINARY FACT about the filesystem: a declared playbook that
// does not exist, a context repo pointing at nothing. There is no reading of
// the project under which those are fine, so they may fail the command.
//
// levelWarn is a HEURISTIC over prose or config style: "the playbook never
// names measure-element.py" is a guess - the playbook may describe the same
// technique in different words. Warnings must not fail by default: one false
// alarm is how a validator gets switched off permanently. --strict promotes
// them for anyone who has decided the heuristics earn their keep.
type checkLevel int

const (
	levelOK checkLevel = iota
	levelWarn
	levelError
)

func (l checkLevel) tag() string {
	switch l {
	case levelError:
		return "ERROR"
	case levelWarn:
		return "WARN "
	default:
		return "ok   "
	}
}

type check struct {
	Level checkLevel
	Msg   string
	Hint  string
}

// errFound is doctor's exit-1 signal when the report contains failures. main
// prints it as the one trailer line under the report.
var errFound = errors.New("executor doctor found errors (see report above)")

func newExecutorDoctorCmd(store storage.TaskStore) *cobra.Command {
	var strict bool

	cmd := &cobra.Command{
		Use:   "doctor [project]",
		Short: "Check the executor profile for completeness and drift",
		Long: "Validates COMPLETENESS, not YAML syntax: that a declared playbook and runtime skill really " +
			"exist, that context repos and worktree slot paths are usable, that the playbook mentions every " +
			"script the runtime skill ships, and that it does not hardcode runtime identifiers (simulator " +
			"ids, ports) pm already resolves from the worktree slots - the copy that silently drifts the " +
			"first time a slot changes.\n\n" +
			"Exit code: 1 on ERROR (a binary fact - a declared file that is not there), 0 on WARN (a " +
			"heuristic over prose, which can be a false alarm). --strict fails on warnings too.",
		Args: cobra.MaximumNArgs(1),
		// The report IS the output; the returned error only sets the exit code.
		// Silenced so neither cobra nor the report gains an "Error:" banner -
		// and no os.Exit in RunE, which would skip deferred cleanups and make
		// the command untestable.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, proj, err := resolveProjectArg(store, args)
			if err != nil {
				cmd.SilenceErrors = false // real failures should still print
				return err
			}
			checks := runExecutorDoctor(proj)
			fmt.Print(renderDoctor(slug, checks, strict))
			if failed(checks, strict) {
				return errFound
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as failures (exit 1)")
	return cmd
}

func failed(checks []check, strict bool) bool {
	for _, c := range checks {
		if c.Level == levelError || (strict && c.Level == levelWarn) {
			return true
		}
	}
	return false
}

func renderDoctor(slug string, checks []check, strict bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# executor doctor: %s\n\n", slug)
	var errs, warns int
	for _, c := range checks {
		fmt.Fprintf(&b, "  [%s] %s\n", c.Level.tag(), c.Msg)
		if c.Hint != "" {
			fmt.Fprintf(&b, "          -> %s\n", c.Hint)
		}
		switch c.Level {
		case levelError:
			errs++
		case levelWarn:
			warns++
		}
	}
	fmt.Fprintf(&b, "\n%d error(s), %d warning(s)", errs, warns)
	if strict {
		b.WriteString(" [--strict: warnings fail]")
	}
	b.WriteString("\n")
	return b.String()
}

// runExecutorDoctor produces the findings for a project. Pure apart from the
// filesystem it deliberately inspects, so it is table-testable.
func runExecutorDoctor(proj *storage.Project) []check {
	var out []check
	add := func(l checkLevel, msg, hint string) { out = append(out, check{Level: l, Msg: msg, Hint: hint}) }

	// Machine knowledge first, before anything the executor block declares: a
	// project with no block still launches solo shifts, and those need the
	// skill as much as `pm finish` needs its own.
	out = append(out, checkAgentSkills(proj)...)
	if !proj.HasExecutor() {
		add(levelWarn, "no `executor` block in project.yaml",
			"`pm executor init` drafts one by scanning the repo's skills and stack")
		// Deliberately NOT a return: a project with no executor block is the
		// FRESH project, i.e. exactly the one whose `statuses` list still has
		// no landing status and whose first `pm run-epic` dies at the gate.
		// Stopping here reported that project as healthy - 0 errors, one
		// warning about a block `init` would write anyway - which is the state
		// this check exists to stop being invisible. GetExecutor() resolves the
		// built-in defaults, and those are the statuses that run would use.
		out = append(out, checkLandingStatuses(proj.GetExecutor(), proj.GetStatuses())...)
		return out
	}
	if proj.Path == "" {
		add(levelError, "project has no `path` - nothing in the executor block can be resolved",
			"set `path` in project.yaml")
		return out
	}
	if st, err := os.Stat(proj.Path); err != nil || !st.IsDir() {
		add(levelError, "project path does not exist: "+proj.Path, "fix `path` in project.yaml")
		return out
	}

	e := proj.GetExecutor()
	out = append(out, checkLandingStatuses(e, proj.GetStatuses())...)
	out = append(out, checkBaseline(e)...)
	out = append(out, checkRuntimePhase(e)...)
	out = append(out, checkRuntimeTools(e, proj.Path)...)
	out = append(out, checkContextRepos(e)...)
	out = append(out, checkSlots(e, proj.Path)...)
	out = append(out, checkHandoff(e, proj)...)
	out = append(out, checkHooksPath(proj.Path)...)
	out = append(out, checkWorkerConfigDir(e, proj)...)
	return out
}

// checkAgentSkills: the procedures a launch from pm hands off to (`/solo`,
// `batch-finish-auto`) are not in this binary - they come from the skills
// repository and are linked into the project's Claude config dir by its
// install.sh. Missing ones are a WARN, not an ERROR: the profile is fine, the
// machine is not, and `--strict` promotes it for anyone who wants the gate.
func checkAgentSkills(proj *storage.Project) []check {
	dir := proj.ResolveClaudeConfigDir()
	missing := storage.MissingSkills(dir, storage.AgentSkills...)
	if len(missing) == 0 {
		return nil
	}
	return []check{{levelWarn,
		fmt.Sprintf("skill(s) %s not found under %s/skills - a solo launch or `pm finish` from this machine starts a session with nothing to follow", strings.Join(missing, ", "), dir),
		storage.SkillsInstallHint(dir)}}
}

// checkWorkerConfigDir: a declared worker config dir that does not exist is an
// ERROR - every worker would start in an empty, logged-out config and die on
// its first call. One that exists but was never used interactively (no
// .claude.json, the file the CLI writes on first run / login) is a WARN: whether
// the keychain holds a login for it is not a filesystem fact, but a dir nobody
// has opened once almost certainly has none.
func checkWorkerConfigDir(e storage.Executor, proj *storage.Project) []check {
	if strings.TrimSpace(e.WorkerClaudeConfigDir) == "" {
		return nil
	}
	dir := proj.ResolveWorkerClaudeConfigDir()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return []check{{Level: levelError, Msg: "executor.worker_claude_config_dir does not exist: " + dir,
			Hint: "mkdir it, then log in once: CLAUDE_CONFIG_DIR=" + dir + " claude"}}
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude.json")); err != nil {
		return []check{{Level: levelWarn, Msg: "executor.worker_claude_config_dir has no .claude.json yet: " + dir,
			Hint: "run CLAUDE_CONFIG_DIR=" + dir + " claude once interactively (login + onboarding) or the first worker fails on its first call"}}
	}
	return nil
}

// checkLandingStatuses reports landing statuses the project's `statuses` list
// does not contain. A missing integration done_status is fatal at run start
// (run-epic hard-gates it), while a missing independent one degrades to
// done_status with a note - hence WARN, not ERROR: the run still works, it just
// records batch subs as "merged" when nothing was merged, which is the exact
// confusion the separate status exists to end.
func checkLandingStatuses(e storage.Executor, statuses []storage.TaskStatus) []check {
	var out []check
	indep, _ := e.IndependentDoneStatus()
	for _, s := range e.LandingStatuses() {
		if statusAllowed(s, statuses) {
			continue
		}
		hint := fmt.Sprintf("add `%s` to `statuses:` in project.yaml", s)
		if string(s) == indep {
			hint += " - without it a verify-green batch sub falls back to `" + e.DoneStatus + "`, claiming a merge that never happened"
		} else {
			hint += " - `pm run-epic` refuses to start an integration epic without it"
		}
		out = append(out, check{Level: levelWarn, Msg: "landing status not in the project's statuses: " + string(s), Hint: hint})
	}
	return out
}

// checkBaseline warns when the project has no verification baseline.
//
// Without one, every worker is told nothing about what was already broken, so
// on a repo whose gate is red on pre-existing failures each worker rediagnoses
// that same breakage and then books it against its own diff. That is the single
// most expensive failure cluster the executor journal records, and it stayed
// invisible for seven runs on a project doctor called healthy.
//
// WARN, not ERROR, per this file's split: a run without a baseline works - it
// behaves exactly as every run did before the field existed. The finding is
// about completeness, not about a broken project.
func checkBaseline(e storage.Executor) []check {
	if bl := strings.TrimSpace(e.Baseline); bl != "" {
		return []check{{levelOK, "baseline -> " + bl, ""}}
	}
	hint := "set `baseline` to the project's full verification command (`pm executor init` detects one) " +
		"so a worker can tell a failure it caused from one it inherited"
	// A tuned verify command is the strongest available evidence of what the
	// baseline should be, and the two being set independently is how the gap
	// opens: `init` writes both, a hand-edited block predating the field keeps
	// only one.
	if v := strings.TrimSpace(e.Phases["verify"].Cmd); v != "" {
		hint = fmt.Sprintf("verify runs %q - baseline is normally the same command, captured once per run "+
			"so a worker can tell a failure it caused from one it inherited", v)
	}
	return []check{{levelWarn, "`baseline` is unset - workers get no list of pre-existing failures", hint}}
}

// checkRuntimePhase: the runtime phase is three settings that only work
// together - a binding (what drives the rig), `rig` (whether the rig is up,
// checked once per run) and `runtime_tools` (what the binding is allowed to
// run outside --yolo) - and each one alone is silent: a `runtime: on` sub with
// no binding gets the generic, with no rig gets no verdict, with no tools gets
// refusals it reports as TODOs. All WARN: every one is a heuristic about a
// phase the project may never enable on a sub. Nothing to say when none of
// the three is set - the phase is simply not in use.
func checkRuntimePhase(e storage.Executor) []check {
	binding := e.Phase(storage.PhaseRuntime)
	rig := strings.TrimSpace(e.Rig)
	bound := binding.Kind() == storage.BindSkill || binding.Kind() == storage.BindCmd
	if !bound && rig == "" && len(e.RuntimeTools) == 0 {
		return nil
	}
	var out []check
	switch binding.Kind() {
	case storage.BindSkip:
		out = append(out, check{levelWarn, "runtime phase is bound to `false` (skip) - a sub's `runtime: on` does nothing here", "bind `phases.runtime` to the skill or command that drives this project's runtime, or drop the binding"})
	case storage.BindGeneric:
		out = append(out, check{levelWarn, "runtime phase is generic while `rig`/`runtime_tools` are set - a `runtime: on` worker improvises a driver", "bind `phases.runtime` to the skill (e.g. `skill: simulator-verify`) or command that drives this project's runtime"})
	default:
		out = append(out, check{levelOK, "runtime phase -> " + describeBinding(binding), ""})
	}
	if rig == "" {
		out = append(out, check{levelWarn, "`rig` is unset - a `runtime: on` worker gets no rig verdict and must probe the runtime itself", "set `rig` to the command that proves the slot's runtime runs the claimed worktree (e.g. the runtime skill's rig-check script with the slot's $SIM_UDID / port)"})
	} else {
		out = append(out, check{levelOK, "rig -> " + rig, ""})
	}
	if bound && len(e.RuntimeTools) == 0 {
		out = append(out, check{levelWarn, "`runtime_tools` is empty - outside --yolo the runtime phase's commands (xcrun, curl, the skill's scripts) are refused", "list the allowlist patterns the binding runs, e.g. `Bash(xcrun simctl:*)`, `Bash(curl:*)`, `Bash($SLOT/.claude/skills/<skill>/scripts/<script>:*)` ($SLOT = the worker's tree)"})
	}
	return out
}

func checkContextRepos(e storage.Executor) []check {
	var out []check
	for _, name := range sortedKeys(e.ContextRepos) {
		path := e.ContextRepos[name]
		if st, err := os.Stat(path); err != nil || !st.IsDir() {
			out = append(out, check{levelError,
				fmt.Sprintf("context_repos[%s] does not exist: %s", name, path),
				"a worker is told it may read this repo - a dead path makes the promise a lie"})
			continue
		}
		out = append(out, check{levelOK, fmt.Sprintf("context_repos[%s] -> %s", name, path), ""})
	}
	return out
}

func checkSlots(e storage.Executor, projPath string) []check {
	slots := e.ResolveWorktrees(projPath)
	if len(slots) == 0 {
		return nil
	}
	var out []check
	for i, s := range slots {
		switch {
		case !pathExists(s.Path):
			// Absent is fine: EnsureWorktree creates it on the first
			// --additional run.
			out = append(out, check{levelOK,
				fmt.Sprintf("slot %d not created yet: %s", i+1, s.Path),
				""})
		case !storage.IsGitWorktree(s.Path):
			out = append(out, check{levelError,
				fmt.Sprintf("slot %d path exists but is not a git worktree: %s", i+1, s.Path),
				"every --additional run claiming this slot will fail; remove it or `git worktree prune`"})
		default:
			out = append(out, check{levelOK, fmt.Sprintf("slot %d ok: %s", i+1, s.Path), ""})
		}
		if len(s.Env) == 0 && len(slots) > 1 {
			out = append(out, check{levelWarn,
				fmt.Sprintf("slot %d has no env", i+1),
				"with several slots, per-slot env (port, device id) is what keeps two runs from colliding"})
		}
	}
	return out
}

func checkHandoff(e storage.Executor, proj *storage.Project) []check {
	h := e.ResolveHandoff(proj.Path, proj.ResolveClaudeConfigDir())
	if !h.Declared {
		return []check{{levelWarn,
			"no `executor.handoff` block - the acceptance has no declared playbook or runtime skill",
			"add handoff.playbook + handoff.runtime_skill so an acceptance session can ask pm instead of guessing"}}
	}

	var out []check
	switch {
	case h.PlaybookPath == "":
		out = append(out, check{levelWarn, "handoff.playbook is unset",
			"without it the acceptance skill has no map from evidence category to a real command here"})
	case !h.PlaybookExists:
		out = append(out, check{levelError, "handoff.playbook does not exist: " + h.PlaybookPath,
			"create it or fix the path"})
	default:
		out = append(out, check{levelOK, "playbook -> " + h.PlaybookPath, ""})
	}

	switch {
	case h.RuntimeSkill == "":
		out = append(out, check{levelWarn, "handoff.runtime_skill is unset",
			"name the skill that drives the real runtime (simulator/device/browser)"})
	case h.SkillPath == "":
		out = append(out, check{levelError,
			fmt.Sprintf("handoff.runtime_skill %q not found under .claude/skills or .claude/commands, nor under %s (skills/ or commands/)", h.RuntimeSkill, h.GlobalRoot),
			"fix the name or add the skill"})
	case h.RuntimeSkillGlobal:
		// Legitimate for a machine-knowledge driver (web-verify), but never
		// silent: a repo that is synced to another machine takes no global
		// skill with it, and this is where that shows up before it costs an
		// acceptance (the 0.49.1 rule, kept as a warning instead of a refusal).
		out = append(out, check{levelWarn,
			fmt.Sprintf("runtime skill /%s -> %s (%d script(s)) - resolved in %s, NOT in the repo", h.RuntimeSkill, h.SkillPath, len(h.Scripts), h.GlobalRoot),
			"a global runtime skill is machine knowledge: every machine that accepts this project needs it under its claude_config_dir; a repo-local .claude/skills/<name> is what travels with the repo"})
	default:
		out = append(out, check{levelOK,
			fmt.Sprintf("runtime skill /%s -> %s (%d script(s))", h.RuntimeSkill, h.SkillPath, len(h.Scripts)), ""})
	}

	// rig_skill is optional - a runtime that needs no standing up has nothing
	// to declare - so an unset value is silence, not a warning. But a declared
	// name that resolves nowhere is a binary filesystem fact: the acceptance
	// would be sent to a cold-start procedure that does not exist.
	switch {
	case h.RigSkill == "":
	case h.RigSkillPath == "":
		out = append(out, check{levelError,
			fmt.Sprintf("handoff.rig_skill %q not found under the repo's .claude/skills or .claude/commands, nor under %s (skills/ or commands/)", h.RigSkill, h.GlobalRoot),
			"fix the name or add the skill"})
	default:
		out = append(out, check{levelOK,
			fmt.Sprintf("rig skill /%s -> %s", h.RigSkill, h.RigSkillPath), ""})
	}

	if !h.PlaybookExists {
		return out
	}
	data, err := os.ReadFile(h.PlaybookPath)
	if err != nil {
		out = append(out, check{levelError, "cannot read playbook: " + err.Error(), ""})
		return out
	}
	text := string(data)
	out = append(out, checkPlaybookTODOs(h.PlaybookPath, text)...)
	out = append(out, checkScriptsMentioned(h, text)...)
	out = append(out, checkRuntimeDrift(e, proj.Path, h.PlaybookPath, text)...)
	return out
}

// checkPlaybookTODOs warns while the scaffold's unfilled slots survive.
//
// Without this the generator would defeat its own purpose: `pm executor init`
// writes a playbook that NAMES every script, so checkScriptsMentioned falls
// silent, while the prose saying what those scripts are FOR is still empty -
// which is precisely the gap the handoff contract exists to close. The names
// are the scaffold; the judgement is the document.
//
// One finding for the file, not one per slot: a fresh scaffold has a dozen and
// listing them all would bury every other check.
func checkPlaybookTODOs(path, playbook string) []check {
	n := strings.Count(playbook, playbookTODO)
	if n == 0 {
		return nil
	}
	return []check{{levelWarn,
		fmt.Sprintf("playbook has %d unfilled %s slot(s): %s", n, playbookTODO, path),
		"a scaffold naming the scripts satisfies the mention check while saying nothing about what they are FOR - fill the slots or delete the ones that do not apply"}}
}

// checkScriptsMentioned warns about a runtime-skill script the playbook never
// names. This is the exact gap that produced this feature: the scripts exist,
// the playbook does not mention them, so an acceptance session redoes their work by
// hand. Heuristic on purpose - a playbook may describe a technique without
// naming the file, hence WARN, never ERROR.
func checkScriptsMentioned(h storage.ResolvedHandoff, playbook string) []check {
	var out []check
	for _, s := range h.Scripts {
		if strings.Contains(playbook, s) {
			continue
		}
		out = append(out, check{levelWarn,
			fmt.Sprintf("playbook never mentions %s (from /%s scripts)", s, h.RuntimeSkill),
			"config says WHAT exists; the playbook has to say what it is FOR and when to reach for it"})
	}
	return out
}

// checkRuntimeDrift warns when the playbook hardcodes a runtime identifier that
// pm already resolves from the worktree slots (a simulator id, a Metro port).
// The copy is correct exactly until a slot changes, and then it sends an acceptance
// at a device that no longer exists.
//
// Matching is on the VERBATIM value, never on the key or a pattern, which keeps
// false alarms to values that really do appear in the text; short values are
// skipped because "1" or "true" would match anything. The finding names the
// line so a human can judge it - it is still a guess about prose.
func checkRuntimeDrift(e storage.Executor, projPath, playbookPath, playbook string) []check {
	lines := strings.Split(playbook, "\n")
	seen := make(map[string]bool)
	var out []check

	for i, s := range e.ResolveWorktrees(projPath) {
		for _, kv := range s.Env {
			key, value, ok := strings.Cut(kv, "=")
			if !ok || len(value) < 4 || seen[value] {
				continue
			}
			for n, line := range lines {
				if !strings.Contains(line, value) {
					continue
				}
				seen[value] = true
				out = append(out, check{levelWarn,
					fmt.Sprintf("playbook hardcodes %s (slot %d) at %s:%d", key, i+1, playbookPath, n+1),
					fmt.Sprintf("%q - point at `pm executor show` instead; this copy goes stale the moment the slot changes",
						strings.TrimSpace(line))})
				break
			}
		}
	}
	return out
}

// checkHooksPath warns when core.hooksPath is configured (in any git scope -
// repo-local, global, or system) but resolves to a dead hooks directory. A
// repo where NO scope configures core.hooksPath at all is deliberately
// silent here: a stock .git/hooks with only *.sample files is normal for
// most repos and must never warn. But once something IS configured -
// including a global override this project never opted into - the entire
// worker-guard investment (and every project hook it protects: secret
// scanning, lint, formatting) is silently protecting nothing while commits
// are reported as hook-checked.
//
// Resolution goes through `git rev-parse --git-path hooks`, invoked with
// `-C projPath` exactly like every lookup this function makes: for a
// relative core.hooksPath, git prints the value already adjusted for
// projPath's depth below the worktree root (e.g. "../../myhooks" from two
// levels down), so joining it back onto projPath - not the worktree root -
// and letting filepath.Join clean the ".." segments away reproduces git's
// own resolution instead of reimplementing it by hand.
func checkHooksPath(projPath string) []check {
	if !isGitRepo(projPath) {
		return nil
	}
	value, err := gitConfigGet(projPath, "core.hooksPath")
	if err != nil {
		return nil // not configured in any scope - nothing to check
	}
	origin := gitConfigOrigin(projPath, "core.hooksPath")
	if value == "" {
		// An explicitly empty value is its own broken state, not "unset":
		// `git rev-parse --git-path hooks` resolves it to the project
		// directory itself (the `hooks` path component is replaced by
		// nothing), which would otherwise make this function report the
		// project root as the hooks dir - a real executable anywhere at the
		// root (a `configure` script, a `gradlew`) would then read as a
		// healthy hook setup.
		return []check{{levelWarn, "core.hooksPath is configured but empty (" + origin + ") - hooks never run",
			"set core.hooksPath to a real directory, or unset it to fall back to the default .git/hooks"}}
	}

	resolved, err := gitRevParseGitPath(projPath, "hooks")
	if err != nil {
		return nil
	}
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(projPath, resolved)
	}
	hint := fmt.Sprintf("core.hooksPath = %q (%s) - project hooks (secret scanning, lint, formatting) do not run without a real hook file there", value, origin)

	entries, err := os.ReadDir(resolved)
	switch {
	case os.IsNotExist(err):
		return []check{{levelWarn, "configured git hooks dir does not exist: " + resolved, hint}}
	case err != nil:
		return []check{{levelWarn, "configured git hooks dir cannot be read: " + resolved + " (" + err.Error() + ")", hint}}
	case !hasHookFile(resolved, entries):
		return []check{{levelWarn, "configured git hooks dir has no hook files: " + resolved, hint}}
	default:
		return []check{{levelOK, "git hooks -> " + resolved, ""}}
	}
}

// hasHookFile reports whether dir's entries contain a real hook file: not a
// directory, not one of the *.sample placeholders `git init` seeds the
// default hooks dir with, and executable - git silently skips a hook file
// that lost its exec bit, and a hooks dir holding only a README or a stray
// .DS_Store must not read as healthy.
//
// Symlinks are followed rather than judged by their own mode. A symlink's
// lstat mode is 0777 on every unix, so counting it as a hook unseen would
// pass a dir whose links all dangle - and linking each hook at a shared
// script is how most hook managers lay the directory out, i.e. exactly the
// setup where a broken link is plausible and where git, which tests the hook
// with access(X_OK) through the link, runs nothing at all. A non-symlink
// whose mode cannot be read fails open (counts as a hook) rather than turn
// this heuristic WARN into a false alarm over a filesystem race.
func hasHookFile(dir string, entries []os.DirEntry) bool {
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".sample") {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			if e.Type()&os.ModeSymlink != 0 {
				continue // dangling link - git has nothing to run
			}
			return true
		}
		if info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		return true
	}
	return false
}

func gitConfigGet(dir, key string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "config", "--get", key).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitRevParseGitPath(dir, path string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--git-path", path).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitConfigOrigin returns the human-readable origin git attributes the
// configured value to (e.g. "file:.git/config" or "file:/home/x/.gitconfig").
// Best-effort: an unresolvable origin degrades to a placeholder rather than
// failing the whole check.
func gitConfigOrigin(dir, key string) string {
	out, err := exec.Command("git", "-C", dir, "config", "--show-origin", "--get", key).Output()
	if err != nil {
		return "unknown origin"
	}
	origin, _, ok := strings.Cut(strings.TrimRight(string(out), "\n"), "\t")
	if !ok {
		return "unknown origin"
	}
	return origin
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// checkRuntimeTools: a runtime_tools pattern is a LITERAL prefix of what the
// worker types, so a path in it has to be one the worker can type from its
// own tree. Two shapes are known to fail (pm-cli-130, the 2026-09-06 e2e
// test): a path under the MAIN checkout while the project runs its workers in
// worktree slots - the worker types the slot's path and the prefix never
// matches (0 of 3 runtime workers ran a single skill script) - and a path
// that does not exist on this machine at all. `$SLOT` is the fix for the
// first: pm expands it to the worker's tree per run.
func checkRuntimeTools(e storage.Executor, projPath string) []check {
	if len(e.RuntimeTools) == 0 {
		return nil
	}
	slots := e.ResolveWorktrees(projPath)
	var out []check
	slotPatterns := 0
	for _, pat := range e.RuntimeTools {
		p := runtimeToolPath(storage.RuntimeToolCommand(pat))
		switch {
		case p == "":
			continue
		case strings.Contains(p, storage.RuntimeSlotPlaceholder) || strings.Contains(p, "${SLOT}"):
			slotPatterns++
		case !filepath.IsAbs(p):
			// Relative to the worker's cwd, which is its tree - fine.
			continue
		case len(slots) > 0 && underDir(p, projPath):
			out = append(out, check{levelWarn,
				fmt.Sprintf("runtime_tools pattern names the MAIN checkout: %s", pat),
				"workers run in a worktree slot and type the slot's path, so this literal prefix never matches there; write the path as `$SLOT/...` - pm expands it to the worker's tree on every run"})
		case !pathExists(literalDir(p)):
			out = append(out, check{levelWarn,
				fmt.Sprintf("runtime_tools pattern names a path that does not exist here: %s", pat),
				"a prefix nothing can type is a refusal waiting to happen - fix the path, or use `$SLOT/...` for a script that lives in the repo"})
		}
	}
	if slotPatterns > 0 {
		out = append(out, check{levelOK, fmt.Sprintf("runtime_tools: %d pattern(s) use $SLOT (expanded to the worker's tree per run)", slotPatterns), ""})
	}
	return out
}

// runtimeToolPath picks the path a runtime_tools command prefix names: the
// first token that looks like one, so `sh /x/rig-check.sh` and `/x/sim-ui.sh`
// both answer `/x/...`. Empty when the prefix is a bare command (`curl`,
// `xcrun simctl`).
func runtimeToolPath(cmd string) string {
	for _, tok := range strings.Fields(cmd) {
		if strings.Contains(tok, "/") || strings.HasPrefix(tok, "$") {
			return tok
		}
	}
	return ""
}

// literalDir is the longest directory prefix of a path before any glob, i.e.
// the part that must exist for the pattern to name anything.
func literalDir(p string) string {
	if i := strings.IndexAny(p, "*?["); i >= 0 {
		p = p[:i]
	}
	return filepath.Dir(p + "x")
}

// underDir reports whether p lies inside dir (both cleaned; symlinks are not
// resolved - a pattern names what the worker types, not what it resolves to).
func underDir(p, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(literalDir(p)))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
