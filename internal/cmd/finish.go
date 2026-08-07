package cmd

import (
	"fmt"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// `pm finish` is the odbiór (acceptance) surface of an executor run.
//
// This file is deliberately only the claim half: `claim` / `release` / `status`
// operate the lock that stops two acceptance sessions from taking the same run
// (see internal/storage/finish_claim.go). The MAIN form - `pm finish <tracker>`,
// which spawns an acceptance worker - is a separate piece of work (pm-cli-100-4)
// and lands as this command's own RunE plus an Args rule; until then the bare
// command prints help and a positional argument is not accepted.

func newFinishCmd(store storage.TaskStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "finish",
		Short: "Odbiór (acceptance) of an executor run - claim it so two sessions never take the same one",
		Long: "Acceptance of an executor run, claimed so two sessions never take the same run.\n\n" +
			"The claim lives next to the run it covers (<project data dir>/.executor/<tracker>.finish.claim), " +
			"never next to whoever is accepting: a run and its acceptance routinely stand on different machines " +
			"(the batch runs on the VPS, the human accepts on the mac), and a lock kept locally by the accepting " +
			"side would have both machines take the same run.\n\n" +
			"Validity is a TTL, not a pid: a pid written on one machine says nothing on another. A claim stays " +
			"valid while the acceptance keeps refreshing it, and expires after " + storage.FinishClaimTTL.String() +
			" without a refresh. The pid and host in the claim are informational - they say where to go look.",
		// Args stays NoArgs until `pm finish <tracker>` (the worker-spawning
		// form) lands in pm-cli-100-4.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().StringP("project", "p", "", "project slug (default: detected from cwd)")
	cmd.AddCommand(
		newFinishClaimCmd(store),
		newFinishReleaseCmd(store),
		newFinishStatusCmd(store),
	)
	return cmd
}

// finishProjectDir resolves the project the same way the other executor-side
// commands do (--project, else cwd detection) and returns its pm DATA dir - the
// directory holding .executor/, where both the run-state and the claim live.
func finishProjectDir(cmd *cobra.Command, store storage.TaskStore) (string, string, error) {
	flag, _ := cmd.Flags().GetString("project")
	var args []string
	if flag != "" {
		args = []string{flag}
	}
	slug, err := resolveProjectSlugArg(store, args)
	if err != nil {
		return "", "", err
	}
	return slug, store.ProjectDir(slug), nil
}

func newFinishClaimCmd(store storage.TaskStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claim <tracker>",
		Short: "Claim a run for acceptance (fails when somebody else holds it)",
		Long: "Claims <tracker> for acceptance. Fails with a non-zero exit when a live claim is already held, " +
			"naming the host, the pid and how long ago it was refreshed, so it is obvious which machine to go " +
			"and look at. A claim nobody has refreshed within the TTL is taken over automatically.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, dir, err := finishProjectDir(cmd, store)
			if err != nil {
				return err
			}
			session, _ := cmd.Flags().GetString("session")
			claim, err := storage.AcquireFinishClaim(dir, args[0], session)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "claimed %s for acceptance (host %s, pid %d)\n",
				claim.TrackerID, claim.Host, claim.PID)
			fmt.Fprintf(cmd.OutOrStdout(), "  claim: %s\n", storage.FinishClaimPath(dir, claim.TrackerID))
			fmt.Fprintf(cmd.OutOrStdout(), "  expires %s from now unless refreshed; release with `pm finish release %s`\n",
				storage.FinishClaimTTL, claim.TrackerID)
			return nil
		},
	}
	cmd.Flags().String("session", "", "CC session or run id to record in the claim (informational)")
	return cmd
}

func newFinishReleaseCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "release <tracker>",
		Short: "Release this host's acceptance claim on a run",
		Long: "Releases the claim on <tracker>, but only when this host holds it. The claiming process has " +
			"normally exited by the time anyone releases (`pm finish claim` returns immediately), so the HOST " +
			"is what identifies the claim here - a claim held by another machine is refused rather than lifted, " +
			"since lifting it would hand that machine's run to a third session.\n\n" +
			"No claim at all is not an error: release is idempotent.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, dir, err := finishProjectDir(cmd, store)
			if err != nil {
				return err
			}
			tracker := args[0]
			current, err := storage.ReadFinishClaim(dir, tracker)
			if err != nil {
				return fmt.Errorf("read finish claim for %s: %w", tracker, err)
			}
			if current == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "no claim on %s - nothing to release\n", tracker)
				return nil
			}
			// The claim read off disk is by definition "the same claim", so the
			// ownership test ReleaseFinishClaim applies (Host+Started) would pass
			// for anyone. The check that actually means something here is the
			// HOST: another machine's claim is not ours to lift, and if it has
			// expired nobody needs it lifted - the next acquire takes it over.
			if current.Host != storage.Hostname() {
				return fmt.Errorf("claim on %s is held by %s (pid %d), not by this host (%s) - "+
					"release it there, or let it expire (%s without a refresh) and it is taken over automatically",
					tracker, current.Host, current.PID, storage.Hostname(), storage.FinishClaimTTL)
			}
			if err := storage.ReleaseFinishClaim(dir, tracker, current); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "released the claim on %s (was held by %s, pid %d)\n",
				tracker, current.Host, current.PID)
			return nil
		},
	}
}

func newFinishStatusCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "status <tracker>",
		Short: "Show who holds the acceptance claim on a run, and since when",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, dir, err := finishProjectDir(cmd, store)
			if err != nil {
				return err
			}
			tracker := args[0]
			out := cmd.OutOrStdout()
			claim, err := storage.ReadFinishClaim(dir, tracker)
			if err != nil {
				// A corrupt claim reads as FREE everywhere else (a truncated file
				// must not wall off a run), so it is reported as free here too -
				// with the reason, since "free" alone would hide a real oddity.
				fmt.Fprintf(out, "%s: free (the claim file is unreadable: %v)\n", tracker, err)
				return nil
			}
			if claim == nil {
				fmt.Fprintf(out, "%s: free (no claim)\n", tracker)
				return nil
			}
			if claim.Expired(time.Now()) {
				fmt.Fprintf(out, "%s: free (expired claim from %s, pid %d - last refreshed %s ago, TTL %s)\n",
					tracker, claim.Host, claim.PID, storage.FinishClaimAge(claim.Refreshed), storage.FinishClaimTTL)
				return nil
			}
			fmt.Fprintf(out, "%s: claimed by %s (pid %d)\n", tracker, claim.Host, claim.PID)
			fmt.Fprintf(out, "  started   %s (%s ago)\n", claim.Started, storage.FinishClaimAge(claim.Started))
			fmt.Fprintf(out, "  refreshed %s (%s ago, expires after %s without a refresh)\n",
				claim.Refreshed, storage.FinishClaimAge(claim.Refreshed), storage.FinishClaimTTL)
			if claim.Session != "" {
				fmt.Fprintf(out, "  session   %s\n", claim.Session)
			}
			return nil
		},
	}
}
