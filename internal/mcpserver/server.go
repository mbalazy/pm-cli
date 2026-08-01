package mcpserver

import (
	"context"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/version"
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
