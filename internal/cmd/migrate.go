package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newMigrateIDsCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate-ids",
		Short: "Migrate task IDs to sequential project-prefixed format (e.g. orbit2-1, atlas-2)",
		// One-shot legacy migration from before project-prefixed IDs existed.
		// Hidden so nobody reaches for it on a modern store by accident.
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := store.ListProjects()
			if err != nil {
				return err
			}

			// This tool predates parent/depends_on. It renumbers IDs WITHOUT
			// rewriting the references pointing at them, so on a store that
			// uses trackers or dependency gates it would silently sever every
			// parent link and depends_on entry. Refuse instead of corrupting.
			for _, slug := range projects {
				tasks, err := store.GetTasks(slug)
				if err != nil {
					continue
				}
				for _, t := range tasks {
					if t.Meta.Parent != "" || len(t.Meta.DependsOn) > 0 {
						return fmt.Errorf("refusing to migrate: %s/%s carries parent/depends_on references, which this legacy tool does not rewrite - renumbering would sever every tracker and dependency gate", slug, t.Meta.ID)
					}
				}
				// Same preflight, same reason: every ID this run would mint is
				// "<prefix>-<n>", so an unsafe `prefix` (hand-edited - this tool
				// predates the validation) is knowable BEFORE the first write.
				// Checked here rather than in the rewrite loop below because
				// this command renumbers project after project: failing mid-run
				// leaves the earlier ones already rewritten, with an error that
				// says nothing about them.
				if err := storage.ValidateProjectPrefix(store.ProjectPrefix(slug)); err != nil {
					return fmt.Errorf("refusing to migrate: %s - %w (fix it in %s)", slug, err, store.ProjectYAML(slug))
				}
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
					// The prefix was validated in the preflight above, so the
					// only variable left here is the numeric suffix - this is
					// the one renumbering path that does NOT go through AddTask
					// (it rewrites existing files via the deliberately lenient
					// WriteTask), and with a hand-edited `prefix: ../x`
					// Filename+Join would land the file outside the project
					// dir, where ReadTasksFromDir never finds it again.
					newID := fmt.Sprintf("%s-%d", prefix, i+1)
					oldPath := t.FilePath

					fmt.Printf("  %s → %s  (%s)\n", t.Meta.ID, newID, t.Meta.Title)

					t.Meta.ID = newID
					t.FilePath = filepath.Join(store.ProjectDir(slug), t.Filename())

					if err := store.WriteTask(t); err != nil {
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
