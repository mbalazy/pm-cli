package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// workerResult is the JSON contract returned by a headless worker (the
// manager<->worker API). It is validated against workerResultSchema.
type workerResult struct {
	Status     string   `json:"status"` // merged | blocked | failed
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
    "status": {"type": "string", "enum": ["merged", "blocked", "failed"]},
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
			task, slug, err := resolveWorkTask(store, args)
			if err != nil {
				return err
			}
			// Task frontmatter `model:` wins over the flag DEFAULT, but an
			// explicitly passed --model always wins over the frontmatter.
			if task.Meta.Model != "" && !cmd.Flags().Changed("model") {
				model = task.Meta.Model
			}
			opts := workOptions{
				standalone: !epic,
				model:      model,
				maxTurns:   maxTurns,
				yolo:       yolo,
				allowDirty: allowDirty,
				timeout:    timeout,
				base:       base,
				additional: additional,
			}

			plan, err := planWork(store, task, slug, opts)
			if err != nil {
				return err
			}

			if dryRun {
				fmt.Printf("# pm work (dry-run)\nproject: %s\ntask: %s\nbranch: %s\nmode: %s\ncwd: %s\n",
					slug, task.Meta.ID, plan.branch, modeLabel(opts.standalone), plan.workDir)
				if plan.worktree {
					fmt.Printf("run: ADDITIONAL worktree - first free of %d slot(s), claimed at run time (lock: %s)\n",
						len(plan.slots), ".pm-executor.lock")
					for i, s := range plan.slots {
						fmt.Printf("  slot %d: %s", i+1, s.Path)
						if len(s.Env) > 0 {
							fmt.Printf("  (env: %s)", strings.Join(s.Env, " "))
						}
						fmt.Println()
					}
					fmt.Printf("fresh branch base: %s\n", plan.base)
					if plan.prepare != "" {
						fmt.Printf("prepare (before worker, in claimed slot): %s\n", plan.prepare)
					}
				} else {
					fmt.Printf("run: DEFAULT (main checkout, clean-tree required)\n")
				}
				fmt.Printf("\n$ claude %s\n\n", strings.Join(quoteArgs(plan.cmdArgs), " "))
				fmt.Printf("=== SYSTEM PROMPT ===\n%s\n\n=== PROMPT ===\n%s\n", plan.sysPrompt, plan.prompt)
				return nil
			}

			// Worktree mode: claim a slot from the pool (first free, or --slot N),
			// ensure + seed + lock it, and point the plan at the CLAIMED slot. The
			// lock is released by the deferred closure on every exit path (success,
			// fail, panic).
			if plan.worktree {
				slot, release, err := acquireWorktreeSlot(plan.proj, plan.slots, slotPin, task.Meta.ID, "work")
				if err != nil {
					return err
				}
				defer release()
				plan.workDir = slot.Path
				plan.env = slot.Env
				if len(plan.slots) > 1 {
					fmt.Fprintf(os.Stderr, "pm work: claimed worktree slot %s\n", slot.Path)
				}
			}

			res, err := executeWork(store, task, plan, opts)
			if err != nil {
				return err
			}

			out, _ := json.MarshalIndent(res, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}

	cmd.Flags().BoolVar(&epic, "epic", false, "epic mode: commit on the current branch, no PR (called by the manager)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "assemble and print the worker prompt + command without invoking claude")
	cmd.Flags().StringVar(&model, "model", "opus", "model for the worker (alias or full name)")
	cmd.Flags().IntVar(&maxTurns, "max-turns", 120, "max agent turns for the worker")
	cmd.Flags().BoolVar(&yolo, "yolo", false, "bypass all permission checks instead of the curated allowlist")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "skip the clean-working-tree precondition (standalone)")
	cmd.Flags().DurationVar(&timeout, "timeout", 45*time.Minute, "max wall-clock time for the worker before it is killed")
	cmd.Flags().StringVar(&base, "base", "", "with --additional: base the fresh task branch forks from (default: executor.base_branch, else the main checkout's current branch)")
	cmd.Flags().BoolVar(&additional, "additional", false, "run in an isolated 'additional' worktree slot (own branch/port/sim) instead of the main checkout; requires executor.worktrees (or legacy additional_worktree) in project.yaml")
	cmd.Flags().IntVar(&slotPin, "slot", 0, "with --additional: pin a specific worktree slot (1-based); default 0 = first free slot")

	return cmd
}

// resolveWorkTask resolves the project slug + task from the command args.
// One arg: task id, project auto-detected from cwd or by scanning. Two args:
// explicit project + task.
func resolveWorkTask(store storage.TaskStore, args []string) (*storage.Task, string, error) {
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
	// Prefer the project of the current directory.
	if slug := detectProjectFromCwd(store); slug != "" {
		if t, err := store.FindTask(slug, query); err == nil {
			return t, slug, nil
		}
	}
	// Fall back to scanning every project for the task id.
	projects, err := store.ListProjects()
	if err != nil {
		return nil, "", err
	}
	for _, slug := range projects {
		if t, err := store.FindTask(slug, query); err == nil {
			return t, slug, nil
		}
	}
	return nil, "", fmt.Errorf("task %q not found in any project (try `pm work <project> <task-id>`)", query)
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
}

// planWork validates preconditions and assembles everything needed to invoke a
// worker for task, without touching git or spending tokens. Used by both the
// dry-run path and the real run.
func planWork(store storage.TaskStore, task *storage.Task, slug string, opts workOptions) (*workPlan, error) {
	proj, err := store.GetProject(slug)
	if err != nil {
		return nil, fmt.Errorf("load project %s: %w", slug, err)
	}
	if proj.Path == "" {
		return nil, fmt.Errorf("project %s has no path - the executor needs a git repo (set `path` in project.yaml)", slug)
	}
	if !isGitRepo(proj.Path) {
		return nil, fmt.Errorf("project path %s is not a git repository - the executor only runs on git projects", proj.Path)
	}

	exec := proj.GetExecutor()
	if !exec.Enabled {
		return nil, fmt.Errorf("executor disabled for project %s (executor.enabled: false)", slug)
	}

	var parent *storage.Task
	if task.Meta.Parent != "" {
		if p, err := store.FindTask(slug, task.Meta.Parent); err == nil {
			parent = p
		}
	}

	branch := resolveWorkBranch(task)
	sessionID := storage.NewSessionID()
	prompt := buildWorkerPrompt(task, parent, proj, slug, exec, branch, opts.standalone)
	sysPrompt := buildWorkerSystemPrompt(exec, opts.standalone, opts.independent)
	cmdArgs := buildClaudeArgs(prompt, sysPrompt, sessionID, opts.model, opts.maxTurns, opts.yolo)

	// An isolated "additional" worktree slot is opt-in PER RUN via --additional;
	// the default is the main checkout (unchanged pre-worktree behaviour). No
	// side effects here - the slot is claimed + locked in acquireWorktreeSlot at
	// run time (not on the dry-run path).
	workDir := proj.Path
	base := ""
	env := []string(nil)
	var slots []storage.ResolvedWorktree
	if opts.additional {
		// Base precedence: --base > executor.base_branch > main checkout's current
		// branch. Resolved here (a read, no side effect) so --dry-run shows it too.
		cur, _ := gitCurrentBranch(proj.Path)
		base = resolveWorktreeBase(opts.base, exec.BaseBranch, cur)
		if opts.slotDir != "" {
			// The caller (epic manager) already claimed a slot - target it.
			workDir = opts.slotDir
			env = opts.slotEnv
		} else {
			slots = exec.ResolveWorktrees(proj.Path)
			if len(slots) == 0 {
				return nil, fmt.Errorf("--additional requested but project %s has no worktree slots configured - set `executor.worktrees` (or legacy `additional_worktree: true` + worktree_path/env) in project.yaml", slug)
			}
			// Provisional until a slot is claimed at run start.
			workDir = slots[0].Path
			env = slots[0].Env
		}
	}

	// Standalone only: the epic manager runs prepare ITSELF, once per run,
	// right after claiming the slot - not per sub (5 subs must not mean 5
	// dependency installs).
	prepare := ""
	if opts.additional && opts.standalone {
		prepare = strings.TrimSpace(exec.Prepare)
	}

	return &workPlan{
		proj: proj, branch: branch, sessionID: sessionID,
		prompt: prompt, sysPrompt: sysPrompt, cmdArgs: cmdArgs,
		workDir: workDir, worktree: opts.additional, base: base, env: env, slots: slots,
		prepare: prepare,
	}, nil
}

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
				if dirty, _ := gitDirty(dir); dirty {
					return nil, fmt.Errorf("working tree at %s is dirty - commit/stash first or pass --allow-dirty", dir)
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
		fmt.Fprintf(os.Stderr, "pm work: prepare in %s: %s\n", dir, plan.prepare)
		if err := runPrepare(dir, plan.prepare); err != nil {
			return nil, fmt.Errorf("prepare cmd (%s) failed in %s: %w", plan.prepare, dir, err)
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
	var run *storage.RunState
	if opts.standalone {
		run = &storage.RunState{
			TaskID:         task.Meta.ID,
			Project:        task.Project,
			Kind:           "work",
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
		_ = storage.WriteRunState(stateDir, run)
		// Journal start line (durable cross-run history; epic subs are journaled
		// by the manager instead). Best-effort like the run-state writes.
		_ = storage.AppendJournal(stateDir, &storage.JournalEntry{
			Event: storage.JournalEventStart, Kind: "work", Project: task.Project, TaskID: task.Meta.ID,
			PID: os.Getpid(), Model: opts.model, Additional: opts.additional, Yolo: opts.yolo, Branch: plan.branch,
			WorkDir: journalDir,
		})
	}

	fmt.Fprintf(os.Stderr, "pm work: launching headless worker for %s on %s (%s)...\n", task.Meta.ID, plan.branch, modeLabel(opts.standalone))

	workStart := time.Now()
	res, sessionID, err := runWorker(dir, plan.cmdArgs, opts.timeout, plan.proj.ResolveClaudeConfigDir(), plan.env)
	if err != nil {
		if run != nil {
			run.Status = storage.RunStatusFailed
			run.Phase = ""
			run.Error = err.Error()
			if len(run.Subs) > 0 {
				run.Subs[0].Status = storage.RunStatusFailed
				run.Subs[0].Note = err.Error()
			}
			_ = storage.WriteRunState(stateDir, run)
			_ = storage.AppendJournal(stateDir, &storage.JournalEntry{
				Event: storage.JournalEventEnd, Kind: "work", Project: task.Project, TaskID: task.Meta.ID,
				PID: os.Getpid(), Model: opts.model, Additional: opts.additional, Yolo: opts.yolo, Branch: plan.branch,
				WorkDir: journalDir,
				Status:  storage.RunStatusFailed, DurationS: int(time.Since(workStart).Seconds()), Error: err.Error(),
				Subs: []storage.JournalSub{{ID: task.Meta.ID, Result: "failed", Note: err.Error(), Branch: plan.branch, DurationS: int(time.Since(workStart).Seconds()), Session: plan.sessionID}},
			})
		}
		return nil, err
	}
	if err := applyWorkerResult(store, task, plan.branch, sessionID, res, opts.standalone, opts.independent); err != nil {
		return nil, err
	}
	if run != nil {
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
		}
		_ = storage.WriteRunState(stateDir, run)
		_ = storage.AppendJournal(stateDir, &storage.JournalEntry{
			Event: storage.JournalEventEnd, Kind: "work", Project: task.Project, TaskID: task.Meta.ID,
			PID: os.Getpid(), Model: opts.model, Additional: opts.additional, Yolo: opts.yolo, Branch: plan.branch,
			WorkDir: journalDir,
			Status:  storage.RunStatusDone, DurationS: int(time.Since(workStart).Seconds()),
			Subs: []storage.JournalSub{{ID: task.Meta.ID, Result: res.Status, Note: strings.TrimSpace(res.Summary), Branch: plan.branch, DurationS: int(time.Since(workStart).Seconds()), Session: sessionID, Turns: res.Turns, CostUSD: res.CostUSD}},
		})
	}
	return res, nil
}

func modeLabel(standalone bool) string {
	if standalone {
		return "standalone"
	}
	return "epic"
}

// buildClaudeArgs assembles the `claude -p` argv for a worker run.
func buildClaudeArgs(prompt, sysPrompt, sessionID, model string, maxTurns int, yolo bool) []string {
	args := []string{
		"-p", prompt,
		"--append-system-prompt", sysPrompt,
		"--output-format", "json",
		"--json-schema", workerResultSchema,
		"--model", model,
		"--max-turns", fmt.Sprintf("%d", maxTurns),
	}
	if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
	if yolo {
		args = append(args, "--dangerously-skip-permissions")
	} else {
		args = append(args,
			"--permission-mode", "acceptEdits",
			"--allowedTools", workerAllowedTools,
			"--disallowedTools", workerDisallowedTools,
		)
	}
	return args
}

// workerAllowedTools is the curated allowlist: file edits (acceptEdits), review
// subagents, and the bash families a build/test/review/pr loop needs.
const workerAllowedTools = "Edit Write Read Grep Glob Task TodoWrite " +
	"Bash(git:*) Bash(gh:*) Bash(go:*) Bash(make:*) Bash(yarn:*) Bash(npm:*) Bash(npx:*) Bash(pnpm:*) " +
	"Bash(node:*) Bash(jest:*) Bash(vitest:*) Bash(eslint:*) Bash(biome:*) Bash(tsc:*) Bash(prettier:*) " +
	"Bash(cargo:*) Bash(python:*) Bash(python3:*) Bash(pytest:*) Bash(ruff:*) Bash(mypy:*) " +
	"Bash(cd:*) Bash(ls:*) Bash(cat:*) Bash(grep:*) Bash(rg:*) Bash(find:*) Bash(echo:*) Bash(sed:*) Bash(awk:*)"

// workerDisallowedTools enforces the autonomy envelope: never force-push, never
// merge to main, never hard-reset.
const workerDisallowedTools = "Bash(git push --force:*) Bash(git push -f:*) Bash(git push --force-with-lease:*) " +
	"Bash(git reset --hard:*) Bash(gh pr merge:*) Bash(git merge:*)"

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

// runWorker invokes claude headless in dir under a wall-clock deadline, parses
// the result envelope, and returns the worker result + the session id. A hung
// claude is killed when the timeout elapses rather than blocking pm forever.
func runWorker(dir string, args []string, timeout time.Duration, configDir string, extraEnv []string) (*workerResult, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	c := exec.CommandContext(ctx, "claude", args...)
	c.Dir = dir
	// Project-defined env (e.g. Metro port, simulator UDID) is appended last so
	// it wins over any inherited value. pm passes these through opaquely.
	c.Env = append(workerEnv(configDir), extraEnv...)
	c.Stderr = os.Stderr
	out, err := c.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, "", fmt.Errorf("worker timed out after %s (raise --timeout if the task legitimately needs longer)", timeout)
	}
	if err != nil {
		return nil, "", fmt.Errorf("claude worker failed: %w", err)
	}
	return parseClaudeResult(out)
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
		out = append(out, kv)
	}
	if pinConfig {
		out = append(out, "CLAUDE_CONFIG_DIR="+configDir)
	}
	out = append(out, "PM_HEADLESS=1")
	return out
}

// parseClaudeResult extracts the worker result + session id from a
// `claude -p --output-format json` envelope.
func parseClaudeResult(data []byte) (*workerResult, string, error) {
	var env claudeEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, "", fmt.Errorf("parse claude result: %w", err)
	}
	if env.StructuredOutput == nil {
		return nil, env.SessionID, fmt.Errorf("worker returned no structured result (is_error=%v): %s", env.IsError, strings.TrimSpace(env.Result))
	}
	// Lift the envelope's run stats onto the result (the model's structured
	// output can't carry them - only the harness knows turns/cost).
	env.StructuredOutput.Turns = env.NumTurns
	env.StructuredOutput.CostUSD = env.TotalCostUSD
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
func applyWorkerResult(store storage.TaskStore, t *storage.Task, branch, sessionID string, res *workerResult, standalone, independent bool) error {
	if release, err := store.LockProject(t.Project); err == nil {
		defer release()
	}
	if fresh, err := store.FindTask(t.Project, t.Meta.ID); err == nil {
		*t = *fresh
	}
	if sessionID != "" {
		t.Meta.Sessions = append(t.Meta.Sessions, sessionID)
	}
	if branch != "" {
		t.Meta.Branch = branch
	}

	t.Meta.Brief = workerBrief(res, branch, standalone)
	t.Body = appendLog(t.Body, workerLogEntry(res, branch, standalone))

	// Autonomy envelope: pm work never moves a task to done/merged on its own.
	// A blocked worker parks the task on `waiting` (human attention) if that
	// status exists for the project; otherwise the status is left untouched.
	if res.Status == "blocked" && !independent {
		statuses := store.GetProjectStatuses(t.Project)
		if statusAllowed(storage.StatusWaiting, statuses) {
			return store.MoveTask(t, storage.StatusWaiting)
		}
	}
	t.Meta.Updated = storage.Today()
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

// displayStatus renders the worker status for human-facing brief/log text. In
// standalone mode nothing is actually merged (the worker opens a draft PR), so
// "merged" reads as "ready (draft PR)". The JSON contract enum is unchanged.
func displayStatus(status string, standalone bool) string {
	if standalone && status == "merged" {
		return "ready (draft PR)"
	}
	return status
}

func workerBrief(res *workerResult, branch string, standalone bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Worker %s on %s (%s). %s", displayStatus(res.Status, standalone), branch, modeLabel(standalone), strings.TrimSpace(res.Summary))
	if len(res.Unresolved) > 0 {
		fmt.Fprintf(&sb, " Unresolved: %s.", strings.Join(res.Unresolved, "; "))
	}
	if len(res.Commits) > 0 {
		fmt.Fprintf(&sb, " Commits: %s.", strings.Join(res.Commits, ", "))
	}
	return sb.String()
}

func workerLogEntry(res *workerResult, branch string, standalone bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Worker run (%s, %s)** - status: %s, branch: %s.\n", storage.Today(), modeLabel(standalone), displayStatus(res.Status, standalone), branch)
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
