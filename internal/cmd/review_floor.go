package cmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/mbalazy/pm/internal/storage"
)

// The review FLOOR is the missing half of "review is decided by pm" (0.38.0).
// That change gave the review phase a CEILING - how many reviewers, which
// model, how many rounds - and left the floor to the prompt. Run pm-cli-129
// (2026-09-06, the runtime e2e test) showed what that is worth: the worker
// returned `verified` with review telemetry `spawns: 0`, the manager merged
// the sub into the epic branch, and nothing anywhere said a word. The first
// landed sub with telemetry and zero spawns - and it was invisible.
//
// Two enforcement points, deliberately redundant:
//   - the hook (judgeStructuredOutput) refuses the `verified` verdict at the
//     moment the worker emits it, so the worker can still spawn a reviewer and
//     finish properly - a demotion after the fact costs a human round-trip;
//   - the manager (demoteUnreviewed) applies the same rule to the envelope it
//     received, so a build where the StructuredOutput matcher never fires, a
//     worker whose transcript had to be recovered, or a worker that ended by
//     plain text still cannot land unreviewed work as verified.
//
// The rule: a `verified` result with a NON-EMPTY production diff (the same
// sizing the reviewer cap uses - tests, lockfiles, snapshots and generated
// files do not count; prose does, since a documentation-only change still
// gets its one reviewer) needs at least one top-level reviewer spawn that
// actually ran. A refused spawn never ran; a custom-type helper is not a
// review. An EMPTY production diff passes: there is nothing to review, and a
// worker that changed nothing (an investigation sub, a no-op) must not be told
// to spawn a reviewer over it. A diff that cannot be measured passes too - the
// floor guards tokens and trust, not correctness, and refusing on a repo shape
// nobody anticipated is the failure mode every other cap here avoids.

// structuredOutputTool is the tool a `--json-schema` worker ends with; its
// tool_input IS the result envelope (status, summary, ...).
const structuredOutputTool = "StructuredOutput"

// reviewSkippedNote is the unresolved line a demoted result carries. It names
// the number the human will look for in `pm executor stats` and the rule that
// demoted the sub, so the parked sub explains itself without the journal.
const reviewSkippedNote = "REVIEW-SKIPPED: worker returned verified with 0 reviewer spawns; pm requires at least one - the work is on the branch, unreviewed; review it (or re-run the sub) before it lands"

// reviewFloorDenyMessage is what the hook tells a worker whose `verified` is
// refused. Like every other refusal in this file's neighbours, it names the
// one legitimate move - and rules out the two ways around it a model reaches
// for (downgrade the verdict to dodge the check, or count a refused spawn).
func reviewFloorDenyMessage(files int) string {
	return "pm review floor: this result says \"verified\" but no reviewer subagent ran on this change (" +
		strconv.Itoa(files) + " production file(s) changed since you started). " +
		"Review is decided by pm, and a \"verified\" needs at least one `" + reviewerAgentType + "` review that actually ran - " +
		"a documentation-only change included, and a spawn pm refused does not count. " +
		"Spawn ONE reviewer now (type `" + reviewerAgentType + "`, no `model` field, synchronous - pm attaches the diff), " +
		"act on what it reports, re-run the verify gate if you changed anything, then return your result. " +
		"Do not return \"blocked\" or \"failed\" instead to skip the review: a verdict chosen to dodge the check is the same unreviewed work with a worse label."
}

// reviewerRan reports whether any top-level, allowed, reviewer-typed spawn
// is on record - the same rule AggregateReviewSpawns' Reviewers count applies,
// restated over the raw lines because the hook reads those.
func reviewerRan(spawns []storage.ReviewSpawn) bool {
	for _, s := range spawns {
		if s.Denied || s.Nested {
			continue
		}
		if s.SubagentType == "" || storage.GenericSubagentType(s.SubagentType) {
			return true
		}
	}
	return false
}

// judgeStructuredOutput is the hook half of the floor. Fail-open on every
// missing input (no telemetry path, no diff base, no git, an unmeasurable
// diff, a verdict other than verified): what it guards is trust in a landed
// sub, and a worker must never be stuck at its final turn because a hook
// could not see the repo.
func judgeStructuredOutput(opts guardOptions, ev hookEvent) string {
	if opts.telemetryPath == "" || normalizeWorkerStatus(ev.ToolInput.Status) != workerVerified {
		return ""
	}
	spawns, err := storage.ReadReviewSpawns(opts.telemetryPath)
	if err != nil || reviewerRan(spawns) {
		return ""
	}
	size := diffStats(guardDir(ev), opts.diffBase)
	if !size.ok || size.files == 0 {
		return ""
	}
	return reviewFloorDenyMessage(size.files)
}

// demoteUnreviewed is the manager half: the envelope said verified, the
// telemetry says no reviewer ran, the production diff is non-empty - so the
// result becomes `blocked` with the REVIEW-SKIPPED line in unresolved, and the
// telemetry carries `skipped` for the journal and `pm executor stats`. The
// caller runs it before applyWorkerResult (which parks a blocked sub) and
// before the manager's merge/push, which only happen on verified.
//
// telemetry nil = the hook was never attached, so nothing was measured, and an
// unmeasured run is not a skipped review. Same fail-open as the hook.
func demoteUnreviewed(errOut io.Writer, res *workerResult, telemetry *storage.ReviewTelemetry, dir, diffBase string) {
	if res == nil || telemetry == nil || res.Status != workerVerified || telemetry.Reviewers > 0 {
		return
	}
	size := diffStats(dir, diffBase)
	if !size.ok || size.files == 0 {
		return
	}
	telemetry.Skipped = true
	res.Status = workerBlocked
	res.Unresolved = append(res.Unresolved, reviewSkippedNote)
	fmt.Fprintf(errOut, "pm work: review floor - worker returned verified with 0 reviewer spawns on %d production file(s); demoted to blocked\n", size.files)
}
