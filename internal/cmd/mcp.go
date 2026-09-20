package cmd

import (
	"github.com/mbalazy/pm-cli/internal/mcpserver"
	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/spf13/cobra"
)

func newMcpCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start MCP server (stdio transport)",
		Long:  "Start a Model Context Protocol server for Claude Code integration. Communicates via stdin/stdout.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mcpserver.Run(store)
		},
	}
}
