package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// Worker envelope status vocabulary - the manager<->worker JSON contract.
//
// "verified" replaced "merged" in 0.34.0. A worker NEVER merges anything: in
// integration mode the manager does the merge after the fact, and in
// independent mode nothing is merged at all, so the word described a git
// operation the process it names cannot perform. What the worker actually
// asserts is that it implemented, reviewed and verified the work.
const (
	workerVerified = "verified"
	workerBlocked  = "blocked"
	workerFailed   = "failed"
	// workerVerifiedLegacy is the pre-0.34 spelling, accepted on input forever:
	// journal lines written before the rename still carry it, as does any worker
	// running an older prompt (e.g. a run started before an upgrade).
	workerVerifiedLegacy = "merged"
)

// normalizeWorkerStatus folds the legacy spelling into the current one, so the
// rest of the engine only ever compares against workerVerified.
func normalizeWorkerStatus(status string) string {
	if status == workerVerifiedLegacy {
		return workerVerified
	}
	return status
}

// workerResult is the JSON contract returned by a headless worker (the
// manager<->worker API). It is validated against workerResultSchema.
type workerResult struct {
	Status     string   `json:"status"` // verified | blocked | failed ("merged" accepted as legacy)
	Summary    string   `json:"summary"`
	Branch     string   `json:"branch"`
	Commits    []string `json:"commits"`
	Unresolved []string `json:"unresolved"`
	// Run stats lifted off the claude envelope by parseClaudeResult - NOT part
	// of the worker's structured-output contract (the model never emits them,
	// so they stay out of workerResultSchema). They feed the run-state +
	// journal so retros can weigh outcomes by effort/cost.
	Turns   int     `json:"turns,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Review is the review-phase telemetry the worker guard hook collected while
	// this worker ran. Same standing as Turns/CostUSD: observed by pm, never
	// claimed by the worker, and deliberately absent from workerResultSchema.
	Review *storage.ReviewTelemetry `json:"review,omitempty"`
}

// claudeEnvelope is the `claude -p --output-format json` result envelope. The
// schema-validated worker result lands in StructuredOutput.
type claudeEnvelope struct {
	Type             string        `json:"type"`
	Subtype          string        `json:"subtype"`
	IsError          bool          `json:"is_error"`
	Result           string        `json:"result"`
	SessionID        string        `json:"session_id"`
	NumTurns         int           `json:"num_turns"`
	TotalCostUSD     float64       `json:"total_cost_usd"`
	StructuredOutput *workerResult `json:"structured_output"`
}

// workerResultSchema constrains the worker's structured output to the contract.
const workerResultSchema = `{
  "type": "object",
  "properties": {
    "status": {"type": "string", "enum": ["verified", "blocked", "failed"]},
    "summary": {"type": "string"},
    "branch": {"type": "string"},
    "commits": {"type": "array", "items": {"type": "string"}},
    "unresolved": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["status", "summary", "branch", "commits", "unresolved"]
}`

func newWorkCmd(store storage.TaskStore) *cobra.Command {
	var (
		epic       bool
		dryRun     bool
		model      string
		maxTurns   int
		yolo       bool
		allowDirty bool
		timeout    time.Duration
		base       string
		additional bool
		slotPin    int
	)

	cmd := &cobra.Command{
		Use:   "work [project] <task-id>",
		Short: "Run an isolated headless worker that implements one task per the project's executor profile",
		Long: "Spawns a fresh headless `claude -p` worker in the project directory that runs the inner " +
			"loop (implement -> test -> review -> fix -> verify) on a single task, following the project's " +
			"executor profile, and returns a JSON result contract.\n\n" +
			"Standalone: ends with a draft PR. With --epic (called by the manager): commits on the current " +
			"branch, no per-sub PR.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			task, slug, err := resolveWorkTask(store, args, "work")
			if err != nil {
				return err
			}
			// Task frontmatter `model:` wins over the flag DEFAULT, but an
			// explicitly passed --model always wins over the frontmatter.
			if task.Meta.Model != "" && !cmd.Flags().Changed("model") {
				model = task.Meta.Model
			}
			warnInertFlags(stderr, "pm work", additional, slotPin, base)
			opts := workOptions{
				standalone: !epic,
				model:      model,
				maxTurns:   maxTurns,
				yolo:       yolo,
				allowDirty: allowDirty,
				timeout:    timeout,
				timeoutSet: cmd.Flags().Changed("timeout"),
				base:       base,
				additional: additional,
				errOut:     stderr,
			}

			plan, err := planWork(store, task, slug, opts)
			if err != nil {
				return err
			}

			if dryRun {
				fmt.Fprintf(stdout, "# pm work (dry-run)\nproject: %s\ntask: %s\nbranch: %s\nmode: %s\ncwd: %s\n",
					slug, task.Meta.ID, plan.branch, modeLabel(opts.standalone), plan.workDir)
				if plan.worktree {
					fmt.Fprintf(stdout, "run: ADDITIONAL worktree - first free of %d slot(s), claimed at run time (lock: %s)\n",
						len(plan.slots), ".pm-executor.lock")
					for i, s := range plan.slots {
						fmt.Fprintf(stdout, "  slot %d: %s", i+1, s.Path)
						if len(s.Env) > 0 {
							fmt.Fprintf(stdout, "  (env: %s)", strings.Join(s.Env, " "))
						}
						fmt.Fprintln(stdout)
					}
					fmt.Fprintf(stdout, "fresh branch base: %s\n", plan.base)
					if plan.prepare != "" {
						fmt.Fprintf(stdout, "prepare (before worker, in claimed slot): %s\n", plan.prepare)
					}
				} else {
					fmt.Fprintf(stdout, "run: DEFAULT (main checkout, clean-tree required)\n")
				}
				if plan.baselineCmd != "" {
					fmt.Fprintf(stdout, "baseline (captured before worker, injected into prompt): %s\n", plan.baselineCmd)
				}
				fmt.Fprintf(stdout, "timeout: %s\n", describeTimeout(plan.timeout, opts.timeoutSet, plan.proj.GetExecutor().Timeout))
				fmt.Fprintf(stdout, "\n$ claude %s\n\n", strings.Join(quoteArgs(plan.cmdArgs), " "))
				fmt.Fprintf(stdout, "=== SYSTEM PROMPT ===\n%s\n\n=== PROMPT ===\n%s\n", plan.sysPrompt, plan.prompt)
				return nil
			}

			// Worktree mode: claim a slot from the pool (first free, or --slot N),
			// ensure + seed + lock it, and point the plan at the CLAIMED slot. The
			// lock is released by the deferred closure on every exit path (success,
			// fail, panic).
			if plan.worktree {
				slot, release, err := acquireWorktreeSlot(plan.proj, slug, plan.slots, slotPin, task.Meta.ID, "work")
				if err != nil {
					return err
				}
				defer release()
				// Re-render prompt+argv for the claimed slot - the plan was built
				// against the provisional slot 1 and the prompt states the repo path.
				plan.retarget(slot.Path, slot.Env)
				if len(plan.slots) > 1 {
					fmt.Fprintf(stderr, "pm work: claimed worktree slot %s\n", slot.Path)
				}
			}

			res, err := executeWork(store, task, plan, opts)
			if err != nil {
				return err
			}

			out, _ := json.MarshalIndent(res, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return nil
		},
	}

	cmd.Flags().BoolVar(&epic, "epic", false, "epic mode: commit on the current branch, no PR (called by the manager)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "assemble and print the worker prompt + command without invoking claude")
	cmd.Flags().StringVar(&model, "model", "opus", "model for the worker (alias or full name)")
	cmd.Flags().IntVar(&maxTurns, "max-turns", 150, "max agent turns for the worker")
	cmd.Flags().BoolVar(&yolo, "yolo", false, "bypass all permission checks instead of the curated allowlist")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "skip the clean-working-tree precondition (standalone)")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultWorkerTimeout, "max wall-clock time for the worker before it is killed (overrides the project's executor.timeout)")
	cmd.Flags().StringVar(&base, "base", "", "with --additional: base the fresh task branch forks from (default: executor.base_branch, else the main checkout's current branch)")
	cmd.Flags().BoolVar(&additional, "additional", false, "run in an isolated 'additional' worktree slot (own branch/port/sim) instead of the main checkout; requires executor.worktrees (or legacy additional_worktree) in project.yaml")
	cmd.Flags().IntVar(&slotPin, "slot", 0, "with --additional: pin a specific worktree slot (1-based); default 0 = first free slot")

	return cmd
}

// resolveWorkTask resolves the project slug + task from the command args.
// One arg: task id, project auto-detected from cwd or by scanning. Two args:
// explicit project + task. cmdName is the invoking command ("work",
// "run-epic") - it only shapes the error text, so a `pm run-epic` miss does
// not tell the user to retry with `pm work`.
func resolveWorkTask(store storage.TaskStore, args []string, cmdName string) (*storage.Task, string, error) {
	if len(args) == 2 {
		slug, err := store.ResolveProject(args[0])
		if err != nil {
			return nil, "", err
		}
		t, err := store.FindTask(slug, args[1])
		if err != nil {
			return nil, "", err
		}
		return t, slug, nil
	}

	query := args[0]
	// The project of the current directory is a PREFERENCE, applied inside a
	// tier (below), not a short-circuit. It used to run FindTask on the cwd
	// project first and return whatever came back - so a TITLE mention in the
	// repo you happen to stand in beat another project's EXACT id: run
	// `pm work pm-cli-18` from a repo holding "Port pm-cli-18 learnings" and
	// the 60-minute worker committed there. Standing somewhere breaks a tie;
	// it does not outrank a stronger match.
	cwdSlug := detectProjectFromCwd(store)

	// Scan every project. FindTask also matches on TITLE
	// SUBSTRING, so a short query ("auth") can hit several projects - and
	// first-hit-wins in sorted ListProjects order silently picked one, which
	// here means spawning a 60-minute worker that commits in the WRONG repo.
	//
	// The scan keeps FindTask's OWN three-tier ranking rather than flattening
	// it: exact ID, then unique ID prefix, then title substring - each tier
	// decides alone, and a later tier is consulted only when the earlier one
	// is empty. Treating the tiers as equals would make `pm work pm-cli-18`
	// ambiguous merely because some other project has a task TITLED "... test
	// for pm-cli-18" - which is not ambiguity, it is a weaker match.
	//
	// ARCHIVED projects are excluded (GetAllTasks/pm context do the same): a
	// shelved client repo holding an imported ticket key must not make every
	// query for that key ambiguous forever. The explicit two-arg form still
	// reaches them - ResolveProject sees every project.
	projects, err := store.ListActiveProjects()
	if err != nil {
		return nil, "", err
	}
	// ...except the one you are standing in: if that project is archived, the
	// cwd says plainly which one is meant, so excluding it would be perverse.
	if cwdSlug != "" && !slices.Contains(projects, cwdSlug) {
		projects = append(projects, cwdSlug)
	}

	var exact []projectHit
	for _, slug := range projects {
		if t, err := store.FindTaskExact(slug, query); err == nil {
			exact = append(exact, projectHit{task: t, slug: slug, label: slug + "/" + t.Meta.ID})
		}
	}
	if len(exact) > 0 {
		return resolveHits(exact, cwdSlug, query, cmdName)
	}

	// Tier 2: ID prefix. Mirrors FindTask's middle tier (internal/storage/
	// store.go) - several hits INSIDE one project make that project ambiguous
	// rather than silently dropping out of the scan.
	var byIDPrefix []projectHit
	for _, slug := range projects {
		tasks, err := store.GetTasks(slug)
		if err != nil {
			continue
		}
		var hits []*storage.Task
		for _, t := range tasks {
			if strings.HasPrefix(strings.ToLower(t.Meta.ID), strings.ToLower(query)) {
				hits = append(hits, t)
			}
		}
		switch len(hits) {
		case 0:
		case 1:
			byIDPrefix = append(byIDPrefix, projectHit{task: hits[0], slug: slug, label: slug + "/" + hits[0].Meta.ID})
		default:
			byIDPrefix = append(byIDPrefix, projectHit{slug: slug, label: slug + "/<several>"})
		}
	}
	if len(byIDPrefix) > 0 {
		return resolveHits(byIDPrefix, cwdSlug, query, cmdName)
	}

	var fuzzy []projectHit
	for _, slug := range projects {
		t, err := store.FindTask(slug, query)
		switch {
		case err == nil:
			fuzzy = append(fuzzy, projectHit{task: t, slug: slug, label: slug + "/" + t.Meta.ID})
		case errors.Is(err, storage.ErrAmbiguousTask):
			// Several candidates INSIDE this project. Without this branch the
			// project contributes nothing to the scan, so a query that is
			// ambiguous in alpha and matches one task in beta silently
			// resolves to beta - first-wins again, by another route.
			fuzzy = append(fuzzy, projectHit{slug: slug, label: slug + "/<several>"})
		}
	}
	if len(fuzzy) == 0 {
		return nil, "", fmt.Errorf("task %q not found in any project (try `pm %s <project> <task-id>`)", query, cmdName)
	}
	return resolveHits(fuzzy, cwdSlug, query, cmdName)
}

// projectHit is one project's answer to a cross-project task scan. task is nil
// when the project held SEVERAL candidates (ambiguous within itself) - such a
// hit can only ever produce an error, never a resolution.
type projectHit struct {
	task  *storage.Task
	slug  string
	label string
}

// resolveHits picks the winner of ONE tier. Several projects matching equally
// is real ambiguity - except when one of them is the project the user is
// standing in, which is as explicit a statement of intent as the two-arg form.
func resolveHits(hits []projectHit, cwdSlug, query, cmdName string) (*storage.Task, string, error) {
	if len(hits) == 1 && hits[0].task != nil {
		return hits[0].task, hits[0].slug, nil
	}
	for _, h := range hits {
		if h.slug == cwdSlug && h.task != nil {
			return h.task, h.slug, nil
		}
	}
	labels := make([]string, len(hits))
	several := false
	for i, h := range hits {
		labels[i] = h.label
		if h.task == nil {
			several = true
		}
	}
	// The remedy has to fit the failure: for a query that is ambiguous INSIDE
	// a project, `pm work <project> <query>` just fails the same way again -
	// what is needed there is a narrower query, not a project name.
	remedy := fmt.Sprintf("disambiguate with `pm %s <project> <task-id>`", cmdName)
	if several {
		remedy = fmt.Sprintf("use the exact task id, or `pm %s <project> <task-id>` for a project that matched once", cmdName)
	}
	return nil, "", fmt.Errorf("ambiguous task %q: matches %s (%s)",
		query, strings.Join(labels, ", "), remedy)
}

// resolveWorkBranch returns the branch a worker commits on: the task's explicit
// branch, or a semantic feat/<slug> derived from the title.
func resolveWorkBranch(t *storage.Task) string {
	if t.Meta.Branch != "" {
		return t.Meta.Branch
	}
	return "feat/" + storage.Slugify(t.Meta.Title)
}

// workOptions are the knobs shared by the `pm work` command and the manager
// (`pm run-epic`). standalone=false is epic mode (the manager owns git; the
// worker only commits on the already-checked-out branch).
type workOptions struct {
	standalone bool
	model      string
	maxTurns   int
	yolo       bool
	allowDirty bool
	timeout    time.Duration
	// timeoutSet = the caller passed --timeout explicitly, so it outranks the
	// project's executor.timeout. Without the flag being distinguishable from its
	// own default, a project profile could never win: the default is always
	// "present" (see resolveWorkerTimeout).
	timeoutSet bool
	base       string // with additional: base branch the fresh task branch forks from
	additional bool   // opt in to an isolated "additional" worktree slot for THIS run
	// slotDir/slotEnv: the worktree slot the CALLER already claimed (the epic
	// manager claims once for the whole run). When set, planWork targets this
	// slot instead of the pool - re-resolving per sub could pick a different
	// slot than the one the manager locked and runs in.
	slotDir string
	slotEnv []string
	// independent = the sub belongs to an independent (batch) epic: the worker
	// runs best-effort (never gives up early, records assumptions + handoff in
	// unresolved) and a non-green result does NOT park the task on waiting - a
	// human returns to every task in the batch anyway.
	independent bool
	// baseline: the pre-rendered "## Verification baseline" prompt section,
	// captured ONCE by the epic manager (executor.baseline on the fork base) and
	// shared by every sub - N subs must not mean N baseline runs. Empty for
	// standalone `pm work`, which captures its own baseline in executeWork
	// (after branch setup + prepare, so it measures exactly what the worker
	// forked from).
	baseline string
	// runWriter: the epic manager's run-state writer, so a sub's worker can
	// heartbeat the SHARED epic-level run-state while it runs. Nil for
	// standalone `pm work` (it owns its own run-state, created in executeWork)
	// and for a bare `pm work --epic` with no manager (no run-state at all).
	runWriter *storage.RunWriter
	// errOut: where this run's progress and warnings go. The cobra commands
	// point it at cmd.ErrOrStderr() so the executor is testable; the zero value
	// falls back to the process's own stderr, so a caller that does not care (a
	// test building workOptions by hand) needs no wiring. There is no stdout
	// counterpart on purpose: everything a run prints to stdout is printed by
	// RunE itself, which has cmd.OutOrStdout() at hand.
	errOut io.Writer
}

// stderr resolves the run's progress writer, defaulting to the process stream.
// Resolved per call, never cached: a test that swaps os.Stderr for a pipe
// around a call still sees its output.
func (o workOptions) stderr() io.Writer {
	if o.errOut != nil {
		return o.errOut
	}
	return os.Stderr
}

// workPlan is the resolved, ready-to-run worker invocation: the project, the
// branch the worker commits on, and the assembled prompts/argv.
type workPlan struct {
	proj      *storage.Project
	branch    string
	sessionID string // pinned via `claude --session-id` so the transcript path is known up front
	prompt    string
	sysPrompt string
	cmdArgs   []string

	// Worktree fields (populated only when THIS run opted in via --additional).
	// worktree = this run uses an isolated "additional" worktree slot. workDir is
	// the dir the worker runs in AND all git ops target: the claimed worktree
	// slot when opted in, else proj.Path (the main checkout). base is the
	// resolved branch the fresh task branch forks from (precedence: --base >
	// executor.base_branch > main checkout's current branch). env is the extra
	// KEY=VALUE pairs injected into the worker. slots is the resolved slot pool
	// when the slot is NOT claimed yet (standalone runs claim at run start, after
	// the dry-run gate); until then workDir/env provisionally point at slot 1.
	// With opts.slotDir set (epic subs) the slot is already claimed and slots is
	// nil.
	workDir  string
	worktree bool
	base     string
	env      []string
	slots    []storage.ResolvedWorktree
	// prepare = executor.prepare, run in the claimed worktree after branch setup
	// and before the worker (worktree mode only). Empty = skip.
	prepare string
	// buildPrompt re-renders the worker prompt for a given work dir; opts are
	// the options the plan was assembled with. Together they let retarget()
	// rebuild prompt+argv after the standalone slot claim lands.
	buildPrompt func(dir string) string
	opts        workOptions
	// baselineCmd = executor.baseline to capture in executeWork right before the
	// worker (standalone runs only - epic subs receive the manager's shared
	// capture via opts.baseline instead, already baked into prompt/cmdArgs).
	baselineCmd string
	// guard is what the PreToolUse hook needs to know about this run: where to
	// record subagent spawns (telemetryPath - keyed by sessionID, so it is known
	// before the worker starts and two concurrent workers never share a file;
	// it lives in the pm data dir next to the run-state, NOT in the git repo),
	// which commit to size the reviewer cap against (diffBase - pinned right
	// before the worker spawns, exact in a way a branch name is not, since that
	// drifts as the worker commits, and a merge-base against a trunk whose name
	// a hook cannot know is not computable at all), and what reviewer subagents
	// run on (reviewModel).
	guard guardOptions
	// timeout is the RESOLVED wall-clock ceiling for this worker (see
	// resolveWorkerTimeout): the run's --timeout when it was passed, else the
	// project's executor.timeout, else the flag's default. Resolved here, in the
	// side-effect-free half, so --dry-run prints the same number the worker is
	// actually given - the whole point of putting the value in project.yaml is
	// that nobody has to remember which one is in force.
	timeout time.Duration
}

// defaultWorkerTimeout is the built-in per-worker ceiling: what a run gets when
// neither --timeout nor the project's executor.timeout says otherwise. Named
// rather than repeated as a literal because three places now have to agree on
// it - both commands' flag defaults and what `pm executor show` reports for a
// project that has not set the field.
const defaultWorkerTimeout = 120 * time.Minute

// resolveWorkerTimeout applies the run's timeout precedence: an explicit
// --timeout beats the project's executor.timeout, which beats the flag's own
// default (120m, the value flagValue already carries when the flag was not
// passed). flagSet is what makes the middle term reachable at all - the flag
// always has a value, so "the flag says 120m" and "nobody said anything" are the
// same string and only cobra can tell them apart.
func resolveWorkerTimeout(flagValue time.Duration, flagSet bool, configured storage.Duration) time.Duration {
	if flagSet || configured <= 0 {
		return flagValue
	}
	return configured.Duration()
}

// describeTimeout renders the resolved ceiling AND which of the three sources
// won it, for the dry-runs. The number alone would be the least useful half:
// the reason a project can carry the value is so nobody has to know whether a
// flag was passed, and that is exactly what this prints back.
func describeTimeout(resolved time.Duration, flagSet bool, configured storage.Duration) string {
	switch {
	case flagSet:
		return fmt.Sprintf("%s (--timeout)", resolved)
	case configured > 0:
		return fmt.Sprintf("%s (executor.timeout)", resolved)
	default:
		return fmt.Sprintf("%s (built-in default)", resolved)
	}
}

// warnInertFlags surfaces flag combinations that silently do nothing. A
// warning on stderr, never an error: the exit code has to stay what it is so
// existing scripts keep working. `--slot` only selects from the worktree pool,
// and `pm work --base` is only consumed when a fresh task branch is created in
// a worktree - both are inert without --additional. `pm run-epic --base` is NOT
// (it is the integration branch's fork point in every mode), so the manager
// passes an empty base here.
func warnInertFlags(w io.Writer, cmdName string, additional bool, slotPin int, base string) {
	if additional {
		return
	}
	if slotPin != 0 {
		fmt.Fprintf(w, "%s: --slot %d ignored without --additional (slots exist only in the worktree pool)\n", cmdName, slotPin)
	}
	if strings.TrimSpace(base) != "" {
		fmt.Fprintf(w, "%s: --base %s ignored without --additional (the branch continues from the current checkout)\n", cmdName, base)
	}
}

// preflightProject resolves the project for an executor run and enforces the
// three preconditions every entry point shares: a configured repo path, that
// path being a git repo, and the executor being enabled. planWork and the
// run-epic manager carried byte-identical copies of this block (only the
// not-a-git-repo wording differed) - one copy now, so a rule can no longer be
// tightened in one command and forgotten in the other.
func preflightProject(store storage.TaskStore, slug string) (*storage.Project, storage.Executor, error) {
	proj, err := store.GetProject(slug)
	if err != nil {
		return nil, storage.Executor{}, fmt.Errorf("load project %s: %w", slug, err)
	}
	if proj.Path == "" {
		return nil, storage.Executor{}, fmt.Errorf("project %s has no path - the executor needs a git repo (set `path` in project.yaml)", slug)
	}
	if !isGitRepo(proj.Path) {
		return nil, storage.Executor{}, fmt.Errorf("project path %s is not a git repository - the executor only runs on git projects", proj.Path)
	}
	exc := proj.GetExecutor()
	if !exc.Enabled {
		return nil, storage.Executor{}, fmt.Errorf("executor disabled for project %s (executor.enabled: false)", slug)
	}
	return proj, exc, nil
}

// errNoWorktreeSlots is the ONE wording for "--additional was asked for but the
// project configures no slots" - `pm work`, `pm run-epic` and the slot claim
// itself all raise it, so the fix instructions cannot drift apart.
func errNoWorktreeSlots(slug string) error {
	return fmt.Errorf("--additional requested but project %s has no worktree slots configured - set `executor.worktrees` (or legacy `additional_worktree: true` + worktree_path/env) in project.yaml", slug)
}

// planWork validates preconditions and assembles everything needed to invoke a
// worker for task, without touching git or spending tokens. Used by both the
// dry-run path and the real run.
func planWork(store storage.TaskStore, task *storage.Task, slug string, opts workOptions) (*workPlan, error) {
	proj, exec, err := preflightProject(store, slug)
	if err != nil {
		return nil, err
	}

	var parent *storage.Task
	if task.Meta.Parent != "" {
		if p, err := store.FindTask(slug, task.Meta.Parent); err == nil {
			parent = p
		}
	}

	branch := resolveWorkBranch(task)
	sessionID := storage.NewSessionID()

	// An isolated "additional" worktree slot is opt-in PER RUN via --additional;
	// the default is the main checkout (unchanged pre-worktree behaviour). No
	// side effects here - the slot is claimed + locked in acquireWorktreeSlot at
	// run time (not on the dry-run path). Resolved BEFORE the prompt is built,
	// because the prompt states the repo path the worker runs in.
	workDir := proj.Path
	base := ""
	env := []string(nil)
	var slots []storage.ResolvedWorktree
	if opts.additional {
		// Base precedence: --base > executor.base_branch > main checkout's current
		// branch. Resolved here (a read, no side effect) so --dry-run shows it too.
		cur, curErr := gitCurrentBranch(proj.Path)
		base = resolveWorktreeBase(opts.base, exec.BaseBranch, cur)
		// Only standalone runs actually consume plan.base (executeWork's
		// gitFreshBranch call, gated on opts.standalone) - an epic sub's base
		// comes from the manager (epicBranch/baseBranch in run_epic.go) and never
		// reads this field. Scoping the hard-error to standalone means a failed
		// gitCurrentBranch read on the main checkout can't fail an epic sub over
		// a value it was never going to use.
		if base == "" && opts.standalone {
			return nil, fmt.Errorf("cannot resolve base branch for %s: git rev-parse --abbrev-ref HEAD at %s failed: %v - pass --base or set executor.base_branch", slug, proj.Path, curErr)
		}
		if opts.slotDir != "" {
			// The caller (epic manager) already claimed a slot - target it.
			workDir = opts.slotDir
			env = opts.slotEnv
		} else {
			slots = exec.ResolveWorktrees(proj.Path)
			if len(slots) == 0 {
				return nil, errNoWorktreeSlots(slug)
			}
			// Provisional until a slot is claimed at run start.
			workDir = slots[0].Path
			env = slots[0].Env
		}
	}

	// The prompt states the dir the worker runs in. For a standalone
	// --additional run the real slot is claimed AFTER planning (workDir here is
	// provisionally slot 1), so the plan carries a rebuild closure and the
	// caller re-renders the prompt once the claim lands (see retarget).
	buildPrompt := func(dir string) string {
		p := buildWorkerPrompt(task, parent, proj, slug, exec, branch, dir, opts.standalone)
		// Epic subs: the manager captured the baseline once for the whole run -
		// bake its section into this sub's prompt here.
		if opts.baseline != "" {
			p += "\n" + opts.baseline
		}
		return p
	}
	prompt := buildPrompt(workDir)
	sysPrompt := buildWorkerSystemPrompt(exec, opts.standalone, opts.independent)
	guard := guardOptions{
		telemetryPath: storage.ReviewTelemetryPath(store.ProjectDir(task.Project), sessionID),
		reviewModel:   exec.ResolveReviewModel(),
		fixRounds:     exec.FixRounds,
	}
	cmdArgs := buildClaudeArgs(prompt, sysPrompt, sessionID, opts.model, opts.maxTurns, opts.yolo, guard, workDir, false)

	// Standalone only: the epic manager runs prepare ITSELF, once per run,
	// right after claiming the slot - not per sub (5 subs must not mean 5
	// dependency installs). Same split for the baseline capture.
	prepare := ""
	baselineCmd := ""
	if opts.standalone {
		if opts.additional {
			prepare = strings.TrimSpace(exec.Prepare)
		}
		if opts.baseline == "" {
			baselineCmd = strings.TrimSpace(exec.Baseline)
		}
	}

	return &workPlan{
		proj: proj, branch: branch, sessionID: sessionID,
		prompt: prompt, sysPrompt: sysPrompt, cmdArgs: cmdArgs,
		workDir: workDir, worktree: opts.additional, base: base, env: env, slots: slots,
		prepare: prepare, baselineCmd: baselineCmd, guard: guard,
		buildPrompt: buildPrompt, opts: opts,
		timeout: resolveWorkerTimeout(opts.timeout, opts.timeoutSet, exec.Timeout),
	}, nil
}

// retarget points the plan at the worktree slot the run actually claimed and
// re-renders everything derived from the work dir (prompt + argv). Without
// this, a worker in slot N would be told "Repo path: <main checkout>" (or
// slot 1's path) and could follow that absolute path straight out of its
// isolation.
func (p *workPlan) retarget(dir string, env []string) {
	p.workDir = dir
	p.env = env
	p.prompt = p.buildPrompt(dir)
	p.cmdArgs = buildClaudeArgs(p.prompt, p.sysPrompt, p.sessionID, p.opts.model, p.opts.maxTurns, p.opts.yolo, p.guard, dir, false)
}

// workerHeartbeatInterval is how often a live worker's run-state is re-stamped.
// A var, not a const, purely so tests can shrink it - production never changes
// it (the signal is deliberately invisible to the user: no flag, no config).
var workerHeartbeatInterval = storage.HeartbeatInterval

// executeWork runs a planned worker and records the result into pm. In
// standalone mode it owns the branch (clean-tree precondition + checkout); in
// epic mode the manager has already checked out the branch.
func executeWork(store storage.TaskStore, task *storage.Task, plan *workPlan, opts workOptions) (*workerResult, error) {
	// dir is the worker cwd and the target of every git op: the "additional"
	// worktree in worktree mode, else the main checkout.
	dir := plan.workDir
	if opts.standalone {
		if plan.worktree {
			// The "additional" worktree is reused across tasks. Give this task a
			// FRESH branch forked from the resolved base branch's current tip, first
			// wiping any leftovers (uncommitted or partially-committed work from a
			// prior, possibly killed, run) so nothing bleeds into it. Ignored
			// deps/configs survive. Base was resolved in planWork (--base >
			// executor.base_branch > main checkout's current branch).
			if err := gitFreshBranch(dir, plan.branch, plan.base); err != nil {
				return nil, fmt.Errorf("prepare fresh branch %s (base %s): %w", plan.branch, plan.base, err)
			}
		} else {
			// Non-worktree: the user's own checkout. Enforce the clean-tree
			// precondition and continue/create the branch in place.
			if !opts.allowDirty {
				if err := requireCleanWorkingTree(dir); err != nil {
					return nil, err
				}
			}
			if err := gitCheckoutBranch(dir, plan.branch); err != nil {
				return nil, fmt.Errorf("prepare branch %s: %w", plan.branch, err)
			}
		}
	}

	// Make the worktree's dependencies current BEFORE the worker spends turns
	// discovering they aren't (executor.prepare, worktree mode only, cwd = the
	// claimed slot). A failed prepare aborts the run - the worker would fail on
	// the same broken deps anyway, just slower and less legibly.
	if plan.worktree && plan.prepare != "" {
		fmt.Fprintf(opts.stderr(), "pm work: prepare in %s: %s\n", dir, plan.prepare)
		if err := runPrepare(dir, plan.prepare); err != nil {
			return nil, fmt.Errorf("prepare cmd (%s) failed in %s: %w", plan.prepare, dir, err)
		}
	}

	// Capture the verification baseline (executor.baseline) on the branch the
	// worker is about to start from, so the worker can judge its verify verdict
	// on NEW failures only instead of rediscovering (and being failed by)
	// pre-existing breakage. Runs after branch setup + prepare - it measures
	// exactly the state the work forks from. Standalone runs only; epic subs
	// carry the manager's once-per-run capture, already baked in by planWork.
	baselineUsed := ""
	if plan.baselineCmd != "" {
		fmt.Fprintf(opts.stderr(), "pm work: baseline in %s: %s\n", dir, plan.baselineCmd)
		if section := captureBaseline(opts.stderr(), dir, plan.baselineCmd); section != "" {
			plan.prompt += "\n" + section
			plan.cmdArgs = buildClaudeArgs(plan.prompt, plan.sysPrompt, plan.sessionID, opts.model, opts.maxTurns, opts.yolo, plan.guard, dir, false)
			baselineUsed = plan.baselineCmd
		}
	}

	// Run-state for observability (standalone only; in epic mode the manager
	// owns the epic-level run-state and updates the per-sub entry). The run-state
	// lives in the pm data dir (where the TUI reads it), NOT the git repo. Every
	// WriteRunState below is best-effort: a failed write only costs a stale
	// dashboard, never correctness, so the error is dropped.
	stateDir := store.ProjectDir(task.Project)
	journalDir := ""
	if plan.worktree {
		journalDir = dir
	}
	var runw *storage.RunWriter
	// One id for this physical run, stamped on the run-state and on every
	// journal line below (see storage.NewRunID). Epic subs are journaled by the
	// manager under ITS run id, so this only matters standalone.
	runID := storage.NewRunID()
	if opts.standalone {
		// Close any earlier crash of this project that never got a terminal
		// line, BEFORE this run writes its own run-state below: the file is
		// keyed by task id, so a re-run of the same task - the most common
		// follow-up to a crash - would otherwise overwrite the dead run's
		// forensics one step before the reconciler looks for them.
		reportReconciledCrashes(opts.stderr(), stateDir, "pm work")
		run := &storage.RunState{
			TaskID:         task.Meta.ID,
			RunID:          runID,
			Project:        task.Project,
			Kind:           storage.RunKindWork,
			Status:         storage.RunStatusRunning,
			PID:            os.Getpid(),
			RepoPath:       dir,
			Started:        time.Now().UTC().Format(time.RFC3339),
			LogPath:        storage.ExecutorLogPath(stateDir, task.Meta.ID),
			CurrentSub:     task.Meta.ID,
			CurrentSession: plan.sessionID,
			Phase:          "running",
			Subs:           []storage.SubRun{{ID: task.Meta.ID, Status: storage.RunStatusRunning, Session: plan.sessionID}},
		}
		// From here on the run-state is only touched through the writer - the
		// heartbeat goroutine below shares this exact struct.
		runw = storage.NewRunWriter(stateDir, run)
		_ = runw.Update(nil)
		// Journal start line (durable cross-run history; epic subs are journaled
		// by the manager instead). Best-effort like the run-state writes.
		start := storage.JournalEntry{
			Event: storage.JournalEventStart, Kind: "work", Project: task.Project, TaskID: task.Meta.ID, RunID: runID,
			PID: os.Getpid(), Model: opts.model, Additional: opts.additional, Yolo: opts.yolo, Branch: plan.branch,
			WorkDir: journalDir, Baseline: baselineUsed,
		}
		_ = storage.AppendJournal(stateDir, &start)
		// From here until the end line, a catchable signal journals its own
		// terminal line instead of leaving the start line orphaned.
		defer armCrashJournal(stateDir, start)()
	}

	fmt.Fprintf(opts.stderr(), "pm work: launching headless worker for %s on %s (%s)...\n", task.Meta.ID, plan.branch, modeLabel(opts.standalone))

	workStart := time.Now()
	// journalEnd pairs the start line above with exactly one terminal line; the
	// three exit paths below differ only in status/error and the sub's stats.
	journalEnd := func(status, errMsg string, sub storage.JournalSub) {
		sub.ID = task.Meta.ID
		sub.Branch = plan.branch
		sub.DurationS = int(time.Since(workStart).Seconds())
		_ = storage.AppendJournal(stateDir, &storage.JournalEntry{
			Event: storage.JournalEventEnd, Kind: "work", Project: task.Project, TaskID: task.Meta.ID, RunID: runID,
			PID: os.Getpid(), Model: opts.model, Additional: opts.additional, Yolo: opts.yolo, Branch: plan.branch,
			WorkDir: journalDir, Baseline: baselineUsed,
			Status: status, DurationS: sub.DurationS, Error: errMsg,
			Subs: []storage.JournalSub{sub},
		})
	}

	// Heartbeat: while the worker runs, nothing else stamps the run-state, so a
	// 40-minute sub looks exactly like a hung one to an observer. Re-stamp the
	// run-state on a ticker for exactly as long as the worker lives - the
	// standalone run's own state, or (epic) the manager's shared epic-level one.
	// stopHeartbeat waits for the goroutine to exit, so after it returns nothing
	// can touch the file again.
	hbw := opts.runWriter
	if runw != nil {
		hbw = runw
	}
	// Create the telemetry file before the worker starts, so a worker that
	// spawned nobody leaves an empty file rather than no file - see
	// storage.InitReviewTelemetry.
	storage.InitReviewTelemetry(plan.guard.telemetryPath)
	// Pin the commit the worker starts from. Everything after this is the
	// worker's own diff, which is what the guard sizes the reviewer cap against.
	// Best-effort: without it the cap degrades to allow-everything, exactly as
	// before the cap existed.
	if sha := gitHeadSHA(dir); sha != "" && sha != plan.guard.diffBase {
		plan.guard.diffBase = sha
		plan.cmdArgs = buildClaudeArgs(plan.prompt, plan.sysPrompt, plan.sessionID, opts.model, opts.maxTurns, opts.yolo, plan.guard, dir, false)
	}
	stopHeartbeat := hbw.Heartbeat(workerHeartbeatInterval)
	// Stopping is idempotent, so the defer only matters if runWorker panics -
	// without it a panic would leave a goroutine stamping "running" forever.
	defer stopHeartbeat()
	// The worker leads its own process group, so this pgid is the only handle
	// anything outside this process has on it. Publishing it lets the board kill
	// the worker tree directly when it has to SIGKILL the manager - a signal the
	// manager cannot forward (see storage.RunState.Kill).
	res, sessionID, err := runWorker(opts.stderr(), dir, plan.cmdArgs, plan.timeout, plan.proj.ResolveClaudeConfigDir(), plan.env, plan.sessionID,
		func(pgid int) { _ = hbw.Update(func(run *storage.RunState) { run.WorkerPGID = pgid }) })
	stopHeartbeat()
	// The worker is gone and the heartbeat died with it, so drop the in-flight
	// session marker in the same breath. It is what an observer gates the
	// heartbeat age on (see renderExecutorDashboard), and the post-worker tail -
	// applyWorkerResult's project flock, then the manager's merge or `git push` -
	// can run for minutes; leaving the marker set there would render a frozen
	// stamp as a live beat, i.e. a healthy run looking hung.
	// WorkerPGID goes with it: the group is gone, and a stale pgid left on the
	// run-state is a pid the board would signal on the next kill.
	_ = hbw.Update(func(run *storage.RunState) {
		run.CurrentSession = ""
		run.WorkerPGID = 0
	})
	// Review telemetry is collected here, off the file the guard hook appended to
	// during the run - BEFORE the error branches, because a worker that died is
	// exactly the one whose review behaviour is worth having a record of. Keyed
	// on plan.sessionID (what pm pinned and baked into the hook command), not on
	// the id the envelope reports, which a dead worker never sends.
	telemetry := storage.CollectReviewTelemetry(store.ProjectDir(task.Project), plan.sessionID)
	// How much of the sub's own work no reviewer ever saw. Measured HERE because
	// this is the only place that knows both halves: the last tip a reviewer could
	// see (telemetry, from the hook) and the tip the sub actually ended on. It has
	// to run before the manager merges or pushes anything, or the count would
	// include commits that are not the sub's.
	countUnreviewedCommits(dir, telemetry)
	if res != nil {
		res.Review = telemetry
	}
	if err != nil {
		// No envelope arrived, so this sub's turns and cost are 0 - and a sub
		// that burned an hour then died is, in the journal, indistinguishable
		// from one nobody started. Read the effort back off the worker's own
		// transcript instead. Recorded on the run-state by id, which serves both
		// callers: standalone (one sub) and the epic manager's shared state,
		// whose journalSubs lifts it from there.
		deadEffort := recoverWorkerEffort(plan.proj.ResolveClaudeConfigDir(), dir, plan.sessionID)
		if deadEffort != nil {
			fmt.Fprintf(opts.stderr(), "pm work: %s\n", describeEffort(deadEffort))
			_ = hbw.Update(func(run *storage.RunState) { setSubEffort(run, task.Meta.ID, deadEffort) })
		}
		if runw != nil {
			_ = runw.Update(func(run *storage.RunState) {
				run.Status = storage.RunStatusFailed
				run.Phase = ""
				run.Error = err.Error()
				if len(run.Subs) > 0 {
					run.Subs[0].Status = storage.RunStatusFailed
					run.Subs[0].Note = err.Error()
					run.Subs[0].Review = telemetry
				}
			})
			journalEnd(storage.RunStatusFailed, err.Error(),
				storage.JournalSub{Result: "failed", Note: err.Error(), Session: plan.sessionID, Review: telemetry, Effort: deadEffort})
		}
		return nil, err
	}
	if err := applyWorkerResult(opts.stderr(), store, task, plan.branch, sessionID, res, opts.standalone, opts.independent); err != nil {
		if runw != nil {
			_ = runw.Update(func(run *storage.RunState) {
				run.Status = storage.RunStatusFailed
				run.Phase = ""
				run.Error = err.Error()
				if len(run.Subs) > 0 {
					run.Subs[0].Status = storage.RunStatusFailed
					run.Subs[0].Note = err.Error()
					run.Subs[0].Turns = res.Turns
					run.Subs[0].CostUSD = res.CostUSD
					run.Subs[0].Review = telemetry
				}
			})
			// The worker itself succeeded here (only the post-processing
			// applyWorkerResult write failed), so res carries real turns/cost
			// off the claude envelope - unlike the runWorker-failure branch
			// above, where res is nil and those fields stay zero.
			journalEnd(storage.RunStatusFailed, err.Error(),
				storage.JournalSub{Result: "failed", Note: err.Error(), Session: sessionID, Turns: res.Turns, CostUSD: res.CostUSD, Review: telemetry})
		}
		return nil, err
	}
	if runw != nil {
		_ = runw.Update(func(run *storage.RunState) {
			run.Status = storage.RunStatusDone
			run.Phase = ""
			run.CurrentSub = ""
			run.CurrentSession = ""
			if len(run.Subs) > 0 {
				run.Subs[0].Status = res.Status
				run.Subs[0].Note = strings.TrimSpace(res.Summary)
				run.Subs[0].Commits = res.Commits
				run.Subs[0].Turns = res.Turns
				run.Subs[0].CostUSD = res.CostUSD
				run.Subs[0].Review = telemetry
			}
		})
		journalEnd(storage.RunStatusDone, "",
			storage.JournalSub{Result: res.Status, Note: strings.TrimSpace(res.Summary), Session: sessionID, Turns: res.Turns, CostUSD: res.CostUSD, Review: telemetry})
	}
	return res, nil
}

func modeLabel(standalone bool) string {
	if standalone {
		return "standalone"
	}
	return "epic"
}

// claudeRun is what genuinely differs between the two kinds of headless run pm
// spawns: the WORKER (`pm work` / `pm run-epic`) and the ACCEPTANCE
// (`pm finish`). Everything else about the invocation - the config-dir pinning,
// the process group, the guard hook - is identical for both, so only the pieces
// that vary live here.
type claudeRun struct {
	prompt    string
	sysPrompt string
	sessionID string
	model     string
	maxTurns  int
	yolo      bool
	// schema validates the run's structured output. It is a parameter rather
	// than a constant inside buildClaudeArgsFor because the two contracts are
	// different shapes: an acceptance forced into workerResultSchema would have
	// to drop its per-sub verdicts, and with them the visual_claims_open counts
	// that are the whole point of accepting a batch nobody watched.
	schema string
	// agents is the --agents payload; empty attaches none. The acceptance run
	// has no review phase, so defining a reviewer agent type for it would
	// describe an agent nothing spawns.
	agents string
	// allowedTools/disallowedTools are the curated permission lists used when
	// yolo is off. They are parameters because the two runs need different
	// tools: an implementer edits and builds, an acceptance run's whole first
	// move is invoking a Skill.
	allowedTools    string
	disallowedTools string
	guard           guardOptions
	// workDir is the repo the run executes in - its .mcp.json, when present,
	// is the one MCP config a headless run keeps when userMCP is false.
	workDir string
	// userMCP keeps the user's whole MCP menu instead of cutting it with
	// --strict-mcp-config. Workers never need it; the acceptance run does -
	// its procedure (batch-finish) reaches pm through user-scope MCP.
	userMCP bool
}

// buildClaudeArgs assembles the `claude -p` argv for a worker run. workDir is
// the repo the worker runs in (its .mcp.json, when present, is the one MCP
// config a worker keeps); workers never keep user-scope MCP.
func buildClaudeArgs(prompt, sysPrompt, sessionID, model string, maxTurns int, yolo bool, guard guardOptions, workDir string, userMCP bool) []string {
	return buildClaudeArgsFor(claudeRun{
		prompt: prompt, sysPrompt: sysPrompt, sessionID: sessionID,
		model: model, maxTurns: maxTurns, yolo: yolo,
		schema: workerResultSchema,
		// The reviewer agent type is defined by pm, never by a file in the
		// project repo: `pm work` requires a clean tree, so a `.claude/agents/`
		// file written per run would dirty it on every single one.
		agents:          reviewerAgentsJSON(guard.reviewModel),
		allowedTools:    workerAllowedTools,
		disallowedTools: workerDisallowedTools,
		guard:           guard,
		workDir:         workDir,
		userMCP:         userMCP,
	})
}

// buildClaudeArgsFor assembles the `claude -p` argv for any headless run.
func buildClaudeArgsFor(r claudeRun) []string {
	args := []string{
		"-p", r.prompt,
		"--append-system-prompt", r.sysPrompt,
		"--output-format", "json",
		"--json-schema", r.schema,
		"--model", r.model,
		"--max-turns", fmt.Sprintf("%d", r.maxTurns),
	}
	if r.sessionID != "" {
		args = append(args, "--session-id", r.sessionID)
	}
	// MCP scope: without this a headless worker inherits the USER's whole MCP
	// menu - measured on run pm-cli-100 as ~180 deferred tool names plus their
	// servers' instructions in EVERY turn, of which the four workers called
	// exactly zero. --strict-mcp-config drops every MCP config except those
	// passed via --mcp-config, so the project's own .mcp.json (checked into the
	// repo, and possibly load-bearing for its workers) is passed back in when
	// it exists. Skills are a separate mechanism and are untouched (measured
	// 2026-08-08 on claude 2.1.226: 78 skills listed before AND after, MCP
	// tools 180 -> 0, turn-1 context 70,270 -> 65,017 tokens) - pm finish's
	// reliance on global skills survives this flag.
	if !r.userMCP {
		args = append(args, "--strict-mcp-config")
		if r.workDir != "" {
			if mcp := filepath.Join(r.workDir, ".mcp.json"); fileExists(mcp) {
				args = append(args, "--mcp-config", mcp)
			}
		}
	}
	// The hook guard rides along in BOTH modes: --yolo waives permission prompts,
	// not the project's right to have its commits pass its own hooks. --settings
	// merges with the repo's settings.json rather than replacing it, so the
	// project's own hooks keep firing (verified against a probe repo).
	//
	// Claude Code materializes this inline JSON into /tmp/claude-settings-<uuid>.json
	// and does not remove it, so a machine that has run N workers carries N of
	// these ~110-byte files. Harmless, but do not go hunting for what wrote them.
	if settings := workerGuardSettings(r.guard); settings != "" {
		args = append(args, "--settings", settings)
	}
	if r.agents != "" {
		args = append(args, "--agents", r.agents)
	}
	if r.yolo {
		args = append(args, "--dangerously-skip-permissions")
	} else {
		args = append(args,
			"--permission-mode", "acceptEdits",
			"--allowedTools", r.allowedTools,
			"--disallowedTools", r.disallowedTools,
		)
	}
	return args
}

// workerAllowedTools is the curated allowlist: file edits (acceptEdits), review
// subagents, and the bash families a build/test/review/pr loop needs.
//
// The `uv run` entries are spelled per subcommand rather than as `Bash(uv:*)`,
// and that is not fussiness. A uv-managed project (pyproject.toml + uv.lock) has
// no PATH-visible pytest/ruff/mypy at all: they live in .venv and the only way
// in is `uv run`. Without these entries the worker's ONLY reachable gate is a
// `make` target, so it cannot narrow a failure to one test file - measured on
// platform.orbit (2026-08-18), where every check is `uv run` behind a
// Makefile. But a blanket `Bash(uv:*)` would also hand it `uv run git push
// --force` and `uv run gh pr merge`: the disallow patterns below match on the
// leading tokens, and the PreToolUse guard (worker_guard.go) reads the whole
// command only for HOOK bypasses, not for force-push or merge. Naming the
// subcommands keeps the envelope shut. `uv run python` opens no new ground -
// `Bash(python:*)` is already here.
const workerAllowedTools = "Edit Write Read Grep Glob Task TodoWrite " +
	"Bash(git:*) Bash(gh:*) Bash(go:*) Bash(make:*) Bash(yarn:*) Bash(npm:*) Bash(npx:*) Bash(pnpm:*) " +
	"Bash(node:*) Bash(jest:*) Bash(vitest:*) Bash(eslint:*) Bash(biome:*) Bash(tsc:*) Bash(prettier:*) " +
	"Bash(cargo:*) Bash(python:*) Bash(python3:*) Bash(pytest:*) Bash(ruff:*) Bash(mypy:*) " +
	"Bash(uv sync:*) Bash(uv lock:*) Bash(uv run pytest:*) Bash(uv run ruff:*) Bash(uv run mypy:*) " +
	"Bash(uv run ty:*) Bash(uv run python:*) Bash(uv run alembic:*) Bash(uv run pre-commit:*) " +
	"Bash(cd:*) Bash(ls:*) Bash(cat:*) Bash(grep:*) Bash(rg:*) Bash(find:*) Bash(echo:*) Bash(sed:*) Bash(awk:*)"

// workerDisallowedTools enforces the autonomy envelope: never force-push, never
// merge to main, never hard-reset, never neuter the project's git hooks.
//
// The hook entries are not theoretical. A worker whose pre-commit hook died on a
// tool missing from the runner (`gitleaks: not found`) tried the legitimate
// workarounds first - `PATH=... git commit`, `export PATH=...` - and the
// allowlist denied every one of them, because each needs a shell construct the
// envelope withholds. The one form it did NOT deny was
// `git -c core.hooksPath=/dev/null commit`, so that is what landed: an
// unreviewed commit that reads as hook-checked and never went through secret
// scanning. The envelope must not leave the forbidden door as the only open one.
//
// Every pattern here was verified against a live `claude -p` in a probe repo
// (2026-08-06), because these rules match on WHOLE TOKENS, not raw string
// prefixes, and guessing gets it wrong in both directions:
//   - `Bash(git -c core.hooksPath:*)` does NOT match
//     `git -c core.hooksPath=/dev/null commit` (the token carries the `=value`),
//     so it is spelled `Bash(git -c *)` instead - which also costs the worker
//     benign `git -c user.name=...`, an acceptable trade for an airtight rule.
//   - Prefix matching cannot see a flag that comes AFTER other arguments:
//     `git commit -m x --no-verify` runs under every pattern below. That hole is
//     closed by the PreToolUse guard (see worker_guard.go), which reads the whole
//     command; these patterns are the cheap first line, not the wall.
const workerDisallowedTools = "Bash(git push --force:*) Bash(git push -f:*) Bash(git push --force-with-lease:*) " +
	"Bash(git reset --hard:*) Bash(gh pr merge:*) Bash(git merge:*) " +
	"Bash(git commit --no-verify:*) Bash(git commit -n:*) Bash(git push --no-verify:*) " +
	"Bash(git -c *) Bash(git --config-env:*) " +
	"Bash(git config core.hooksPath:*) Bash(git config --local core.hooksPath:*)"

// prepareTimeout caps executor.prepare (a dependency install is minutes, a hang
// must not eat the whole worker timeout).
const prepareTimeout = 15 * time.Minute

// runPrepare executes the project's prepare command in dir via `sh -c`, with
// output streamed to stderr (-> the run log for background runs).
func runPrepare(dir, command string) error {
	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", command)
	c.Dir = dir
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	err := c.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out after %s", prepareTimeout)
	}
	return err
}

// baselineTimeout caps the executor.baseline capture (a full verification run
// is minutes; a hang must not eat the worker's budget). A var, not a const,
// purely so tests can shrink it.
var baselineTimeout = 15 * time.Minute

// baselineOutputCap bounds how much captured output lands in the prompt. The
// TAIL is kept - test/lint runners print summaries and totals last.
const baselineOutputCap = 8000

// captureBaseline runs the executor.baseline command in dir and renders the
// "## Verification baseline" prompt section from its outcome. A non-zero exit
// is the expected signal of a red baseline, not an error. Returns "" (with a
// warning on errOut) only when the command could not run at all - the run then
// proceeds without a baseline rather than aborting.
func captureBaseline(errOut io.Writer, dir, command string) string {
	ctx, cancel := context.WithTimeout(context.Background(), baselineTimeout)
	defer cancel()
	// Same process-group enforcement as the worker: a baseline command that
	// backgrounds anything (a dev server, a watcher) would otherwise outlive its
	// own cap and hold the output pipe open past it.
	c := groupCmd(ctx, "sh", "-c", command)
	c.Dir = dir
	var combined bytes.Buffer
	c.Stdout = &combined
	c.Stderr = &combined // one writer for both: exec dedups it onto a single fd
	// No onStart: the baseline runs before any run-state exists to publish to,
	// and its cap is enforced entirely inside this process.
	err := runGroupCmd(ctx, c, nil)
	out := combined.Bytes()
	if ctx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(errOut, "pm work: baseline cmd timed out after %s - continuing without a baseline\n", baselineTimeout)
		return ""
	}
	if err == nil {
		return baselineSection(command, 0, "")
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// The command left something holding its output open, so WaitDelay had
		// to unblock us (the leftovers are killed by now). Wait only reports
		// ErrWaitDelay when the command itself exited 0 - a non-zero exit always
		// wins as *ExitError - so this baseline is GREEN. Degrading to "no
		// baseline" here would cost every worker in the run the one section that
		// tells it which failures are pre-existing.
		fmt.Fprintf(errOut, "pm work: baseline cmd left background processes holding its output - killed them after %s\n", procWaitDelay)
		return baselineSection(command, 0, "")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		// The command never ran (sh missing, dir gone, ...) - no data to report.
		fmt.Fprintf(errOut, "pm work: baseline cmd could not run (%v) - continuing without a baseline\n", err)
		return ""
	}
	text := strings.TrimSpace(string(out))
	if len(text) > baselineOutputCap {
		text = "(... first " + fmt.Sprintf("%d", len(text)-baselineOutputCap) + " bytes truncated ...)\n" + text[len(text)-baselineOutputCap:]
	}
	return baselineSection(command, exitErr.ExitCode(), text)
}

// runWorker invokes claude headless in dir under a wall-clock deadline, parses
// the result envelope, and returns the worker result + the session id. A hung
// claude is killed when the timeout elapses rather than blocking pm forever.
// onSpawn (optional) receives the worker group's pgid once it is up; see
// runGroupCmd.
// sessionID is the id pm minted and pinned with `claude --session-id`, so a
// worker that dies without an envelope can still be asked what it said - see
// workerLastWords.
func runWorker(errOut io.Writer, dir string, args []string, timeout time.Duration, configDir string, extraEnv []string, sessionID string, onSpawn func(pgid int)) (*workerResult, string, error) {
	return runHeadless(errOut, "pm work", dir, args, timeout, configDir, extraEnv, sessionID, onSpawn, parseWorkerOutput)
}

// envelopeParser turns a `claude -p --output-format json` envelope into a typed
// result plus the session id. It is the ONLY thing runHeadless needs to know
// about the shape of the run it is driving, which is what lets the worker and
// the acceptance share every line of the process handling below - the deadline,
// the process group, the stderr tail, the WaitDelay recovery and the
// last-words death report are all properties of running `claude -p`, not of
// either contract.
//
// pinned is the session id pm minted and passed as `claude --session-id`. The
// envelope's own id is preferred when the bytes yield one; pinned is the
// fallback that keeps the transcript recovery reachable when they do not -
// pm knew the id before the worker ran, so a parse failure never has to
// re-discover it.
type envelopeParser[T any] func(data []byte, errOut io.Writer, configDir, dir, pinned string) (*T, string, error)

// runHeadless invokes claude headless in dir under a wall-clock deadline and
// parses its envelope with parse. label prefixes the progress lines so a reader
// of a run log can tell which command produced them.
func runHeadless[T any](errOut io.Writer, label, dir string, args []string, timeout time.Duration, configDir string, extraEnv []string, sessionID string, onSpawn func(pgid int), parse envelopeParser[T]) (*T, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	c := groupCmd(ctx, "claude", args...)
	c.Dir = dir
	// Project-defined env (e.g. Metro port, simulator UDID) is appended last so
	// it wins over any inherited value. pm passes these through opaquely.
	c.Env = append(workerEnv(configDir), extraEnv...)
	var stdout bytes.Buffer
	c.Stdout = &stdout
	// Stderr keeps streaming to the run log AND is tailed into a bounded buffer:
	// on a death with no transcript (a missing binary, a rejected flag) it is
	// the only thing the worker leaves behind, and the note used to get none of it.
	stderrTail := &tailWriter{n: workerDeathTailBytes}
	c.Stderr = io.MultiWriter(os.Stderr, stderrTail)
	err := runGroupCmd(ctx, c, onSpawn)
	if ctx.Err() == context.DeadlineExceeded {
		return nil, "", fmt.Errorf("worker timed out after %s (raise --timeout if the task legitimately needs longer)", timeout)
	}
	if err != nil {
		// The worker exited but something it spawned kept the inherited stdout
		// pipe open, so WaitDelay had to unblock us (the group is dead by now).
		// If the envelope made it through first, the run really did finish -
		// honour the result instead of discarding a completed worker's work.
		if errors.Is(err, exec.ErrWaitDelay) {
			if res, sessionID, perr := parse(stdout.Bytes(), errOut, configDir, dir, sessionID); perr == nil {
				fmt.Fprintf(errOut, "%s: worker left background processes holding stdout - killed them after %s\n", label, procWaitDelay)
				return res, sessionID, nil
			}
		}
		return nil, sessionID, workerDeathError(configDir, dir, sessionID, stderrTail.String(), err)
	}
	return parse(stdout.Bytes(), errOut, configDir, dir, sessionID)
}

// workerDeathError turns a bare non-zero exit into an error that says what the
// worker actually said, and marks it as an account wall when those last words
// name one. Before this every such death read "claude worker failed: exit
// status 1" and the reason lived only in a transcript nobody knew to open.
func workerDeathError(configDir, dir, sessionID, stderrTail string, err error) error {
	last := workerLastWords(configDir, dir, sessionID, stderrTail)
	if last == "" {
		return fmt.Errorf("claude worker failed: %w", err)
	}
	if reason := accountWallReason(last); reason != "" {
		return &accountWallError{reason: reason, lastWords: last, err: err}
	}
	return fmt.Errorf("claude worker failed: %w - last worker message: %s", err, last)
}

// workerEnv returns the worker's environment with ANTHROPIC_API_KEY /
// ANTHROPIC_AUTH_TOKEN stripped, so the headless `claude -p` worker authenticates
// with the Claude subscription (keychain) instead of the pay-per-token API.
//
// The executor is built to spawn MANY headless workers. With ANTHROPIC_API_KEY
// set, every worker bills the API (opus headless ~ $15/$75 per Mtok), and a
// drained/disabled API balance fails every worker with exit 1. claude -p on the
// subscription is supported within plan limits, which is the whole economic
// premise of the executor.
// workerEnv strips ANTHROPIC keys and, when the project uses a non-default
// Claude config dir (configDir != ~/.claude), pins CLAUDE_CONFIG_DIR so the
// worker authenticates with that project's account/config (e.g. a company Team
// account in ~/.claude-alt) instead of the personal default.
//
// It also marks the process with PM_HEADLESS=1 so project-side hooks (e.g. a
// SessionStart hook that boots a simulator + Metro for interactive worktree
// sessions) can tell a headless worker apart from an interactive launch and
// skip work a headless run cannot use. Interactive board launches go through a
// different path (tui/board claudeLaunchEnv) and never carry this marker.
func workerEnv(configDir string) []string {
	src := os.Environ()
	out := make([]string, 0, len(src)+2)
	pinConfig := configDir != "" && configDir != storage.DefaultClaudeConfigDir()
	for _, kv := range src {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			continue
		}
		if pinConfig && strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			continue // replaced below
		}
		if strings.HasPrefix(kv, "PM_HEADLESS=") {
			continue // replaced below; a nested worker inherits one already
		}
		out = append(out, kv)
	}
	if pinConfig {
		out = append(out, "CLAUDE_CONFIG_DIR="+configDir)
	}
	out = append(out, "PM_HEADLESS=1")
	return out
}

// parseClaudeResult extracts the worker result + session id from whatever
// `claude -p --output-format json` wrote (object or message-array shape - see
// decodeClaudeOutput). The session id is returned on EVERY error path that
// could yield one: it is what the transcript recovery is keyed on.
func parseClaudeResult(data []byte) (*workerResult, string, error) {
	out, err := decodeClaudeOutput(data)
	if err != nil {
		return nil, out.SessionID, fmt.Errorf("parse claude result: %w", err)
	}
	var env claudeEnvelope
	if err := json.Unmarshal(out.Result, &env); err != nil {
		return nil, out.SessionID, fmt.Errorf("parse claude result: %w", err)
	}
	if env.SessionID == "" {
		env.SessionID = out.SessionID
	}
	if env.StructuredOutput == nil {
		return nil, env.SessionID, fmt.Errorf("worker returned no structured result (is_error=%v): %s", env.IsError, strings.TrimSpace(env.Result))
	}
	// Lift the envelope's run stats onto the result (the model's structured
	// output can't carry them - only the harness knows turns/cost).
	env.StructuredOutput.Turns = env.NumTurns
	env.StructuredOutput.CostUSD = env.TotalCostUSD
	// One place folds the legacy "merged" spelling into "verified", so nothing
	// downstream (verdict checks, briefs, run-state, journal) has to know two
	// words for one outcome.
	env.StructuredOutput.Status = normalizeWorkerStatus(env.StructuredOutput.Status)
	return env.StructuredOutput, env.SessionID, nil
}

// applyWorkerResult records the worker outcome into pm: session, brief, log,
// and a conservative status move (only blocked -> waiting; never auto-done).
// In independent (batch) mode nothing is parked - every task in the batch gets
// a human follow-up regardless, so waiting would only add noise.
//
// t was read BEFORE the worker ran - potentially 30+ minutes ago. Writing that
// stale copy back would clobber any edit made meanwhile from another session
// (a brief update, a link, a log note), so the outcome is applied to a FRESH
// read of the task, under the project's cross-process lock; t is then updated
// in place so callers (driveSub's status checks) see the written state.
func applyWorkerResult(errOut io.Writer, store storage.TaskStore, t *storage.Task, branch, sessionID string, res *workerResult, standalone, independent bool) error {
	release, lockErr := store.LockProject(t.Project)
	if lockErr != nil {
		// Degrade to an unlocked write rather than dropping the worker's result,
		// but say so - a silent degrade hides that a concurrent session's edit
		// may get clobbered.
		fmt.Fprintf(errOut, "pm work: project lock unavailable (%v) - recording result without it\n", lockErr)
		release = func() {}
	}
	defer release()
	// The fresh re-read is a PRECONDITION, not a best-effort refresh (same rule
	// as Store.MoveTask): a miss means the task file was deleted - or stopped
	// parsing - while the worker ran, so the 30-minute-old copy in hand is unfit
	// to write. Writing it anyway RESURRECTED the deleted task, carrying a
	// worker brief nobody would ever look for. Refuse instead; the caller
	// surfaces it (`pm work` as its error, the manager as a parked sub's note).
	fresh, err := store.FindTaskExact(t.Project, t.Meta.ID)
	if err != nil {
		return fmt.Errorf("record worker result for %s: %w", t.Meta.ID, err)
	}
	*t = *fresh
	if sessionID != "" {
		t.Meta.Sessions = append(t.Meta.Sessions, sessionID)
	}
	if branch != "" {
		t.Meta.Branch = branch
	}

	t.Meta.Brief = workerBrief(res, branch, standalone, independent)
	t.Body = appendLog(t.Body, workerLogEntry(res, branch, standalone, independent))

	// Autonomy envelope: pm work never moves a task to a landing status on its
	// own. A blocked worker parks the task on `waiting` (human attention) if that
	// status exists for the project; otherwise the status is left untouched.
	if res.Status == workerBlocked && !independent {
		statuses := store.GetProjectStatuses(t.Project)
		if statusAllowed(storage.StatusWaiting, statuses) {
			// Write our mutations first, then hand off to MoveTask - it takes the
			// project lock itself, so OURS must be released (in-process flock
			// nesting deadlocks). Idempotent release; the defer stays harmless.
			if err := store.WriteTask(t); err != nil {
				return err
			}
			release()
			return store.MoveTask(t, storage.StatusWaiting)
		}
	}
	t.Meta.Updated = storage.Now()
	return store.WriteTask(t)
}

func statusAllowed(s storage.TaskStatus, allowed []storage.TaskStatus) bool {
	for _, a := range allowed {
		if a == s {
			return true
		}
	}
	return false
}

// displayStatus renders the worker status for human-facing brief/log text,
// naming what actually happened to the work in each mode: a standalone run ends
// in a draft PR, an independent (batch) sub ends pushed to origin awaiting a
// human, and only an integration sub is genuinely merged (by the manager, right
// after this text is written). The JSON contract enum is unaffected.
func displayStatus(status string, standalone, independent bool) string {
	if status != workerVerified {
		return status
	}
	switch {
	case standalone:
		return "ready (draft PR)"
	case independent:
		return "ready (pushed)"
	default:
		return "verified (merging into the epic branch)"
	}
}

func workerBrief(res *workerResult, branch string, standalone, independent bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Worker %s on %s (%s). %s", displayStatus(res.Status, standalone, independent), branch, modeLabel(standalone), strings.TrimSpace(res.Summary))
	if len(res.Unresolved) > 0 {
		fmt.Fprintf(&sb, " Unresolved: %s.", strings.Join(res.Unresolved, "; "))
	}
	if len(res.Commits) > 0 {
		fmt.Fprintf(&sb, " Commits: %s.", strings.Join(res.Commits, ", "))
	}
	return sb.String()
}

func workerLogEntry(res *workerResult, branch string, standalone, independent bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Worker run (%s, %s)** - status: %s, branch: %s.\n", storage.Today(), modeLabel(standalone), displayStatus(res.Status, standalone, independent), branch)
	if s := strings.TrimSpace(res.Summary); s != "" {
		fmt.Fprintf(&sb, "%s\n", s)
	}
	if len(res.Commits) > 0 {
		fmt.Fprintf(&sb, "Commits: %s\n", strings.Join(res.Commits, ", "))
	}
	if len(res.Unresolved) > 0 {
		sb.WriteString("Unresolved:\n")
		for _, u := range res.Unresolved {
			fmt.Fprintf(&sb, "- %s\n", u)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// appendLog appends an entry to the Log zone (everything outside the Spec
// markers). It never rewrites the Spec.
func appendLog(body, entry string) string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return entry
	}
	return body + "\n\n" + entry
}
