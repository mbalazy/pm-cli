package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// `pm runs` - one row per tracker, every project, this machine and the remote
// runners. The counting rule lives in storage (storage.LocalRunRows) because
// the board renders the same rows; this file is the CLI display plus the one
// thing the board does not do, going out over ssh.

// remoteRunsTimeout bounds ONE remote's whole answer, connection included. A
// var so a test can shrink it. The reason there is a deadline at all: a machine
// that accepts the TCP connection and then stops talking (a VPS mid-suspend)
// would otherwise hang the table forever, which is the failure ConnectTimeout
// alone does not cover.
var remoteRunsTimeout = 20 * time.Second

// remoteConnectTimeout is the ssh-level connect timeout, in seconds. Same value
// the batch-finish discovery step uses, for the same reason: a sleeping runner
// must not block the view of local runs.
const remoteConnectTimeout = "5"

// runsPayload is the --json shape. An OBJECT with a rows key, not a bare array,
// because it is what the board and the remote fetch both parse: a top-level
// array has nowhere to grow a field, and this is the one shape that has to stay
// stable across two pm versions talking to each other over ssh.
type runsPayload struct {
	Rows []storage.RunRow `json:"rows"`
}

func newRunsCmd(store storage.TaskStore) *cobra.Command {
	var (
		asJSON  bool
		local   bool
		project string
		remote  string
	)

	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List executor runs and acceptances (one row per tracker, all projects, local + remote)",
		Long: "Lists every tracker with the state of its run and of its acceptance (odbiór), across all " +
			"projects and - unless --local is given - across the remote runners in the global config " +
			"(`pm config show`).\n\n" +
			"RUN is prepped (nothing has run it) | running N/M | done N/M | failed | stale (marked running, " +
			"but its process is gone). ACCEPTANCE is - | running (host, age) | done | failed | stale, plus the " +
			"number of visual claims still open: a detached acceptance never touches the runtime, so those " +
			"checks are waiting for a human even when the acceptance says done.\n\n" +
			"A remote runner that cannot be reached, or whose pm is too old to know this command, costs ONE " +
			"row with a note - never the local rows and never a non-zero exit.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if local && remote != "" {
				return fmt.Errorf("--local and --remote are contradictory: --local means no remote is contacted at all")
			}

			var projects []string
			if project != "" {
				slug, err := store.ResolveProject(project)
				if err != nil {
					return err
				}
				projects = []string{slug}
			}

			rows, err := storage.LocalRunRows(store, projects)
			if err != nil {
				return err
			}

			if !local {
				remoteRows, err := remoteRunRows(cmd.Context(), store, remote, project)
				if err != nil {
					return err
				}
				rows = append(rows, remoteRows...)
				storage.SortRunRows(rows)
			}

			if asJSON {
				return writeRunsJSON(cmd.OutOrStdout(), rows)
			}
			return writeRunsTable(cmd.OutOrStdout(), rows)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit JSON ({\"rows\": [...]}) instead of a table")
	cmd.Flags().BoolVar(&local, "local", false, "This machine only - do not contact any remote runner")
	cmd.Flags().StringVarP(&project, "project", "p", "", "Only this project")
	cmd.Flags().StringVar(&remote, "remote", "", "Only this remote runner (by name), instead of all of them")

	return cmd
}

// remoteRunRows collects the rows of the configured remote runners.
//
// --remote NAME narrows the REMOTES, not the whole listing: the local rows are
// the thing this screen is anchored on, and dropping them would make the flag a
// silent second --local.
//
// A broken global config is an ERROR here, matching storage.LoadConfig's rule: a
// YAML typo must not read as "you have no remote runners" when the whole point
// of the file is that you do. An unreachable MACHINE is the opposite case and
// yields a note row.
func remoteRunRows(ctx context.Context, store storage.TaskStore, only, project string) ([]storage.RunRow, error) {
	cfg, err := store.LoadConfig()
	if err != nil {
		return nil, err
	}
	remotes := cfg.Remotes
	if only != "" {
		r, ok := cfg.Remote(only)
		if !ok {
			return nil, fmt.Errorf("unknown remote %q - declared remotes: %s (see `pm config show`)",
				only, remoteNames(cfg))
		}
		remotes = []storage.Remote{*r}
	}

	if ctx == nil {
		ctx = context.Background()
	}
	var rows []storage.RunRow
	// Sequentially, one remote at a time: the registry holds a machine or two,
	// each capped by remoteRunsTimeout, and a fan-out would buy a couple of
	// seconds at the price of concurrent ssh output interleaving into the notes.
	for _, r := range remotes {
		fetched, err := fetchRemoteRuns(ctx, r)
		if err != nil {
			rows = append(rows, storage.RunRow{Remote: r.Name, Project: r.Name, Note: err.Error()})
			continue
		}
		for _, row := range fetched {
			// The remote answered with --local, so its rows are its own; the
			// Remote field is stamped HERE rather than trusted from over there,
			// which also keeps a row honest if a future remote ever answers with
			// rows it had relayed from elsewhere.
			row.Remote = r.Name
			// --project is filtered on the returned slug rather than passed
			// through: project resolution is fuzzy and runs against the
			// projects of whichever machine performs it, so asking the remote to
			// resolve the name could quietly answer about a different project.
			if project != "" && row.Project != project {
				continue
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func remoteNames(cfg *storage.PMConfig) string {
	if cfg == nil || len(cfg.Remotes) == 0 {
		return "(none declared)"
	}
	names := make([]string, 0, len(cfg.Remotes))
	for _, r := range cfg.Remotes {
		names = append(names, r.Name)
	}
	return strings.Join(names, ", ")
}

// fetchRemoteRuns asks one remote for its own rows: `pm runs --json --local`.
//
// --local on the far side is not an optimisation, it is what keeps this
// terminating: without it the remote would read ITS config and ssh onwards,
// so two machines listing each other would recurse and a third would be pulled
// in behind pm's back.
//
// Every failure - no route, no such host, a pm too old to have this command, a
// pm that answers with something unparsable - comes back as an error for the
// caller to turn into a note row. `ssh` is resolved from PATH on purpose: it is
// also the seam the tests use.
func fetchRemoteRuns(ctx context.Context, r storage.Remote) ([]storage.RunRow, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteRunsTimeout)
	defer cancel()

	// BatchMode=yes alongside ConnectTimeout: a host that wants a password or a
	// passphrase must fail instead of blocking a listing on a prompt nobody is
	// watching (`pm runs` is also what the board polls).
	args := []string{
		"-o", "ConnectTimeout=" + remoteConnectTimeout,
		"-o", "BatchMode=yes",
		r.SSH, r.PM, "runs", "--json", "--local",
	}
	c := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("unreachable: no answer within %s", remoteRunsTimeout)
		}
		return nil, fmt.Errorf("unreachable: %v%s", err, detail(stderr.String()))
	}

	var payload runsPayload
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		// The likeliest cause by far, and the one worth naming: the remote pm
		// predates this command, so it printed usage or an error rather than
		// JSON. pm versions on the two machines are independent.
		return nil, fmt.Errorf("remote pm cannot answer `runs --json`%s", detail(stdout.String()))
	}
	return payload.Rows, nil
}

// detail appends the first meaningful line of a command's output to a message,
// bounded: a note is one table row, and an ssh failure can be a paragraph.
func detail(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if r := []rune(ln); len(r) > 120 {
			ln = string(r[:120]) + "…"
		}
		return " (" + ln + ")"
	}
	return ""
}

func writeRunsJSON(w io.Writer, rows []storage.RunRow) error {
	// Never a nil slice: the board and any script parse `rows` as a list, and
	// "no runs" must encode as [] rather than null.
	if rows == nil {
		rows = []storage.RunRow{}
	}
	data, err := json.MarshalIndent(runsPayload{Rows: rows}, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

func writeRunsTable(w io.Writer, rows []storage.RunRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "No trackers found.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "PROJECT\tTRACKER\tTITLE\tRUN\tACCEPTANCE\n")
	fmt.Fprintf(tw, "-------\t-------\t-----\t---\t----------\n")
	for _, row := range rows {
		project := row.Project
		if row.Remote != "" {
			// Qualified with the remote's name because the same tracker id can
			// exist on two machines - batch tasks on the VPS get their own ids
			// out of that machine's pm, and a bare id would make the two rows
			// indistinguishable.
			project = row.Remote + "/" + row.Project
		}
		if row.Note != "" {
			// A placeholder row for a machine that did not answer: the note
			// takes the title's place and there is nothing to say about a run.
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", project, "-", row.Note, "-", "-")
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			project, row.Tracker, truncateTitle(row.Title), row.Run.String(), row.Accept.String())
	}
	return tw.Flush()
}

// truncateTitle keeps the table readable when a tracker title is a sentence.
// The full title stays in --json.
func truncateTitle(title string) string {
	const max = 40
	if r := []rune(title); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return title
}
