// Package api provides an HTTP REST API for remote control of the Argus daemon.
// It wraps the same runner and DB that the TUI uses, enabling task management
// and agent interaction from mobile devices or scripts over Tailscale.
package api

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// TaskCreator creates a task from name, prompt, project, and todoPath.
type TaskCreator func(name, prompt, project, todoPath string) (*model.Task, error)

// Server is the HTTP REST API server.
type Server struct {
	db         *db.DB
	runner     *agent.Runner
	token      string
	createTask TaskCreator
	httpSrv    *http.Server
}

// New creates a new API server.
func New(database *db.DB, runner *agent.Runner, token string, creator TaskCreator) *Server {
	return &Server{
		db:         database,
		runner:     runner,
		token:      token,
		createTask: creator,
	}
}

// ListenAndServe starts the HTTP server on the given port.
// Tries port, then port+1 through port+8 if the port is in use.
// Returns the actual port used.
func (s *Server) ListenAndServe(port int) (int, error) {
	mux := s.routes()

	// Auth middleware skips the dashboard route (GET /) so the page can load
	// and prompt for the token. All /api/* routes require auth.
	handler := authMiddleware(s.token, mux, "/")

	// Add CORS headers for mobile browser access over Tailscale.
	handler = corsMiddleware(handler)

	var ln net.Listener
	var err error
	actualPort := port
	for i := 0; i < 9; i++ {
		ln, err = net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", actualPort))
		if err == nil {
			break
		}
		actualPort++
	}
	if err != nil {
		return 0, fmt.Errorf("api listen: %w", err)
	}

	srv := &http.Server{Handler: handler}
	s.httpSrv = srv
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("api http serve: %v", err)
		}
	}()
	return actualPort, nil
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// corsMiddleware adds CORS headers for cross-origin requests from mobile browsers.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
