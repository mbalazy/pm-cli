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
func buildWorkerPrompt(t *storage.Task, parent *storage.Task, proj *storage.Project, slug string, exec storage.Executor, branch string, standalone bool) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Task: #%s %s\n", t.Meta.ID, t.Meta.Title)
	fmt.Fprintf(&sb, "Project: %s", slug)
	if proj.Stack != "" {
		fmt.Fprintf(&sb, " | Stack: %s", proj.Stack)
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "Repo path: %s\n", proj.Path)
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
- Commit early and often: after each meaningful, self-contained change, make a small logical commit with a concise imperative message. Uncommitted work is lost if the run dies.

## Inner loop
Run these phases in order. The user prompt gives the BINDING for each phase (skill | cmd | generic | skip):
1. implement - write the code. Per checkpoint: code -> lint + typecheck -> commit.
2. test - add/extend automated tests for what you built; commit them.
3. review - get a FRESH, adversarial, diff-only review. This MUST be a different perspective than the implementer (a bound review skill, or independent reviewer subagents). Reviewers see ONLY the diff + the AC.
4. fix - apply fixes for valid findings (review is read-only, so fixing is a separate step), commit, then re-review.
   Repeat review->fix until the review is clean OR you reach the review->fix round cap. If still not clean at the cap, STOP and return status "blocked" with the unresolved findings.
5. verify - the GATE. Run the project's full verification (tests + lint + typecheck). It MUST pass before success. If it cannot be made green within AC scope, return status "failed" (or "blocked") with the reason.

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
	sb.WriteString(`- status: "merged" = implemented + reviewed-clean + verify-green (ready to merge); "blocked" = escalated to a human (review could not converge, or ambiguous/out-of-scope); "failed" = verify could not pass.
- summary: 2-4 sentences of what you did.
- branch: the git branch you committed on.
- commits: short hashes of the commits you created (empty if none).
- unresolved: unresolved findings / blockers / open questions (empty if clean).
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
- Everything you could not finish or verify (needs a simulator/visual check, a design or product decision, an answer from a human) goes into ` + "`unresolved`" + ` as a concrete, actionable handoff item prefixed "TODO: ".
- Status semantics in this mode: "merged" = verify is green (open handoff items in unresolved are fine and expected); "failed" = verify stays red despite best effort; "blocked" ONLY when no meaningful progress was possible at all.
`)
	}

	return sb.String()
}

// genericPhasePrompt is the engine's built-in instruction for a phase, used
// when the project leaves that phase's binding empty.
func genericPhasePrompt(phase string) string {
	switch phase {
	case storage.PhaseImplement:
		return "Implement the smallest change that satisfies the AC, matching the repo's existing style and conventions. After each logical unit, run the repo's linter and type-checker (if present), fix issues, then commit."
	case storage.PhaseTest:
		return "Add or extend automated tests covering the behavior you implemented, following the repo's existing test conventions. Commit them."
	case storage.PhaseReview:
		return "Spawn 3 independent reviewer subagents via the Task tool. Give each ONLY the branch diff (`git diff main...HEAD`, or vs the merge-base) plus the AC, and instruct each to actively REFUTE the change: correctness bugs, missed/over-shot AC, broken edge cases, style violations. Treat a finding as valid if >=2 reviewers raise it OR any reviewer finds a clear correctness bug."
	case storage.PhaseVerify:
		return "Detect and run the repo's full verification - tests + lint + typecheck (e.g. `go vet ./... && go test ./...`; or `yarn jest && yarn biome check && yarn tsc --noEmit`; or `npm test`). All must pass. If you cannot determine the commands, list that in `unresolved`."
	case storage.PhasePR:
		return "Open a DRAFT pull request with `gh pr create --draft`, title from the task, body summarizing the change and an AC checklist. Do not mark it ready for review."
	default:
		return "(no generic defined)"
	}
}
