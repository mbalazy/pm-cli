// Package cmd is the cobra tree behind the `pm` binary - every subcommand.
// It predates internal/service and mostly does not go through it: the CLI
// reads and writes tasks with internal/storage directly, and `pm today` is
// the one command that calls the service layer the MCP server and the HTTP
// API share. A rule added there does NOT reach the CLI by itself - `pm add`
// carries its own status default and its own lock - so the two are kept in
// step by hand.
//
// What is this package's own is the executor (work*.go, run_epic*.go,
// finish*.go, worker_guard.go): launching headless `claude -p` workers,
// enforcing the review policy from the outside through a PreToolUse hook,
// and recording what every run did.
package cmd

import (
	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/mbalazy/pm-cli/internal/version"
	"github.com/spf13/cobra"
)

func newStore() storage.TaskStore {
	return storage.NewStore()
}

func NewRootCmd() *cobra.Command {
	store := newStore()

	root := &cobra.Command{
		Use:     "pm",
		Short:   "Local project manager and control plane for AI coding agents",
		Long:    "A local, file-based project manager with an interactive TUI board, an MCP server for Claude Code, and an autonomous executor that runs tasks through headless workers.",
		Version: version.Version,
		RunE: func(cmd *cobra.Command, args []string) error {
			// default: open board, auto-detect project from cwd
			return runBoard(store, detectProjectFromCwd(store))
		},
		SilenceUsage: true,
	}

	root.AddCommand(
		newInitCmd(store),
		newProjectsCmd(store),
		newAddCmd(store),
		newListCmd(store),
		newContextCmd(store),
		newTodayCmd(store),
		newShowCmd(store),
		newMvCmd(store),
		newReorderCmd(store),
		newDoneCmd(store),
		newEditCmd(store),
		newBoardCmd(store),
		newWorkCmd(store),
		newRunEpicCmd(store),
		newFinishCmd(store),
		newRunsCmd(store),
		newServeCmd(store),
		newConfigCmd(store),
		newExecutorCmd(store),
		newJournalCmd(store),
		newTimelineCmd(store),
		newMcpCmd(store),
		newMigrateIDsCmd(store),
		newSessionIDCmd(),
		newWorkerGuardCmd(),
		newDocsCmd(),
	)

	return root
}
