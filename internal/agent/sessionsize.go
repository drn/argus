package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// SessionSizePath returns the path to the sidecar file recording a task
// session's last PTY size: ~/.argus/sessions/<taskID>.size.
//
// The PTY size is only available while the session is alive (SessionStatus
// RPC); once the process exits the dimensions are gone, but the on-disk
// session log keeps bytes formatted for that width. The sidecar persists
// the size so dead-session preview rendering can re-emulate the log tail
// at the width the bytes were actually formatted for — re-emulating at
// pane width scrambles output (absolute cursor positioning clamps at the
// right edge and autowraps). See gotchas/pty-terminal.md.
func SessionSizePath(taskID string) string {
	return filepath.Join(SessionsDir(), taskID+".size")
}

// SaveSessionSize records the PTY size for a task's session. Best-effort:
// errors are swallowed — the preview falls back to pane dimensions when no
// sidecar exists, matching pre-sidecar behavior.
func SaveSessionSize(taskID string, cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	path := SessionSizePath(taskID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	os.WriteFile(path, fmt.Appendf(nil, "%d %d\n", cols, rows), 0o600) //nolint:errcheck // best-effort
}

// LoadSessionSize reads the persisted PTY size for a task's session.
// Returns ok=false when the sidecar is missing or malformed.
func LoadSessionSize(taskID string) (cols, rows int, ok bool) {
	data, err := os.ReadFile(SessionSizePath(taskID))
	if err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(string(data), "%d %d", &cols, &rows); err != nil {
		return 0, 0, false
	}
	if cols <= 0 || rows <= 0 {
		return 0, 0, false
	}
	return cols, rows, true
}
