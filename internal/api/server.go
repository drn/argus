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
	"strconv"
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/clipboard"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/mcp"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/push"
	"github.com/drn/argus/internal/tui/settings"
)

// TaskCreator creates a task from name, prompt, project, and optional backend.
// backend overrides the per-project / global default backend for this task; pass
// "" to use the configured default. autoName signals the underlying creator to
// fire async Haiku name-gen when name was string-interpolated from prompt
// rather than user-typed.
type TaskCreator func(name, prompt, project, backend string, autoName bool) (*model.Task, error)

// Server is the HTTP REST API server.
type Server struct {
	db          *db.DB
	runner      *agent.Runner
	token       string
	createTask  TaskCreator
	httpSrv     *http.Server
	push        *push.Manager
	scheduler   ScheduleRunner   // optional; set by SetScheduler before ListenAndServe
	clipboard   *clipboard.Store // optional; set by SetClipboard before ListenAndServe
	mcpRegistry *mcp.Registry    // optional; set by SetMCPRegistry before ListenAndServe

	// eventBus broadcasts model.Events to attached SSE subscribers. Server
	// implements events.Sink (see Emit) so the daemon can wire the global
	// events.SetSink to the same broker that serves /api/events/stream.
	eventBus *eventBus

	// stopCh is closed by Shutdown to signal background goroutines (idle
	// watcher, push fan-out housekeeping) to terminate. Range over <-stopCh
	// in select-loops to honour shutdown.
	stopCh chan struct{}

	// lastResizeCols tracks the most recent cols seen on /resize per task.
	// xterm.js fires /resize on every mount (even when the viewport is the
	// same size), so this debounces redundant kicks that would otherwise
	// destroy any in-flight interactive UI Claude is rendering on reopen.
	// Genuine viewport resizes fall through because cols differs from the
	// cached value.
	lastResizeMu   sync.Mutex
	lastResizeCols map[string]uint16

	// pluginSections is the in-memory mirror of the `plugin_settings` table
	// (PR 7 of the plugin substrate). The DB is the source of truth; this
	// shadow lets the daemon answer "is this plugin still registered?"
	// without round-tripping SQLite on every call. May be nil during early
	// boot before [Server.RehydratePluginSections] runs — handlers check
	// before dereferencing.
	pluginSections *settings.Registry

	// pluginSubmitFn is the test seam for handleSubmitPluginSectionValues.
	// Production sets this to defaultPluginSubmit (a real HTTP POST to the
	// plugin's callback_url); tests override it to assert on what would
	// have been forwarded without spinning up a fake plugin server.
	pluginSubmitFn func(ctx context.Context, callbackURL, authHeader string, body []byte) (int, []byte, error)
}

// New creates a new API server. pushMgr is optional; pass nil to disable
// push notifications entirely (the /api/push/* endpoints will return 503 and
// no idle-watcher goroutine starts). Daemon owns the manager so it can also
// be wired into the scheduler for kick-off pushes — see daemon/daemon.go.
func New(database *db.DB, runner *agent.Runner, token string, creator TaskCreator, pushMgr *push.Manager) *Server {
	srv := &Server{
		db:             database,
		runner:         runner,
		token:          token,
		createTask:     creator,
		push:           pushMgr,
		eventBus:       newEventBus(),
		stopCh:         make(chan struct{}),
		lastResizeCols: make(map[string]uint16),
		pluginSections: settings.NewRegistry(),
	}
	srv.rehydratePluginSections()
	// Start the idle watcher unconditionally. It fires session.idle events
	// for the plugin substrate (PR 2) every tick, and ALSO triggers Web
	// Push notifications when pushMgr is non-nil. Splitting the two
	// responsibilities keeps plugins observing idle transitions even on
	// daemons that opted out of push.
	go srv.idleWatcher()
	if pushMgr == nil {
		log.Printf("api: push disabled (no push manager provided)")
	}
	return srv
}

// ListenAndServe starts the HTTP server on the given port.
// Tries port, then port+1 through port+8 if the port is in use.
// Returns the actual port used.
//
// Binds to 127.0.0.1 plus the Tailscale CGNAT address (when present) — never
// to 0.0.0.0. This keeps the API reachable over Tailscale and via local tools
// (including `tailscale serve` TLS termination forwarding to localhost) while
// refusing connections from untrusted LANs like hotel/cafe WiFi. If Tailscale
// is not running the API still comes up on localhost only; remote PWA access
// is disabled until Tailscale is back.
func (s *Server) ListenAndServe(port int) (int, error) {
	mux := s.routes()

	// Auth middleware skips the dashboard route (GET /) and /vendor/ static
	// assets so the page can load and prompt for the token. All /api/* routes
	// require auth.
	handler := authMiddleware(s.token, s.db, s.push, mux, "/",
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

	// Localhost is required: the daemon must be reachable at least over
	// loopback for `tailscale serve` TLS termination and local tooling.
	// If even loopback fails to bind across the retry window, surface the
	// error — there is nothing useful we can do without a listener.
	localLn, actualPort, err := bindWithRetry("127.0.0.1", port, 9)
	if err != nil {
		return 0, err
	}
	listeners := []net.Listener{localLn}

	// Tailscale binding is best-effort. If detection or bind fails (CLI
	// missing, interface flapping, address briefly unavailable) the API
	// still comes up on localhost; remote PWA access stays paused until
	// the user restarts the daemon with Tailscale up. This avoids the
	// "all-or-nothing" failure mode where a transient Tailscale flap
	// during startup would take the entire API offline.
	if ts := tailscaleIP(); ts != nil {
		tsLn, err := net.Listen("tcp", net.JoinHostPort(ts.String(), strconv.Itoa(actualPort)))
		if err != nil {
			log.Printf("api: tailscale bind %s:%d failed (%v) — continuing on localhost only", ts, actualPort, err)
		} else {
			listeners = append(listeners, tsLn)
			log.Printf("api: bound localhost and tailscale (%s:%d)", ts, actualPort)
		}
	} else {
		log.Printf("api: tailscale not detected; bound localhost only — remote access disabled")
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
	for _, ln := range listeners {
		go func() {
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Printf("api http serve (%s): %v", ln.Addr(), err)
			}
		}()
	}
	return actualPort, nil
}

// bindWithRetry opens a TCP listener on addr, retrying with port+1..port+attempts-1
// when each preceding port is already in use. Returns the bound listener and the
// port it actually used. Wraps the last syscall error with %w so callers can
// `errors.Is(err, syscall.EADDRINUSE)` etc. for diagnostics.
func bindWithRetry(addr string, port, attempts int) (net.Listener, int, error) {
	var lastErr error
	for i := range attempts {
		actual := port + i
		ln, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(actual)))
		if err == nil {
			return ln, actual, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("api listen: failed to bind %s on ports %d-%d: %w", addr, port, port+attempts-1, lastErr)
}

// rehydratePluginSections loads every persisted section into the in-memory
// registry. Called at boot; daemon restarts inherit the prior set
// transparently. Single corrupt rows are logged and skipped so one bad row
// can't take the registry offline — see settings.Registry.Replace for the
// same defensive policy.
func (s *Server) rehydratePluginSections() {
	if s.pluginSections == nil || s.db == nil {
		return
	}
	rows, err := s.db.ListPluginSections()
	if err != nil {
		log.Printf("api: rehydrate plugin sections: %v", err)
		return
	}
	out := make([]settings.Section, 0, len(rows))
	for _, row := range rows {
		sec, perr := row.ToSection()
		if perr != nil {
			log.Printf("api: skip corrupt plugin section scope=%q title=%q: %v", row.Scope, row.Title, perr)
			continue
		}
		out = append(out, sec)
	}
	n := s.pluginSections.Replace(out)
	if n > 0 {
		log.Printf("api: rehydrated %d plugin section(s)", n)
	}
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
