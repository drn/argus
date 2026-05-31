package model

import (
	"errors"
	"mime"
	"path/filepath"
	"strings"
	"time"
)

// ArtifactType classifies a registered session artifact so the Argus Web SPA
// can pick a renderer. The viewers are: html → sandboxed iframe, markdown →
// client-rendered into a srcdoc iframe, pdf → embedded object + download
// fallback, image → <img>, text → <pre>.
type ArtifactType string

const (
	ArtifactHTML     ArtifactType = "html"
	ArtifactMarkdown ArtifactType = "markdown"
	ArtifactPDF      ArtifactType = "pdf"
	ArtifactImage    ArtifactType = "image"
	ArtifactText     ArtifactType = "text"
)

// ValidArtifactType reports whether t is one of the recognized types.
func ValidArtifactType(t ArtifactType) bool {
	switch t {
	case ArtifactHTML, ArtifactMarkdown, ArtifactPDF, ArtifactImage, ArtifactText:
		return true
	}
	return false
}

// MaxArtifactBytes caps a single registered artifact. 25 MiB comfortably
// covers a self-contained HTML report, a multi-page PDF, or a hi-res
// screenshot while bounding disk use under a misbehaving producer. The copy
// step at registration enforces it.
const MaxArtifactBytes = 25 * 1024 * 1024

// Artifact is a file an agent/skill produced and registered for viewing in
// Argus Web. The bytes live at ~/.argus/artifacts/<task-id>/<filename>; this
// struct is the manifest row that scopes serving to the registered set — a
// row MUST exist for a file to be served, so user-supplied names only select
// a registered row and never build a filesystem path directly.
type Artifact struct {
	ID        string       `json:"id"`
	TaskID    string       `json:"task_id"`
	Name      string       `json:"name"`     // display title
	Filename  string       `json:"filename"` // sanitized on-disk basename
	Type      ArtifactType `json:"type"`
	Size      int64        `json:"size"`
	CreatedAt time.Time    `json:"created_at"`
}

// InferArtifactType maps a filename's extension to an ArtifactType, defaulting
// to ArtifactText for anything unrecognized.
func InferArtifactType(name string) ArtifactType {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return ArtifactHTML
	case ".md", ".markdown":
		return ArtifactMarkdown
	case ".pdf":
		return ArtifactPDF
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".bmp", ".ico":
		return ArtifactImage
	default:
		return ArtifactText
	}
}

// ArtifactContentType returns the HTTP Content-Type for serving the artifact.
// Image subtypes are derived from the extension (defaulting to image/png);
// everything else is fixed by type. Served with X-Content-Type-Options:
// nosniff, so the explicit type is authoritative.
func ArtifactContentType(a Artifact) string {
	switch a.Type {
	case ArtifactHTML:
		return "text/html; charset=utf-8"
	case ArtifactMarkdown:
		return "text/markdown; charset=utf-8"
	case ArtifactPDF:
		return "application/pdf"
	case ArtifactImage:
		if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(a.Filename))); ct != "" {
			return ct
		}
		return "image/png"
	default:
		return "text/plain; charset=utf-8"
	}
}

// ErrInvalidArtifactName is returned by SanitizeArtifactFilename when the
// source path has no usable basename (empty, ".", or "..").
var ErrInvalidArtifactName = errors.New("invalid artifact filename")

// SanitizeArtifactFilename reduces an arbitrary source path to a safe on-disk
// basename. filepath.Base strips any directory components (so "../" and
// absolute prefixes are gone); we then reject the degenerate results ("", ".",
// "..") rather than silently coercing them, so the caller can surface a clear
// error. The returned name is guaranteed to contain no path separators and no
// ".." segment — defense the serving path still re-checks.
func SanitizeArtifactFilename(path string) (string, error) {
	base := filepath.Base(strings.TrimSpace(path))
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return "", ErrInvalidArtifactName
	}
	return base, nil
}
