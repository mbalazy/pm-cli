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
			Instructions: "Project manager for freelance tasks. Use pm_context at session start to see current work.",
		},
	)

	registerTools(s, store)
	registerResources(s, store)

	return s.Run(context.Background(), &mcp.StdioTransport{})
}
