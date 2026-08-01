package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// subOutcome records what happened to one sub during a manager run, for the
// end-of-run summary.
type subOutcome struct {
	id     string
	result string // merged | blocked | failed | skipped | conflict | manual
	note   string
	branch string // branch the sub's work landed on; empty for skips (no worker ran)
}

func newRunEpicCmd(store storage.TaskStore) *cobra.Command {
	var (
		model       string
		maxTurns    int
		yolo        bool
		timeout     time.Duration
		base        string
		noPR        bool
		allowDirty  bool
		dryRun      bool
		additional  bool
		slotPin     int
		independent bool
	)

	cmd := &cobra.Command{
		Use:   "run-epic [project] <tracker-id>",
		Short: "Sequentially drive a parent+subtask epic via isolated workers on an integration branch",
		Long: "The manager loop. Creates an `epic/<tracker>` integration branch, then for each ready sub " +
			"(by Order) branches `feat/<slug>` off it, runs `pm work` (the worker) there, and merges back on " +
			"verify-green -> status merged. Async: a blocked/failed sub is parked (status + reason in pm) and " +
			"the manager continues. Re-entrant: re-running skips merged/done subs. Ends by opening ONE draft " +
			"epic->main PR for human review. Never auto-merges to main, force-pushes, or closes the parent.\n\n" +
			"Independent (batch) mode - `epic_mode: independent` on the tracker, or --independent: for a batch " +
			"of UNRELATED tasks. Each sub gets its own fresh branch off the base branch, the worker runs " +
			"best-effort (records assumptions + handoff instead of parking), and the branch is pushed to origin " +
			"when it carries commits. No integration branch, no merging, no epic PR - a human finishes each " +
			"task on its own branch later.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := epicOptions{
				model: model, maxTurns: maxTurns, yolo: yolo, timeout: timeout,
				base: base, noPR: noPR, allowDirty: allowDirty,
				additional: additional, slotPin: slotPin, independent: independent,
				out: cmd.OutOrStdout(), errOut: cmd.ErrOrStderr(),
			}
			// --base is NOT inert here (it is the integration branch's fork point
			// in default mode too), so it is deliberately not passed in.
			warnInertFlags(opts.stderr(), "pm run-epic", additional, slotPin, "")

			plan, err := planEpic(store, args, opts)
			if err != nil {
				return err
			}
			if dryRun {
				printEpicDryRun(plan, opts)
				return nil
			}
			return executeEpic(store, plan, opts)
		},
	}

	cmd.Flags().StringVar(&model, "model", "opus", "model for the workers")
	cmd.Flags().IntVar(&maxTurns, "max-turns", 150, "max agent turns per worker")
	cmd.Flags().BoolVar(&yolo, "yolo", false, "bypass all permission checks in the workers")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Minute, "max wall-clock time per worker")
	cmd.Flags().StringVar(&base, "base", "", "base branch the integration branch forks from (default: with --additional executor.base_branch, else main)")
	cmd.Flags().BoolVar(&noPR, "no-pr", false, "do not open the final epic->main draft PR")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "skip the clean-working-tree precondition")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan (subs, branches, readiness) without running anything")
	cmd.Flags().BoolVar(&additional, "additional", false, "run the whole epic in an isolated 'additional' worktree slot instead of the main checkout; requires executor.worktrees (or legacy additional_worktree) in project.yaml")
	cmd.Flags().IntVar(&slotPin, "slot", 0, "with --additional: pin a specific worktree slot (1-based); default 0 = first free slot")
	cmd.Flags().BoolVar(&independent, "independent", false, "batch mode for unrelated subs: each on its own branch off the base, pushed, never merged (also enabled by `epic_mode: independent` on the tracker)")

	return cmd
}

// epicOptions are the run-level knobs `pm run-epic` resolves from its flags,
// plus the command's output writers (so RunE, planEpic and executeEpic can be
// driven by a test without capturing the process streams).
type epicOptions struct {
	model       string
	maxTurns    int
	yolo        bool
	timeout     time.Duration
	base        string
	noPR        bool
	allowDirty  bool
	additional  bool
	slotPin     int
	independent bool
	out         io.Writer
	errOut      io.Writer
}

// stdout/stderr resolve the run's writers, defaulting to the process streams -
// the same contract as workOptions.stderr(), so an epicOptions built by hand
// (every planEpic test does) stays safe to hand to executeEpic.
func (o epicOptions) stdout() io.Writer {
	if o.out != nil {
		return o.out
	}
	return os.Stdout
}

func (o epicOptions) stderr() io.Writer {
	if o.errOut != nil {
		return o.errOut
	}
	return os.Stderr
}

// epicPlan is everything a run resolves BEFORE it touches git, pm or a worker:
// the project + executor preflight, the tracker's ready subs, the mode, and the
// branches. It is exactly the planWork/executeWork split `pm work` already uses
// - assembling it has NO side effects (reads only), which is what makes the
// manager's preflight testable and lets --dry-run print the real plan.
type epicPlan struct {
	tracker     *storage.Task
	slug        string
	proj        *storage.Project
	exc         storage.Executor
	subs        []*storage.Task
	independent bool
	startStatus storage.TaskStatus
	doneStatus  storage.TaskStatus
	epicBranch  string
	baseBranch  string
	slots       []storage.ResolvedWorktree
	// workDir is where the run will happen - the main checkout, or (additional
	// mode) a PROVISIONAL description of the slot pool for the dry-run display;
	// the real slot is claimed in executeEpic.
	workDir string
}

// planEpic resolves and validates a `pm run-epic` invocation without mutating
// anything: no branch is created, no status moves, no run-state is written.
func planEpic(store storage.TaskStore, args []string, opts epicOptions) (*epicPlan, error) {
	tracker, slug, err := resolveWorkTask(store, args)
	if err != nil {
		return nil, err
	}
	proj, exc, err := preflightProject(store, slug)
	if err != nil {
		return nil, err
	}
	// --additional is opt-in PER RUN; the default is the main checkout
	// (unchanged pre-worktree behaviour). It requires the project to have at
	// least one worktree slot configured.
	slots := exc.ResolveWorktrees(proj.Path)
	if opts.additional && len(slots) == 0 {
		return nil, errNoWorktreeSlots(slug)
	}

	subs, err := readySubs(store, slug, tracker.Meta.ID)
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, fmt.Errorf("%s is not a tracker - no subtasks have parent: %s", tracker.Meta.ID, tracker.Meta.ID)
	}

	// Independent (batch) mode: the tracker declares it in frontmatter
	// (epic_mode: independent) so board launches need no extra flag;
	// --independent is the CLI override for a tracker without it.
	if err := storage.ValidateEpicMode(tracker.Meta.EpicMode); err != nil {
		return nil, fmt.Errorf("tracker %s: %w", tracker.Meta.ID, err)
	}
	independentMode := opts.independent || tracker.Meta.EpicMode == storage.EpicModeIndependent

	// Base precedence for the integration branch: --base > (only with
	// --additional) executor.base_branch > "main". In default mode base_branch
	// is not consulted, so behaviour matches pre-worktree run-epic.
	// Independent mode is new (no compat concern) and every sub forks from
	// the base directly, so executor.base_branch always applies there.
	execBase := ""
	if opts.additional || independentMode {
		execBase = exc.BaseBranch
	}

	workDir := proj.Path
	if opts.additional {
		// Provisional (dry-run display); the real slot is claimed in executeEpic.
		workDir = describeSlotPool(slots)
	}

	return &epicPlan{
		tracker:     tracker,
		slug:        slug,
		proj:        proj,
		exc:         exc,
		subs:        subs,
		independent: independentMode,
		startStatus: storage.TaskStatus(exc.StartStatus),
		doneStatus:  storage.TaskStatus(exc.DoneStatus),
		epicBranch:  "epic/" + tracker.Meta.ID,
		baseBranch:  resolveWorktreeBase(opts.base, execBase, "main"),
		slots:       slots,
		workDir:     workDir,
	}, nil
}

// printEpicDryRun renders the resolved plan (subs, branches, readiness) plus
// the once-per-run commands the real run would execute.
func printEpicDryRun(plan *epicPlan, opts epicOptions) {
	printEpicPlan(opts.stdout(), plan.tracker, plan.epicBranch, plan.baseBranch, plan.startStatus, plan.doneStatus,
		plan.subs, opts.additional, plan.workDir, plan.independent)
	if opts.additional {
		if prep := strings.TrimSpace(plan.exc.Prepare); prep != "" {
			fmt.Fprintf(opts.stdout(), "\nprepare (once per run, in claimed slot): %s\n", prep)
		}
	}
	if bl := strings.TrimSpace(plan.exc.Baseline); bl != "" {
		fmt.Fprintf(opts.stdout(), "\nbaseline (once per run, injected into every worker prompt): %s\n", bl)
	}
}

// executeEpic is the manager loop proper: claim the worktree slot, set up the
// integration branch (or verify the base in independent mode), prepare + capture
// the baseline once, then drive every ready sub and record the run.
func executeEpic(store storage.TaskStore, plan *epicPlan, opts epicOptions) error {
	tracker, slug, subs := plan.tracker, plan.slug, plan.subs
	epicBranch, baseBranch, doneStatus := plan.epicBranch, plan.baseBranch, plan.doneStatus
	independentMode := plan.independent
	errOut := opts.stderr()

	// Hard gate, not a warning: MoveTask validates statuses, so with an
	// unlisted done status every "mark merged" move would fail AFTER the
	// work merged into the integration branch - and the re-entrant done
	// check would re-drive those subs on the next run. Deliberately here and
	// not in planEpic: --dry-run has always printed the plan regardless.
	if !statusAllowed(doneStatus, store.GetProjectStatuses(slug)) {
		return fmt.Errorf("done status %q is not in project %s statuses - add it to project.yaml (statuses: [todo, doing, %s, ...]) before running the epic", doneStatus, slug, doneStatus)
	}

	// Additional mode: the whole epic runs in ONE claimed worktree slot
	// (first free, or --slot N), leaving the user's main checkout untouched.
	// Ensure + seed + lock it ONCE for the run; the deferred release frees
	// the lock on every normal exit (a kill is handled by the board, see
	// killRun). Every per-sub plan targets this claimed slot via
	// opts.slotDir/slotEnv - subs never re-resolve the pool mid-run.
	workDir := plan.proj.Path
	var claimedEnv []string
	if opts.additional {
		slot, release, err := acquireWorktreeSlot(plan.proj, slug, plan.slots, opts.slotPin, tracker.Meta.ID, "run-epic")
		if err != nil {
			return err
		}
		defer release()
		workDir = slot.Path
		claimedEnv = slot.Env
		if len(plan.slots) > 1 {
			fmt.Fprintf(errOut, "pm run-epic: claimed worktree slot %s\n", workDir)
		}
		// Wipe any leftovers from a previous (possibly killed) run before
		// touching the integration branch, so half-done work never bleeds in.
		// Preserves the integration branch's COMMITTED history (re-entrancy)
		// and ignored deps/configs - it only drops the dirty working tree.
		if err := gitCleanWorktree(workDir); err != nil {
			return fmt.Errorf("reset worktree %s to a clean state: %w", workDir, err)
		}
	}

	// Precondition: clean working tree (the manager switches branches in
	// place). Skipped in additional mode - the worktree is executor-managed
	// and the user's main checkout is intentionally left alone.
	if !opts.allowDirty && !opts.additional {
		if err := requireCleanWorkingTree(workDir); err != nil {
			return err
		}
	}

	if independentMode {
		if !branchExists(workDir, baseBranch) {
			return fmt.Errorf("independent mode: base branch %q does not exist at %s", baseBranch, workDir)
		}
		fmt.Fprintf(errOut, "pm run-epic: %s INDEPENDENT - each sub on its own branch off %s (%d sub(s))\n", tracker.Meta.ID, baseBranch, len(subs))
	} else {
		if err := gitEnsureBranch(workDir, epicBranch, baseBranch); err != nil {
			return fmt.Errorf("create integration branch %s: %w", epicBranch, err)
		}
		fmt.Fprintf(errOut, "pm run-epic: %s on %s (%d sub(s))\n", tracker.Meta.ID, epicBranch, len(subs))
	}

	// Prepare the claimed slot ONCE for the whole run (deps install etc.) -
	// per-sub would repeat it for every worker. Runs after branch setup so
	// the lockfile prepare sees is the one every sub forks from; in
	// independent mode check the base branch out first for the same reason.
	if opts.additional {
		if prep := strings.TrimSpace(plan.exc.Prepare); prep != "" {
			if independentMode {
				if err := gitEnsureBranch(workDir, baseBranch, ""); err != nil {
					return fmt.Errorf("checkout %s for prepare: %w", baseBranch, err)
				}
			}
			fmt.Fprintf(errOut, "pm run-epic: prepare in %s: %s\n", workDir, prep)
			if err := runPrepare(workDir, prep); err != nil {
				return fmt.Errorf("prepare cmd (%s) failed in %s: %w", prep, workDir, err)
			}
		}
	}

	workOpts := workOptions{
		standalone: false, model: opts.model, maxTurns: opts.maxTurns, yolo: opts.yolo,
		allowDirty: true, timeout: opts.timeout, additional: opts.additional, independent: independentMode,
		errOut: errOut,
	}
	if opts.additional {
		workOpts.slotDir = workDir
		workOpts.slotEnv = claimedEnv
	}

	// Capture the verification baseline ONCE for the whole run, on the
	// branch every sub forks from (base in independent mode, the
	// integration branch otherwise), so each worker judges its verify on
	// NEW failures only - instead of every sub re-discovering the same
	// pre-existing breakage and reporting a false failure. Best-effort: a
	// baseline that cannot run degrades to no-baseline, never aborts.
	baselineUsed := ""
	if bl := strings.TrimSpace(plan.exc.Baseline); bl != "" {
		ref := epicBranch
		if independentMode {
			ref = baseBranch
		}
		if err := gitEnsureBranch(workDir, ref, ""); err != nil {
			fmt.Fprintf(errOut, "pm run-epic: checkout %s for baseline failed (%v) - continuing without a baseline\n", ref, err)
		} else {
			fmt.Fprintf(errOut, "pm run-epic: baseline in %s: %s\n", workDir, bl)
			workOpts.baseline = captureBaseline(errOut, workDir, bl)
			if workOpts.baseline != "" {
				baselineUsed = bl
			}
		}
	}

	// Run-state for observability: the manager owns the epic-level file;
	// driveSub fills in each sub's worker session as it goes. It lives in
	// the pm data dir (where the TUI reads it), NOT the git repo. Every
	// WriteRunState below is best-effort observability: a failed write only
	// costs a stale dashboard, never correctness, so the error is dropped.
	stateDir := store.ProjectDir(slug)
	// One id for this physical run, stamped on the run-state and on
	// every journal line below, so `pm executor stats` can tell this run
	// apart from any other that happens to reuse the pid.
	runID := storage.NewRunID()
	run := &storage.RunState{
		TaskID:   tracker.Meta.ID,
		RunID:    runID,
		Project:  slug,
		Kind:     "run-epic",
		Status:   storage.RunStatusRunning,
		PID:      os.Getpid(),
		RepoPath: workDir,
		Started:  time.Now().UTC().Format(time.RFC3339),
		LogPath:  storage.ExecutorLogPath(stateDir, tracker.Meta.ID),
	}
	for _, s := range subs {
		init := "pending"
		if s.Meta.Status == doneStatus || s.Meta.Status == storage.StatusDone {
			init = "skipped"
		}
		run.Subs = append(run.Subs, storage.SubRun{ID: s.Meta.ID, Status: init})
	}
	// From here on the run-state is only touched through the writer: each
	// sub's worker heartbeats this same struct from its own goroutine, so
	// every mutation has to be serialized (see storage.RunWriter).
	rw := storage.NewRunWriter(stateDir, run)
	_ = rw.Update(nil)

	// Journal: durable cross-run history (retro feedstock). Start line now;
	// end line with per-sub outcomes after the loop. Best-effort like the
	// run-state writes above. Independent mode has no integration branch,
	// so the journal records the fork base instead.
	journalBranch := epicBranch
	if independentMode {
		journalBranch = "independent:" + baseBranch
	}
	journalDir := ""
	if opts.additional {
		journalDir = workDir
	}
	epicStart := time.Now()
	_ = storage.AppendJournal(stateDir, &storage.JournalEntry{
		Event: storage.JournalEventStart, Kind: "run-epic", Project: slug, TaskID: tracker.Meta.ID, RunID: runID,
		PID: os.Getpid(), Model: opts.model, Additional: opts.additional, Yolo: opts.yolo, Independent: independentMode, Branch: journalBranch,
		WorkDir: journalDir, Baseline: baselineUsed,
	})

	// ID -> sub for the dependency gate. Pointers are shared with the loop,
	// so statuses mutated by driveSub are visible to later subs' gates.
	byID := make(map[string]*storage.Task, len(subs))
	for _, s := range subs {
		byID[s.Meta.ID] = s
	}

	var outcomes []subOutcome
	subDurations := map[string]int{} // sub id -> driveSub wall-clock seconds
	for _, sub := range subs {
		// Decide, without spending a worker, whether this sub runs. Covers
		// the re-entrant done skip, the manual gate, the not-ready skip, and
		// the depends_on gate (see classifySub).
		oc, drive, announce := classifySub(sub, byID, plan.startStatus, doneStatus)
		if !drive {
			outcomes = append(outcomes, oc)
			_ = rw.Update(func(run *storage.RunState) { updateSubRun(run, oc.id, oc.result, oc.note) })
			if announce != "" {
				fmt.Fprintf(errOut, "pm run-epic: %s\n", announce)
			}
			continue
		}

		_ = rw.Update(func(run *storage.RunState) {
			run.CurrentSub = sub.Meta.ID
			updateSubRun(run, sub.Meta.ID, storage.RunStatusRunning, "")
		})

		subOpts := workOpts
		if sub.Meta.Model != "" {
			subOpts.model = sub.Meta.Model
			fmt.Fprintf(errOut, "pm run-epic: %s model override -> %s\n", sub.Meta.ID, sub.Meta.Model)
		}

		subStart := time.Now()
		if independentMode {
			oc = driveSubIndependent(store, workDir, slug, tracker, sub, baseBranch, doneStatus, subOpts, rw, opts.additional)
		} else {
			oc = driveSub(store, workDir, slug, tracker, sub, epicBranch, doneStatus, subOpts, rw, opts.additional)
		}
		subDurations[oc.id] = int(time.Since(subStart).Seconds())
		outcomes = append(outcomes, oc)

		_ = rw.Update(func(run *storage.RunState) {
			run.CurrentSub = ""
			run.CurrentSession = ""
			updateSubRun(run, oc.id, oc.result, oc.note)
		})
	}

	_ = rw.Update(func(run *storage.RunState) { run.Status = storage.RunStatusDone })

	// Journal end line: the run's durable record (outcome + duration per sub).
	// The per-sub stats are read through the writer like every other access.
	var jSubs []storage.JournalSub
	rw.Read(func(run *storage.RunState) { jSubs = journalSubs(outcomes, subDurations, run) })
	_ = storage.AppendJournal(stateDir, &storage.JournalEntry{
		Event: storage.JournalEventEnd, Kind: "run-epic", Project: slug, TaskID: tracker.Meta.ID, RunID: runID,
		PID: os.Getpid(), Model: opts.model, Additional: opts.additional, Yolo: opts.yolo, Independent: independentMode, Branch: journalBranch,
		WorkDir: journalDir, Baseline: baselineUsed,
		Status: storage.RunStatusDone, DurationS: int(time.Since(epicStart).Seconds()),
		Subs: jSubs,
	})

	if independentMode {
		printEpicSummary(opts.stdout(), tracker, "independent, off "+baseBranch, outcomes)
		fmt.Fprintf(errOut, "\nEach sub lives on its own branch (pushed to origin when it carried commits). Finish + verify every task by hand, then open per-task PRs - nothing was merged anywhere.\n")
		return nil
	}

	// Leave the worktree (or main checkout) on the integration branch with
	// the accumulated work. Best-effort: the branch already exists (created
	// above) and any real checkout failure surfaced earlier; a stray here
	// only affects which branch is checked out at exit.
	_ = gitEnsureBranch(workDir, epicBranch, "")

	printEpicSummary(opts.stdout(), tracker, "integration: "+epicBranch, outcomes)

	if !opts.noPR && anyMerged(outcomes) {
		openEpicPR(errOut, workDir, epicBranch, baseBranch, tracker)
	}
	fmt.Fprintf(errOut, "\nThe epic->%s PR and closing %s stay human-gated - review the integration branch when convenient.\n", baseBranch, tracker.Meta.ID)
	return nil
}

// classifySub decides what happens to a sub BEFORE any worker is spawned. It is
// pure (never mutates the sub) so both the manager loop and tests can rely on
// it. It returns the recorded outcome, drive=true only when the sub should be
// handed to driveSub, and an optional stderr line to announce a skip.
//
// Precedence: a finished sub is a re-entrant skip; a manual sub is a PERMANENT
// human-only gate (no worker, status untouched, re-skipped forever until the
// human moves it to done - distinct "manual" outcome, not "skipped"); a
// non-ready sub is skipped quietly; an unmet depends_on parks the sub without a
// worker but leaves it on its ready status so a later re-run picks it up.
func classifySub(sub *storage.Task, byID map[string]*storage.Task, startStatus, doneStatus storage.TaskStatus) (oc subOutcome, drive bool, announce string) {
	if sub.Meta.Status == doneStatus || sub.Meta.Status == storage.StatusDone {
		return subOutcome{sub.Meta.ID, "skipped", "already " + string(sub.Meta.Status), ""}, false, ""
	}
	if sub.Meta.Mode == "manual" {
		return subOutcome{sub.Meta.ID, "manual", "manual sub - run by hand, move to " + string(doneStatus) + " when done", ""},
			false, sub.Meta.ID + " skipped - manual sub (human-only)"
	}
	if sub.Meta.Status != startStatus {
		return subOutcome{sub.Meta.ID, "skipped", "not ready (status " + string(sub.Meta.Status) + ")", ""}, false, ""
	}
	if reason := unmetDeps(sub, byID, doneStatus); reason != "" {
		return subOutcome{sub.Meta.ID, "skipped", reason, ""}, false, sub.Meta.ID + " skipped - " + reason
	}
	return subOutcome{id: sub.Meta.ID}, true, ""
}

// unmetDeps returns a human reason if any of sub's depends_on entries is not yet
// satisfied (merged into the integration branch = doneStatus, or done), else "".
// An unknown dependency id is treated as unmet (likely a typo - surface it).
func unmetDeps(sub *storage.Task, byID map[string]*storage.Task, doneStatus storage.TaskStatus) string {
	var unmet []string
	for _, dep := range sub.Meta.DependsOn {
		d := byID[dep]
		if d == nil {
			unmet = append(unmet, dep+" (unknown)")
			continue
		}
		if d.Meta.Status != doneStatus && d.Meta.Status != storage.StatusDone {
			unmet = append(unmet, dep+" ("+string(d.Meta.Status)+")")
		}
	}
	if len(unmet) > 0 {
		return "waiting on " + strings.Join(unmet, ", ")
	}
	return ""
}

// readySubs returns the children of trackerID for project slug, sorted by Order
// then ID (the manager's sequencing).
func readySubs(store storage.TaskStore, slug, trackerID string) ([]*storage.Task, error) {
	tasks, err := store.GetTasks(slug)
	if err != nil {
		return nil, err
	}
	var subs []*storage.Task
	for _, t := range tasks {
		if t.Meta.Parent == trackerID {
			subs = append(subs, t)
		}
	}
	sort.SliceStable(subs, func(i, j int) bool {
		if subs[i].Meta.Order != subs[j].Meta.Order {
			return subs[i].Meta.Order < subs[j].Meta.Order
		}
		return subs[i].Meta.ID < subs[j].Meta.ID
	})
	return subs, nil
}

// logIfErr surfaces a non-fatal executor bookkeeping failure (a status move,
// a parent-feedback write) on stderr without aborting the run. These rarely
// fail (local file writes in a dir we own), but a silent failure here would
// desync a sub's board status from reality - so make it visible.
func logIfErr(errOut io.Writer, context string, err error) {
	if err != nil {
		fmt.Fprintf(errOut, "pm run-epic: %s: %v\n", context, err)
	}
}

// pushIfAhead pushes branch to origin when it carries commits base does not
// have. Returns a short human note for the outcome ("" when there was nothing
// to push). Never fatal - a failed push only means the work stays local.
//
// A FAILED ahead-check (typo'd base branch, detached ref) is not "nothing to
// push": that silent 0 was exactly the partial-work-dies-at-next-wipe scenario
// this mechanism exists to prevent. When in doubt, push anyway and say why.
func pushIfAhead(dir, branch, base string) string {
	ahead, err := gitAheadCount(dir, branch, base)
	if err == nil && ahead == 0 {
		return ""
	}
	if !gitHasRemote(dir) {
		return "no remote - branch " + branch + " stays local"
	}
	if perr := gitPush(dir, branch); perr != nil {
		note := "push failed (" + perr.Error() + ") - branch " + branch + " stays local"
		if err != nil {
			note += "; ahead-check also failed (" + err.Error() + ")"
		}
		return note
	}
	if err != nil {
		return "pushed " + branch + " (ahead-check failed: " + err.Error() + " - pushed to be safe)"
	}
	return "pushed " + branch
}

// updateSubRun sets the status (and note) of sub id in the run-state, appending
// an entry if it is not present yet.
func updateSubRun(run *storage.RunState, id, status, note string) {
	for i := range run.Subs {
		if run.Subs[i].ID == id {
			run.Subs[i].Status = status
			run.Subs[i].Note = note
			return
		}
	}
	run.Subs = append(run.Subs, storage.SubRun{ID: id, Status: status, Note: note})
}

// setSubSession pins the worker's session on the run and on sub id, so the live
// agent-view can tail the right transcript. Called under the run writer's lock.
func setSubSession(run *storage.RunState, id, session string) {
	run.CurrentSession = session
	for i := range run.Subs {
		if run.Subs[i].ID == id {
			run.Subs[i].Session = session
		}
	}
}

// setSubStats records the worker envelope's effort stats on sub id. Called
// under the run writer's lock.
func setSubStats(run *storage.RunState, id string, turns int, cost float64) {
	for i := range run.Subs {
		if run.Subs[i].ID == id {
			run.Subs[i].Turns = turns
			run.Subs[i].CostUSD = cost
		}
	}
}

// journalSubs converts the manager's outcomes into journal sub records,
// attaching each sub's driveSub wall-clock plus the worker session and
// envelope stats (from the run-state, which driveSub filled in as it went).
func journalSubs(outcomes []subOutcome, durations map[string]int, run *storage.RunState) []storage.JournalSub {
	byID := make(map[string]storage.SubRun, len(run.Subs))
	for _, s := range run.Subs {
		byID[s.ID] = s
	}
	out := make([]storage.JournalSub, 0, len(outcomes))
	for _, o := range outcomes {
		sr := byID[o.id]
		out = append(out, storage.JournalSub{
			ID: o.id, Result: o.result, Note: o.note, Branch: o.branch,
			DurationS: durations[o.id], Session: sr.Session,
			Turns: sr.Turns, CostUSD: sr.CostUSD,
		})
	}
	return out
}

func briefReason(res *workerResult) string {
	if len(res.Unresolved) > 0 {
		return strings.Join(res.Unresolved, "; ")
	}
	return strings.TrimSpace(res.Summary)
}

const managerNotesHeading = "## Manager Notes (cross-cutting)"

// parkedFindings is the feedback to bubble up for a non-merged sub: its open
// questions if any, else its summary, so the parent always carries a one-line
// reason even when the worker raised no explicit questions.
func parkedFindings(res *workerResult) []string {
	if len(res.Unresolved) > 0 {
		return res.Unresolved
	}
	if s := strings.TrimSpace(res.Summary); s != "" {
		return []string{s}
	}
	return []string{"(no detail)"}
}

// recordSubFeedback records a sub's findings into the parent's Manager Notes
// section inside the Spec (so later subs, whose prompt carries the parent SPEC,
// see them, and the human sees parked subs in one place). Falls back to the Log
// when the parent has no Spec block. Per-sub dedupe: any prior lines for the
// same subID are replaced, so re-running the epic refreshes a sub's note instead
// of stacking duplicates. outcome != "merged" tags the line (e.g. "x2 · blocked")
// so a parked sub reads differently from a merged sub's cross-cutting note.
func recordSubFeedback(store storage.TaskStore, parent *storage.Task, subID, outcome string, findings []string) error {
	// The parent was read at run start and is rewritten after EVERY sub, while
	// other sessions may be editing it - refresh under the project lock so a
	// mid-run edit (spec update, note) is never clobbered by a stale copy.
	if release, err := store.LockProject(parent.Project); err == nil {
		defer release()
	}
	// PRECONDITION, not a best-effort refresh (same rule as Store.MoveTask and
	// applyWorkerResult): if the parent's file is gone - deleted, or no longer
	// parsing - while the epic ran, the copy the manager has held since run
	// start must NOT be written back; that write resurrected a deleted tracker
	// from a stale pointer. Refuse and let logIfErr surface it.
	fresh, err := store.FindTaskExact(parent.Project, parent.Meta.ID)
	if err != nil {
		return fmt.Errorf("refresh parent %s: %w", parent.Meta.ID, err)
	}
	*parent = *fresh
	note := managerNoteBlock(subID, outcome, findings)
	spec := storage.ExtractSpec(parent.Body)
	if spec == "" {
		body := dropSubLines(parent.Body, subID)
		if note != "" { // empty findings = just clear this sub's stale lines
			body = appendLog(body, managerNotesHeading+"\n"+note)
		}
		parent.Body = body
	} else {
		spec = dropSubLines(spec, subID)
		if note != "" {
			if strings.Contains(spec, managerNotesHeading) {
				spec = strings.TrimRight(spec, "\n") + "\n" + note
			} else {
				spec = strings.TrimRight(spec, "\n") + "\n\n" + managerNotesHeading + "\n" + note
			}
		}
		parent.Body = storage.ApplySpec(parent.Body, spec)
	}
	parent.Meta.Updated = storage.Today()
	return store.WriteTask(parent)
}

// dropSubLines removes any Manager-Notes bullet that belongs to subID, so a
// sub's feedback can be refreshed in place. Matches "- [subID]" and the tagged
// "- [subID · outcome]" form, but not a different sub that shares a prefix
// (e.g. dropping "x2" must not touch "x2-1").
func dropSubLines(body, subID string) string {
	exact := "- [" + subID + "]"
	tagged := "- [" + subID + " "
	var kept []string
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, exact) || strings.HasPrefix(t, tagged) {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n")
}

func managerNoteBlock(subID, outcome string, findings []string) string {
	tag := subID
	if outcome != "" && outcome != "merged" {
		tag = subID + " · " + outcome
	}
	var sb strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&sb, "- [%s] %s\n", tag, strings.TrimSpace(f))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func anyMerged(outcomes []subOutcome) bool {
	for _, o := range outcomes {
		if o.result == "merged" {
			return true
		}
	}
	return false
}

// openEpicPR best-effort pushes the integration branch and opens a DRAFT
// epic->base PR targeting the same resolved base the integration branch was
// forked from (--base flag > executor.base_branch > main) - a hardcoded main
// here would carry every base-only commit into the PR diff. Failures are
// reported with a manual fallback, never fatal.
func openEpicPR(errOut io.Writer, dir, epicBranch, baseBranch string, tracker *storage.Task) {
	if !gitHasRemote(dir) {
		fmt.Fprintf(errOut, "\nno git remote configured - push %s and open the epic->%s PR manually.\n", epicBranch, baseBranch)
		return
	}
	if err := gitPush(dir, epicBranch); err != nil {
		fmt.Fprintf(errOut, "\n%v\nopen the epic->%s PR manually once pushed.\n", err, baseBranch)
		return
	}
	title := fmt.Sprintf("Epic %s: %s", tracker.Meta.ID, tracker.Meta.Title)
	body := fmt.Sprintf("Integration PR for epic %s. Subs were implemented + reviewed + verified on isolated branches and merged into `%s`.\n\nReview and merge when ready - this PR is intentionally a draft.", tracker.Meta.ID, epicBranch)
	// Deadline like every other network step: a wedged gh (auth prompt on a
	// headless box, API hang) must not hang the manager at the very end of an
	// otherwise finished run.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, "gh", "pr", "create", "--draft", "--base", baseBranch, "--head", epicBranch, "--title", title, "--body", body)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(errOut, "\ngh pr create timed out after 2m\nopen the epic->%s PR manually.\n", baseBranch)
		return
	}
	if err != nil {
		fmt.Fprintf(errOut, "\ngh pr create failed: %s\nopen the epic->%s PR manually.\n", strings.TrimSpace(string(out)), baseBranch)
		return
	}
	fmt.Fprintf(errOut, "\nopened draft PR: %s\n", strings.TrimSpace(string(out)))
}

// describeSlotPool renders the worktree pool for dry-run display: the single
// slot's path, or "first free of: p1 | p2 | ..." when there is a real pool.
func describeSlotPool(slots []storage.ResolvedWorktree) string {
	if len(slots) == 1 {
		return slots[0].Path
	}
	paths := make([]string, 0, len(slots))
	for _, s := range slots {
		paths = append(paths, s.Path)
	}
	return "first free of: " + strings.Join(paths, " | ")
}

func printEpicPlan(w io.Writer, tracker *storage.Task, epicBranch, base string, startStatus, doneStatus storage.TaskStatus, subs []*storage.Task, additional bool, workDir string, independent bool) {
	runLine := "run: DEFAULT (main checkout, clean-tree required)"
	if additional {
		runLine = "run: ADDITIONAL worktree " + workDir + " (isolated branch/port/sim; main checkout untouched)"
	}
	branchLine := fmt.Sprintf("integration branch: %s (off %s)", epicBranch, base)
	if independent {
		branchLine = fmt.Sprintf("mode: INDEPENDENT - each sub on its own branch off %s, pushed to origin; no integration branch, no merging, no epic PR", base)
	}
	fmt.Fprintf(w, "# pm run-epic (dry-run)\ntracker: %s  %s\n%s\n%s\nready status: %s -> done status: %s\n\nsubs (in Order):\n",
		tracker.Meta.ID, tracker.Meta.Title, runLine, branchLine, startStatus, doneStatus)
	for _, s := range subs {
		ready := "skip"
		if s.Meta.Status == doneStatus || s.Meta.Status == storage.StatusDone {
			ready = "done"
		} else if s.Meta.Mode == "manual" {
			ready = "MANUAL"
		} else if s.Meta.Status == startStatus {
			ready = "READY"
		}
		dep := ""
		if len(s.Meta.DependsOn) > 0 {
			dep = "  depends_on=" + strings.Join(s.Meta.DependsOn, ",")
		}
		fmt.Fprintf(w, "  [%-6s] %-14s %-8s order=%d  -> %s%s\n", ready, s.Meta.ID, s.Meta.Status, s.Meta.Order, resolveWorkBranch(s), dep)
	}
}

// printEpicSummary renders the per-sub outcomes under a header describing WHERE
// the work went. The caller passes that label whole ("integration: epic/x", or
// "independent, off main"): the header used to hardcode "integration: %s", so
// an independent run - which has no integration branch at all - printed
// "(integration: independent, off main)".
func printEpicSummary(w io.Writer, tracker *storage.Task, label string, outcomes []subOutcome) {
	fmt.Fprintf(w, "\n# Epic %s summary (%s)\n", tracker.Meta.ID, label)
	for _, o := range outcomes {
		line := fmt.Sprintf("  %-9s %s", o.result, o.id)
		if o.note != "" {
			line += "  - " + o.note
		}
		fmt.Fprintln(w, line)
	}
}
