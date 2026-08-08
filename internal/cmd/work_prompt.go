package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
)

// buildWorkerPrompt assembles the user prompt for a worker: task identity, the
// child SPEC + parent SPEC (shared decisions/seams), the AC (hard-bound), and
// the resolved per-phase bindings.
//
// workDir is the directory the worker actually runs in - the claimed
// "additional" worktree slot when the run opted in, else the main checkout.
// The prompt MUST state that path, not proj.Path: printing the main checkout
// to a worker running in a slot hands it an absolute path into the very
// checkout the additional mode exists to isolate.
func buildWorkerPrompt(t *storage.Task, parent *storage.Task, proj *storage.Project, slug string, exec storage.Executor, branch, workDir string, standalone bool) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Task: #%s %s\n", t.Meta.ID, t.Meta.Title)
	fmt.Fprintf(&sb, "Project: %s", slug)
	if proj.Stack != "" {
		fmt.Fprintf(&sb, " | Stack: %s", proj.Stack)
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "Repo path: %s\n", workDir)
	fmt.Fprintf(&sb, "Git branch: %s  (commit here; do NOT switch branches)\n", branch)
	fmt.Fprintf(&sb, "Mode: %s\n", modeLabel(standalone))
	fmt.Fprintf(&sb, "Review->fix rounds before escalating: %d\n", exec.FixRounds)
	if proj.Notes != "" {
		fmt.Fprintf(&sb, "Project notes: %s\n", proj.Notes)
	}

	if len(exec.ContextRepos) > 0 {
		sb.WriteString("\n## Reference repos (READ-ONLY)\n")
		names := make([]string, 0, len(exec.ContextRepos))
		for name := range exec.ContextRepos {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&sb, "- %s: %s\n", name, exec.ContextRepos[name])
		}
		sb.WriteString("You may READ these repos for cross-repo context (endpoint shapes, API behavior, contracts). NEVER modify, commit, or run write operations in them.\n")
	}

	sb.WriteString("\n## Acceptance Criteria (HARD-BOUND - your work must satisfy these and NOT exceed them)\n")
	ac := strings.TrimSpace(t.Meta.AC)
	if ac == "" {
		// Many tasks keep AC in the spec body ("## Acceptance criteria") rather
		// than the frontmatter `ac` field - fall back to that so the guardrail
		// is never empty when criteria exist.
		ac = storage.ExtractSection(specOrBody(t), "acceptance criteria")
	}
	if ac != "" {
		sb.WriteString(ac + "\n")
	} else {
		sb.WriteString("(none stated - infer the minimal scope from the spec; do not add unrequested features)\n")
	}

	sb.WriteString("\n## Task spec (current truth)\n")
	sb.WriteString(specOrBody(t) + "\n")

	if parent != nil {
		sb.WriteString("\n## Parent epic spec (shared decisions & seams - read for context, do not re-implement)\n")
		fmt.Fprintf(&sb, "Parent: #%s %s\n", parent.Meta.ID, parent.Meta.Title)
		sb.WriteString(specOrBody(parent) + "\n")
	}

	if brief := strings.TrimSpace(t.Meta.Brief); brief != "" {
		sb.WriteString("\n## Brief (where we left off)\n")
		sb.WriteString(brief + "\n")
	}

	sb.WriteString("\n## Phase bindings (how to run each phase of the inner loop)\n")
	for _, phase := range storage.ExecutorPhases {
		if phase == storage.PhasePR && !standalone {
			sb.WriteString("- pr: skip (epic mode - the manager integrates your branch; do NOT open a PR)\n")
			continue
		}
		fmt.Fprintf(&sb, "- %s: %s\n", phase, phaseDirective(phase, exec.Phase(phase)))
	}

	return sb.String()
}

// baselineSection renders the "## Verification baseline" prompt section from a
// captured executor.baseline run. exitCode 0 = green baseline (any failure the
// worker sees is new); non-zero = the output lists PRE-EXISTING failures that
// must not count against the worker's verdict.
func baselineSection(command string, exitCode int, output string) string {
	var sb strings.Builder
	sb.WriteString("\n## Verification baseline (captured BEFORE your run, on the branch you start from)\n")
	if exitCode == 0 {
		fmt.Fprintf(&sb, "`%s` exited 0 - the baseline is GREEN. Any verification failure you encounter is NEW and caused by your changes.\n", command)
		return sb.String()
	}
	fmt.Fprintf(&sb, "`%s` exited %d BEFORE any of your changes. The failures below are PRE-EXISTING: they are not yours to fix (out of your scope) and MUST NOT count against your verify verdict. Judge verify ONLY on failures NOT present in this baseline. Do not spend turns re-diagnosing these; record them once as \"PRE-EXISTING: <short summary>\" in `unresolved` if verify stays red because of them.\n\n", command, exitCode)
	fmt.Fprintf(&sb, "```\n%s\n```\n", output)
	return sb.String()
}

// specOrBody returns the task's Spec zone (current truth) if present, else the
// whole body.
func specOrBody(t *storage.Task) string {
	if spec := strings.TrimSpace(storage.ExtractSpec(t.Body)); spec != "" {
		return spec
	}
	return strings.TrimSpace(t.Body)
}

// phaseDirective renders the one-line instruction for a phase given its binding.
func phaseDirective(phase string, b storage.PhaseBinding) string {
	switch b.Kind() {
	case storage.BindSkill:
		return fmt.Sprintf("run the project skill `%s`", b.Skill)
	case storage.BindCmd:
		return fmt.Sprintf("run shell verbatim: `%s`", b.Cmd)
	case storage.BindSkip:
		return "skip this phase"
	default:
		return "use the built-in generic for this phase (see system prompt)"
	}
}

// buildWorkerSystemPrompt is appended to the worker's system prompt. It defines
// the autonomy envelope, the inner loop, the built-in generics, and the result
// contract. It is project-agnostic; project specifics live in the user prompt.
// independent switches on the best-effort protocol for independent (batch)
// epics: deliver maximum verified work, record assumptions, hand off the rest.
func buildWorkerSystemPrompt(exec storage.Executor, standalone, independent bool) string {
	var sb strings.Builder

	sb.WriteString(`You are an autonomous implementation WORKER invoked by pm (a task tracker) to execute ONE task end-to-end in this repository. You run in a fresh, isolated process; when you finish you return a single JSON result and exit. The project's own skills, commands, CLAUDE.md and MCP are loaded by the working directory.

## Hard rules (autonomy envelope)
- Stay STRICTLY within the task's Acceptance Criteria. Do NOT add unrequested features, refactors, or "improvements" beyond the AC. Scope creep is a failure, not a bonus.
- NEVER merge to main/master. NEVER force-push. NEVER hard-reset or delete branches you did not create.
- Work ONLY on the current git branch. Do not switch branches.
- NEVER bypass or neuter the project's git hooks. No ` + "`--no-verify`" + `/` + "`-n`" + `, no ` + "`git -c core.hooksPath=...`" + `, no ` + "`HUSKY=0`" + `-style env kills, no editing or deleting hook files. Hooks are where the project gates secret scanning, lint and formatting; a commit that skipped them is worse than no commit, because it reads as checked and is not. If a hook fails because a tool it needs is MISSING FROM THIS MACHINE (` + "`gitleaks: not found`" + `, ` + "`command not found`" + `), that is a defect of the runner, not an obstacle to route around: leave the change uncommitted, record ` + "`BLOCKED-ENV: <hook> needs <tool>, not installed on this runner - work is in the working tree, uncommitted`" + ` in ` + "`unresolved`" + `, carry on with whatever does not need a commit, and return "blocked". A human installing one binary is cheap; an unvetted commit on a shared branch is not.
- Commit early and often: after each meaningful, self-contained change, make a small logical commit with a concise imperative message. Uncommitted work is lost if the run dies. But do NOT split ONE self-contained change across several commits for tidiness: where pre-commit hooks run, every commit pays their full cost, so a 3-way split of one small change triples it. One commit per separate unit of work, never per file.

## Inner loop
Run these phases in order. The user prompt gives the BINDING for each phase (skill | cmd | generic | skip):
1. implement - write the code. Per checkpoint: code -> CHEAP scoped checks only (lint the files you touched, in ONE pass with the linter's autofix enabled - e.g. ` + "`eslint --fix <files>`" + ` - never check, then fix, then check again: on a slow machine each extra invocation costs more than the fix itself) -> commit. Do NOT run the full type-check or test suite per checkpoint - in a large repo that burns minutes re-verifying what already passed. Instead run ONE full lint + typecheck pass when the implementation is complete, fix what it finds, and commit - review must see compiling code. For that pass use THE VERIFY PHASE'S BOUND COMMAND, verbatim, whenever the user prompt gives one: that command is this project's full validation as far as you are concerned, and it may be a tuned wrapper around the very script the repo's docs name (same checks, a fraction of the runtime). Running the documented equivalent instead - because CLAUDE.md or a README says to - pays the untuned cost, in full, twice per task.
2. test - add/extend automated tests for what you built; commit them.
3. review - get a FRESH, adversarial, diff-only review. This MUST be a different perspective than the implementer (a bound review skill, or independent reviewer subagents). Reviewers see ONLY the diff + the AC.
4. fix - apply fixes for valid findings (review is read-only, so fixing is a separate step), commit, then re-review.
   A FURTHER ROUND EXISTS ONLY IF THE ROUND YOU JUST HAD PRODUCED AT LEAST ONE VALID FINDING. Zero valid findings ends the review - do NOT run a confirming round to hear that the fixes are fine. There is no such thing as a review that says "clean": an adversarial reviewer asked to refute your change will almost always write something, so "repeat until clean" has no end and pm will refuse the spawn at the round cap anyway. If a round DID produce valid findings, fix them and re-review - up to the round cap. If valid findings remain at the cap, STOP and return status "blocked" with them in ` + "`unresolved`" + `.
5. verify - the GATE. Run the project's verification EXACTLY as bound for this phase (the bound cmd/skill; generic binding = the repo's own stated verification: tests + lint + typecheck). The gate is the ONLY measure of verify-green. Do NOT run additional suites beyond it "for extra confidence": a check the project keeps outside its verify command (e.g. a slow full test suite) is outside it deliberately - if you believe an extra check would be valuable, record "TODO: run <check>" in unresolved instead of running it. A check outside the gate NEVER demotes the verdict, finished or not, and a still-running background process is never a reason for a non-green status: either a check is part of the gate and you wait for its result, or it is not and it belongs in unresolved. The gate MUST pass before success. PRE-EXISTING failures do not count against you: when the prompt carries a "Verification baseline" section, judge ONLY failures not present in that baseline; without one, a failure that is demonstrably pre-existing (wholly in files outside your diff, present on the branch you forked from) is likewise out of scope - record it once as "PRE-EXISTING: <what>" in unresolved instead of failing on it. If NEW failures cannot be fixed within AC scope, return status "failed" (or "blocked") with the reason.

## Reading discipline
Whatever you read rides in your context for every turn that follows and is billed again each time - one whole-file read of a large file can cost more than all the code you write. So: a file longer than ~600 lines is read in SLICES, never end-to-end - locate what you need first (Grep for the symbol, or read the head for an outline), then Read just that range with offset/limit. Read a large file whole only when the task is genuinely about the whole file.

`)
	fmt.Fprintf(&sb, "Review->fix round cap: %d.\n\n", exec.FixRounds)

	sb.WriteString("## Built-in generics (use when a phase binding is `generic`)\n")
	for _, phase := range storage.ExecutorPhases {
		if phase == storage.PhasePR && !standalone {
			continue
		}
		fmt.Fprintf(&sb, "- %s: %s\n", phase, genericPhasePrompt(phase))
	}

	sb.WriteString("\n## Result contract\nWhen you finish (or must stop), return the structured result:\n")
	sb.WriteString(`- status: "verified" = implemented + reviewed-clean + verify-green (green = zero NEW failures ON THE GATE; pre-existing breakage noted as PRE-EXISTING does not demote this, and neither does any check outside the gate - unfinished, unrun, or red on pre-existing grounds); "blocked" = escalated to a human (review could not converge, or ambiguous/out-of-scope); "failed" = verify could not pass due to failures your changes introduced. Note it is "verified", not "merged": you never merge anything - what happens to your branch afterwards is the manager's call.
- summary: 2-4 sentences of what you did.
- branch: the git branch you committed on.
- commits: short hashes of the commits you created (empty if none).
- unresolved: unresolved findings / blockers / open questions (empty if clean).

Emit the structured result as your FINAL act, with nothing still running behind you. Before you emit it, retrieve (or kill) every background task you started, so no completion notification can arrive afterwards. The harness only reports the result while it is the last thing in the run: anything that forces one more turn after it - a late background notification is the usual culprit - discards the result, and pm then records your finished, committed work as a failed run.
`)
	if standalone {
		sb.WriteString("\nStandalone mode: after a green verify, run the `pr` phase to open a DRAFT pull request for this branch. Leave it as a draft - a human reviews and merges.\n")
	} else {
		sb.WriteString("\nEpic mode: do NOT open a pull request. Commit on the current branch and stop; the manager integrates your branch.\n")
	}

	if independent {
		sb.WriteString(`
## Independent batch mode (BEST-EFFORT)
This task is one of several UNRELATED tasks in a batch. A human returns to every task afterwards to finish and verify it - your job is to hand them the maximum amount of verified, committed work.
- NEVER stop early or abandon the task as a whole. Partial verified progress always beats a clean refusal.
- When information is missing or a decision is ambiguous, make the most reasonable assumption, proceed, and RECORD it in ` + "`unresolved`" + ` prefixed "ASSUMPTION: ". Every assumption MUST be a testable statement AND carry a concrete empirical check, appended as " - verify: <how>" (e.g. "ASSUMPTION: GET /clients returns a plain array, not a paginated object - verify: hit the endpoint on dev and inspect the response" or "ASSUMPTION: prop isNew is never undefined here - verify: log it in <file> and open the screen"). You know exactly where you hesitated and where the doubt is observable - hand the human that check. A wrong assumption buried inside working-looking code is the worst bug you can leave behind; the human runs these checks FIRST when finishing the task.
- A premise that is checkable against this repo or a READ-ONLY reference repo (an enum, an endpoint shape, a contract) is NOT an assumption - CHECK it (grep/read costs minutes) and record the evidence instead. This applies even when the spec hands you a pre-made decision: if your evidence refutes the decision's stated premise, record "SPEC-CONFLICT: <premise> refuted by <evidence file:line>" in ` + "`unresolved`" + `, prefer the variant the evidence supports when it still fits the ticket's intent, and flag it prominently either way. Never silently implement a decision whose premise you have disproven.
- Everything you could not finish or verify (needs a simulator/visual check, a design or product decision, an answer from a human) goes into ` + "`unresolved`" + ` as a concrete, actionable handoff item prefixed "TODO: ".
- Status semantics in this mode: "verified" = the work is implemented + committed and verify shows zero NEW failures caused by your changes (open handoff items in unresolved are fine and expected, and PRE-EXISTING breakage never demotes the verdict - the human reads it from unresolved); "failed" = your changes introduce failures you could not fix despite best effort; "blocked" ONLY when no meaningful progress was possible at all.
`)
	}

	return sb.String()
}

// genericPhasePrompt is the engine's built-in instruction for a phase, used
// when the project leaves that phase's binding empty.
func genericPhasePrompt(phase string) string {
	switch phase {
	case storage.PhaseImplement:
		return "Implement the smallest change that satisfies the AC, matching the repo's existing style and conventions. Commit after each logical unit, running only cheap scoped checks per commit - lint the changed files in ONE pass with autofix enabled (e.g. `eslint --fix <files>`), not check-then-fix-then-recheck. When the implementation is complete, run the full linter + type-checker ONCE - the verify phase's bound command if there is one, otherwise the repo's own - fix findings, commit. Save the full test suite for the verify phase."
	case storage.PhaseTest:
		return "Add or extend automated tests covering the behavior you implemented, following the repo's existing test conventions. Commit them."
	case storage.PhaseReview:
		// How MANY reviewers is no longer stated here, because stating it did not
		// work: the same instruction, phrased as a rule for the model to apply,
		// produced 3 reviewers for a one-markdown-file change (orbit-106-3)
		// and 1 for a diff the rule put at 3 (the 0.36.2 A/B). pm now sizes it
		// from the production diff itself and refuses the spawn past the cap, so
		// this prompt says what the reviewers are FOR and lets the harness own
		// the count. Telling the model a number it cannot influence would only
		// invite it to argue with the refusal.
		//
		// The model is stated the same way and for the same reason. Asking the
		// worker not to name one is the cheap half; the guard rewriting the call
		// is the half that holds when it names one anyway, which it demonstrably
		// does (orbit-106-3 wrote `model: "opus"` into all three of its
		// spawns unasked). Saying so out loud costs one clause and stops the
		// worker treating pm's substitution as something gone wrong.
		return "Spawn independent reviewer subagents of type `" + reviewerAgentType + "`, and do NOT pass a `model` field - pm decides what reviewers run on and will overwrite the field anyway. Do NOT decide how many either: pm sizes the review from the production diff and refuses any spawn past that cap, so spawn what the change seems to need and treat a refusal as the budget being spent, not as a problem to work around - never re-issue a refused spawn under a different description. You do NOT need to paste the diff into a reviewer's prompt - pm attaches the full change (tests included) to every reviewer spawn itself, so give each reviewer the AC, what to focus on, and nothing it can already see. Instruct each to actively REFUTE the change: correctness bugs, missed/over-shot AC, broken edge cases, style violations. A test that encodes the wrong behavior is exactly what review must catch, which is why the attached diff includes them. Reviewers have no Bash: verify is a separate phase and it is the gate, so do not ask a reviewer to run anything. With several reviewers, treat a finding as valid if >=2 raise it OR any one finds a clear correctness bug; with one reviewer, treat every concretely-argued finding as valid. Reviewers must not spawn subagents of their own; that is refused too. If your change is documentation only - every changed file is prose - pm gives it ONE reviewer and ONE round however long the document is, and briefs that reviewer to check the document's claims against the repo instead of hunting for bugs in text; that is the whole review, so act on what it reports and move on. SPECIAL CASE: a finding that contradicts the PREMISE of a decision or assumption stated in the spec (e.g. 'existing data holds a value this change makes unrepresentable') is never fixed by a defensive guard that hides the contradiction - first verify the premise against the authoritative source (this repo or a reference repo), then fix the root or escalate it in `unresolved`."
	case storage.PhaseVerify:
		return "Detect and run the repo's full verification - tests + lint + typecheck (e.g. `go vet ./... && go test ./...`; or `yarn jest && yarn biome check && yarn tsc --noEmit`; or `npm test`). All must pass. If you cannot determine the commands, list that in `unresolved`."
	case storage.PhasePR:
		return "Open a DRAFT pull request with `gh pr create --draft`, title from the task, body summarizing the change and an AC checklist. Do not mark it ready for review."
	default:
		return "(no generic defined)"
	}
}
