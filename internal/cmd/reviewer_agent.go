package cmd

import (
	"encoding/json"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
)

// Reviewer subagents are the largest single line item the executor spends on:
// in a 2026-08 epic sixteen of them accounted for 55.3M tokens, 58% of
// the whole epic, and every one of them ran on opus for no reason anybody chose
// - a subagent with no model named inherits the session's, and the session was
// the run's --model.
//
// Pinning them takes THREE layers, and the middle one is the reason the other
// two exist:
//
//  1. pm hands `claude` an agent definition (--agents) whose model is fixed.
//     Verified against claude 2.1.224 on 2026-08-07: a `pm-reviewer` type
//     defined this way spawned with `agentType: "pm-reviewer"` in its
//     .meta.json and answered on claude-haiku-4-5 while the session ran on
//     something else - the definition's model is real, not advisory.
//  2. The review prompt names the type and tells the worker not to pass a model.
//  3. The guard hook rewrites the call anyway, because a `model` named BY THE
//     CALLER beats the definition's - and the caller does name one: in the
//     third sub of a 2026-08 epic the worker wrote `model: "opus"` into all
//     three of its Agent calls unprompted, while in subs one and two it named
//     none. Layer 2
//     asks; layer 3 is what makes the answer not matter.

const reviewerAgentType = storage.ReviewerAgentType

// reviewerAgentTools is what a reviewer may reach for.
//
// Agent is absent because a reviewer spawning its own subagent is refused by the
// guard (see review_cap.go), and leaving it off the definition means the
// reviewer never tries in the first place - a refusal it never has to spend a
// turn on.
//
// Bash is absent because it was the single largest way reviewers spent tokens:
// 340 calls and 0.69M characters across the 16 reviewers of a 2026-08
// epic. A reviewer given the diff up front has nothing left to shell
// out for. The deliberate loss is that it can no longer run the tests or read
// `git log` - neither of which is its job. Verify is a separate phase of the
// inner loop and it is the gate; a reviewer's job is to find what is wrong with
// the change in front of it.
var reviewerAgentTools = []string{"Read", "Grep", "Glob"}

// reviewerAgentPrompt is the reviewer's system prompt. Deliberately thin: the
// per-call prompt the worker writes carries the actual change, the AC and what
// to look at, and duplicating that here would put two sets of instructions in
// front of the same agent.
const reviewerAgentPrompt = "You are an adversarial code reviewer working for an automated executor. " +
	"You are given a change and the criteria it must meet. Your job is to find what is WRONG with it: " +
	"correctness bugs, unhandled cases, criteria the change does not actually satisfy, and claims in the " +
	"change's own description that the code does not support. Report findings with file and line, each one " +
	"concrete enough to act on, and say plainly when you found nothing rather than manufacturing a finding. " +
	"You do not fix anything and you do not commit; the agent that spawned you decides what to do with what you report."

const reviewerAgentDescription = "Adversarially reviews a change against its acceptance criteria and reports findings"

// reviewerAgentsJSON renders the --agents payload. The type is defined even
// when there is no model to pin (review_model: inherit) - the prompt names it
// unconditionally, and a named type that does not exist is a failed spawn. What
// "inherit" turns off is the model key alone, so the reviewer falls back to the
// session's model exactly as it did before any of this existed, while keeping
// its tool list and its system prompt.
func reviewerAgentsJSON(model string) string {
	def := map[string]any{
		"description": reviewerAgentDescription,
		"prompt":      reviewerAgentPrompt,
		"tools":       reviewerAgentTools,
	}
	if m := strings.TrimSpace(model); m != "" {
		def["model"] = m
	}
	b, err := json.Marshal(map[string]any{reviewerAgentType: def})
	if err != nil {
		return ""
	}
	return string(b)
}

// The generic-type predicate the three policies below share lives in storage
// (storage.GenericSubagentType) so the telemetry aggregate can apply the same
// definition of "a spawn pm treats as a reviewer" when advancing the
// reviewed-tip signal. The rationale is inheritance: generic types carry no
// model of their own, and pinning a model onto a type whose own definition
// names one would be pm overruling a decision someone made on purpose.

// pinReviewerAgent applies pm's model policy to one subagent spawn, mutating
// the raw tool input in place and reporting whether anything changed.
//
// The raw map matters: the input carries fields pm has no opinion about
// (`description`, and whatever a later build adds - `run_in_background` used to
// be one of them until forceSyncSpawn below grew an opinion), and updatedInput
// REPLACES the whole input - rebuilding it from pm's own struct would silently
// drop them.
//
// Two rules, and the split between them is the AC of this change:
//   - a spawn with no type at all becomes a reviewer. This is a safety net, not
//     the main path: the tool requires the field, so in practice it never fires.
//   - the model is pinned only on types that would otherwise inherit the run's.
//     A spawn naming a custom type is left completely alone - that type has its
//     own definition, and pm second-guessing it would be a different change from
//     the one this is.
func pinReviewerAgent(ti map[string]any, model string) bool {
	if ti == nil || strings.TrimSpace(model) == "" {
		return false
	}
	changed := false
	subType, _ := ti["subagent_type"].(string)
	if strings.TrimSpace(subType) == "" {
		subType = reviewerAgentType
		ti["subagent_type"] = subType
		changed = true
	}
	if !storage.GenericSubagentType(subType) {
		return changed
	}
	if cur, _ := ti["model"].(string); cur != model {
		ti["model"] = model
		changed = true
	}
	return changed
}

// attachReviewPacket puts the change under review into the spawn's prompt, and
// reports whether it did.
//
// Gated on the prompt not already carrying a diff, because a worker that did
// the right thing must not be punished for it with a second copy - and the
// telemetry's has_diff field, which measures exactly that, is recorded before
// this runs, so pm's own attachment can never flatter the measurement.
func attachReviewPacket(ti map[string]any, dir, baseSHA string) bool {
	if ti == nil {
		return false
	}
	subType, _ := ti["subagent_type"].(string)
	if !storage.GenericSubagentType(subType) {
		return false
	}
	prompt, _ := ti["prompt"].(string)
	if prompt == "" || promptCarriesDiff(prompt) {
		return false
	}
	packet := buildReviewPacket(dir, baseSHA)
	if packet == "" {
		return false
	}
	ti["prompt"] = prompt + packet
	return true
}

// forceSyncSpawn makes a generic-type subagent spawn synchronous, and reports
// whether it changed anything.
//
// The cause it removes, established from the pm-cli-100 worker transcripts
// (sessions 315ff4ff round 1, 3f5742db round 2): the Agent tool runs subagents
// IN THE BACKGROUND by default on the measured build (claude 2.1.224 - "Async
// agent launched... you will be notified"), and a headless worker whose mandate
// is "return the envelope" ends its turn while the reviewer is still running.
// Re-invoked without the reviewer's notification in hand, it loads TaskStop,
// kills the reviewer and returns "blocked - review not collected". Three subs
// in one epic landed there, each costing a human a hand-run review round and a
// hand merge. A synchronous spawn removes the whole window: the worker blocks
// in the tool call and the tool result IS the report.
//
// Same scope as the model pin: generic types only - a custom agent type
// running in the background is a decision someone made on purpose. An absent
// field is forced too, because absent MEANS background on the measured build.
func forceSyncSpawn(ti map[string]any) bool {
	if ti == nil {
		return false
	}
	subType, _ := ti["subagent_type"].(string)
	if !storage.GenericSubagentType(subType) {
		return false
	}
	if bg, ok := ti["run_in_background"].(bool); ok && !bg {
		return false
	}
	ti["run_in_background"] = false
	return true
}

// rewriteAgentSpawn applies every policy pm has about a spawn it is allowing,
// and returns what it changed - empty when it changed nothing, which is the
// signal to stay silent rather than emit an inert rewrite.
func rewriteAgentSpawn(ti map[string]any, dir, baseSHA, reviewModel string) []string {
	var applied []string
	if pinReviewerAgent(ti, reviewModel) {
		applied = append(applied, "pinned reviewer subagents to "+reviewModel)
	}
	if attachReviewPacket(ti, dir, baseSHA) {
		applied = append(applied, "attached the diff under review to the prompt")
	}
	if forceSyncSpawn(ti) {
		applied = append(applied, "forced the spawn synchronous (run_in_background: false) - the tool result is the reviewer's report; a background reviewer outlives the worker's turn and is never collected")
	}
	return applied
}
