package mcpserver

import (
	"context"

	"github.com/mbalazy/pm/internal/service"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The project timeline's MCP surface; the semantics live in
// service.TimelineAdd / service.TimelineList. The descriptions carry the rules
// an agent needs at the moment it writes: task Log vs timeline, a state fits
// on a screen, write at the brief checkpoint, stale means a new state.

func registerTimelineTools(s *mcp.Server, store storage.TaskStore) {
	// pm_timeline_add
	mcp.AddTool(s, &mcp.Tool{
		Name: "pm_timeline_add",
		Description: "Record what happened TO a project in its timeline - a direction change, a client decision, a milestone, a new person, a document arriving - or a new dated `state` snapshot of where the project stands. " +
			"What happened INSIDE a task belongs in that task's Log (pm_update_task body_append), not here; commits, PRs and other machine events never go here. " +
			"Kinds: `event` (one to three sentences), `decision` (one sentence plus refs to the full record), `state` (10-20 lines that fit on a screen: where we stand, what blocks, what is next, open questions, links - longer material stays in a document the state refers to). " +
			"End every state line with its provenance: [verified YYYY-MM-DD by <command or doc>] for a fact checked in THIS session, [assumed] for one that was not. A line with no marker is read as unverified on every later read; a [verified] marker without a day, or with a future day, is refused. Never copy a [verified] marker forward from an older state without re-running its source. " +
			"Entries are append-only and never edited: a changed picture is a NEW state, and older states stay so 'where did we stand on date X' stays answerable. " +
			"Write at the same checkpoint where you save a brief, when something happened at project level. The result carries the stale signal - when `stale` is true, write a new state.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in service.TimelineAddInput) (*mcp.CallToolResult, any, error) {
		return respond(service.TimelineAdd(store, in))
	})

	// pm_timeline_list
	mcp.AddTool(s, &mcp.Tool{
		Name: "pm_timeline_list",
		Description: "Read a project's timeline. With no since/kind/limit: the default read - the latest `state` in full plus every entry written after it, oldest first, with `stale` (10+ entries or 7+ days since that state: time to write a new one) and a `note` when there is something to act on (no entries, no state yet, stale); with no state yet, the newest entries. Never read a state alone - it goes out of date with the first entry after it. `verification` counts the state's lines (verified / recheck / assumed / unmarked - an unmarked line is never verified) and lists in `recheck_lines` the verified lines 7+ days old: re-establish those with a tool call before relying on them. " +
			"With since (YYYY-MM-DD), kind or limit: the matching entries newest first, capped, with total vs shown. " +
			"Come here when the state does not say WHY (a decision), for what happened between two dates, or when a task contradicts the state; pm_context already carries the default read for its project.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in service.TimelineListInput) (*mcp.CallToolResult, any, error) {
		return respond(service.TimelineList(store, in))
	})
}
