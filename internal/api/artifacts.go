package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// handleListArtifacts returns the registered-artifact metadata for a task.
// Authenticated like every /api/* route (artifact paths are not in the
// auth-skip list). Device tokens are fine — listing is read-only.
func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	arts, err := s.db.Artifacts(id)
	if err != nil {
		uxlog.Log("[api] artifacts list failed: id=%s err=%v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if arts == nil {
		arts = []*model.Artifact{}
	}
	uxlog.Log("[api] artifacts list: id=%s count=%d", id, len(arts))
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": arts})
}

// handleGetArtifact serves the raw bytes of a single registered artifact with
// the correct Content-Type. Security model:
//
//   - The {name} path segment selects a manifest ROW, it never builds a path
//     directly. No row for (task, name) → 404, regardless of what is on disk.
//     This is the scoping allowlist.
//   - Defense in depth: the resolved file path is re-checked to be inside the
//     task's artifact dir (Clean + symlink-resolved prefix check) so a stored
//     filename can never escape even if the sanitizer were bypassed.
//   - X-Frame-Options is overridden to SAMEORIGIN here (corsMiddleware sets a
//     global DENY) so the SPA can embed HTML/PDF artifacts in an iframe; the
//     SPA additionally sandboxes the HTML frame so artifact scripts cannot read
//     the parent's token.
func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")

	art, err := s.db.GetArtifact(id, name)
	if err != nil {
		uxlog.Log("[api] artifact lookup failed: id=%s name=%q err=%v", id, name, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if art == nil {
		// Unregistered name (or path-traversal attempt) — no row, no serve.
		uxlog.Log("[api] artifact 404 (no manifest row): id=%s name=%q", id, name)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
		return
	}

	full, ok := resolveArtifactPath(id, art.Filename)
	if !ok {
		uxlog.Log("[api] artifact path escaped dir, refusing: id=%s file=%q", id, art.Filename)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid artifact path"})
		return
	}

	f, err := os.Open(full) //nolint:gosec // G304: full is rooted at ArtifactsDir(id) and prefix-validated by resolveArtifactPath against a registered manifest row.
	if err != nil {
		// Row exists but bytes are gone (manual deletion / disk loss).
		uxlog.Log("[api] artifact open failed: id=%s file=%q err=%v", id, art.Filename, err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact bytes not found"})
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", model.ArtifactContentType(*art))
	// Allow same-origin framing (override corsMiddleware's global DENY) so the
	// SPA viewer can iframe HTML/PDF. frame-ancestors 'self' is the modern
	// equivalent and is honoured by browsers that ignore X-Frame-Options.
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
	// Artifacts can be regenerated under the same name; never let an
	// intermediary serve a stale copy.
	w.Header().Set("Cache-Control", "no-store")
	uxlog.Log("[api] artifact serve: id=%s file=%q type=%s bytes=%d", id, art.Filename, art.Type, info.Size())
	// ServeContent gives range support (PDF/image viewers seek) and HEAD
	// handling; it respects the Content-Type we set above rather than sniffing.
	http.ServeContent(w, r, art.Filename, info.ModTime(), f)
}

// resolveArtifactPath joins the task's artifact dir with a stored filename and
// verifies the result stays inside that dir, following symlinks. Returns
// (path, true) when safe. filename comes from a manifest row (already
// basename-sanitized at registration), but this re-validates so a future
// change to the write path can't silently open a traversal hole.
func resolveArtifactPath(taskID, filename string) (string, bool) {
	dir := agent.ArtifactsDir(taskID)
	full := filepath.Join(dir, filename)

	// Cheap lexical check first: the cleaned join must live directly under dir.
	cleanDir := filepath.Clean(dir)
	if full != filepath.Join(cleanDir, filepath.Base(filename)) {
		return "", false
	}
	if !strings.HasPrefix(full, cleanDir+string(filepath.Separator)) {
		return "", false
	}

	// Symlink check: resolve the real path and confirm it is still inside the
	// real artifact dir. EvalSymlinks errors if the file doesn't exist yet —
	// that's fine, the caller's os.Open will 404. Only treat a successful
	// resolution that escapes the dir as a hard refusal.
	if realPath, err := filepath.EvalSymlinks(full); err == nil {
		realDir, derr := filepath.EvalSymlinks(cleanDir)
		if derr != nil {
			return "", false
		}
		if realPath != realDir && !strings.HasPrefix(realPath, realDir+string(filepath.Separator)) {
			return "", false
		}
	}
	return full, true
}
