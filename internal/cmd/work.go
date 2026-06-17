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
}

// claudeEnvelope is the `claude -p --output-format json` result envelope. The
// schema-validated worker result lands in StructuredOutput.
type claudeEnvelope struct {
	Type             string        `json:"type"`
	Subtype          string        `json:"subtype"`
	IsError          bool          `json:"is_error"`
	Result           string        `json:"result"`
	SessionID        string        `json:"session_id"`
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
			opts := workOptions{
				standalone: !epic,
				model:      model,
				maxTurns:   maxTurns,
				yolo:       yolo,
				allowDirty: allowDirty,
				timeout:    timeout,
			}

			plan, err := planWork(store, task, slug, opts)
			if err != nil {
				return err
			}

			if dryRun {
				fmt.Printf("# pm work (dry-run)\nproject: %s\ntask: %s\nbranch: %s\nmode: %s\ncwd: %s\n\n",
					slug, task.Meta.ID, plan.branch, modeLabel(opts.standalone), plan.proj.Path)
				fmt.Printf("$ claude %s\n\n", strings.Join(quoteArgs(plan.cmdArgs), " "))
				fmt.Printf("=== SYSTEM PROMPT ===\n%s\n\n=== PROMPT ===\n%s\n", plan.sysPrompt, plan.prompt)
				return nil
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
	sysPrompt := buildWorkerSystemPrompt(exec, opts.standalone)
	cmdArgs := buildClaudeArgs(prompt, sysPrompt, sessionID, opts.model, opts.maxTurns, opts.yolo)

	return &workPlan{proj: proj, branch: branch, sessionID: sessionID, prompt: prompt, sysPrompt: sysPrompt, cmdArgs: cmdArgs}, nil
}

// executeWork runs a planned worker and records the result into pm. In
// standalone mode it owns the branch (clean-tree precondition + checkout); in
// epic mode the manager has already checked out the branch.
func executeWork(store storage.TaskStore, task *storage.Task, plan *workPlan, opts workOptions) (*workerResult, error) {
	if opts.standalone {
		if !opts.allowDirty {
			if dirty, _ := gitDirty(plan.proj.Path); dirty {
				return nil, fmt.Errorf("working tree at %s is dirty - commit/stash first or pass --allow-dirty", plan.proj.Path)
			}
		}
		if err := gitCheckoutBranch(plan.proj.Path, plan.branch); err != nil {
			return nil, fmt.Errorf("prepare branch %s: %w", plan.branch, err)
		}
	}

	// Run-state for observability (standalone only; in epic mode the manager
	// owns the epic-level run-state and updates the per-sub entry). The run-state
	// lives in the pm data dir (where the TUI reads it), NOT the git repo.
	stateDir := store.ProjectDir(task.Project)
	var run *storage.RunState
	if opts.standalone {
		run = &storage.RunState{
			TaskID:         task.Meta.ID,
			Project:        task.Project,
			Kind:           "work",
			Status:         storage.RunStatusRunning,
			PID:            os.Getpid(),
			RepoPath:       plan.proj.Path,
			Started:        time.Now().UTC().Format(time.RFC3339),
			LogPath:        storage.ExecutorLogPath(stateDir, task.Meta.ID),
			CurrentSub:     task.Meta.ID,
			CurrentSession: plan.sessionID,
			Phase:          "running",
			Subs:           []storage.SubRun{{ID: task.Meta.ID, Status: storage.RunStatusRunning, Session: plan.sessionID}},
		}
		_ = storage.WriteRunState(stateDir, run)
	}

	fmt.Fprintf(os.Stderr, "pm work: launching headless worker for %s on %s (%s)...\n", task.Meta.ID, plan.branch, modeLabel(opts.standalone))

	res, sessionID, err := runWorker(plan.proj.Path, plan.cmdArgs, opts.timeout)
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
		}
		return nil, err
	}
	if err := applyWorkerResult(store, task, plan.branch, sessionID, res, opts.standalone); err != nil {
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
		}
		_ = storage.WriteRunState(stateDir, run)
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

// runWorker invokes claude headless in dir under a wall-clock deadline, parses
// the result envelope, and returns the worker result + the session id. A hung
// claude is killed when the timeout elapses rather than blocking pm forever.
func runWorker(dir string, args []string, timeout time.Duration) (*workerResult, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	c := exec.CommandContext(ctx, "claude", args...)
	c.Dir = dir
	c.Env = workerEnv()
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
func workerEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			continue
		}
		out = append(out, kv)
	}
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
	return env.StructuredOutput, env.SessionID, nil
}

// applyWorkerResult records the worker outcome into pm: session, brief, log,
// and a conservative status move (only blocked -> waiting; never auto-done).
func applyWorkerResult(store storage.TaskStore, t *storage.Task, branch, sessionID string, res *workerResult, standalone bool) error {
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
	if res.Status == "blocked" {
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
