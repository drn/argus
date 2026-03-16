// Package uxlog provides debug logging for the Argus TUI (UX layer).
// Logs are written to ~/.argus/ux.log, separate from daemon logs,
// to help diagnose issues like tasks failing to start or being
// unexpectedly auto-completed.
package uxlog

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	mu   sync.Mutex
	file *os.File
)

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
}

// Path returns the default UX log path for the given data directory.
func Path(dataDir string) string {
	return dataDir + "/ux.log"
}
