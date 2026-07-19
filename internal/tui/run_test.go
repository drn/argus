package tui

import (
	"errors"
	"os"
	"testing"

	"github.com/creack/pty"
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

// TestProbeTerminalDev_RealTerminal_Succeeds is the regression test for a
// false-positive bug in the fix above: probeTerminalDev propagated
// tcell.devTty.Close()'s return value directly, but Close() closes a field
// only populated by Start() (never called here), so it always returned
// os.ErrInvalid ("invalid argument") — even when NewDevTtyFromDev's own
// open/IsTerminal/GetState checks all succeeded. That made the TUI refuse to
// start on every real terminal, not just genuinely-absent ones. A pty slave
// is a real tty device (term.IsTerminal is true for it) without requiring
// this test process's own controlling terminal, so this exercises the exact
// code path production hits — unlike TestApp_Run_NoControllingTerminal_
// ReturnsCleanError above, which stubs probeTerminal out entirely.
func TestProbeTerminalDev_RealTerminal_Succeeds(t *testing.T) {
	ptmx, tty, err := pty.Open()
	testutil.NoError(t, err)
	defer ptmx.Close()
	defer tty.Close()

	testutil.NoError(t, probeTerminalDev(tty.Name()))
}

// TestProbeTerminalDev_NotATerminal_Errors guards the other direction: a real
// non-terminal device must still be reported as unusable.
func TestProbeTerminalDev_NotATerminal_Errors(t *testing.T) {
	err := probeTerminalDev(os.DevNull)
	if err == nil {
		t.Fatal("expected an error probing a non-terminal device")
	}
}
