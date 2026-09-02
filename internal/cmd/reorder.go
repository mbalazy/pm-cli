package cmd

import (
	"fmt"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newReorderCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "reorder <parent> <child-id>...",
		Short: "Renumber a parent's subtasks' order field to match the given ID sequence",
		Long: "Sets the order field of the listed subtasks to 10, 20, 30... in the order given,\n" +
			"so the parent rollup (pm context / pm_context) and the board list them in that order.\n" +
			"Children not listed keep their current order. The parent is resolved by its full ID\n" +
			"across all projects (children may use a different prefix than the parent).",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			parentID := args[0]
			wantIDs := args[1:]

			allTasks, err := store.GetAllTasks()
			if err != nil {
				return err
			}

			// Resolve the project that owns the parent (exact ID match).
			slug := ""
			for _, t := range allTasks {
				if t.Meta.ID == parentID {
					slug = t.Project
					break
				}
			}
			if slug == "" {
				return fmt.Errorf("parent task %q not found", parentID)
			}

			// Everything below is a read-modify-write over SEVERAL task files,
			// and WriteTask persists the whole struct - so an unlocked reorder
			// reverts a concurrent (locked) pm_update_task on any of these
			// children wholesale, brief and body included. Take the lock and
			// re-read the children FRESH inside it: the copies from the
			// GetAllTasks scan above predate the critical section.
			if release, lockErr := store.LockProject(slug); lockErr == nil {
				defer release()
			}
			fresh, err := store.GetTasks(slug)
			if err != nil {
				return err
			}

			// Index the parent's actual children by ID.
			children := make(map[string]*storage.Task)
			for _, t := range fresh {
				if t.Meta.Parent == parentID {
					children[t.Meta.ID] = t
				}
			}
			if len(children) == 0 {
				return fmt.Errorf("%q has no subtasks (nothing names it as parent)", parentID)
			}

			// Validate every requested ID is a real child before writing anything.
			var unknown []string
			for _, id := range wantIDs {
				if _, ok := children[id]; !ok {
					unknown = append(unknown, id)
				}
			}
			if len(unknown) > 0 {
				return fmt.Errorf("not subtasks of %s: %s", parentID, strings.Join(unknown, ", "))
			}

			for i, id := range wantIDs {
				t := children[id]
				t.Meta.Order = (i + 1) * 10
				t.Meta.Updated = storage.Now()
				if err := store.WriteTask(t); err != nil {
					return fmt.Errorf("write %s: %w", id, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-13s order=%d\n", id, t.Meta.Order)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Reordered %d subtask(s) of %s\n", len(wantIDs), parentID)
			return nil
		},
	}
}
