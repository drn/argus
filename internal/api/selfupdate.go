package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
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

// spawnSuccessorDaemon starts a fresh `argus daemon` process detached from
// this one. The new daemon's startup kills the existing daemon via the PID
// file and rebinds the Unix socket.
func spawnSuccessorDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// exe comes from os.Executable() — i.e. the path the daemon itself was
	// started with, not user-supplied input.
	cmd := exec.Command(exe, "daemon", "start") //nolint:gosec // exe is os.Executable()
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	// Detach the child so we don't wait/reap it.
	return cmd.Process.Release()
}
