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
	result string // merged | blocked | failed | skipped | conflict
	note   string
}

func newRunEpicCmd(store storage.TaskStore) *cobra.Command {
	var (
		model      string
		maxTurns   int
		yolo       bool
		timeout    time.Duration
		base       string
		noPR       bool
		allowDirty bool
		dryRun     bool
	)

	cmd := &cobra.Command{
		Use:   "run-epic [project] <tracker-id>",
		Short: "Sequentially drive a parent+subtask epic via isolated workers on an integration branch",
		Long: "The manager loop. Creates an `epic/<tracker>` integration branch, then for each ready sub " +
			"(by Order) branches `feat/<slug>` off it, runs `pm work` (the worker) there, and merges back on " +
			"verify-green -> status merged. Async: a blocked/failed sub is parked (status + reason in pm) and " +
			"the manager continues. Re-entrant: re-running skips merged/done subs. Ends by opening ONE draft " +
			"epic->main PR for human review. Never auto-merges to main, force-pushes, or closes the parent.",
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

			subs, err := readySubs(store, slug, tracker.Meta.ID)
			if err != nil {
				return err
			}
			if len(subs) == 0 {
				return fmt.Errorf("%s is not a tracker - no subtasks have parent: %s", tracker.Meta.ID, tracker.Meta.ID)
			}

			startStatus := storage.TaskStatus(exc.StartStatus)
			doneStatus := storage.TaskStatus(exc.DoneStatus)
			epicBranch := "epic/" + tracker.Meta.ID

			if dryRun {
				printEpicPlan(tracker, epicBranch, base, startStatus, doneStatus, subs)
				return nil
			}

			if !statusAllowed(doneStatus, store.GetProjectStatuses(slug)) {
				fmt.Fprintf(os.Stderr, "warning: done status %q is not in project %s statuses - add it to project.yaml so merged subs show on the board\n", doneStatus, slug)
			}

			// Precondition: clean working tree (the manager switches branches in place).
			if !allowDirty {
				if dirty, _ := gitDirty(proj.Path); dirty {
					return fmt.Errorf("working tree at %s is dirty - commit/stash first or pass --allow-dirty", proj.Path)
				}
			}

			if err := gitEnsureBranch(proj.Path, epicBranch, base); err != nil {
				return fmt.Errorf("create integration branch %s: %w", epicBranch, err)
			}
			fmt.Fprintf(os.Stderr, "pm run-epic: %s on %s (%d sub(s))\n", tracker.Meta.ID, epicBranch, len(subs))

			opts := workOptions{standalone: false, model: model, maxTurns: maxTurns, yolo: yolo, allowDirty: true, timeout: timeout}

			// Run-state for observability: the manager owns the epic-level file;
			// driveSub fills in each sub's worker session as it goes.
			run := &storage.RunState{
				TaskID:  tracker.Meta.ID,
				Project: slug,
				Kind:    "run-epic",
				Status:  storage.RunStatusRunning,
				PID:     os.Getpid(),
				Started: time.Now().UTC().Format(time.RFC3339),
				LogPath: storage.ExecutorLogPath(proj.Path, tracker.Meta.ID),
			}
			for _, s := range subs {
				init := "pending"
				if s.Meta.Status == doneStatus || s.Meta.Status == storage.StatusDone {
					init = "skipped"
				}
				run.Subs = append(run.Subs, storage.SubRun{ID: s.Meta.ID, Status: init})
			}
			_ = storage.WriteRunState(proj.Path, run)

			var outcomes []subOutcome
			for _, sub := range subs {
				// Re-entrant: skip already-finished subs; only pick up ready ones.
				if sub.Meta.Status == doneStatus || sub.Meta.Status == storage.StatusDone {
					oc := subOutcome{sub.Meta.ID, "skipped", "already " + string(sub.Meta.Status)}
					outcomes = append(outcomes, oc)
					updateSubRun(run, oc.id, oc.result, oc.note)
					_ = storage.WriteRunState(proj.Path, run)
					continue
				}
				if sub.Meta.Status != startStatus {
					oc := subOutcome{sub.Meta.ID, "skipped", "not ready (status " + string(sub.Meta.Status) + ")"}
					outcomes = append(outcomes, oc)
					updateSubRun(run, oc.id, oc.result, oc.note)
					_ = storage.WriteRunState(proj.Path, run)
					continue
				}

				run.CurrentSub = sub.Meta.ID
				updateSubRun(run, sub.Meta.ID, storage.RunStatusRunning, "")
				_ = storage.WriteRunState(proj.Path, run)

				oc := driveSub(store, proj, slug, tracker, sub, epicBranch, doneStatus, opts, run)
				outcomes = append(outcomes, oc)

				run.CurrentSub = ""
				run.CurrentSession = ""
				updateSubRun(run, oc.id, oc.result, oc.note)
				_ = storage.WriteRunState(proj.Path, run)
			}

			run.Status = storage.RunStatusDone
			_ = storage.WriteRunState(proj.Path, run)

			// Leave the user on the integration branch with the accumulated work.
			_ = gitEnsureBranch(proj.Path, epicBranch, "")

			printEpicSummary(tracker, epicBranch, outcomes)

			if !noPR && anyMerged(outcomes) {
				openEpicPR(proj.Path, epicBranch, tracker)
			}
			fmt.Fprintf(os.Stderr, "\nThe epic->main PR and closing %s stay human-gated - review the integration branch when convenient.\n", tracker.Meta.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&model, "model", "opus", "model for the workers")
	cmd.Flags().IntVar(&maxTurns, "max-turns", 120, "max agent turns per worker")
	cmd.Flags().BoolVar(&yolo, "yolo", false, "bypass all permission checks in the workers")
	cmd.Flags().DurationVar(&timeout, "timeout", 45*time.Minute, "max wall-clock time per worker")
	cmd.Flags().StringVar(&base, "base", "main", "base branch the integration branch is created from")
	cmd.Flags().BoolVar(&noPR, "no-pr", false, "do not open the final epic->main draft PR")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "skip the clean-working-tree precondition")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan (subs, branches, readiness) without running anything")

	return cmd
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

// driveSub runs one ready sub: branch off the integration branch, run the
// worker, and on verify-green merge back -> done status. Blocked/failed/conflict
// subs are parked (status + reason in pm) and the manager moves on.
func driveSub(store storage.TaskStore, proj *storage.Project, slug string, tracker, sub *storage.Task, epicBranch string, doneStatus storage.TaskStatus, opts workOptions, run *storage.RunState) subOutcome {
	dir := proj.Path
	branch := resolveWorkBranch(sub)

	// Branch the sub off the current integration branch (which already carries
	// the prior subs - seams resolve via sequencing).
	if err := gitEnsureBranch(dir, epicBranch, ""); err != nil {
		return subOutcome{sub.Meta.ID, "failed", "checkout integration: " + err.Error()}
	}
	if err := gitEnsureBranch(dir, branch, epicBranch); err != nil {
		return subOutcome{sub.Meta.ID, "failed", "create branch: " + err.Error()}
	}

	_ = store.MoveTask(sub, storage.StatusDoing)

	plan, err := planWork(store, sub, slug, opts)
	if err != nil {
		_ = store.MoveTask(sub, storage.StatusWaiting)
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
	_ = storage.WriteRunState(dir, run)

	res, err := executeWork(store, sub, plan, opts)
	if err != nil {
		_ = store.MoveTask(sub, storage.StatusWaiting)
		return subOutcome{sub.Meta.ID, "failed", err.Error()}
	}

	if res.Status != "merged" {
		// applyWorkerResult already parked a blocked sub on waiting; make sure a
		// failed sub is parked too (not left dangling on doing).
		if sub.Meta.Status != storage.StatusWaiting {
			_ = store.MoveTask(sub, storage.StatusWaiting)
		}
		return subOutcome{sub.Meta.ID, res.Status, briefReason(res)}
	}

	// Verify-green: merge the sub back into the integration branch.
	if err := gitEnsureBranch(dir, epicBranch, ""); err != nil {
		_ = store.MoveTask(sub, storage.StatusWaiting)
		return subOutcome{sub.Meta.ID, "failed", "checkout integration to merge: " + err.Error()}
	}
	msg := fmt.Sprintf("Merge %s (%s) into %s", branch, sub.Meta.ID, epicBranch)
	if err := gitMergeNoFF(dir, branch, msg); err != nil {
		// A seam the ordering did not resolve - escalate to a human.
		_ = store.MoveTask(sub, storage.StatusWaiting)
		return subOutcome{sub.Meta.ID, "conflict", err.Error()}
	}
	_ = store.MoveTask(sub, doneStatus)
	_ = gitDeleteBranch(dir, branch)

	// Propagate cross-cutting findings to the parent SPEC so later subs see them.
	if len(res.Unresolved) > 0 {
		_ = recordCrossCutting(store, tracker, sub.Meta.ID, res.Unresolved)
	}
	return subOutcome{sub.Meta.ID, "merged", briefReason(res)}
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

func briefReason(res *workerResult) string {
	if len(res.Unresolved) > 0 {
		return strings.Join(res.Unresolved, "; ")
	}
	return strings.TrimSpace(res.Summary)
}

const managerNotesHeading = "## Manager Notes (cross-cutting)"

// recordCrossCutting appends a sub's unresolved findings to the parent's Manager
// Notes section inside the Spec (so later subs, whose prompt carries the parent
// SPEC, see them). Falls back to the Log when the parent has no Spec block.
func recordCrossCutting(store storage.TaskStore, parent *storage.Task, subID string, findings []string) error {
	note := managerNoteBlock(subID, findings)
	spec := storage.ExtractSpec(parent.Body)
	if spec == "" {
		parent.Body = appendLog(parent.Body, managerNotesHeading+"\n"+note)
	} else {
		if strings.Contains(spec, managerNotesHeading) {
			spec = strings.TrimRight(spec, "\n") + "\n" + note
		} else {
			spec = strings.TrimRight(spec, "\n") + "\n\n" + managerNotesHeading + "\n" + note
		}
		parent.Body = storage.ApplySpec(parent.Body, spec)
	}
	parent.Meta.Updated = storage.Today()
	return store.WriteTask(parent)
}

func managerNoteBlock(subID string, findings []string) string {
	var sb strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&sb, "- [%s] %s\n", subID, strings.TrimSpace(f))
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

func printEpicPlan(tracker *storage.Task, epicBranch, base string, startStatus, doneStatus storage.TaskStatus, subs []*storage.Task) {
	fmt.Printf("# pm run-epic (dry-run)\ntracker: %s  %s\nintegration branch: %s (off %s)\nready status: %s -> done status: %s\n\nsubs (in Order):\n",
		tracker.Meta.ID, tracker.Meta.Title, epicBranch, base, startStatus, doneStatus)
	for _, s := range subs {
		ready := "skip"
		if s.Meta.Status == startStatus {
			ready = "READY"
		} else if s.Meta.Status == doneStatus || s.Meta.Status == storage.StatusDone {
			ready = "done"
		}
		fmt.Printf("  [%-5s] %-14s %-8s order=%d  -> feat/%s\n", ready, s.Meta.ID, s.Meta.Status, s.Meta.Order, storage.Slugify(s.Meta.Title))
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
