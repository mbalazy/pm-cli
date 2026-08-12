package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// `pm finish record` is the durable half of a HAND-DRIVEN acceptance.
//
// A headless `pm finish <tracker>` leaves <tracker>.finish.json behind, and
// that file is the only thing the ACCEPTANCE column (`pm runs`, board R) ever
// reads once the claim is gone. An acceptance driven by a live session
// (batch-finish-auto) takes the claim but writes no such file - so the moment
// its claim lapsed, the run it accepted read as never accepted at all. This
// command lets that session leave the SAME artifact the headless run would
// have: the verdict, the open visual claims, and optionally the report.
//
// It deliberately writes NO journal lines: `pm executor stats` pairs a run's
// `start` and `end` lines by run_id, and an `end` with no `start` would read
// as a crashed run in the deaths report. A hand acceptance therefore shows up
// in the runs table but not in the stats histogram - a known, documented gap.
func newFinishRecordCmd(store storage.TaskStore) *cobra.Command {
	var (
		result     string
		claimsOpen int
		note       string
		session    string
		reportFrom string
	)
	cmd := &cobra.Command{
		Use:   "record <tracker>",
		Short: "Record a hand-driven acceptance's verdict so the runs table keeps it after the claim lapses",
		Long: "Records the outcome of an acceptance performed BY HAND (a batch-finish-auto session, or a human) " +
			"as the same <tracker>.finish.json a headless `pm finish` run leaves behind. Without it a hand-driven " +
			"acceptance is visible only while its claim lives; once the claim lapses, the ACCEPTANCE column reads " +
			"\"-\" as if the run was never accepted.\n\n" +
			"--result takes the acceptance contract's three words: done (everything accepted), partial (some subs " +
			"accepted, the rest named in the report), blocked (the acceptance looked and could not accept). " +
			"--claims-open carries the visual claims still awaiting a human eye - the number the runs table sums " +
			"into the morning TODO.\n\n" +
			"A LIVE claim held by someone else refuses the write (they are still accepting; pass the --session the " +
			"claim was taken with to identify yourself). Recording does NOT release your claim - release it " +
			"separately with `pm finish release`.\n\n" +
			"Re-running overwrites the previous acceptance state, exactly as re-running `pm finish` does; the one " +
			"thing never overwritten is a headless acceptance that is running right now.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			verdict := strings.TrimSpace(result)
			switch verdict {
			case storage.AcceptCellDone, storage.AcceptCellPartial, storage.AcceptCellBlocked:
			case "":
				return fmt.Errorf("--result is required: done | partial | blocked (the acceptance result contract)")
			default:
				return fmt.Errorf("--result %q is not in the acceptance contract - use done | partial | blocked", result)
			}
			if claimsOpen < 0 {
				return fmt.Errorf("--claims-open %d: a count of open visual claims cannot be negative", claimsOpen)
			}

			// Fuzzy resolution like the main `pm finish` form (prefix/title work),
			// because the resolved id names the state file being written.
			tracker, slug, err := resolveFinishTracker(cmd, store, args[0])
			if err != nil {
				return err
			}
			dir := store.ProjectDir(slug)
			id := tracker.Meta.ID

			// A live claim is someone accepting RIGHT NOW. Recording over their
			// work needs their identity: the same --session the claim was taken
			// with. An expired or corrupt claim reads as free, as everywhere else.
			claim, err := storage.ReadFinishClaim(dir, id)
			if err != nil && !storage.IsCorruptFinishClaim(err) {
				return fmt.Errorf("read finish claim for %s: %w", id, err)
			}
			if claim != nil && !claim.Expired(time.Now()) {
				if session == "" || session != claim.Session {
					return fmt.Errorf("a live acceptance claim on %s is held by %s (pid %d%s) - that acceptance is "+
						"still in flight; record from it with its --session, or wait out the claim (TTL %s)",
						id, claim.Host, claim.PID, sessionSuffix(claim.Session), storage.FinishClaimTTL)
				}
			}

			// A headless acceptance in flight will write its own verdict when it
			// returns; recording over its live run-state would be overwritten by
			// the very next heartbeat anyway. A STALE `running` (dead pid) is the
			// recovery case and records fine.
			if prev, rerr := storage.ReadFinishRunState(dir, id); rerr == nil && prev != nil &&
				prev.Status == storage.RunStatusRunning && prev.IsLive() {
				return fmt.Errorf("a headless acceptance of %s is running right now (pid %d, started %s) - "+
					"it records its own result when it returns", id, prev.PID, prev.Started)
			}

			// The report first: a verdict that points at a report which failed to
			// write would be worse than failing whole. Empty is refused rather than
			// passed through - writeFinishReport treats empty as "remove the old
			// report", and an accidental empty file must not eat a real one.
			if reportFrom != "" {
				body, rerr := os.ReadFile(reportFrom)
				if rerr != nil {
					return fmt.Errorf("read the report to record: %w", rerr)
				}
				if strings.TrimSpace(string(body)) == "" {
					return fmt.Errorf("the report file %s is empty - record without --report, or point it at the real report", reportFrom)
				}
				if werr := writeFinishReport(storage.FinishReportPath(dir, id), string(body)); werr != nil {
					return fmt.Errorf("write the acceptance report: %w", werr)
				}
			}

			now := time.Now().UTC().Format(time.RFC3339)
			st := &storage.RunState{
				TaskID:  id,
				RunID:   storage.NewRunID(),
				Project: slug,
				Kind:    storage.RunKindFinish,
				Status:  storage.RunStatusDone,
				PID:     os.Getpid(),
				Started: now,
				Subs: []storage.SubRun{{
					ID:               id,
					Status:           verdict,
					Note:             strings.TrimSpace(note),
					Session:          session,
					VisualClaimsOpen: claimsOpen,
				}},
			}
			if err := storage.WriteRunState(dir, st); err != nil {
				return fmt.Errorf("write the acceptance run-state: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "recorded the acceptance of %s: %s", id, verdict)
			if claimsOpen > 0 {
				fmt.Fprintf(out, ", %d visual claim(s) open", claimsOpen)
			}
			fmt.Fprintln(out)
			fmt.Fprintf(out, "  state: %s\n", storage.FinishRunPath(dir, id))
			if reportFrom != "" {
				fmt.Fprintf(out, "  report: %s\n", storage.FinishReportPath(dir, id))
			}
			if claim != nil && !claim.Expired(time.Now()) && session == claim.Session {
				fmt.Fprintf(out, "  your claim is still held - release it with `pm finish release %s --session %s`\n",
					id, session)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&result, "result", "", "the acceptance's verdict: done | partial | blocked (required)")
	cmd.Flags().IntVar(&claimsOpen, "claims-open", 0, "visual claims still awaiting a human eye (summed into the runs table)")
	cmd.Flags().StringVar(&note, "note", "", "one-line note recorded on the acceptance (shown beside the verdict in run details)")
	cmd.Flags().StringVar(&session, "session", "", "the session id the claim was taken with - identifies the recorder against a live claim")
	cmd.Flags().StringVar(&reportFrom, "report", "", "path to the acceptance report markdown to save as <tracker>.finish.md (F opens it)")
	return cmd
}
