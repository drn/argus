package tui

import (
	"os"
	"testing"
)

// TestMain installs no-op stubs for the package-level openers so the tui
// test suite never actually spawns tmux windows or opens a browser. Any
// test that wants to verify an opener was called swaps in its own stub
// inside the test (with t.Cleanup to restore).
//
// Without these defaults, running `go test ./internal/tui/` in any
// developer terminal would shell out to open/tmux/gh whenever an exercised
// path reaches openInFinder / openFile / openTerminal / openURL / openPR.
func TestMain(m *testing.M) {
	browserOpener = func(string) error { return nil }
	systemOpener = func(...string) error { return nil }
	terminalOpener = func(string) error { return nil }
	prOpener = func(string) error { return nil }
	os.Exit(m.Run())
}
