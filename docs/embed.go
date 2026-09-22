// Package docs embeds the canonical agent-facing documentation so the binary
// can print it wherever pm is installed (`pm docs guide` / `pm docs
// authoring`). The markdown files in this directory stay the single source of
// truth: they are readable in the repo, versioned with the code, and shipped
// inside the binary - a user's CLAUDE.md or AGENTS.md is a consumer of
// `pm docs guide` output, never a second copy to keep in sync by hand.
package docs

import _ "embed"

// AgentGuide is the canonical usage contract for AI agents driving pm via MCP.
// It is one markdown block (wrapped in pm:agent-guide markers) that any MCP
// client's instructions file takes, so the install is a plain append:
// `pm docs guide >> ~/.claude/CLAUDE.md` for Claude Code,
// `pm docs guide >> ~/.codex/AGENTS.md` for Codex.
//
//go:embed agent-guide.md
var AgentGuide string

// TaskAuthoring is the task-authoring ruleset the agent guide tells agents to
// read before pm_add_task (spec/body/brief/links content, ONLY-VERIFIED-FACTS,
// task hygiene).
//
//go:embed task-authoring.md
var TaskAuthoring string
