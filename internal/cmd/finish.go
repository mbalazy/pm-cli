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
			"Validity is a TTL, not a pid: a pid written on one machine says nothing on another. A claim is valid " +
			"for " + storage.FinishClaimTTL.String() + " from its last refresh, and the pid and host it records are " +
			"informational - they say which machine to go and look at.\n\n" +
			"Nothing in pm refreshes a claim yet: the acceptance worker that will keep it alive lands with the " +
			"main `pm finish <tracker>` form (pm-cli-100-4). Until then a claim simply lapses after the TTL.",
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

// finishProjectSlug resolves the project the same way the other executor-side
// commands do: --project, else cwd detection.
func finishProjectSlug(cmd *cobra.Command, store storage.TaskStore) (string, error) {
	flag, _ := cmd.Flags().GetString("project")
	var args []string
	if flag != "" {
		args = []string{flag}
	}
	return resolveProjectSlugArg(store, args)
}

// finishTarget resolves the project dir AND validates the tracker id every
// subcommand takes. The id becomes a file name under .executor/, so it is
// checked once, at the entry point, for the read paths too - storage validates
// its own writers, but `pm finish status ../../x` should be refused rather than
// quietly reading somewhere it has no business.
func finishTarget(cmd *cobra.Command, store storage.TaskStore, args []string) (string, string, error) {
	slug, err := finishProjectSlug(cmd, store)
	if err != nil {
		return "", "", err
	}
	tracker := args[0]
	if err := storage.ValidateTaskID(tracker); err != nil {
		return "", "", err
	}
	// A claim on a tracker that does not exist protects nothing while reading
	// as a success - and a mistyped id is the likeliest way two sessions each
	// get a green light on the same actual run. Resolve it, exactly like every
	// other id-taking surface in pm.
	if _, err := store.FindTaskExact(slug, tracker); err != nil {
		return "", "", fmt.Errorf("no task %s in project %s - a claim on a tracker that does not exist "+
			"would protect nothing: %w", tracker, slug, err)
	}
	return store.ProjectDir(slug), tracker, nil
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
			dir, tracker, err := finishTarget(cmd, store, args)
			if err != nil {
				return err
			}
			session, _ := cmd.Flags().GetString("session")
			claim, err := storage.AcquireFinishClaim(dir, tracker, session)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "claimed %s for acceptance (host %s, pid %d)\n",
				claim.TrackerID, claim.Host, claim.PID)
			fmt.Fprintf(cmd.OutOrStdout(), "  claim: %s\n", storage.FinishClaimPath(dir, claim.TrackerID))
			fmt.Fprintf(cmd.OutOrStdout(), "  started: %s\n", claim.Started)
			fmt.Fprintf(cmd.OutOrStdout(), "  release it with `pm finish release %s --started %s`\n",
				claim.TrackerID, claim.Started)
			// Say plainly that nothing refreshes it yet: the claim is honoured
			// for the TTL and then reads as free, and NOTHING in pm currently
			// moves the stamp - the acceptance worker that will is pm finish
			// <tracker> (pm-cli-100-4). Promising a refresher that does not
			// exist would be worse than saying so.
			fmt.Fprintf(cmd.OutOrStdout(), "  valid for %s - nothing refreshes it yet, so a longer acceptance "+
				"can be taken over once it lapses\n", storage.FinishClaimTTL)
			return nil
		},
	}
	cmd.Flags().String("session", "", "CC session or run id to record in the claim (informational)")
	return cmd
}

func newFinishReleaseCmd(store storage.TaskStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release <tracker>",
		Short: "Release an acceptance claim you can identify as yours",
		Long: "Releases the claim on <tracker> - but only one you can NAME, with --started (the stamp " +
			"`pm finish claim` prints) or --session (the id you passed when claiming).\n\n" +
			"What naming it buys, precisely: it stops an ACCIDENTAL release. The claiming process has normally " +
			"exited by the time anyone releases (`pm finish claim` returns immediately), so a release that lifted " +
			"whatever claim it found would let a second session on the same machine - the ordinary case, two CC " +
			"sessions on one mac - delete a live claim it never took and then take the run. It is NOT " +
			"authentication: `pm finish status` prints the stamp, so anyone determined to override a claim can. " +
			"That is deliberate (a wedged acceptance has to be recoverable), and it is why an override is a " +
			"separate, explicit act rather than the default.\n\n" +
			"A claim that has already EXPIRED is cleared without an identifier: it reads as free to everyone " +
			"anyway, so removing it takes nothing from anybody. A live claim you cannot identify is left alone - " +
			"wait it out, since an unrefreshed claim expires on its own.\n\n" +
			"No claim at all is not an error: release is idempotent.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, tracker, err := finishTarget(cmd, store, args)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			current, err := storage.ReadFinishClaim(dir, tracker)
			if err != nil {
				if storage.IsCorruptFinishClaim(err) {
					// Every other surface treats a corrupt claim as free, so
					// release must not be the one command that turns it into a
					// dead end demanding a manual rm.
					fmt.Fprintf(out, "no usable claim on %s - the claim file is unreadable (%v); "+
						"it reads as free and the next claim takes it over\n", tracker, err)
					return nil
				}
				return fmt.Errorf("read finish claim for %s: %w", tracker, err)
			}
			if current == nil {
				fmt.Fprintf(out, "no claim on %s - nothing to release\n", tracker)
				return nil
			}

			started, _ := cmd.Flags().GetString("started")
			session, _ := cmd.Flags().GetString("session")
			expired := current.Expired(time.Now())
			switch {
			case expired:
				// Void already - everything else reports it free, so clearing it
				// takes nothing from anybody, whichever machine wrote it.
			case current.Host != storage.Hostname():
				return fmt.Errorf("claim on %s is held by %s (pid %d), not by this host (%s) - "+
					"release it there, or leave it: an unrefreshed claim expires after %s",
					tracker, current.Host, current.PID, storage.Hostname(), storage.FinishClaimTTL)
			case started != "" && started == current.Started:
			case session != "" && session == current.Session:
			default:
				return fmt.Errorf("claim on %s is held by %s (pid %d), started %s%s - "+
					"pass --started %s (or the --session you claimed with) to release it, "+
					"or leave it: an unrefreshed claim expires after %s",
					tracker, current.Host, current.PID, current.Started,
					sessionSuffix(current.Session), current.Started, storage.FinishClaimTTL)
			}

			// storage's own ownership test (Host+Started) is satisfied by
			// construction here - `current` came off disk - so the check that
			// carries weight is the one above. The call still goes through
			// ReleaseFinishClaim so the re-read there catches a claim that
			// changed hands between our read and the unlink.
			if err := storage.ReleaseFinishClaim(dir, tracker, current); err != nil {
				return err
			}
			note := ""
			if expired && started == "" && session == "" {
				note = " - it had already expired"
			}
			fmt.Fprintf(out, "released the claim on %s (was held by %s, pid %d)%s\n",
				tracker, current.Host, current.PID, note)
			return nil
		},
	}
	cmd.Flags().String("started", "", "the claim's `started` stamp, as printed by `pm finish claim`")
	cmd.Flags().String("session", "", "the session id recorded in the claim (`pm finish claim --session`)")
	return cmd
}

// sessionSuffix renders a claim's session for a message, or nothing when the
// claim carries none.
func sessionSuffix(session string) string {
	if session == "" {
		return ""
	}
	return fmt.Sprintf(", session %s", session)
}

func newFinishStatusCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "status <tracker>",
		Short: "Show who holds the acceptance claim on a run, and since when",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, tracker, err := finishTarget(cmd, store, args)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			claim, err := storage.ReadFinishClaim(dir, tracker)
			if err != nil {
				if storage.IsCorruptFinishClaim(err) {
					// Garbage CONTENT reads as free everywhere else (a truncated
					// file must not wall off a run), so report it as free here
					// too - with the reason, since a bare "free" would hide a
					// real oddity.
					fmt.Fprintf(out, "%s: free (the claim file is unparseable: %v)\n", tracker, err)
					return nil
				}
				// An I/O failure is NOT free: `pm finish claim` refuses on it, so
				// status must not answer "go ahead" where claim will refuse.
				return fmt.Errorf("cannot read the claim for %s: %w", tracker, err)
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
