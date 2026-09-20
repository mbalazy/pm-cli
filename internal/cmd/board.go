package cmd

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/mbalazy/pm-cli/internal/tui/board"
	"github.com/spf13/cobra"
)

func newBoardCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "board [project]",
		Short: "Open interactive kanban board",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := ""
			if len(args) > 0 {
				slug, err := store.ResolveProject(args[0])
				if err != nil {
					return err
				}
				project = slug
			}
			if project == "" {
				project = detectProjectFromCwd(store)
			}
			return runBoard(store, project)
		},
	}
}

func detectProjectFromCwd(store storage.TaskStore) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	slug, err := storage.ResolveProjectFromCwd(store, cwd)
	if err != nil {
		return ""
	}
	return slug
}

func runBoard(store storage.TaskStore, project string) error {
	m := board.New(store, project)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
