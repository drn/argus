// Package uxlog provides debug logging for the Argus TUI (UX layer).
// Logs are written to ~/.argus/ux.log, separate from daemon logs,
// to help diagnose issues like tasks failing to start or being
// unexpectedly auto-completed.
package uxlog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

var (
	mu   sync.Mutex
	file *os.File
)

// maxSize is the size, in bytes, above which ux.log is rotated down to its
// most recent half. A var (not a const) so tests can shrink it and exercise
// rotation without writing tens of megabytes of real log lines.
var maxSize int64 = 20 * 1024 * 1024 // 20MB

// Init opens the log file for writing. Safe to call multiple times;
// subsequent calls are no-ops if already initialized.
func Init(path string) error {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	file = f
	// ux.log accumulates across many separate process launches (Init opens
	// O_APPEND, never O_TRUNC), so a file that's already oversized from a
	// prior session must be capped right away — not only once this process
	// happens to write enough new lines itself.
	rotateIfOversizedLocked()
	return nil
}

// Close closes the log file.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		file.Close()
		file = nil
	}
}

// Log writes a timestamped line to the UX log file.
// No-op if Init has not been called.
func Log(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return
	}
	ts := time.Now().Format("2006/01/02 15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(file, "%s %s\n", ts, msg)
	rotateIfOversizedLocked()
}

// rotateIfOversizedLocked truncates the log file in place, keeping only its
// most recent maxSize/2 bytes, once it exceeds maxSize. Must be called with
// mu held and file non-nil.
//
// ux.log is a pure human-facing diagnostic sink: nothing in the codebase
// reads it back for byte offsets or programmatic state (grepped every
// reference; see context/knowledge/gotchas/misc.md), so truncating it is
// safe purely from a correctness standpoint.
//
// The daemon and the TUI each open their own independent O_APPEND handle to
// this same path — there is no shared in-process state between them — which
// is why this rotates via IN-PLACE TRUNCATION rather than the more familiar
// rename-and-reopen scheme. An O_APPEND write always targets the file's
// CURRENT end-of-file as tracked by the inode, not a cached offset, so
// truncating the file out from under another process's already-open
// O_APPEND handle is safe: that process's next write simply lands at the
// new, smaller end of file. A rename-based rotation would NOT have that
// property — a process holding an already-open handle to the pre-rotation
// inode (now reachable only via the renamed name) would keep appending to
// it forever, silently defeating the cap for that process until it happens
// to reopen the file.
//
// Best-effort: any error here leaves the file as-is rather than risking log
// loss or a crash in a pure debug-logging path. No cross-process lock
// guards the read-modify-write below, so two processes rotating at the
// exact same moment could race; for a debug log that's an acceptable,
// low-consequence trade-off over the complexity of an advisory file lock.
func rotateIfOversizedLocked() {
	info, err := file.Stat()
	if err != nil || info.Size() <= maxSize {
		return
	}

	keep := maxSize / 2
	readFrom := info.Size() - keep

	// A separate short-lived read/write handle: the long-lived write handle
	// (`file`) is opened O_APPEND, and Go's (*os.File).WriteAt refuses to
	// write to an O_APPEND file.
	rw, err := os.OpenFile(file.Name(), os.O_RDWR, 0600)
	if err != nil {
		return
	}
	defer rw.Close()

	tail := make([]byte, keep)
	n, err := rw.ReadAt(tail, readFrom)
	if err != nil && !errors.Is(err, io.EOF) {
		return
	}
	tail = tail[:n]
	// Align on a line boundary so the kept tail doesn't start mid-line.
	if idx := bytes.IndexByte(tail, '\n'); idx >= 0 {
		tail = tail[idx+1:]
	}

	if _, err := rw.WriteAt(tail, 0); err != nil {
		return
	}
	_ = rw.Truncate(int64(len(tail)))
}

// Path returns the default UX log path for the given data directory.
func Path(dataDir string) string {
	return dataDir + "/ux.log"
}

// Writer returns an io.Writer for the underlying log file, or io.Discard if
// Init has not been called.
//
// **Critical use case:** the TUI process must redirect `slog`'s default handler
// to this writer at startup, otherwise every `slog.Info`/`slog.Error` call
// anywhere in argus's TUI-process code paths (autorename, runner, push,
// orchestration, scheduler, etc.) writes to os.Stderr — which is the live
// terminal. tcell does NOT route through os.Stderr, so those writes land at
// the terminal's current cursor position, leaving stale cells until tcell's
// next emit happens to overwrite them. Visible as torn cells, log fragments
// scattered across the screen, mis-positioned content, etc. — and these
// artifacts are only cleared by `screen.Sync()` (Ctrl+L).
//
// The daemon does this redirect at `cmd/argus/main.go:runDaemon`; the TUI
// must mirror it via `runTUI`. See CLAUDE.md hard rule "no direct stderr
// writes from TUI-process code".
func Writer() io.Writer {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return io.Discard
	}
	return file
}
