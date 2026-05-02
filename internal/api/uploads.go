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
// `name`, `prompt`, `project`, `backend` text fields plus zero or more `files`
// parts. Caller is responsible for setting the body cap before calling.
func parseMultipartTaskForm(r *http.Request) (name, prompt, project, backend string, atts []agent.Attachment, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", "", "", "", nil, fmt.Errorf("multipart: %w", err)
	}
	var totalBytes int64
	for {
		part, perr := mr.NextPart()
		if errors.Is(perr, io.EOF) {
			break
		}
		if perr != nil {
			return "", "", "", "", nil, fmt.Errorf("read part: %w", perr)
		}
		if part.FileName() == "" {
			// Text field — name/prompt/project/backend. Cap at 1MB to bound a
			// malicious client trying to push GBs through a "name" field.
			b, rerr := io.ReadAll(io.LimitReader(part, 1<<20))
			formName := part.FormName()
			part.Close() //nolint:errcheck
			if rerr != nil {
				return "", "", "", "", nil, fmt.Errorf("read field %s: %w", formName, rerr)
			}
			switch formName {
			case "name":
				name = string(b)
			case "prompt":
				prompt = string(b)
			case "project":
				project = string(b)
			case "backend":
				backend = string(b)
			}
			continue
		}
		att, rerr := readFilePart(part, len(atts), &totalBytes)
		if rerr != nil {
			return "", "", "", "", nil, rerr
		}
		atts = append(atts, att)
	}
	return name, prompt, project, backend, atts, nil
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
		att, rerr := readFilePart(part, len(atts), &totalBytes)
		if rerr != nil {
			return nil, rerr
		}
		atts = append(atts, att)
	}
	return atts, nil
}

// readFilePart consumes one multipart file part: enforces the count cap,
// sanitizes the filename, reads up to maxAttachmentBytes+1 (so we detect
// overrun), and updates *totalBytes to enforce the batch cap. The part is
// always closed before return. Caller must hold the existing attachment
// count to enforce maxAttachmentCount across multiple parts.
//
// Errors returned are sentinels (errAttachmentTooLarge, errBadAttachmentName,
// etc.) when the input is invalid; wrapped %w errors when the read itself
// fails — statusForUploadErr maps both correctly.
func readFilePart(part multipartPart, existingCount int, totalBytes *int64) (agent.Attachment, error) {
	defer part.Close() //nolint:errcheck
	if existingCount >= maxAttachmentCount {
		return agent.Attachment{}, errTooManyAttachments
	}
	clean, cerr := sanitizeAttachmentName(part.FileName())
	if cerr != nil {
		return agent.Attachment{}, cerr
	}
	// Cap each part read to per-file limit + 1 so we can detect overrun.
	buf, rerr := io.ReadAll(io.LimitReader(part, maxAttachmentBytes+1))
	if rerr != nil {
		return agent.Attachment{}, fmt.Errorf("read file %s: %w", clean, rerr)
	}
	if int64(len(buf)) > maxAttachmentBytes {
		return agent.Attachment{}, errAttachmentTooLarge
	}
	if len(buf) == 0 {
		return agent.Attachment{}, errEmptyAttachment
	}
	*totalBytes += int64(len(buf))
	if *totalBytes > maxAttachmentTotalBytes {
		return agent.Attachment{}, errAttachmentTotalLarge
	}
	return agent.Attachment{Name: clean, Data: buf}, nil
}

// multipartPart is the subset of *multipart.Part that readFilePart needs —
// declared here so the helper is trivially mockable in unit tests without
// a full multipart Reader.
type multipartPart interface {
	io.Reader
	io.Closer
	FileName() string
}

// sanitizeAttachmentName strips path components, control chars, Unicode
// bidi overrides, and leading dashes that could be misread as CLI flags.
// Returns errBadAttachmentName if nothing usable remains.
//
// Rules:
//   - filepath.Base() removes any directory components a malicious client
//     might have prefixed (after normalizing `\` → `/` for Windows paths).
//   - Reject ".", "..", and empty after Base.
//   - Replace ASCII control chars (<0x20, 0x7f), null, and path separators
//     with underscore.
//   - Replace Unicode bidi override codepoints (LTR/RTL marks, embeddings,
//     isolates, BOM) with underscore — these can render filenames
//     deceptively in a terminal even though they're not separators.
//   - Trim leading dashes so the path can't be misread as a CLI flag if
//     the agent ever passes it as an argument.
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
		case isBidiOverride(r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	clean := strings.TrimSpace(b.String())
	clean = strings.TrimLeft(clean, "-")
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

// isBidiOverride returns true for Unicode codepoints that change visual text
// direction without being visible characters — these can make a filename
// render in the terminal as a name other than what's stored on disk.
func isBidiOverride(r rune) bool {
	switch r {
	case 0x200E, 0x200F, // LTR/RTL marks
		0x202A, 0x202B, 0x202C, 0x202D, 0x202E, // explicit embedding/overrides
		0x2066, 0x2067, 0x2068, 0x2069, // isolates
		0xFEFF: // zero-width no-break space (BOM)
		return true
	}
	return false
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
		// Don't echo the absolute path back to the client — uxlog has it.
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create attachments directory"})
		return
	}

	saved := make([]string, 0, len(atts))
	for _, a := range atts {
		final, ferr := uniquePath(dir, a.Name)
		if ferr != nil {
			uxlog.Log("[uploads] uniquePath failed task=%s name=%q err=%v", id, a.Name, ferr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not allocate filename"})
			return
		}
		// Names are sanitized by parseUploadOnlyForm (filepath.Base + ASCII filter)
		// and written under the worktree-relative `dir`; `final` cannot escape.
		if werr := os.WriteFile(final, a.Data, 0o600); werr != nil { //nolint:gosec // path validated above
			uxlog.Log("[uploads] write failed task=%s path=%s err=%v", id, final, werr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save attachment"})
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

// statusForUploadErr maps parse errors to HTTP status codes. Sentinel
// "input was bad" errors map to 4xx; non-sentinel errors (typically a
// broken connection mid-body or malformed multipart envelope) map to 500
// because they reflect infrastructure failure, not client mistakes.
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
		return http.StatusInternalServerError
	}
}

