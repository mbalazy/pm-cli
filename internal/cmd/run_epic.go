package cmd

import (
	"fmt"
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
			tracker, slug, err := resolveWorkTask(store, args)
			if err != nil {
				return err
			}
			proj, err := store.GetProject(slug)
			if err != nil {
				return fmt.Errorf("load project %s: %w", slug, err)
			}
			if proj.Path == "" {
				return fmt.Errorf("project %s has no path - the executor needs a git repo (set `path` in project.yaml)", slug)
			}
			if !isGitRepo(proj.Path) {
				return fmt.Errorf("project path %s is not a git repository", proj.Path)
			}
			exc := proj.GetExecutor()
			if !exc.Enabled {
				return fmt.Errorf("executor disabled for project %s (executor.enabled: false)", slug)
			}
			// --additional is opt-in PER RUN; the default is the main checkout
			// (unchanged pre-worktree behaviour). It requires the project to have the
			// additional worktree configured.
			if additional && !exc.AdditionalWorktree {
				return fmt.Errorf("--additional requested but project %s has no additional worktree configured - set `executor.additional_worktree: true` (+ worktree_path/base_branch/env) in project.yaml", slug)
			}

			subs, err := readySubs(store, slug, tracker.Meta.ID)
			if err != nil {
				return err
			}
			if len(subs) == 0 {
				return fmt.Errorf("%s is not a tracker - no subtasks have parent: %s", tracker.Meta.ID, tracker.Meta.ID)
			}

			// Independent (batch) mode: the tracker declares it in frontmatter
			// (epic_mode: independent) so board launches need no extra flag;
			// --independent is the CLI override for a tracker without it.
			if err := storage.ValidateEpicMode(tracker.Meta.EpicMode); err != nil {
				return fmt.Errorf("tracker %s: %w", tracker.Meta.ID, err)
			}
			independentMode := independent || tracker.Meta.EpicMode == storage.EpicModeIndependent

			startStatus := storage.TaskStatus(exc.StartStatus)
			doneStatus := storage.TaskStatus(exc.DoneStatus)
			epicBranch := "epic/" + tracker.Meta.ID
			// Base precedence for the integration branch: --base > (only with
			// --additional) executor.base_branch > "main". In default mode base_branch
			// is not consulted, so behaviour matches pre-worktree run-epic.
			// Independent mode is new (no compat concern) and every sub forks from
			// the base directly, so executor.base_branch always applies there.
			execBase := ""
			if additional || independentMode {
				execBase = exc.BaseBranch
			}
			baseBranch := resolveWorktreeBase(base, execBase, "main")

			workDir := proj.Path
			if additional {
				workDir = resolveWorktreePath(proj, exc)
			}

			if dryRun {
				printEpicPlan(tracker, epicBranch, baseBranch, startStatus, doneStatus, subs, additional, workDir, independentMode)
				return nil
			}

			if !statusAllowed(doneStatus, store.GetProjectStatuses(slug)) {
				fmt.Fprintf(os.Stderr, "warning: done status %q is not in project %s statuses - add it to project.yaml so merged subs show on the board\n", doneStatus, slug)
			}

			// Additional mode: the whole epic runs in the single "additional"
			// worktree (workDir), leaving the user's main checkout untouched. Ensure
			// + seed + lock it ONCE for the run; the deferred release frees the lock
			// on every normal exit (a kill is handled by the board, see killRun).
			if additional {
				release, err := prepareWorktree(proj, workDir, tracker.Meta.ID, "run-epic")
				if err != nil {
					return err
				}
				defer release()
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
			if !allowDirty && !additional {
				if dirty, _ := gitDirty(workDir); dirty {
					return fmt.Errorf("working tree at %s is dirty - commit/stash first or pass --allow-dirty", workDir)
				}
			}

			if independentMode {
				if !branchExists(workDir, baseBranch) {
					return fmt.Errorf("independent mode: base branch %q does not exist at %s", baseBranch, workDir)
				}
				fmt.Fprintf(os.Stderr, "pm run-epic: %s INDEPENDENT - each sub on its own branch off %s (%d sub(s))\n", tracker.Meta.ID, baseBranch, len(subs))
			} else {
				if err := gitEnsureBranch(workDir, epicBranch, baseBranch); err != nil {
					return fmt.Errorf("create integration branch %s: %w", epicBranch, err)
				}
				fmt.Fprintf(os.Stderr, "pm run-epic: %s on %s (%d sub(s))\n", tracker.Meta.ID, epicBranch, len(subs))
			}

			opts := workOptions{standalone: false, model: model, maxTurns: maxTurns, yolo: yolo, allowDirty: true, timeout: timeout, additional: additional, independent: independentMode}

			// Run-state for observability: the manager owns the epic-level file;
			// driveSub fills in each sub's worker session as it goes. It lives in
			// the pm data dir (where the TUI reads it), NOT the git repo. Every
			// WriteRunState below is best-effort observability: a failed write only
			// costs a stale dashboard, never correctness, so the error is dropped.
			stateDir := store.ProjectDir(slug)
			run := &storage.RunState{
				TaskID:   tracker.Meta.ID,
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
			_ = storage.WriteRunState(stateDir, run)

			// Journal: durable cross-run history (retro feedstock). Start line now;
			// end line with per-sub outcomes after the loop. Best-effort like the
			// run-state writes above. Independent mode has no integration branch,
			// so the journal records the fork base instead.
			journalBranch := epicBranch
			if independentMode {
				journalBranch = "independent:" + baseBranch
			}
			epicStart := time.Now()
			_ = storage.AppendJournal(stateDir, &storage.JournalEntry{
				Event: storage.JournalEventStart, Kind: "run-epic", Project: slug, TaskID: tracker.Meta.ID,
				PID: os.Getpid(), Model: model, Additional: additional, Yolo: yolo, Branch: journalBranch,
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
				oc, drive, announce := classifySub(sub, byID, startStatus, doneStatus)
				if !drive {
					outcomes = append(outcomes, oc)
					updateSubRun(run, oc.id, oc.result, oc.note)
					_ = storage.WriteRunState(stateDir, run)
					if announce != "" {
						fmt.Fprintf(os.Stderr, "pm run-epic: %s\n", announce)
					}
					continue
				}

				run.CurrentSub = sub.Meta.ID
				updateSubRun(run, sub.Meta.ID, storage.RunStatusRunning, "")
				_ = storage.WriteRunState(stateDir, run)

				subStart := time.Now()
				if independentMode {
					oc = driveSubIndependent(store, workDir, slug, tracker, sub, baseBranch, doneStatus, opts, run, additional)
				} else {
					oc = driveSub(store, workDir, slug, tracker, sub, epicBranch, doneStatus, opts, run, additional)
				}
				subDurations[oc.id] = int(time.Since(subStart).Seconds())
				outcomes = append(outcomes, oc)

				run.CurrentSub = ""
				run.CurrentSession = ""
				updateSubRun(run, oc.id, oc.result, oc.note)
				_ = storage.WriteRunState(stateDir, run)
			}

			run.Status = storage.RunStatusDone
			_ = storage.WriteRunState(stateDir, run)

			// Journal end line: the run's durable record (outcome + duration per sub).
			_ = storage.AppendJournal(stateDir, &storage.JournalEntry{
				Event: storage.JournalEventEnd, Kind: "run-epic", Project: slug, TaskID: tracker.Meta.ID,
				PID: os.Getpid(), Model: model, Additional: additional, Yolo: yolo, Branch: journalBranch,
				Status: storage.RunStatusDone, DurationS: int(time.Since(epicStart).Seconds()),
				Subs: journalSubs(outcomes, subDurations, run),
			})

			if independentMode {
				printEpicSummary(tracker, "independent, off "+baseBranch, outcomes)
				fmt.Fprintf(os.Stderr, "\nEach sub lives on its own branch (pushed to origin when it carried commits). Finish + verify every task by hand, then open per-task PRs - nothing was merged anywhere.\n")
				return nil
			}

			// Leave the worktree (or main checkout) on the integration branch with
			// the accumulated work. Best-effort: the branch already exists (created
			// above) and any real checkout failure surfaced earlier; a stray here
			// only affects which branch is checked out at exit.
			_ = gitEnsureBranch(workDir, epicBranch, "")

			printEpicSummary(tracker, epicBranch, outcomes)

			if !noPR && anyMerged(outcomes) {
				openEpicPR(workDir, epicBranch, tracker)
			}
			fmt.Fprintf(os.Stderr, "\nThe epic->main PR and closing %s stay human-gated - review the integration branch when convenient.\n", tracker.Meta.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&model, "model", "opus", "model for the workers")
	cmd.Flags().IntVar(&maxTurns, "max-turns", 120, "max agent turns per worker")
	cmd.Flags().BoolVar(&yolo, "yolo", false, "bypass all permission checks in the workers")
	cmd.Flags().DurationVar(&timeout, "timeout", 45*time.Minute, "max wall-clock time per worker")
	cmd.Flags().StringVar(&base, "base", "", "base branch the integration branch forks from (default: with --additional executor.base_branch, else main)")
	cmd.Flags().BoolVar(&noPR, "no-pr", false, "do not open the final epic->main draft PR")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "skip the clean-working-tree precondition")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan (subs, branches, readiness) without running anything")
	cmd.Flags().BoolVar(&additional, "additional", false, "run the whole epic in the isolated 'additional' worktree instead of the main checkout; requires executor.additional_worktree in project.yaml")
	cmd.Flags().BoolVar(&independent, "independent", false, "batch mode for unrelated subs: each on its own branch off the base, pushed, never merged (also enabled by `epic_mode: independent` on the tracker)")

	return cmd
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
		return subOutcome{sub.Meta.ID, "skipped", "already " + string(sub.Meta.Status)}, false, ""
	}
	if sub.Meta.Mode == "manual" {
		return subOutcome{sub.Meta.ID, "manual", "manual sub - run by hand, move to " + string(doneStatus) + " when done"},
			false, sub.Meta.ID + " skipped - manual sub (human-only)"
	}
	if sub.Meta.Status != startStatus {
		return subOutcome{sub.Meta.ID, "skipped", "not ready (status " + string(sub.Meta.Status) + ")"}, false, ""
	}
	if reason := unmetDeps(sub, byID, doneStatus); reason != "" {
		return subOutcome{sub.Meta.ID, "skipped", reason}, false, sub.Meta.ID + " skipped - " + reason
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
func logIfErr(context string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "pm run-epic: %s: %v\n", context, err)
	}
}

// driveSub runs one ready sub: branch off the integration branch, run the
// worker, and on verify-green merge back -> done status. Blocked/failed/conflict
// subs are parked (status + reason in pm) and the manager moves on.
func driveSub(store storage.TaskStore, workDir, slug string, tracker, sub *storage.Task, epicBranch string, doneStatus storage.TaskStatus, opts workOptions, run *storage.RunState, worktree bool) subOutcome {
	dir := workDir // worktree in worktree mode, else the main checkout
	branch := resolveWorkBranch(sub)

	// Branch the sub off the current integration branch (which already carries
	// the prior subs - seams resolve via sequencing).
	if err := gitEnsureBranch(dir, epicBranch, ""); err != nil {
		return subOutcome{sub.Meta.ID, "failed", "checkout integration: " + err.Error()}
	}
	// On the reused "additional" worktree, force a FRESH feat branch from the
	// integration tip (wiping any leftovers), so a re-run after a kill doesn't
	// resurrect the previous partial branch. On the main checkout keep the
	// create-or-continue behaviour.
	newBranch := gitEnsureBranch
	if worktree {
		newBranch = gitFreshBranch
	}
	if err := newBranch(dir, branch, epicBranch); err != nil {
		return subOutcome{sub.Meta.ID, "failed", "create branch: " + err.Error()}
	}

	logIfErr("move "+sub.Meta.ID+" to doing", store.MoveTask(sub, storage.StatusDoing))

	plan, err := planWork(store, sub, slug, opts)
	if err != nil {
		logIfErr("park "+sub.Meta.ID, store.MoveTask(sub, storage.StatusWaiting))
		return subOutcome{sub.Meta.ID, "failed", err.Error()}
	}

	// Surface the worker's session up front so the live agent-view knows which
	// transcript to tail (claude --session-id pins it before any output).
	run.CurrentSession = plan.sessionID
	for i := range run.Subs {
		if run.Subs[i].ID == sub.Meta.ID {
			run.Subs[i].Session = plan.sessionID
		}
	}
	_ = storage.WriteRunState(store.ProjectDir(slug), run)

	res, err := executeWork(store, sub, plan, opts)
	if err != nil {
		logIfErr("park "+sub.Meta.ID, store.MoveTask(sub, storage.StatusWaiting))
		return subOutcome{sub.Meta.ID, "failed", err.Error()}
	}

	// Stamp the worker's envelope stats onto the sub's run-state entry so the
	// dashboard and the journal (journalSubs) can weigh the outcome by effort.
	for i := range run.Subs {
		if run.Subs[i].ID == sub.Meta.ID {
			run.Subs[i].Turns = res.Turns
			run.Subs[i].CostUSD = res.CostUSD
		}
	}

	if res.Status != "merged" {
		// applyWorkerResult already parked a blocked sub on waiting; make sure a
		// failed sub is parked too (not left dangling on doing).
		if sub.Meta.Status != storage.StatusWaiting {
			logIfErr("park "+sub.Meta.ID, store.MoveTask(sub, storage.StatusWaiting))
		}
		// Bubble the parked sub's reason + open questions up to the parent so the
		// human sees, in one place (the epic), why a sub stalled - without opening
		// each child. Status itself stays in the child + generated rollup; this is
		// open-questions content, not a status table.
		logIfErr("record feedback for "+sub.Meta.ID, recordSubFeedback(store, tracker, sub.Meta.ID, res.Status, parkedFindings(res)))
		return subOutcome{sub.Meta.ID, res.Status, briefReason(res)}
	}

	// Verify-green: merge the sub back into the integration branch.
	if err := gitEnsureBranch(dir, epicBranch, ""); err != nil {
		logIfErr("park "+sub.Meta.ID, store.MoveTask(sub, storage.StatusWaiting))
		return subOutcome{sub.Meta.ID, "failed", "checkout integration to merge: " + err.Error()}
	}
	msg := fmt.Sprintf("Merge %s (%s) into %s", branch, sub.Meta.ID, epicBranch)
	if err := gitMergeNoFF(dir, branch, msg); err != nil {
		// A seam the ordering did not resolve - escalate to a human.
		logIfErr("park "+sub.Meta.ID, store.MoveTask(sub, storage.StatusWaiting))
		return subOutcome{sub.Meta.ID, "conflict", err.Error()}
	}
	logIfErr("mark "+sub.Meta.ID+" "+string(doneStatus), store.MoveTask(sub, doneStatus))
	_ = gitDeleteBranch(dir, branch) // best-effort cleanup; the merge already landed

	// Refresh the parent's Manager Notes for this sub: record cross-cutting
	// findings for later subs, and (with empty findings) clear any stale parked
	// note now that the sub has merged.
	logIfErr("refresh feedback for "+sub.Meta.ID, recordSubFeedback(store, tracker, sub.Meta.ID, "merged", res.Unresolved))
	return subOutcome{sub.Meta.ID, "merged", briefReason(res)}
}

// driveSubIndependent runs one ready sub in independent (batch) mode: fresh
// branch off the base, best-effort worker, then push whatever landed and record
// the handoff. Nothing is merged and nothing parks on waiting - every task in
// the batch gets a human follow-up on its own branch, so a non-green sub simply
// stays on its WIP status with the reason in the handoff (brief/log + Manager
// Notes).
func driveSubIndependent(store storage.TaskStore, workDir, slug string, tracker, sub *storage.Task, baseBranch string, doneStatus storage.TaskStatus, opts workOptions, run *storage.RunState, worktree bool) subOutcome {
	dir := workDir
	branch := resolveWorkBranch(sub)

	// On the reused "additional" worktree force a FRESH branch from the base tip
	// (wiping leftovers); on the main checkout keep create-or-continue.
	newBranch := gitEnsureBranch
	if worktree {
		newBranch = gitFreshBranch
	}
	if err := newBranch(dir, branch, baseBranch); err != nil {
		return subOutcome{sub.Meta.ID, "failed", "create branch: " + err.Error()}
	}

	logIfErr("move "+sub.Meta.ID+" to doing", store.MoveTask(sub, storage.StatusDoing))

	plan, err := planWork(store, sub, slug, opts)
	if err != nil {
		return subOutcome{sub.Meta.ID, "failed", err.Error()}
	}

	run.CurrentSession = plan.sessionID
	for i := range run.Subs {
		if run.Subs[i].ID == sub.Meta.ID {
			run.Subs[i].Session = plan.sessionID
		}
	}
	_ = storage.WriteRunState(store.ProjectDir(slug), run)

	res, err := executeWork(store, sub, plan, opts)
	if err != nil {
		// Worker died (timeout/crash). Push whatever it committed before dying so
		// partial work survives the worktree's next wipe.
		note := err.Error()
		if pushNote := pushIfAhead(dir, branch, baseBranch); pushNote != "" {
			note += "; " + pushNote
		}
		return subOutcome{sub.Meta.ID, "failed", note}
	}

	for i := range run.Subs {
		if run.Subs[i].ID == sub.Meta.ID {
			run.Subs[i].Turns = res.Turns
			run.Subs[i].CostUSD = res.CostUSD
		}
	}

	// Push regardless of outcome - in best-effort mode partial work on origin
	// beats perfect work lost to the next branch wipe.
	note := briefReason(res)
	if pushNote := pushIfAhead(dir, branch, baseBranch); pushNote != "" {
		note = strings.TrimSpace(pushNote + "; " + note)
	}

	if res.Status != "merged" {
		logIfErr("record feedback for "+sub.Meta.ID, recordSubFeedback(store, tracker, sub.Meta.ID, res.Status, parkedFindings(res)))
		return subOutcome{sub.Meta.ID, res.Status, note}
	}

	logIfErr("mark "+sub.Meta.ID+" "+string(doneStatus), store.MoveTask(sub, doneStatus))
	logIfErr("refresh feedback for "+sub.Meta.ID, recordSubFeedback(store, tracker, sub.Meta.ID, "merged", res.Unresolved))
	return subOutcome{sub.Meta.ID, "merged", note}
}

// pushIfAhead pushes branch to origin when it carries commits base does not
// have. Returns a short human note for the outcome ("" when there was nothing
// to push). Never fatal - a failed push only means the work stays local.
func pushIfAhead(dir, branch, base string) string {
	if gitAheadCount(dir, branch, base) == 0 {
		return ""
	}
	if !gitHasRemote(dir) {
		return "no remote - branch " + branch + " stays local"
	}
	if err := gitPush(dir, branch); err != nil {
		return "push failed (" + err.Error() + ") - branch " + branch + " stays local"
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
			ID: o.id, Result: o.result, Note: o.note,
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
// epic->main PR. Failures are reported with a manual fallback, never fatal.
func openEpicPR(dir, epicBranch string, tracker *storage.Task) {
	if !gitHasRemote(dir) {
		fmt.Fprintf(os.Stderr, "\nno git remote configured - push %s and open the epic->main PR manually.\n", epicBranch)
		return
	}
	if err := gitPush(dir, epicBranch); err != nil {
		fmt.Fprintf(os.Stderr, "\n%v\nopen the epic->main PR manually once pushed.\n", err)
		return
	}
	title := fmt.Sprintf("Epic %s: %s", tracker.Meta.ID, tracker.Meta.Title)
	body := fmt.Sprintf("Integration PR for epic %s. Subs were implemented + reviewed + verified on isolated branches and merged into `%s`.\n\nReview and merge when ready - this PR is intentionally a draft.", tracker.Meta.ID, epicBranch)
	c := exec.Command("gh", "pr", "create", "--draft", "--base", "main", "--head", epicBranch, "--title", title, "--body", body)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "\ngh pr create failed: %s\nopen the epic->main PR manually.\n", strings.TrimSpace(string(out)))
		return
	} else {
		fmt.Fprintf(os.Stderr, "\nopened draft PR: %s\n", strings.TrimSpace(string(out)))
	}
}

func printEpicPlan(tracker *storage.Task, epicBranch, base string, startStatus, doneStatus storage.TaskStatus, subs []*storage.Task, additional bool, workDir string, independent bool) {
	runLine := "run: DEFAULT (main checkout, clean-tree required)"
	if additional {
		runLine = "run: ADDITIONAL worktree " + workDir + " (isolated branch/port/sim; main checkout untouched)"
	}
	branchLine := fmt.Sprintf("integration branch: %s (off %s)", epicBranch, base)
	if independent {
		branchLine = fmt.Sprintf("mode: INDEPENDENT - each sub on its own branch off %s, pushed to origin; no integration branch, no merging, no epic PR", base)
	}
	fmt.Printf("# pm run-epic (dry-run)\ntracker: %s  %s\n%s\n%s\nready status: %s -> done status: %s\n\nsubs (in Order):\n",
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
		fmt.Printf("  [%-6s] %-14s %-8s order=%d  -> %s%s\n", ready, s.Meta.ID, s.Meta.Status, s.Meta.Order, resolveWorkBranch(s), dep)
	}
}

func printEpicSummary(tracker *storage.Task, epicBranch string, outcomes []subOutcome) {
	fmt.Printf("\n# Epic %s summary (integration: %s)\n", tracker.Meta.ID, epicBranch)
	for _, o := range outcomes {
		line := fmt.Sprintf("  %-9s %s", o.result, o.id)
		if o.note != "" {
			line += "  - " + o.note
		}
		fmt.Println(line)
	}
}
