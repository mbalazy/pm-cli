package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
)

// subFlow carries the ~30% of driving a sub that differs between the two epic
// modes. Everything else - resolve the branch, fork it, move the sub to doing,
// plan, run the worker, stamp its stats, record feedback, shape the outcome -
// is identical and lives in driveSubFlow.
//
// The two constructors below (driveSub / driveSubIndependent) fill EVERY hook;
// only prepare is optional (nil = nothing to do before the sub's branch is
// created). Keeping them non-nil is what lets driveSubFlow read as one straight
// sequence instead of a mode conditional at every step.
type subFlow struct {
	// base is the branch the sub's own branch forks from: the integration
	// branch, or the batch's base.
	base string
	// green is the outcome word recorded for a verify-green sub, naming what
	// THIS mode did with the work: subMerged (integration) or subPushed
	// (independent). It reaches the run-state, the journal and the parent's
	// Manager Notes.
	green string
	// prepare runs before the sub's branch is created. Integration mode checks
	// out the integration branch first, so the sub forks off the prior subs'
	// merged work. A returned error fails the sub verbatim as its note.
	prepare func(dir string) error
	// park sets a sub aside when its run did not go green. Integration mode
	// parks it on waiting; independent mode deliberately does NOTHING - a human
	// returns to every task in a batch anyway, so the sub stays on its WIP
	// status with the reason in the handoff.
	park func(sub *storage.Task)
	// crashNote/note post-process the outcome note of a dead worker and of a
	// completed run respectively. Independent mode pushes the branch in both
	// (partial work on origin beats work lost to the next wipe) and says so.
	crashNote func(dir, branch, note string) string
	note      func(dir, branch, note string) string
	// land runs after a verify-green worker and before the done-status move:
	// integration mode merges the sub back into the integration branch. A
	// non-nil return aborts the sub with that outcome (the hook has already
	// parked it).
	land func(sub *storage.Task, dir, branch string) *subOutcome
	// cleanup runs after the done-status move (integration mode deletes the
	// merged sub branch).
	cleanup func(dir, branch string)
}

// keepNote is the note hook for a mode that has nothing to add.
func keepNote(_, _, note string) string { return note }

// driveSubFlow runs one ready sub end to end: fork its branch, hand it to a
// worker, and record what came back. The mode-specific steps come from flow.
func driveSubFlow(store storage.TaskStore, workDir, slug string, tracker, sub *storage.Task, flow subFlow, doneStatus storage.TaskStatus, opts workOptions, rw *storage.RunWriter, worktree bool) subOutcome {
	dir := workDir // worktree in worktree mode, else the main checkout
	branch := resolveWorkBranch(sub)
	errOut := opts.stderr()

	if flow.prepare != nil {
		if err := flow.prepare(dir); err != nil {
			return subOutcome{sub.Meta.ID, subFailed, err.Error(), branch}
		}
	}
	// On the reused "additional" worktree, force a FRESH branch from the base
	// tip (wiping any leftovers), so a re-run after a kill doesn't resurrect the
	// previous partial branch. On the main checkout keep the create-or-continue
	// behaviour.
	newBranch := gitEnsureBranch
	if worktree {
		newBranch = gitFreshBranch
	}
	if err := newBranch(dir, branch, flow.base); err != nil {
		return subOutcome{sub.Meta.ID, subFailed, "create branch: " + err.Error(), branch}
	}

	logIfErr(errOut, "move "+sub.Meta.ID+" to doing", store.MoveTask(sub, storage.StatusDoing))

	plan, err := planWork(store, sub, slug, opts)
	if err != nil {
		flow.park(sub)
		return subOutcome{sub.Meta.ID, subFailed, err.Error(), branch}
	}

	// Surface the worker's session up front so the live agent-view knows which
	// transcript to tail (claude --session-id pins it before any output).
	_ = rw.Update(func(run *storage.RunState) { setSubSession(run, sub.Meta.ID, plan.sessionID) })

	// Hand the worker the epic-level writer so it heartbeats THIS run-state
	// while it runs. opts is a value copy, so this cannot leak to the next sub.
	opts.runWriter = rw
	res, err := executeWork(store, sub, plan, opts)
	if err != nil {
		// An account wall is not this sub's failure - the next worker would hit
		// the same one seconds later. Report it as `aborted` (the manager stops
		// the run on that word) and deliberately do NOT park: parking on
		// `waiting` would make a human flip every affected sub back by hand,
		// when nothing about them is wrong. The manager returns the sub to its
		// ready status instead, so a re-run after the wall clears just picks it up.
		var wall *accountWallError
		if errors.As(err, &wall) {
			return subOutcome{sub.Meta.ID, subAborted, err.Error(), branch}
		}
		flow.park(sub)
		return subOutcome{sub.Meta.ID, subFailed, flow.crashNote(dir, branch, err.Error()), branch}
	}

	// Stamp the worker's envelope stats onto the sub's run-state entry so the
	// dashboard and the journal (journalSubs) can weigh the outcome by effort.
	_ = rw.Update(func(run *storage.RunState) { setSubStats(run, sub.Meta.ID, res.Turns, res.CostUSD, res.Review) })

	note := flow.note(dir, branch, briefReason(res))

	if res.Status != workerVerified {
		// applyWorkerResult already parked a blocked sub on waiting; make sure a
		// failed sub is parked too (not left dangling on doing).
		if sub.Meta.Status != storage.StatusWaiting {
			flow.park(sub)
		}
		// Bubble the parked sub's reason + open questions up to the parent so the
		// human sees, in one place (the epic), why a sub stalled - without opening
		// each child. Status itself stays in the child + generated rollup; this is
		// open-questions content, not a status table.
		logIfErr(errOut, "record feedback for "+sub.Meta.ID, recordSubFeedback(store, tracker, sub.Meta.ID, res.Status, parkedFindings(res)))
		return subOutcome{sub.Meta.ID, res.Status, note, branch}
	}

	if oc := flow.land(sub, dir, branch); oc != nil {
		return *oc
	}
	logIfErr(errOut, "mark "+sub.Meta.ID+" "+string(doneStatus), store.MoveTask(sub, doneStatus))
	flow.cleanup(dir, branch)

	// Refresh the parent's Manager Notes for this sub: record cross-cutting
	// findings for later subs, and (with empty findings) clear any stale parked
	// note now that the sub has landed.
	logIfErr(errOut, "refresh feedback for "+sub.Meta.ID, recordSubFeedback(store, tracker, sub.Meta.ID, flow.green, res.Unresolved))
	return subOutcome{sub.Meta.ID, flow.green, note, branch}
}

// driveSub runs one ready sub in INTEGRATION mode: branch off the integration
// branch, run the worker, and on verify-green merge back -> done status.
// Blocked/failed/conflict subs are parked (status + reason in pm) and the
// manager moves on.
func driveSub(store storage.TaskStore, workDir, slug string, tracker, sub *storage.Task, epicBranch string, doneStatus storage.TaskStatus, opts workOptions, rw *storage.RunWriter, worktree bool) subOutcome {
	errOut := opts.stderr()
	park := func(sub *storage.Task) {
		logIfErr(errOut, "park "+sub.Meta.ID, store.MoveTask(sub, storage.StatusWaiting))
	}
	flow := subFlow{
		base:  epicBranch,
		green: subMerged,
		// Branch the sub off the current integration branch (which already
		// carries the prior subs - seams resolve via sequencing).
		prepare: func(dir string) error {
			if err := gitEnsureBranch(dir, epicBranch, ""); err != nil {
				return fmt.Errorf("checkout integration: %w", err)
			}
			return nil
		},
		park:      park,
		crashNote: keepNote,
		note:      keepNote,
		land: func(sub *storage.Task, dir, branch string) *subOutcome {
			// Verify-green: merge the sub back into the integration branch.
			if err := gitEnsureBranch(dir, epicBranch, ""); err != nil {
				park(sub)
				return &subOutcome{sub.Meta.ID, subFailed, "checkout integration to merge: " + err.Error(), branch}
			}
			msg := fmt.Sprintf("Merge %s (%s) into %s", branch, sub.Meta.ID, epicBranch)
			if err := gitMergeNoFF(dir, branch, msg); err != nil {
				// A seam the ordering did not resolve - escalate to a human.
				park(sub)
				return &subOutcome{sub.Meta.ID, subConflict, err.Error(), branch}
			}
			return nil
		},
		cleanup: func(dir, branch string) {
			_ = gitDeleteBranch(dir, branch) // best-effort cleanup; the merge already landed
		},
	}
	return driveSubFlow(store, workDir, slug, tracker, sub, flow, doneStatus, opts, rw, worktree)
}

// driveSubIndependent runs one ready sub in independent (batch) mode: fresh
// branch off the base, best-effort worker, then push whatever landed and record
// the handoff. Nothing is merged and nothing parks on waiting - every task in
// the batch gets a human follow-up on its own branch, so a non-green sub simply
// stays on its WIP status with the reason in the handoff (brief/log + Manager
// Notes).
func driveSubIndependent(store storage.TaskStore, workDir, slug string, tracker, sub *storage.Task, baseBranch string, doneStatus storage.TaskStatus, opts workOptions, rw *storage.RunWriter, worktree bool) subOutcome {
	flow := subFlow{
		base: baseBranch,
		// Nothing is merged here - the manager pushes the branch and that is the
		// whole of it, so the sub is recorded (and lands) as pushed.
		green: subPushed,
		// No prepare: a batch sub forks straight off the base, never off a
		// sibling's work.
		park: func(*storage.Task) {},
		crashNote: func(dir, branch, note string) string {
			// Worker died (timeout/crash). Push whatever it committed before dying
			// so partial work survives the worktree's next wipe.
			if pushNote := pushIfAhead(dir, branch, baseBranch); pushNote != "" {
				note += "; " + pushNote
			}
			return note
		},
		note: func(dir, branch, note string) string {
			// Push regardless of outcome - in best-effort mode partial work on
			// origin beats perfect work lost to the next branch wipe.
			if pushNote := pushIfAhead(dir, branch, baseBranch); pushNote != "" {
				note = strings.TrimSpace(pushNote + "; " + note)
			}
			return note
		},
		land:    func(*storage.Task, string, string) *subOutcome { return nil },
		cleanup: func(string, string) {},
	}
	return driveSubFlow(store, workDir, slug, tracker, sub, flow, doneStatus, opts, rw, worktree)
}
