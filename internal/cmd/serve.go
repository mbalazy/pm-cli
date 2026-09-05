package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/mbalazy/pm/internal/feed"
	"github.com/mbalazy/pm/internal/server"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// `pm serve` - the cockpit's HTTP face: JSON over the same service functions
// the MCP server uses, an SSE change feed, and the embedded React bundle.
// Read-only and unauthenticated by design; it binds to localhost and remote
// access is Tailscale's job.

const defaultServeAddr = "127.0.0.1:7070"

func newServeCmd(store storage.TaskStore) *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the web cockpit: JSON API + change feed + embedded front end (localhost)",
		Long: "Starts an HTTP server with the JSON API under /api/ (projects, groups, tasks, " +
			"context, runs, focus, attention, changes), a Server-Sent Events feed at /api/events, the " +
			"change feed's scheduler (git/gh in the projects' checkouts, every cockpit.refresh.every " +
			"inside cockpit.refresh.window only - see `pm config show`), and the React cockpit built " +
			"into this binary (a placeholder page until `make web` has run).\n\n" +
			"No authentication and no TLS: bind to localhost (the default) and reach it remotely " +
			"through Tailscale or an ssh tunnel. The API WRITES too - task status, focus, notes, " +
			"the cockpit settings - and its run control starts and stops executor runs (every POST " +
			"needs the X-PM-Client header the cockpit sends); the change feed's cache lives under " +
			"<pm data dir>/.cockpit/.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("listen on %s: %w", addr, err)
			}
			handler := server.NewHandler(store, server.Options{
				// The ssh fetch lives in this package (pm runs); the
				// server takes it as a hook so it never imports cmd.
				RemoteRuns: func(ctx context.Context, s storage.TaskStore) ([]storage.RunRow, error) {
					return remoteRunRows(ctx, s, "", "")
				},
				// git/gh run through the executor's process-group runner,
				// so a timeout kills a pager or credential helper too.
				Feed: feed.New(store.RootDir(), feed.Sources(store.RootDir(), feedRunner)),
			})
			srv := &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: 10 * time.Second,
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			srv.RegisterOnShutdown(handler.StopStreams)
			go handler.RunScheduler(ctx)

			errCh := make(chan error, 1)
			go func() { errCh <- srv.Serve(ln) }()
			fmt.Fprintf(cmd.OutOrStdout(), "pm serve listening on http://%s\n", ln.Addr())

			select {
			case err := <-errCh:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			case <-ctx.Done():
			}
			// Graceful: let in-flight responses finish. Shutdown never
			// cancels a request's context, and an SSE stream is an active
			// connection until its handler returns - so the streams are cut
			// explicitly (StopStreams, registered below) and Shutdown then
			// completes at once instead of waiting out the 5 s with a tab open.
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				_ = srv.Close()
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&addr, "addr", defaultServeAddr, "Address to listen on")
	return cmd
}

// feedRunner runs one of the change feed's external commands (git, gh)
// through groupCmd: its own process group, SIGTERM-then-SIGKILL on the whole
// group when the context ends, a bounded Wait. feed.CommandTimeout caps the
// call; the env strips pagers and prompts the way feed.DefaultRunner does.
func feedRunner(ctx context.Context, dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, feed.CommandTimeout)
	defer cancel()
	c := groupCmd(ctx, name, args...)
	c.Dir = dir
	c.Env = append(c.Environ(), "GH_PAGER=cat", "PAGER=cat", "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return stdout.String(), fmt.Errorf("%s timed out after %s", name, feed.CommandTimeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), fmt.Errorf("%s: %w%s", name, err, detail(stderr.String()))
		}
		return stdout.String(), fmt.Errorf("%s: %w", name, err)
	}
	return stdout.String(), nil
}
