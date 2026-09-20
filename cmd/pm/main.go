// Command pm is the pm binary: the CLI, the kanban TUI (`pm board`, or a bare
// `pm`), the MCP server Claude Code registers (`pm mcp`), the cockpit's HTTP
// server (`pm serve`) and the unattended executor (`pm work`, `pm run-epic`,
// `pm finish`). Every subcommand lives in internal/cmd; this file builds the
// root command and stamps the process start time the board measures its own
// startup against.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/mbalazy/pm-cli/internal/cmd"
	"github.com/mbalazy/pm-cli/internal/tui/board"
)

func init() {
	board.ProcessStart = time.Now()
}

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
