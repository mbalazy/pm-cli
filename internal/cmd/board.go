package cmd

import (
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/board"
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
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	projects, err := store.ListProjects()
	if err != nil {
		return ""
	}
	for _, slug := range projects {
		proj, err := store.GetProject(slug)
		if err != nil || proj == nil || proj.Path == "" {
			continue
		}
		projectPath, err := filepath.Abs(proj.Path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(projectPath, cwd)
		if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))) {
			return slug
		}
	}
	return ""
}

func runBoard(store storage.TaskStore, project string) error {
	m := board.New(store, project)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
