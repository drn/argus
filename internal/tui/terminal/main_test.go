package terminal

import (
	"os"
	"testing"
)

// TestMain installs a no-op stub for openPRFn so any test that exercises a
// code path reaching OpenPR doesn't actually spawn `open`. Tests that want
// to assert the opener fired swap in their own stub via t.Cleanup.
func TestMain(m *testing.M) {
	openPRFn = func(string) error { return nil }
	os.Exit(m.Run())
}
