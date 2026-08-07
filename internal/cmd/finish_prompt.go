package cmd

import (
	"fmt"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
)

// The acceptance run's contract with its worker, and nothing else.
//
// The PROCEDURE of an odbiór - discovering the run, verifying every ASSUMPTION
// the batch's workers recorded, fixing the refuted ones, working the TODO
// handoff, prepping each per-task PR - lives in the global `batch-finish-auto`
// skill and its sibling `batch-finish`, some 630 lines across two SKILL.md
// files. It is deliberately NOT restated here: a headless `claude -p` sees the
// global skills and can invoke them with the Skill tool (verified 2026-08-07,
// twice: it listed both skills by name, and returned the content of one it was
// told to run), so copying the procedure into this prompt would only create a
// second copy to drift out of step with the first.
//
// pm gives the PROCESS - the claim, the run-state, the report path, the result
// contract - and the skill gives the procedure.

// finishResult is the JSON contract an acceptance worker returns. Deliberately
// NOT workerResult: an acceptance produces a verdict PER SUB of the batch it
// accepted, and the branch/commits shape of a worker result has nowhere to put
// them.
type finishResult struct {
	Status  string `json:"status"`  // done | partial | blocked
	Summary string `json:"summary"` // a few sentences
	// Report is the full markdown report (the per-sub table the skill's final
	// step produces). pm writes it to FinishReportPath - see writeFinishReport.
	Report string      `json:"report"`
	Subs   []finishSub `json:"subs"`
	// Run stats lifted off the claude envelope by parseFinishResult, exactly as
	// workerResult carries them: observed by pm, never claimed by the worker,
	// and therefore absent from finishResultSchema.
	Turns   int     `json:"turns,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
}

// finishSub is the acceptance verdict on ONE sub of the batch.
type finishSub struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"` // clean | fixed | blocked
	// VisualClaimsOpen is load-bearing, not decoration. A detached acceptance
	// never touches the simulator (the user's rule: anything that runs
	// unattended keeps its hands off the runtime), so every claim that can only
	// be settled by looking at a screen stays unresolved - and without this
	// number a run where half the visual work was never checked reports itself
	// as plain "done". It is the human's morning TODO list.
	VisualClaimsOpen int      `json:"visual_claims_open"`
	PushedCommits    []string `json:"pushed_commits"`
}

// finishResultSchema constrains the acceptance worker's structured output.
// visual_claims_open is REQUIRED for the reason given on the field above: made
// optional it would simply be omitted, and an omitted count reads as zero.
const finishResultSchema = `{
  "type": "object",
  "properties": {
    "status": {"type": "string", "enum": ["done", "partial", "blocked"]},
    "summary": {"type": "string"},
    "report": {"type": "string"},
    "subs": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "verdict": {"type": "string", "enum": ["clean", "fixed", "blocked"]},
          "visual_claims_open": {"type": "integer"},
          "pushed_commits": {"type": "array", "items": {"type": "string"}}
        },
        "required": ["id", "verdict", "visual_claims_open", "pushed_commits"]
      }
    }
  },
  "required": ["status", "summary", "report", "subs"]
}`

// Acceptance status vocabulary, mirroring work.go's worker* constants.
const (
	finishDone    = "done"
	finishPartial = "partial"
	finishBlocked = "blocked"
)

// buildFinishPrompt assembles the acceptance worker's user prompt. It is short
// on purpose (see the file header): identity, where things are, which sim mode
// this run is in, and the instruction to run the skill.
func buildFinishPrompt(tracker *storage.Task, proj *storage.Project, slug, workDir, reportPath string, sim bool) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Acceptance (odbiór) of run #%s %s\n", tracker.Meta.ID, tracker.Meta.Title)
	fmt.Fprintf(&sb, "Project: %s", slug)
	if proj.Stack != "" {
		fmt.Fprintf(&sb, " | Stack: %s", proj.Stack)
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "Repo path: %s\n", workDir)
	fmt.Fprintf(&sb, "Tracker to accept: %s\n", tracker.Meta.ID)
	fmt.Fprintf(&sb, "Simulator: %s\n", finishSimLabel(sim))

	sb.WriteString("\n## What to do\n")
	fmt.Fprintf(&sb, "Invoke the Skill tool with skill name `batch-finish-auto`, for tracker `%s` in project `%s`, "+
		"with the flag `%s`. Follow that skill; it is the procedure, and this prompt does not repeat it.\n",
		tracker.Meta.ID, slug, finishSimFlag(sim))

	sb.WriteString("\n## Where the report goes\n")
	fmt.Fprintf(&sb, "pm writes the `report` field of your result to %s. Put the full markdown report there "+
		"(the per-sub table the skill's final step produces) - do NOT write that file yourself.\n", reportPath)

	sb.WriteString("\n## Returning the result\n")
	sb.WriteString("Emit the result with StructuredOutput as your FINAL act, with nothing still running behind you: " +
		"retrieve or kill every background task you started BEFORE emitting it. The envelope carries the structured " +
		"result only while that call is the last thing in the run - a late background-task notification forces one " +
		"more turn and the result is lost.\n")

	return sb.String()
}

// buildFinishSystemPrompt is the acceptance run's autonomy envelope: what this
// run is, what it may do, and what the fields of its result mean.
func buildFinishSystemPrompt(sim bool) string {
	var sb strings.Builder

	sb.WriteString("You are an autonomous ACCEPTANCE (odbiór) run spawned by pm - the third kind of pm run, " +
		"alongside `pm work` (implement one task) and `pm run-epic` (drive an epic). You run headless in a fresh, " +
		"isolated process; when you finish you return a single JSON result and exit.\n\n")

	sb.WriteString("## The procedure is the skill's\n")
	sb.WriteString("The `batch-finish-auto` skill holds the whole acceptance procedure. Read it and follow it. " +
		"Nothing below overrides it; the rules here are the ones pm owns because they are about the RUN, not " +
		"about how an odbiór is done.\n\n")

	sb.WriteString("## Hard rules\n")
	sb.WriteString("- Do NOT move any task to a done/closed status. Acceptance does not close tasks - that is a " +
		"human's decision, and both pm's own rules and the skill say so.\n")
	sb.WriteString("- Do NOT merge anything and do NOT open pull requests.\n")
	sb.WriteString("- Pushing fixes to a sub's OWN branch is allowed and expected, headless and overnight included - " +
		"that is what this run is for.\n")
	sb.WriteString("- Never bypass or neuter the project's git hooks (no `--no-verify`/`-n`, no " +
		"`core.hooksPath`, no editing or deleting hook files). A pm hook blocks these; if a hook fails because a " +
		"tool it needs is missing from this machine, record `BLOCKED-ENV: <hook> needs <tool>` and leave that " +
		"sub's work unpushed rather than routing around it.\n")
	sb.WriteString("- You may spawn one subagent per sub of the batch. Those subagents must not spawn subagents " +
		"of their own; that is refused.\n\n")

	sb.WriteString("## Simulator\n")
	if sim {
		sb.WriteString("This run was launched WITH `--sim`, so a human is present and the runtime is yours to " +
			"use. Settle the visual claims you can, and count only what genuinely remains in " +
			"`visual_claims_open`.\n\n")
	} else {
		sb.WriteString("This run is DETACHED and must not touch the simulator or any other shared runtime. " +
			"That is deliberate: anything that runs unattended keeps its hands off it. So every claim that can " +
			"only be settled by looking at a screen stays OPEN - do not guess it either way, count it.\n\n")
	}

	sb.WriteString("## Result contract\n")
	sb.WriteString("- `status`: \"done\" = every sub accepted with nothing left for a human beyond the visual " +
		"claims you counted; \"partial\" = some subs accepted, others still need work; \"blocked\" = you could " +
		"not accept the batch at all (say why in `summary`).\n")
	sb.WriteString("- `summary`: a few sentences. `report`: the full markdown report - pm saves it for the human.\n")
	sb.WriteString("- `subs[]`: one entry per sub you looked at. `verdict` is \"clean\" (nothing needed doing), " +
		"\"fixed\" (you changed something and pushed it) or \"blocked\" (a human has to). `pushed_commits` lists " +
		"the short hashes you actually pushed, empty when you pushed nothing.\n")
	sb.WriteString("- `visual_claims_open`: how many claims about that sub remain unsettled because they could " +
		"only be checked by looking at a running app. This number is REQUIRED and it is read: an acceptance " +
		"reporting \"done\" while half its visual claims were never checked is exactly what it exists to prevent. " +
		"Report it honestly; 0 only when there genuinely were none left.\n\n")

	sb.WriteString("Emit the structured result as your FINAL act, with no background task still running - the " +
		"harness reports it only while it is the last thing in the run.\n")

	return sb.String()
}

// finishSimFlag is the flag passed on to the skill, which takes --sim/--no-sim.
func finishSimFlag(sim bool) string {
	if sim {
		return "--sim"
	}
	return "--no-sim"
}

func finishSimLabel(sim bool) string {
	if sim {
		return "ON (--sim: a human is present, the runtime may be used)"
	}
	return "OFF (--no-sim: detached, the simulator is never touched)"
}
