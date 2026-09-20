// Package mcpserver is `pm mcp`: the stdio MCP server Claude Code registers,
// 14 tools over the same internal/service functions the HTTP API calls. The
// handlers are deliberately thin - decode, call, respond - and every
// parameter description an agent reads comes from the service package's
// argument structs rather than a second copy here.
//
// Output budgets are part of the contract, because a rollup that runs at
// every session start is paid for at every session start: pm_context caps
// task bodies and collapses finished trackers, and pm_list_tasks answers
// inside an explicit {total, shown, note} wrapper.
package mcpserver

import (
	"context"

	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/mbalazy/pm-cli/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func Run(store storage.TaskStore) error {
	s := mcp.NewServer(
		&mcp.Implementation{
			Name:    "pm",
			Version: version.Version,
		},
		&mcp.ServerOptions{
			Instructions: "Local project manager and AI-agent control plane. Use pm_context at session start to see current work.",
		},
	)

	registerTools(s, store)

	return s.Run(context.Background(), &mcp.StdioTransport{})
}
