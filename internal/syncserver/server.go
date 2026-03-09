package syncserver

import (
	"crypto/subtle"
	"net/http"
	"sync"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// Server is the pm-sync HTTP server that accepts synced data from clients
// and serves task/project data via REST endpoints.
type Server struct {
	store    *storage.Store
	token    string
	mu       sync.RWMutex
	lastSync time.Time
}

func New(store *storage.Store, token string) *Server {
	return &Server{
		store: store,
		token: token,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /sync/status", s.handleSyncStatus)
	mux.HandleFunc("POST /sync/bulk", s.handleSyncBulk)

	mux.HandleFunc("GET /projects", s.handleListProjects)
	mux.HandleFunc("GET /projects/{slug}", s.handleGetProject)
	mux.HandleFunc("PUT /projects/{slug}", s.handlePutProject)

	mux.HandleFunc("GET /tasks/{project}", s.handleListTasks)
	mux.HandleFunc("GET /tasks/{project}/{id}", s.handleGetTask)
	mux.HandleFunc("PUT /tasks/{project}/{id}", s.handlePutTask)
	mux.HandleFunc("DELETE /tasks/{project}/{id}", s.handleDeleteTask)

	return s.authMiddleware(mux)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		token := auth[len(prefix):]

		if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return srv.ListenAndServe()
}
