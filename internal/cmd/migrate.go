package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newMigrateIDsCmd(store *storage.Store) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate-ids",
		Short: "Migrate task IDs to sequential project-prefixed format (e.g. orbit2-1, atlas-2)",
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := store.ListProjects()
			if err != nil {
				return err
			}

			for _, slug := range projects {
				prefix := store.ProjectPrefix(slug)
				tasks, err := store.GetTasks(slug)
				if err != nil {
					fmt.Printf("skip %s: %v\n", slug, err)
					continue
				}

				// Sort by created date, then old ID for stable ordering
				sort.Slice(tasks, func(i, j int) bool {
					if tasks[i].Meta.Created == tasks[j].Meta.Created {
						return tasks[i].Meta.ID < tasks[j].Meta.ID
					}
					return tasks[i].Meta.Created < tasks[j].Meta.Created
				})

				fmt.Printf("\n%s (prefix: %s, %d tasks):\n", slug, prefix, len(tasks))

				for i, t := range tasks {
					newID := fmt.Sprintf("%s-%d", prefix, i+1)
					oldPath := t.FilePath

					fmt.Printf("  %s → %s  (%s)\n", t.Meta.ID, newID, t.Meta.Title)

					t.Meta.ID = newID
					t.FilePath = filepath.Join(store.ProjectDir(slug), t.Filename())

					if err := storage.WriteTask(t); err != nil {
						return fmt.Errorf("write %s: %w", newID, err)
					}

					if oldPath != t.FilePath {
						os.Remove(oldPath)
					}
				}
			}

			fmt.Println("\nDone.")
			return nil
		},
	}
}
