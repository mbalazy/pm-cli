package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
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
	out = append(out, checkContextRepos(e)...)
	out = append(out, checkSlots(e, proj.Path)...)
	out = append(out, checkHandoff(e, proj.Path)...)
	return out
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

func checkHandoff(e storage.Executor, projPath string) []check {
	h := e.ResolveHandoff(projPath)
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
			fmt.Sprintf("handoff.runtime_skill %q not found under .claude/skills or .claude/commands", h.RuntimeSkill),
			"fix the name or add the skill"})
	default:
		out = append(out, check{levelOK,
			fmt.Sprintf("runtime skill /%s -> %s (%d script(s))", h.RuntimeSkill, h.SkillPath, len(h.Scripts)), ""})
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
	out = append(out, checkRuntimeDrift(e, projPath, h.PlaybookPath, text)...)
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

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
