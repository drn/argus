package tui

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
)

// TestApp_Run_NoControllingTerminal_ReturnsCleanError is the regression test
// for the startup panic: a nil-tty crash inside tcell's EnableMouse path when
// Run() is launched with no controlling terminal available (tview's
// SetScreen silently swallows tcell.Screen.Init()'s error, see the
// probeTerminal doc comment in app.go). Before the fix, Run() proceeded past
// SetScreen straight into EnableMouse and panicked several frames deep inside
// tcell/terminfo. With the fix, Run() must return a clean, wrapped error
// BEFORE ever constructing a real tcell screen — this test overrides
// probeTerminal so it never needs (or opens) a real /dev/tty, keeping it
// runnable in CI.
func TestApp_Run_NoControllingTerminal_ReturnsCleanError(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	sentinel := errors.New("open /dev/tty: device not configured")
	orig := probeTerminal
	probeTerminal = func() error { return sentinel }
	t.Cleanup(func() { probeTerminal = orig })

	err := app.Run()
	if err == nil {
		t.Fatal("expected Run() to return an error when no controlling terminal is available")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected error to wrap the probeTerminal failure, got: %v", err)
	}
	testutil.Nil(t, app.screen)
}
