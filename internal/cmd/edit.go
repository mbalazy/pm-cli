package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newEditCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <project> <task-id>",
		Short: "Open task in $EDITOR",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, err := store.ResolveProject(args[0])
			if err != nil {
				return err
			}
			task, err := store.FindTask(slug, args[1])
			if err != nil {
				return err
			}

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "nvim"
			}

			c := exec.Command(editor, task.FilePath)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr

			if err := c.Run(); err != nil {
				return fmt.Errorf("editor exited with error: %w", err)
			}
			return nil
		},
	}
}
