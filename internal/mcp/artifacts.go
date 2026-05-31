package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
)

// ArtifactStore persists registered-artifact manifest rows. *db.DB satisfies
// it. Kept as an interface so the mcp package doesn't depend on db directly,
// mirroring ClipboardSetter / MessageStore.
type ArtifactStore interface {
	UpsertArtifact(a *model.Artifact) (*model.Artifact, error)
}

// SetArtifactManager wires artifact registration. When set (and task
// management is also wired so cwd/id resolution works), the server exposes the
// artifact_register tool. Must be called before ListenAndServe.
func (s *Server) SetArtifactManager(store ArtifactStore) {
	s.artifacts = store
}

// artifactsEnabled reports whether artifact registration is available. Requires
// both the store and task management (the tool resolves the caller's task by
// id/cwd, which needs the task DB).
func (s *Server) artifactsEnabled() bool {
	return s.artifacts != nil && s.taskMgmtEnabled()
}

// artifactToolDefs are exposed only when SetArtifactManager has been called.
var artifactToolDefs = []Tool{
	{
		Name: "artifact_register",
		Description: `Register a file you produced (an HTML report, PDF, rendered markdown, image, or text) so the user can VIEW it — rendered, not just downloaded — in Argus Web, including on mobile over Tailscale.

Argus copies the file into durable per-task storage at registration time, so you may write it anywhere you can (e.g. /tmp or inside the worktree) and then register that path; the original is not tracked and may be deleted afterward. Re-registering the same filename overwrites the previous copy (last write wins).

The agent process does not know its own task ID, so identify yourself by passing either ` + "`id`" + ` (sub-tasks should use the ` + "`ARGUS_TASK_ID`" + ` env var exported into every worktree) or ` + "`cwd`" + ` (Argus resolves to the task whose worktree the cwd lives under, longest-prefix wins). At least one is required.

Guideline: artifacts render best when SELF-CONTAINED — inline all CSS/JS/images, no external CDN references — because the viewer loads them in a sandboxed frame with no network guarantees. Maximum size is 25 MiB.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path":  map[string]interface{}{"type": "string", "description": "Absolute or relative path to the file you produced (e.g. /tmp/coaching-reports/coaching-2026-05-30.html). Copied into durable storage now."},
				"title": map[string]interface{}{"type": "string", "description": "Optional display title shown in the artifacts list. Defaults to the file's basename."},
				"type":  map[string]interface{}{"type": "string", "description": "Optional artifact type: html, markdown, pdf, image, or text. Inferred from the file extension when omitted."},
				"id":    map[string]interface{}{"type": "string", "description": "Task ID. If omitted, cwd is used to resolve the task."},
				"cwd":   map[string]interface{}{"type": "string", "description": "Working directory inside the task's worktree. Used when id is omitted."},
			},
			"required": []string{"path"},
		},
	},
}

func (s *Server) toolArtifactRegister(id interface{}, args json.RawMessage) *Response {
	if !s.artifactsEnabled() {
		return toolError(id, "artifact registration not configured")
	}

	var p struct {
		Path  string `json:"path"`
		Title string `json:"title"`
		Type  string `json:"type"`
		ID    string `json:"id"`
		Cwd   string `json:"cwd"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if p.Path == "" {
		return toolError(id, "path is required")
	}

	task, err := s.resolveTask(p.ID, p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	// Sanitize the destination basename from the source path. This strips any
	// directory components and rejects degenerate names — the stored filename
	// can never contain a path separator or "..".
	filename, err := model.SanitizeArtifactFilename(p.Path)
	if err != nil {
		return toolError(id, fmt.Sprintf("invalid artifact path %q: %v", p.Path, err))
	}

	// Resolve the artifact type: explicit (validated) or inferred from the ext.
	atype := model.ArtifactType(p.Type)
	if p.Type == "" {
		atype = model.InferArtifactType(filename)
	} else if !model.ValidArtifactType(atype) {
		return toolError(id, fmt.Sprintf("invalid type %q: must be html, markdown, pdf, image, or text", p.Type))
	}

	size, err := s.copyArtifact(task.ID, p.Path, filename)
	if err != nil {
		log.Printf("[mcp] artifact_register copy failed: id=%s path=%s err=%v", task.ID, p.Path, err)
		return toolError(id, fmt.Sprintf("Failed to register artifact: %v", err))
	}

	title := p.Title
	if title == "" {
		title = filename
	}

	stored, err := s.artifacts.UpsertArtifact(&model.Artifact{
		TaskID:   task.ID,
		Name:     title,
		Filename: filename,
		Type:     atype,
		Size:     size,
	})
	if err != nil {
		// Roll back the copied bytes so a manifest failure doesn't leave an
		// unreferenced (and therefore unservable) file on disk.
		os.Remove(filepath.Join(agent.ArtifactsDir(task.ID), filename)) //nolint:errcheck
		log.Printf("[mcp] artifact_register manifest failed: id=%s err=%v", task.ID, err)
		return toolError(id, fmt.Sprintf("Failed to record artifact: %v", err))
	}

	log.Printf("[mcp] artifact_register ok: id=%s file=%s type=%s bytes=%d", task.ID, filename, stored.Type, size)
	return toolResult(id, fmt.Sprintf("Registered artifact %q (%s, %d bytes) for task %s (%s). View it in Argus Web → task → Artifacts.", filename, stored.Type, size, task.ID, task.Name))
}

// copyArtifact copies the source file at srcPath into the task's durable
// artifact dir under the sanitized filename, enforcing the size cap. The copy
// is atomic (temp file in the same dir + rename) so a partial write or a
// concurrent serve never observes a half-written artifact. Returns the bytes
// written.
//
// The size cap is enforced with an io.LimitReader at MaxArtifactBytes+1: if
// the copy reaches that ceiling the source exceeds the cap and we reject,
// removing the temp file. The daemon (not the sandboxed agent) runs this, so
// reading the agent's /tmp or worktree source is permitted.
func (s *Server) copyArtifact(taskID, srcPath, filename string) (int64, error) {
	src, err := os.Open(srcPath) //nolint:gosec // G304: srcPath is an agent-supplied path read by the unsandboxed daemon for a single-user local tool; bytes are copied, never executed.
	if err != nil {
		return 0, fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat source: %w", err)
	}
	if info.IsDir() {
		return 0, fmt.Errorf("source is a directory, not a file")
	}
	if info.Size() > model.MaxArtifactBytes {
		return 0, fmt.Errorf("artifact exceeds %d byte cap (got %d)", model.MaxArtifactBytes, info.Size())
	}

	dir := agent.ArtifactsDir(taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, fmt.Errorf("create artifact dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".artifact-*.tmp")
	if err != nil {
		return 0, fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// LimitReader at cap+1 catches a source that grows past the cap between
	// Stat and copy (e.g. a still-being-written file).
	n, copyErr := io.Copy(tmp, io.LimitReader(src, model.MaxArtifactBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		os.Remove(tmpName) //nolint:errcheck
		return 0, fmt.Errorf("copy: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpName) //nolint:errcheck
		return 0, fmt.Errorf("close temp: %w", closeErr)
	}
	if n > model.MaxArtifactBytes {
		os.Remove(tmpName) //nolint:errcheck
		return 0, fmt.Errorf("artifact exceeds %d byte cap", model.MaxArtifactBytes)
	}

	dest := filepath.Join(dir, filename)
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return 0, fmt.Errorf("rename into place: %w", err)
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		return 0, fmt.Errorf("chmod: %w", err)
	}
	return n, nil
}
