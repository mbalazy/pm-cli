package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// `pm finish` is the odbiór (acceptance) surface of an executor run - the THIRD
// kind of pm run, alongside `pm work` (implement one task) and `pm run-epic`
// (drive an epic).
//
// Two halves live here. The subcommands `claim` / `release` / `status` operate
// the lock that stops two acceptance sessions from taking the same run (see
// internal/storage/finish_claim.go). The main form, `pm finish <tracker>`,
// spawns a headless acceptance worker and wraps it in the same harness every
// other executor run gets: the claim, a run-state with a heartbeat, the guard
// hook, the executor journal, and a report artifact on disk.
//
// What the worker is TOLD is short - see finish_prompt.go for why (the odbiór
// procedure is the `batch-finish-auto` skill's, and a headless worker can
// invoke it).

func newFinishCmd(store storage.TaskStore) *cobra.Command {
	var (
		opts    finishOptions
		noSim   bool
		noYolo  bool
		dryRun  bool
		slotPin int
	)

	cmd := &cobra.Command{
		Use:   "finish [tracker]",
		Short: "Odbiór (acceptance) of an executor run - spawn an acceptance worker, claimed so two sessions never take the same run",
		Long: "Accepts an executor run: spawns a headless acceptance worker that runs the `batch-finish-auto` " +
			"skill for <tracker>, and wraps it in the executor harness (claim, run-state, guard hook, journal, " +
			"report). Without a tracker it prints this help; the `claim`/`release`/`status` subcommands operate " +
			"the lock on its own.\n\n" +
			"The claim lives next to the run it covers (<project data dir>/.executor/<tracker>.finish.claim), " +
			"never next to whoever is accepting: a run and its acceptance routinely stand on different machines " +
			"(the batch runs on the VPS, the human accepts on the mac), and a lock kept locally by the accepting " +
			"side would have both machines take the same run.\n\n" +
			"Validity is a TTL, not a pid: a pid written on one machine says nothing on another. A claim is valid " +
			"for " + storage.FinishClaimTTL.String() + " from its last refresh, and the pid and host it records are " +
			"informational - they say which machine to go and look at. A run started here refreshes its own claim " +
			"for as long as it lasts, and releases it on the way out.\n\n" +
			"The acceptance never touches the simulator unless --sim is passed: anything running detached keeps " +
			"its hands off a shared runtime, so its visual claims come back counted rather than guessed.\n\n" +
			"Note that a tracker id spelled exactly like a subcommand (`claim`, `release`, `status`) reaches the " +
			"subcommand, not the acceptance run.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if opts.sim && noSim {
				return fmt.Errorf("--sim and --no-sim contradict each other - pass one (the default is --no-sim)")
			}
			opts.yolo = !noYolo
			opts.errOut = stderr
			if slotPin != 0 && !opts.additional {
				fmt.Fprintf(stderr, "pm finish: --slot %d ignored without --additional (slots exist only in the worktree pool)\n", slotPin)
			}

			tracker, slug, err := resolveFinishTracker(cmd, store, args[0])
			if err != nil {
				return err
			}
			plan, err := planFinish(store, tracker, slug, opts)
			if err != nil {
				return err
			}
			if dryRun {
				printFinishDryRun(stdout, plan)
				return nil
			}

			res, err := executeFinish(plan)
			if err != nil {
				return err
			}
			out, _ := json.MarshalIndent(res, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return nil
		},
	}
	cmd.PersistentFlags().StringP("project", "p", "", "project slug (default: detected from cwd)")
	cmd.Flags().BoolVar(&opts.sim, "sim", false, "let the acceptance use the simulator/runtime (only for a run with a human present)")
	cmd.Flags().BoolVar(&noSim, "no-sim", false, "never touch the simulator/runtime (the default; visual claims come back counted, not guessed)")
	cmd.Flags().BoolVar(&noYolo, "no-yolo", false, "run under the curated worker allowlist instead of bypassing permission prompts")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "assemble and print the acceptance prompt + command without claiming anything or invoking claude")
	cmd.Flags().StringVar(&opts.model, "model", "opus", "model for the acceptance worker (alias or full name)")
	cmd.Flags().IntVar(&opts.maxTurns, "max-turns", finishMaxTurns, "max agent turns for the acceptance worker")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", finishTimeout, "max wall-clock time for the acceptance worker before it is killed")
	cmd.Flags().BoolVar(&opts.additional, "additional", false, "run in an isolated 'additional' worktree slot instead of the main checkout")
	cmd.Flags().IntVar(&slotPin, "slot", 0, "with --additional: pin a specific worktree slot (1-based); default 0 = first free slot")
	cmd.AddCommand(
		newFinishClaimCmd(store),
		newFinishReleaseCmd(store),
		newFinishStatusCmd(store),
	)
	return cmd
}

// finishTimeout is the acceptance run's default wall clock, DOUBLE `pm work`'s.
// An acceptance is an orchestrator, not an implementer: it spawns one agent per
// sub of the batch and a batch is normally 5-8 subs, so the run is a sum of
// runs rather than one of them. (`pm work`'s own default is untouched.)
const finishTimeout = 120 * time.Minute

// finishMaxTurns is the turn budget, above `pm work`'s 150 for the same reason
// finishTimeout is above its hour: driving 5-8 sub-acceptances plus the
// discovery and report steps is several tasks' worth of turns in one run.
const finishMaxTurns = 300

// finishOptions are the knobs of one acceptance run.
type finishOptions struct {
	sim        bool
	model      string
	maxTurns   int
	yolo       bool
	timeout    time.Duration
	additional bool
	// errOut: where this run's progress and warnings go (cmd.ErrOrStderr()).
	// The zero value falls back to the process's own stderr, so a test building
	// finishOptions by hand needs no wiring - same contract as workOptions.
	errOut io.Writer
}

func (o finishOptions) stderr() io.Writer {
	if o.errOut != nil {
		return o.errOut
	}
	return os.Stderr
}

// finishPlan is a resolved, ready-to-run acceptance invocation. Assembled by
// planFinish with NO side effects at all - in particular nothing is claimed -
// so --dry-run can print it without taking anything away from a real run.
type finishPlan struct {
	proj       *storage.Project
	tracker    *storage.Task
	slug       string
	stateDir   string // the project's pm data dir - where claim, run-state, journal and report live
	workDir    string // the acceptance worker's cwd
	sessionID  string
	prompt     string
	sysPrompt  string
	cmdArgs    []string
	reportPath string
	opts       finishOptions
}

// resolveFinishTracker resolves the tracker to accept within the project the
// --project flag names (or the cwd implies). Resolution is FindTask's, so a
// prefix or a title substring works exactly as it does for `pm get`/`pm work`;
// the resolved task's own id is what everything downstream uses, since it is
// the name of the claim file, the run-state and the report.
func resolveFinishTracker(cmd *cobra.Command, store storage.TaskStore, query string) (*storage.Task, string, error) {
	slug, err := finishProjectSlug(cmd, store)
	if err != nil {
		return nil, "", err
	}
	t, err := store.FindTask(slug, query)
	if err != nil {
		return nil, "", err
	}
	// Belt and braces: the id names files under .executor/. Every id that
	// reaches here came through Store.AddTask and is valid by construction, but
	// this is the point where it stops being data and starts being a path.
	if err := storage.ValidateTaskID(t.Meta.ID); err != nil {
		return nil, "", err
	}
	return t, slug, nil
}

// planFinish assembles everything an acceptance run needs, without touching the
// claim, git, or a single token.
func planFinish(store storage.TaskStore, tracker *storage.Task, slug string, opts finishOptions) (*finishPlan, error) {
	proj, _, err := preflightProject(store, slug)
	if err != nil {
		return nil, err
	}
	if opts.additional {
		// Refused, not silently ignored. Claiming a slot from the shared pool
		// (and telling the board about it) is pm-cli-100-5's work, and a flag
		// that quietly ran in the main checkout instead would put an acceptance
		// into the very checkout a slot exists to keep it out of.
		return nil, fmt.Errorf("--additional is not wired up for `pm finish` yet (worktree slots for the acceptance run land in pm-cli-100-5) - run without it to accept in the main checkout at %s", proj.Path)
	}

	stateDir := store.ProjectDir(slug)
	sessionID := storage.NewSessionID()
	reportPath := storage.FinishReportPath(stateDir, tracker.Meta.ID)
	workDir := proj.Path

	prompt := buildFinishPrompt(tracker, proj, slug, workDir, reportPath, opts.sim)
	sysPrompt := buildFinishSystemPrompt(opts.sim)

	return &finishPlan{
		proj: proj, tracker: tracker, slug: slug,
		stateDir: stateDir, workDir: workDir, sessionID: sessionID,
		prompt: prompt, sysPrompt: sysPrompt,
		cmdArgs:    buildClaudeArgsFor(finishClaudeRun(prompt, sysPrompt, sessionID, opts)),
		reportPath: reportPath, opts: opts,
	}, nil
}

// finishClaudeRun is the acceptance run's `claude -p` shape, and the two empty
// fields in it are the load-bearing ones.
//
// The guard is attached (workerGuardSettings renders it from a zero
// guardOptions), so the bash and write rules that protect `.git` and the hook
// directory apply here exactly as they do to a worker - `--yolo` waives
// permission PROMPTS, not the project's right to have its commits pass its own
// hooks. But guardOptions is left ZERO in telemetryPath, diffBase and
// fixRounds on purpose: with a telemetry path set, judgeAgentSpawn refuses a
// subagent spawn past a reviewer cap sized from the DIFF, and an acceptance
// spawns one agent per SUB of the batch - a quantity that has nothing to do
// with any diff. Under the review cap the acceptance would die on its second
// sub.
//
// A nested spawn (a sub-acceptance agent spawning its own) stays blocked even
// with no telemetry, and that is deliberate too: in epic orbit-106 one
// reviewer's own Explore burned 6.1M tokens outside any budget. If an
// acceptance agent turns out to need one, that is its own decision to take,
// not a quiet exception here.
//
// No `--agents` either: the acceptance has no review phase, so pinning a
// pm-reviewer type onto it would define an agent nothing spawns.
func finishClaudeRun(prompt, sysPrompt, sessionID string, opts finishOptions) claudeRun {
	return claudeRun{
		prompt: prompt, sysPrompt: sysPrompt, sessionID: sessionID,
		model: opts.model, maxTurns: opts.maxTurns, yolo: opts.yolo,
		schema: finishResultSchema,
		guard:  guardOptions{},
	}
}

func printFinishDryRun(out io.Writer, plan *finishPlan) {
	fmt.Fprintf(out, "# pm finish (dry-run)\nproject: %s\ntracker: %s %s\ncwd: %s\n",
		plan.slug, plan.tracker.Meta.ID, plan.tracker.Meta.Title, plan.workDir)
	fmt.Fprintf(out, "sim: %s\n", finishSimLabel(plan.opts.sim))
	fmt.Fprintf(out, "model: %s | max turns: %d | timeout: %s\n", plan.opts.model, plan.opts.maxTurns, plan.opts.timeout)
	fmt.Fprintf(out, "permissions: %s\n", finishPermissionsLabel(plan.opts.yolo))
	fmt.Fprintf(out, "report: %s\n", plan.reportPath)
	fmt.Fprintf(out, "run-state: %s\n", storage.FinishRunPath(plan.stateDir, plan.tracker.Meta.ID))
	fmt.Fprintf(out, "claim: %s\n", finishClaimLabel(plan.stateDir, plan.tracker.Meta.ID))
	fmt.Fprintf(out, "(dry run: nothing was claimed, no run-state was written, no worker was spawned)\n")
	fmt.Fprintf(out, "\n$ claude %s\n\n", strings.Join(quoteArgs(plan.cmdArgs), " "))
	fmt.Fprintf(out, "=== SYSTEM PROMPT ===\n%s\n\n=== PROMPT ===\n%s\n", plan.sysPrompt, plan.prompt)
}

func finishPermissionsLabel(yolo bool) string {
	if yolo {
		return "--dangerously-skip-permissions (default: the odbiór procedure is broad by nature; the pm worker-guard hook still rides along)"
	}
	return "curated worker allowlist (--no-yolo)"
}

// finishClaimLabel renders the claim state for the dry-run, in the same words
// `pm finish status` uses. A dry-run that did not say who holds the claim would
// leave the one question worth asking before launching unanswered.
func finishClaimLabel(stateDir, tracker string) string {
	claim, err := storage.ReadFinishClaim(stateDir, tracker)
	switch {
	case err != nil && storage.IsCorruptFinishClaim(err):
		return fmt.Sprintf("free (the claim file is unparseable: %v)", err)
	case err != nil:
		return fmt.Sprintf("unknown (cannot read the claim: %v)", err)
	case claim == nil:
		return "free (no claim)"
	case claim.Expired(time.Now()):
		return fmt.Sprintf("free (expired claim from %s, pid %d - last refreshed %s ago)",
			claim.Host, claim.PID, storage.FinishClaimAge(claim.Refreshed))
	}
	return fmt.Sprintf("HELD by %s (pid %d) since %s - this run would refuse to start",
		claim.Host, claim.PID, claim.Started)
}

// executeFinish claims the run, spawns the acceptance worker under the full
// executor harness, and records what came back.
//
// The claim comes FIRST and a live one is fatal: the whole point of the lock is
// that a second acceptance of the same run never starts, so a busy claim must
// cost nothing but the error message - no worker, no run-state, no journal line.
func executeFinish(plan *finishPlan) (*finishResult, error) {
	errOut := plan.opts.stderr()
	tracker := plan.tracker.Meta.ID

	claim, err := storage.AcquireFinishClaim(plan.stateDir, tracker, plan.sessionID)
	if err != nil {
		return nil, err
	}
	// LIFO: stopRefresh (registered second) runs first, so nothing is still
	// moving the claim's stamp when the release reads it.
	defer func() {
		if rerr := storage.ReleaseFinishClaim(plan.stateDir, tracker, claim); rerr != nil {
			fmt.Fprintf(errOut, "pm finish: could not release the acceptance claim on %s: %v\n", tracker, rerr)
		}
	}()
	defer startFinishClaimRefresh(errOut, plan.stateDir, tracker, claim, storage.FinishClaimRefreshInterval)()

	runID := storage.NewRunID()
	run := &storage.RunState{
		TaskID:  tracker,
		RunID:   runID,
		Project: plan.slug,
		// Verbatim, never a literal of its own: the kind is what routes this
		// state to <tracker>.finish.json, and any other spelling writes it over
		// the run-state of the very run being accepted (see storage.RunKindFinish).
		Kind:           storage.RunKindFinish,
		Status:         storage.RunStatusRunning,
		PID:            os.Getpid(),
		RepoPath:       plan.workDir,
		Started:        time.Now().UTC().Format(time.RFC3339),
		LogPath:        storage.FinishRunLogPath(plan.stateDir, tracker),
		CurrentSub:     tracker,
		CurrentSession: plan.sessionID,
		Phase:          "running",
		Subs:           []storage.SubRun{{ID: tracker, Status: storage.RunStatusRunning, Session: plan.sessionID}},
	}
	runw := storage.NewRunWriter(plan.stateDir, run)
	_ = runw.Update(nil)
	journalBase := func() *storage.JournalEntry {
		return &storage.JournalEntry{
			Kind: storage.RunKindFinish, Project: plan.slug, TaskID: tracker, RunID: runID,
			PID: os.Getpid(), Model: plan.opts.model, Yolo: plan.opts.yolo,
		}
	}
	start := journalBase()
	start.Event = storage.JournalEventStart
	_ = storage.AppendJournal(plan.stateDir, start)

	fmt.Fprintf(errOut, "pm finish: launching headless acceptance worker for %s (sim %s)...\n",
		tracker, map[bool]string{true: "on", false: "off"}[plan.opts.sim])

	runStart := time.Now()
	journalEnd := func(status, errMsg string, sub storage.JournalSub) {
		e := journalBase()
		e.Event = storage.JournalEventEnd
		e.Status = status
		e.Error = errMsg
		e.DurationS = int(time.Since(runStart).Seconds())
		sub.ID = tracker
		sub.DurationS = e.DurationS
		e.Subs = []storage.JournalSub{sub}
		_ = storage.AppendJournal(plan.stateDir, e)
	}

	stopHeartbeat := runw.Heartbeat(workerHeartbeatInterval)
	defer stopHeartbeat()
	res, sessionID, err := runHeadless(errOut, "pm finish", plan.workDir, plan.cmdArgs, plan.opts.timeout,
		plan.proj.ResolveClaudeConfigDir(), nil, plan.sessionID,
		// The worker leads its own process group, so publishing its pgid is the
		// only handle the board has on it when it must SIGKILL this manager -
		// a signal this process cannot forward.
		func(pgid int) { _ = runw.Update(func(st *storage.RunState) { st.WorkerPGID = pgid }) },
		parseFinishOutput)
	stopHeartbeat()
	// The worker is gone and the heartbeat died with it: drop the in-flight
	// session marker in the same breath (an observer gates the heartbeat age on
	// it, and the tail after the worker - writing the report - would otherwise
	// render a frozen stamp as a live beat) and the pgid with it, since a stale
	// group id is one the next kill would signal after the kernel recycled it.
	_ = runw.Update(func(st *storage.RunState) {
		st.CurrentSession = ""
		st.WorkerPGID = 0
	})

	if err != nil {
		_ = runw.Update(func(st *storage.RunState) {
			st.Status = storage.RunStatusFailed
			st.Phase = ""
			st.Error = err.Error()
			if len(st.Subs) > 0 {
				st.Subs[0].Status = storage.RunStatusFailed
				st.Subs[0].Note = err.Error()
			}
		})
		journalEnd(storage.RunStatusFailed, err.Error(),
			storage.JournalSub{Result: finishBlocked, Note: err.Error(), Session: plan.sessionID})
		return nil, err
	}

	// The report is the artifact a human reads in the morning, so a failed write
	// is announced loudly and recorded on the run - but it does not fail the
	// run: the acceptance itself already happened, and reporting it as failed
	// would say something untrue about the work.
	note := strings.TrimSpace(res.Summary)
	if werr := writeFinishReport(plan.reportPath, res.Report); werr != nil {
		fmt.Fprintf(errOut, "pm finish: could not write the acceptance report to %s: %v\n", plan.reportPath, werr)
		note = strings.TrimSpace(note + " [report not saved: " + werr.Error() + "]")
	} else if strings.TrimSpace(res.Report) == "" {
		fmt.Fprintf(errOut, "pm finish: the acceptance returned no report - nothing written to %s\n", plan.reportPath)
	} else {
		fmt.Fprintf(errOut, "pm finish: report written to %s\n", plan.reportPath)
	}

	_ = runw.Update(func(st *storage.RunState) {
		st.Status = storage.RunStatusDone
		st.Phase = ""
		st.CurrentSub = ""
		if len(st.Subs) > 0 {
			st.Subs[0].Status = res.Status
			st.Subs[0].Note = finishRunNote(res, note)
			st.Subs[0].Turns = res.Turns
			st.Subs[0].CostUSD = res.CostUSD
		}
	})
	journalEnd(storage.RunStatusDone, "", storage.JournalSub{
		Result: res.Status, Note: finishRunNote(res, note), Session: sessionID,
		Turns: res.Turns, CostUSD: res.CostUSD,
	})
	return res, nil
}

// finishRunNote is what a board card and a journal line say about an
// acceptance. The count of unsettled visual claims is carried into it because
// that is the number that decides whether "done" means done: a detached
// acceptance never looks at a screen, so an accepted batch can still leave a
// morning's worth of checking, and a note reading only "done" would hide it.
func finishRunNote(res *finishResult, summary string) string {
	open := 0
	for _, s := range res.Subs {
		open += s.VisualClaimsOpen
	}
	if open == 0 {
		return summary
	}
	return strings.TrimSpace(fmt.Sprintf("%s [%d visual claim(s) still open across %d sub(s)]", summary, open, len(res.Subs)))
}

// writeFinishReport saves the acceptance's markdown report. An empty report
// writes nothing - an empty file would read as "the acceptance found nothing to
// say", which is a different statement from "it said nothing".
func writeFinishReport(path, report string) error {
	report = strings.TrimSpace(report)
	if report == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(report+"\n"), 0644)
}

// startFinishClaimRefresh keeps a claim alive for as long as the acceptance
// runs, and returns its stop function - idempotent, and it WAITS for the
// goroutine to exit, so once it returns nothing can touch the claim again
// (which is what lets the release that follows read the claim unsynchronised).
// Same shape as storage.RunWriter.Heartbeat, for the same reason.
//
// A refusal to refresh is reported and the ticker keeps going. It is genuinely
// bad news - past the TTL another acceptance may take the run over - but
// aborting a two-hour acceptance over one transient failure is worse than
// finishing it noisily, and there is nothing to undo either way: the work the
// acceptance has already pushed is pushed.
func startFinishClaimRefresh(errOut io.Writer, projectDir, tracker string, claim *storage.FinishClaim, interval time.Duration) func() {
	if interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := storage.RefreshFinishClaim(projectDir, tracker, claim); err != nil {
					fmt.Fprintf(errOut, "pm finish: could not refresh the acceptance claim on %s: %v\n", tracker, err)
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		<-exited
	}
}

// finishEnvelope is the `claude -p --output-format json` envelope of an
// acceptance run - claudeEnvelope's shape with the acceptance contract in
// structured_output.
type finishEnvelope struct {
	IsError          bool          `json:"is_error"`
	Result           string        `json:"result"`
	SessionID        string        `json:"session_id"`
	NumTurns         int           `json:"num_turns"`
	TotalCostUSD     float64       `json:"total_cost_usd"`
	StructuredOutput *finishResult `json:"structured_output"`
}

// parseFinishOutput is the acceptance's envelopeParser, with the same
// transcript rescue parseWorkerOutput performs and for the same reason: the
// envelope carries structured_output only while the StructuredOutput call is
// the run's LAST act, and a late background-task notification forcing one more
// turn used to turn a finished run into "returned no structured result".
// The recovery is always ANNOUNCED - a silent one would hide the worker's turn
// order behind pm papering over it.
func parseFinishOutput(data []byte, errOut io.Writer, configDir, dir string) (*finishResult, string, error) {
	res, sessionID, err := parseFinishResult(data)
	if err == nil || sessionID == "" {
		return res, sessionID, err
	}
	var rec *finishResult
	path := scanTranscripts(configDir, dir, sessionID, func(r io.Reader) bool {
		rec = scanFinishStructuredOutput(r)
		return rec != nil
	})
	if rec == nil {
		return res, sessionID, err
	}
	var env finishEnvelope
	if json.Unmarshal(data, &env) == nil {
		rec.Turns = env.NumTurns
		rec.CostUSD = env.TotalCostUSD
	}
	if errOut != nil {
		fmt.Fprintf(errOut, "pm finish: envelope carried no structured result - recovered status %q from the acceptance worker's last StructuredOutput call in %s (it kept talking after emitting it, most likely a late background-task notification)\n", rec.Status, path)
	}
	return rec, sessionID, nil
}

func parseFinishResult(data []byte) (*finishResult, string, error) {
	var env finishEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, "", fmt.Errorf("parse claude result: %w", err)
	}
	if env.StructuredOutput == nil {
		return nil, env.SessionID, fmt.Errorf("acceptance worker returned no structured result (is_error=%v): %s", env.IsError, strings.TrimSpace(env.Result))
	}
	// Turns/cost are the harness's, not the model's - lift them here, exactly
	// as parseClaudeResult does for a worker.
	env.StructuredOutput.Turns = env.NumTurns
	env.StructuredOutput.CostUSD = env.TotalCostUSD
	return env.StructuredOutput, env.SessionID, nil
}

// scanFinishStructuredOutput returns the last usable acceptance result in a
// transcript stream. LAST, not first: an acceptance that revises its verdict
// calls the tool more than once, and the final call is the one the harness
// would have reported.
func scanFinishStructuredOutput(r io.Reader) *finishResult {
	var found *finishResult
	eachStructuredOutput(r, func(raw json.RawMessage) {
		var res finishResult
		if json.Unmarshal(raw, &res) != nil {
			return
		}
		if strings.TrimSpace(res.Status) == "" {
			return
		}
		found = &res
	})
	return found
}

// finishProjectSlug resolves the project the same way the other executor-side
// commands do: --project, else cwd detection.
func finishProjectSlug(cmd *cobra.Command, store storage.TaskStore) (string, error) {
	flag, _ := cmd.Flags().GetString("project")
	var args []string
	if flag != "" {
		args = []string{flag}
	}
	return resolveProjectSlugArg(store, args)
}

// finishTarget resolves the project dir AND validates the tracker id every
// subcommand takes. The id becomes a file name under .executor/, so it is
// checked once, at the entry point, for the read paths too - storage validates
// its own writers, but `pm finish status ../../x` should be refused rather than
// quietly reading somewhere it has no business.
func finishTarget(cmd *cobra.Command, store storage.TaskStore, args []string) (string, string, error) {
	slug, err := finishProjectSlug(cmd, store)
	if err != nil {
		return "", "", err
	}
	tracker := args[0]
	if err := storage.ValidateTaskID(tracker); err != nil {
		return "", "", err
	}
	// A claim on a tracker that does not exist protects nothing while reading
	// as a success - and a mistyped id is the likeliest way two sessions each
	// get a green light on the same actual run. Resolve it, exactly like every
	// other id-taking surface in pm.
	if _, err := store.FindTaskExact(slug, tracker); err != nil {
		return "", "", fmt.Errorf("no task %s in project %s - a claim on a tracker that does not exist "+
			"would protect nothing: %w", tracker, slug, err)
	}
	return store.ProjectDir(slug), tracker, nil
}

func newFinishClaimCmd(store storage.TaskStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claim <tracker>",
		Short: "Claim a run for acceptance (fails when somebody else holds it)",
		Long: "Claims <tracker> for acceptance. Fails with a non-zero exit when a live claim is already held, " +
			"naming the host, the pid and how long ago it was refreshed, so it is obvious which machine to go " +
			"and look at. A claim nobody has refreshed within the TTL is taken over automatically.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, tracker, err := finishTarget(cmd, store, args)
			if err != nil {
				return err
			}
			session, _ := cmd.Flags().GetString("session")
			claim, err := storage.AcquireFinishClaim(dir, tracker, session)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "claimed %s for acceptance (host %s, pid %d)\n",
				claim.TrackerID, claim.Host, claim.PID)
			fmt.Fprintf(cmd.OutOrStdout(), "  claim: %s\n", storage.FinishClaimPath(dir, claim.TrackerID))
			fmt.Fprintf(cmd.OutOrStdout(), "  started: %s\n", claim.Started)
			fmt.Fprintf(cmd.OutOrStdout(), "  release it with `pm finish release %s --started %s`\n",
				claim.TrackerID, claim.Started)
			// Say plainly that nothing refreshes it yet: the claim is honoured
			// for the TTL and then reads as free, and NOTHING in pm currently
			// moves the stamp - the acceptance worker that will is pm finish
			// <tracker> (pm-cli-100-4). Promising a refresher that does not
			// exist would be worse than saying so.
			fmt.Fprintf(cmd.OutOrStdout(), "  valid for %s - nothing refreshes it yet, so a longer acceptance "+
				"can be taken over once it lapses\n", storage.FinishClaimTTL)
			return nil
		},
	}
	cmd.Flags().String("session", "", "CC session or run id to record in the claim (informational)")
	return cmd
}

func newFinishReleaseCmd(store storage.TaskStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release <tracker>",
		Short: "Release an acceptance claim you can identify as yours",
		Long: "Releases the claim on <tracker> - but only one you can NAME, with --started (the stamp " +
			"`pm finish claim` prints) or --session (the id you passed when claiming).\n\n" +
			"What naming it buys, precisely: it stops an ACCIDENTAL release. The claiming process has normally " +
			"exited by the time anyone releases (`pm finish claim` returns immediately), so a release that lifted " +
			"whatever claim it found would let a second session on the same machine - the ordinary case, two CC " +
			"sessions on one mac - delete a live claim it never took and then take the run. It is NOT " +
			"authentication: `pm finish status` prints the stamp, so anyone determined to override a claim can. " +
			"That is deliberate (a wedged acceptance has to be recoverable), and it is why an override is a " +
			"separate, explicit act rather than the default.\n\n" +
			"A claim that has already EXPIRED is cleared without an identifier: it reads as free to everyone " +
			"anyway, so removing it takes nothing from anybody. A live claim you cannot identify is left alone - " +
			"wait it out, since an unrefreshed claim expires on its own.\n\n" +
			"No claim at all is not an error: release is idempotent.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, tracker, err := finishTarget(cmd, store, args)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			current, err := storage.ReadFinishClaim(dir, tracker)
			if err != nil {
				if storage.IsCorruptFinishClaim(err) {
					// Every other surface treats a corrupt claim as free, so
					// release must not be the one command that turns it into a
					// dead end demanding a manual rm.
					fmt.Fprintf(out, "no usable claim on %s - the claim file is unreadable (%v); "+
						"it reads as free and the next claim takes it over\n", tracker, err)
					return nil
				}
				return fmt.Errorf("read finish claim for %s: %w", tracker, err)
			}
			if current == nil {
				fmt.Fprintf(out, "no claim on %s - nothing to release\n", tracker)
				return nil
			}

			started, _ := cmd.Flags().GetString("started")
			session, _ := cmd.Flags().GetString("session")
			expired := current.Expired(time.Now())
			switch {
			case expired:
				// Void already - everything else reports it free, so clearing it
				// takes nothing from anybody, whichever machine wrote it.
			case current.Host != storage.Hostname():
				return fmt.Errorf("claim on %s is held by %s (pid %d), not by this host (%s) - "+
					"release it there, or leave it: an unrefreshed claim expires after %s",
					tracker, current.Host, current.PID, storage.Hostname(), storage.FinishClaimTTL)
			case started != "" && started == current.Started:
			case session != "" && session == current.Session:
			default:
				return fmt.Errorf("claim on %s is held by %s (pid %d), started %s%s - "+
					"pass --started %s (or the --session you claimed with) to release it, "+
					"or leave it: an unrefreshed claim expires after %s",
					tracker, current.Host, current.PID, current.Started,
					sessionSuffix(current.Session), current.Started, storage.FinishClaimTTL)
			}

			// storage's own ownership test (Host+Started) is satisfied by
			// construction here - `current` came off disk - so the check that
			// carries weight is the one above. The call still goes through
			// ReleaseFinishClaim so the re-read there catches a claim that
			// changed hands between our read and the unlink.
			if err := storage.ReleaseFinishClaim(dir, tracker, current); err != nil {
				return err
			}
			note := ""
			if expired && started == "" && session == "" {
				note = " - it had already expired"
			}
			fmt.Fprintf(out, "released the claim on %s (was held by %s, pid %d)%s\n",
				tracker, current.Host, current.PID, note)
			return nil
		},
	}
	cmd.Flags().String("started", "", "the claim's `started` stamp, as printed by `pm finish claim`")
	cmd.Flags().String("session", "", "the session id recorded in the claim (`pm finish claim --session`)")
	return cmd
}

// sessionSuffix renders a claim's session for a message, or nothing when the
// claim carries none.
func sessionSuffix(session string) string {
	if session == "" {
		return ""
	}
	return fmt.Sprintf(", session %s", session)
}

func newFinishStatusCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "status <tracker>",
		Short: "Show who holds the acceptance claim on a run, and since when",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, tracker, err := finishTarget(cmd, store, args)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			claim, err := storage.ReadFinishClaim(dir, tracker)
			if err != nil {
				if storage.IsCorruptFinishClaim(err) {
					// Garbage CONTENT reads as free everywhere else (a truncated
					// file must not wall off a run), so report it as free here
					// too - with the reason, since a bare "free" would hide a
					// real oddity.
					fmt.Fprintf(out, "%s: free (the claim file is unparseable: %v)\n", tracker, err)
					return nil
				}
				// An I/O failure is NOT free: `pm finish claim` refuses on it, so
				// status must not answer "go ahead" where claim will refuse.
				return fmt.Errorf("cannot read the claim for %s: %w", tracker, err)
			}
			if claim == nil {
				fmt.Fprintf(out, "%s: free (no claim)\n", tracker)
				return nil
			}
			if claim.Expired(time.Now()) {
				fmt.Fprintf(out, "%s: free (expired claim from %s, pid %d - last refreshed %s ago, TTL %s)\n",
					tracker, claim.Host, claim.PID, storage.FinishClaimAge(claim.Refreshed), storage.FinishClaimTTL)
				return nil
			}
			fmt.Fprintf(out, "%s: claimed by %s (pid %d)\n", tracker, claim.Host, claim.PID)
			fmt.Fprintf(out, "  started   %s (%s ago)\n", claim.Started, storage.FinishClaimAge(claim.Started))
			fmt.Fprintf(out, "  refreshed %s (%s ago, expires after %s without a refresh)\n",
				claim.Refreshed, storage.FinishClaimAge(claim.Refreshed), storage.FinishClaimTTL)
			if claim.Session != "" {
				fmt.Fprintf(out, "  session   %s\n", claim.Session)
			}
			return nil
		},
	}
}
