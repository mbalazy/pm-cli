// Package docs embeds the canonical agent-facing documentation so the binary
// can print it wherever pm is installed (`pm docs claude` / `pm docs
// authoring`). The markdown files in this directory stay the single source of
// truth: they are readable in the repo, versioned with the code, and shipped
// inside the binary - a user's CLAUDE.md is a consumer of `pm docs claude`
// output, never a second copy to keep in sync by hand.
package docs

import _ "embed"

// AgentGuide is the canonical usage contract for AI agents driving pm via MCP.
// It is shaped as a CLAUDE.md block (wrapped in pm:agent-guide markers) so the
// install is a plain append: `pm docs claude >> ~/.claude/CLAUDE.md`.
//
//go:embed agent-guide.md
var AgentGuide string

// TaskAuthoring is the task-authoring ruleset the agent guide tells agents to
// read before pm_add_task (spec/body/brief/links content, ONLY-VERIFIED-FACTS,
// task hygiene).
//
//go:embed task-authoring.md
var TaskAuthoring string
