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
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/clipboard"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/push"
)

// TaskCreator creates a task from name, prompt, project, and todoPath.
// autoName signals the underlying creator to fire async Haiku name-gen
// when name was string-interpolated from prompt rather than user-typed.
type TaskCreator func(name, prompt, project, todoPath string, autoName bool) (*model.Task, error)

// Server is the HTTP REST API server.
type Server struct {
	db         *db.DB
	runner     *agent.Runner
	token      string
	createTask TaskCreator
	httpSrv    *http.Server
	push       *push.Manager
	scheduler  ScheduleRunner   // optional; set by SetScheduler before ListenAndServe
	clipboard  *clipboard.Store // optional; set by SetClipboard before ListenAndServe

	// stopCh is closed by Shutdown to signal background goroutines (idle
	// watcher, push fan-out housekeeping) to terminate. Range over <-stopCh
	// in select-loops to honour shutdown.
	stopCh chan struct{}
}

// New creates a new API server.
func New(database *db.DB, runner *agent.Runner, token string, creator TaskCreator) *Server {
	srv := &Server{
		db:         database,
		runner:     runner,
		token:      token,
		createTask: creator,
		stopCh:     make(chan struct{}),
	}
	// Push manager — best-effort. If VAPID keys can't be loaded/generated we
	// keep going without push (the endpoints just return 503).
	if mgr, err := push.New(database); err == nil {
		srv.push = mgr
		// Start idle watcher in the background.
		go srv.idleWatcher()
	} else {
		log.Printf("api: push disabled: %v", err)
	}
	return srv
}

// ListenAndServe starts the HTTP server on the given port.
// Tries port, then port+1 through port+8 if the port is in use.
// Returns the actual port used.
func (s *Server) ListenAndServe(port int) (int, error) {
	mux := s.routes()

	// Auth middleware skips the dashboard route (GET /) and /vendor/ static
	// assets so the page can load and prompt for the token. All /api/* routes
	// require auth.
	handler := authMiddleware(s.token, s.db, mux, "/",
		"/share",
		"/vendor/",
		"/manifest.webmanifest",
		"/sw.js",
		"/icon-192.png",
		"/icon-512.png",
		"/apple-touch-icon.png",
	)

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

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// ReadTimeout bounds total time the server spends reading a request
		// (headers + body). Important for the upload endpoints (50MB cap):
		// without it, a slowloris client trickling 1 byte/second could pin
		// a goroutine for hours under MaxBytesReader's size cap. 5 minutes
		// is generous for legitimate uploads at 100KB/s; the SPA times out
		// its own fetches at 60s for FormData. ReadTimeout does NOT affect
		// SSE: SSE responses are GETs with no body, so once headers are
		// read the timeout no longer applies to the response phase.
		ReadTimeout: 300 * time.Second,
		IdleTimeout: 120 * time.Second,
		// NOTE: WriteTimeout is intentionally omitted. Setting it would kill
		// long-lived SSE streams (handleStreamOutput) after the timeout.
		// Non-streaming handlers all complete well within ReadHeaderTimeout.
	}
	s.httpSrv = srv
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("api http serve: %v", err)
		}
	}()
	return actualPort, nil
}

// Shutdown gracefully stops the HTTP server and signals background goroutines
// (idle watcher) to exit.
func (s *Server) Shutdown(ctx context.Context) error {
	// Signal goroutines first so they don't race with DB close downstream.
	select {
	case <-s.stopCh:
		// already closed
	default:
		close(s.stopCh)
	}
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// corsMiddleware adds CORS headers for cross-origin requests from mobile browsers.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
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
