package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/uxlog"
)

// Per-file and total caps for uploads.
const (
	maxAttachmentBytes      int64 = 10 * 1024 * 1024 // 10 MB per file
	maxAttachmentTotalBytes int64 = 50 * 1024 * 1024 // 50 MB per request
	maxAttachmentCount            = 20
)

// errAttachmentTooLarge / errTooManyAttachments are sentinel errors returned
// by parseAttachments so callers can map them to 4xx responses.
var (
	errAttachmentTooLarge   = errors.New("attachment exceeds 10MB cap")
	errAttachmentTotalLarge = errors.New("attachments exceed 50MB total cap")
	errTooManyAttachments   = errors.New("too many attachments")
	errEmptyAttachment      = errors.New("attachment is empty")
	errBadAttachmentName    = errors.New("invalid attachment name")
)

// parseMultipartTaskForm reads a multipart/form-data POST /api/tasks request:
// `name`, `prompt`, `project` text fields plus zero or more `files` parts.
// Caller is responsible for setting the body cap before calling.
func parseMultipartTaskForm(r *http.Request) (name, prompt, project string, atts []agent.Attachment, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", "", "", nil, fmt.Errorf("multipart: %w", err)
	}
	var totalBytes int64
	for {
		part, perr := mr.NextPart()
		if errors.Is(perr, io.EOF) {
			break
		}
		if perr != nil {
			return "", "", "", nil, fmt.Errorf("read part: %w", perr)
		}
		fname := part.FileName()
		formName := part.FormName()
		if fname == "" {
			// text field
			b, rerr := io.ReadAll(io.LimitReader(part, 1<<20))
			part.Close() //nolint:errcheck
			if rerr != nil {
				return "", "", "", nil, fmt.Errorf("read field %s: %w", formName, rerr)
			}
			switch formName {
			case "name":
				name = string(b)
			case "prompt":
				prompt = string(b)
			case "project":
				project = string(b)
			}
			continue
		}
		// file part
		if len(atts) >= maxAttachmentCount {
			part.Close() //nolint:errcheck
			return "", "", "", nil, errTooManyAttachments
		}
		clean, cerr := sanitizeAttachmentName(fname)
		if cerr != nil {
			part.Close() //nolint:errcheck
			return "", "", "", nil, cerr
		}
		// Cap each part read to per-file limit + 1 so we can detect overrun.
		buf, rerr := io.ReadAll(io.LimitReader(part, maxAttachmentBytes+1))
		part.Close() //nolint:errcheck
		if rerr != nil {
			return "", "", "", nil, fmt.Errorf("read file %s: %w", clean, rerr)
		}
		if int64(len(buf)) > maxAttachmentBytes {
			return "", "", "", nil, errAttachmentTooLarge
		}
		if len(buf) == 0 {
			return "", "", "", nil, errEmptyAttachment
		}
		totalBytes += int64(len(buf))
		if totalBytes > maxAttachmentTotalBytes {
			return "", "", "", nil, errAttachmentTotalLarge
		}
		atts = append(atts, agent.Attachment{Name: clean, Data: buf})
	}
	return name, prompt, project, atts, nil
}

// parseUploadOnlyForm reads a multipart upload-only POST (no name/prompt/project
// fields). Used by the mid-session upload endpoint.
func parseUploadOnlyForm(r *http.Request) ([]agent.Attachment, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("multipart: %w", err)
	}
	var atts []agent.Attachment
	var totalBytes int64
	for {
		part, perr := mr.NextPart()
		if errors.Is(perr, io.EOF) {
			break
		}
		if perr != nil {
			return nil, fmt.Errorf("read part: %w", perr)
		}
		if part.FileName() == "" {
			part.Close() //nolint:errcheck
			continue
		}
		if len(atts) >= maxAttachmentCount {
			part.Close() //nolint:errcheck
			return nil, errTooManyAttachments
		}
		clean, cerr := sanitizeAttachmentName(part.FileName())
		if cerr != nil {
			part.Close() //nolint:errcheck
			return nil, cerr
		}
		buf, rerr := io.ReadAll(io.LimitReader(part, maxAttachmentBytes+1))
		part.Close() //nolint:errcheck
		if rerr != nil {
			return nil, fmt.Errorf("read file %s: %w", clean, rerr)
		}
		if int64(len(buf)) > maxAttachmentBytes {
			return nil, errAttachmentTooLarge
		}
		if len(buf) == 0 {
			return nil, errEmptyAttachment
		}
		totalBytes += int64(len(buf))
		if totalBytes > maxAttachmentTotalBytes {
			return nil, errAttachmentTotalLarge
		}
		atts = append(atts, agent.Attachment{Name: clean, Data: buf})
	}
	return atts, nil
}

// sanitizeAttachmentName strips path components, control chars, and any
// characters that would be unsafe in a shell prompt or on disk. Returns
// errBadAttachmentName if nothing usable remains.
//
// Rules:
//   - filepath.Base() removes any directory components a malicious client
//     might have prefixed.
//   - Reject ".", "..", and empty after Base.
//   - Replace whitespace + control + backslash + null with underscore.
//   - Cap at 100 chars (preserving extension when possible).
func sanitizeAttachmentName(raw string) (string, error) {
	// Strip directory parts using BOTH the OS separator and "/" — clients on
	// Windows might send "C:\foo\bar.png" which filepath.Base on Unix leaves
	// intact. Normalize separators first.
	raw = strings.ReplaceAll(raw, "\\", "/")
	name := filepath.Base(raw)
	if name == "" || name == "." || name == ".." || name == "/" {
		return "", errBadAttachmentName
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			b.WriteRune('_')
		case r == '/' || r == '\\' || r == 0:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	clean := strings.TrimSpace(b.String())
	if clean == "" || clean == "." || clean == ".." {
		return "", errBadAttachmentName
	}
	// Cap length while preserving the extension when possible.
	const maxLen = 100
	if len(clean) > maxLen {
		ext := filepath.Ext(clean)
		base := strings.TrimSuffix(clean, ext)
		if len(ext) > 16 {
			ext = ext[:16]
		}
		keep := max(maxLen-len(ext), 1)
		keep = min(keep, len(base))
		clean = base[:keep] + ext
	}
	return clean, nil
}

// handleUploadFiles writes user-uploaded attachments into the task's worktree
// at <worktree>/.context/<name>. Used for mid-session uploads. Filenames that
// already exist in the dir are auto-suffixed with -1, -2, … to avoid clobber.
func (s *Server) handleUploadFiles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.Get(id)
	if err != nil || task == nil || task.Worktree == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task or worktree not found"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentTotalBytes+1<<20) // headroom for multipart envelope
	atts, err := parseUploadOnlyForm(r)
	if err != nil {
		uxlog.Log("[uploads] parse failed task=%s err=%v", id, err)
		writeJSON(w, statusForUploadErr(err), map[string]string{"error": err.Error()})
		return
	}
	if len(atts) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no files provided"})
		return
	}

	// task.Worktree is a fixed path under the daemon's HOME; AttachmentsDir
	// is a constant. The join cannot be tainted by user input.
	dir := filepath.Join(task.Worktree, agent.AttachmentsDir)
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // path constant + worktree
		uxlog.Log("[uploads] mkdir failed task=%s dir=%s err=%v", id, dir, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir: " + err.Error()})
		return
	}

	saved := make([]string, 0, len(atts))
	for _, a := range atts {
		final, ferr := uniquePath(dir, a.Name)
		if ferr != nil {
			uxlog.Log("[uploads] uniquePath failed task=%s name=%q err=%v", id, a.Name, ferr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": ferr.Error()})
			return
		}
		// Names are sanitized by parseUploadOnlyForm (filepath.Base + ASCII filter)
		// and written under the worktree-relative `dir`; `final` cannot escape.
		if werr := os.WriteFile(final, a.Data, 0o600); werr != nil { //nolint:gosec // path validated above
			uxlog.Log("[uploads] write failed task=%s path=%s err=%v", id, final, werr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write: " + werr.Error()})
			return
		}
		saved = append(saved, "./"+agent.AttachmentsDir+"/"+filepath.Base(final))
	}

	uxlog.Log("[uploads] saved task=%s files=%d total_bytes=%d", id, len(atts), totalBytes(atts))
	writeJSON(w, http.StatusOK, map[string]any{"paths": saved})
}

// totalBytes sums attachment payload sizes for logging.
func totalBytes(atts []agent.Attachment) int {
	n := 0
	for _, a := range atts {
		n += len(a.Data)
	}
	return n
}

// uniquePath returns a path under dir that does not already exist, suffixing
// the filename with -1, -2, … before the extension as needed. `name` is
// validated by sanitizeAttachmentName before reaching here, so the joined
// candidate cannot escape `dir`.
func uniquePath(dir, name string) (string, error) {
	candidate := filepath.Join(dir, name)
	if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) { //nolint:gosec // path validated
		return candidate, nil
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 1000; i++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) { //nolint:gosec // path validated
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find unique name for %q", name)
}

// statusForUploadErr maps the sentinel parse errors to HTTP status codes.
func statusForUploadErr(err error) int {
	switch {
	case errors.Is(err, errAttachmentTooLarge),
		errors.Is(err, errAttachmentTotalLarge),
		errors.Is(err, errTooManyAttachments):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, errBadAttachmentName),
		errors.Is(err, errEmptyAttachment):
		return http.StatusBadRequest
	default:
		return http.StatusBadRequest
	}
}

