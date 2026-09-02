package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

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
		Short: "Serve the web cockpit: JSON API + change feed + embedded front end (read-only, localhost)",
		Long: "Starts an HTTP server with the read-only JSON API under /api/ (projects, tasks, context, " +
			"runs, focus), a Server-Sent Events change feed at /api/events, and the React cockpit " +
			"built into this binary (a placeholder page until `make web` has run).\n\n" +
			"No authentication and no TLS: bind to localhost (the default) and reach it remotely " +
			"through Tailscale or an ssh tunnel. Nothing here mutates pm data.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("listen on %s: %w", addr, err)
			}
			srv := &http.Server{
				Handler: server.NewHandler(store, server.Options{
					// The ssh fetch lives in this package (pm runs); the
					// server takes it as a hook so it never imports cmd.
					RemoteRuns: func(ctx context.Context, s storage.TaskStore) ([]storage.RunRow, error) {
						return remoteRunRows(ctx, s, "", "")
					},
				}),
				ReadHeaderTimeout: 10 * time.Second,
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

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
			// Graceful: let in-flight responses finish, then cut the SSE
			// streams, which only end when their request context does.
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
