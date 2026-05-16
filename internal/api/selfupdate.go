package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/drn/argus/internal/selfupdate"
)

// spawnDelay is the gap between flushing the /api/update success response
// and exec'ing the successor daemon. The successor's startup will SIGTERM
// us via the PID file, so the response must reach the client first.
const spawnDelay = 500 * time.Millisecond

// handleGetSourcePath returns the configured Argus source path.
func (s *Server) handleGetSourcePath(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	cfg := s.db.Config()
	writeJSON(w, http.StatusOK, map[string]string{"path": cfg.Argus.SourcePath})
}

type sourcePathReq struct {
	Path string `json:"path"`
}

// handleSetSourcePath persists the Argus source path. Master-token-only.
//
// We accept any directory path here without further validation: the master
// token already grants the holder full control over a process that runs
// arbitrary code (agent commands, go install, etc.), so additional path
// allow-listing would not strengthen the trust model.
func (s *Server) handleSetSourcePath(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	var req sourcePathReq
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	path := strings.TrimSpace(req.Path)
	if err := s.db.SetConfigValue("argus.source_path", path); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

// handleUpdateSelf runs `go install ./...` against the configured source path
// and, on success, spawns a successor daemon to replace this one. The
// successor's startup will SIGTERM the current daemon via the PID file, so
// this endpoint's HTTP response must be flushed before the spawn completes.
//
// No request body is read — all parameters come from the server's own config
// (the master-only `argus.source_path` setting).
func (s *Server) handleUpdateSelf(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	cfg := s.db.Config()
	output, err := selfupdate.Run(cfg.Argus.SourcePath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"output":  output,
			"error":   err.Error(),
			"restart": false,
		})
		return
	}

	// Write the success response before spawning the successor — once the
	// new daemon kills us, the connection is gone.
	writeJSON(w, http.StatusOK, map[string]any{
		"output":  output,
		"restart": true,
	})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Brief delay so the response reaches the client before our process dies.
	go func() {
		time.Sleep(spawnDelay)
		if err := spawnSuccessorDaemon(); err != nil {
			// If spawn fails the running daemon stays alive — the user can
			// still operate the old binary. The detached daemon discards
			// stderr, so use slog (which writes to ~/.argus/daemon.log).
			slog.Error("[api] update: spawn successor failed", "err", err)
		}
	}()
}

// errSpawnFromTestBinary is returned when spawnSuccessorDaemon is invoked
// from a *.test binary. The fork target would be the test binary itself,
// and Go's test framework treats "daemon start" as positional args and
// re-runs every test in the package — a fork bomb.
var errSpawnFromTestBinary = errors.New("spawnSuccessorDaemon refused: running under a Go test binary")

// isTestBinary mirrors the same check in internal/daemon/client. Go's test
// framework compiles binaries with a .test suffix or under a _test/ path.
// Keep in sync with internal/agent/cleanup.go and internal/daemon/client/client.go.
func isTestBinary() bool {
	return strings.HasSuffix(os.Args[0], ".test") ||
		strings.Contains(os.Args[0], "/_test/")
}

// spawnSuccessorDaemon starts a fresh `argus daemon` process detached from
// this one. The new daemon's startup kills the existing daemon via the PID
// file and rebinds the Unix socket.
//
// The body is delegated to spawnSuccessorDaemonFork (in spawn_fork.go) so
// the test-binary backstop is the only branch exercised under `go test`.
// spawnSuccessorDaemonFork is excluded from the coverage gate because
// exercising it would re-create the exact fork bomb errSpawnFromTestBinary
// exists to prevent.
func spawnSuccessorDaemon() error {
	if isTestBinary() {
		return errSpawnFromTestBinary
	}
	return spawnSuccessorDaemonFork()
}
