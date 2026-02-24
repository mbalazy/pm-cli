package cmd

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/board"
	"github.com/spf13/cobra"
)

func newBoardCmd(store *storage.Store) *cobra.Command {
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
			return runBoard(store, project)
		},
	}
}

func runBoard(store *storage.Store, project string) error {
	m := board.New(store, project)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
