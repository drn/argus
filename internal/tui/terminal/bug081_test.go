package terminal

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

// BUG-081 — "scrolls up a line then jumps straight back to the bottom,
// repeatedly; I cannot scroll up unless I restart the TUI".
//
// A pane rendering through the REPLAY path (no live session — a finished /
// in-review Hera worker, or a BUG-013 dead handle) never runs renderLive, so
// tp.emu stays nil for the pane's whole lifetime. InAltScreen() used to read
// tp.emu alone, so it reported "not on the alternate screen" for exactly those
// panes — even when the reconstructed content was unambiguously a full-screen
// agent's in-place redraw with zero linear scrollback. Both scroll guards
// (BUG-031's keyboard suppression and BUG-026's wheel forwarding) hang off that
// one predicate, so scroll mode was entered against content whose maxScroll is
// 0, and the next paint clamped the offset straight back to 0 — one notch up,
// snap to the bottom, on every notch, forever.

// altScreenRecording synthesises what a full-screen agent (Claude Code, Codex,
// vim) writes to its session log: enter the alternate screen once, then repaint
// in place with absolute cursor addressing and never emit a line feed. The
// emulator therefore accumulates NO scrollback, and the replay build's
// maxScroll is 0 no matter how many bytes it reads.
func altScreenRecording(frames, rows int) []byte {
	var b strings.Builder
	b.WriteString("\x1b[?1049h\x1b[2J\x1b[H")
	for f := range frames {
		b.WriteString("\x1b[H")
		for r := 1; r <= rows; r++ {
			fmt.Fprintf(&b, "\x1b[%d;1Hframe %d row %d", r, f, r)
		}
	}
	return []byte(b.String())
}

// mainScreenRecording is the same size of log, but line-oriented (real
// scrollback) — the control case that must keep scrolling exactly as before.
func mainScreenRecording(lines int) []byte {
	var b strings.Builder
	for i := range lines {
		fmt.Fprintf(&b, "scrollable line %d\r\n", i)
	}
	return []byte(b.String())
}

// replayPane builds a pane in the state bindPane leaves behind for a task with
// no live session: a task ID whose on-disk log is the only content source, and
// no live emulator. It draws until the async replay rebuild has landed, which
// is the steady state a user is looking at before they reach for the wheel.
func replayPane(t *testing.T, taskID string, log []byte) (*TerminalPane, tcell.Screen) {
	t.Helper()
	setupTaskLog(t, taskID, string(log))

	tp := NewTerminalPane()
	tp.SetTaskID(taskID)
	tp.SetSession(nil)
	tp.SetRect(0, 0, 82, 26)

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	screen.SetSize(82, 26)

	// Draw, let the background rebuild finish, draw again so Draw consumes the
	// pending flag — the same two-frame settle the real UI goes through.
	for range 3 {
		tp.Draw(screen)
		waitReplayBuild(t, tp)
	}
	return tp, screen
}

func waitReplayBuild(t *testing.T, tp *TerminalPane) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tp.mu.Lock()
		building := tp.replayBuilding
		tp.mu.Unlock()
		if !building {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("replay rebuild never finished")
}

func TestTerminalPane_BUG081_AltScreenReplayPaneReportsAltScreen(t *testing.T) {
	tp, _ := replayPane(t, "bug081-alt", altScreenRecording(30, 24))

	// Precondition: this is genuinely the blind state — no live emulator, and a
	// replay emulator that offers nothing to scroll into.
	if tp.emu != nil {
		t.Fatal("replay-path pane should have no live emulator")
	}
	testutil.Equal(t, tp.replayEmuMaxScroll, 0)

	if !tp.InAltScreen() {
		t.Error("InAltScreen() = false for a pane replaying alternate-screen content; " +
			"the guard is blind whenever the pane has no live emulator")
	}
}

func TestTerminalPane_BUG081_ScrollUpDoesNotSnapBackOnAltScreenReplay(t *testing.T) {
	tp, screen := replayPane(t, "bug081-snap", altScreenRecording(30, 24))

	// Three wheel notches, each followed by the redraw that used to clamp the
	// offset back to 0. The offset must never leave the live tail: entering
	// scroll mode at all IS the visible twitch the user reported.
	for notch := 1; notch <= 3; notch++ {
		tp.ScrollUp(mouseScrollStep)
		if got := tp.ScrollOffset(); got != 0 {
			t.Fatalf("notch %d: ScrollUp entered scroll mode (offset=%d) on content with no "+
				"scrollback; the next paint clamps it back to 0 — that round trip is the snap-back", notch, got)
		}
		tp.Draw(screen)
		waitReplayBuild(t, tp)
		testutil.Equal(t, tp.ScrollOffset(), 0)
	}
}

func TestTerminalPane_BUG081_AccelScrollUpSuppressedOnAltScreenReplay(t *testing.T) {
	tp, _ := replayPane(t, "bug081-accel", altScreenRecording(30, 24))

	testutil.Equal(t, tp.AccelScrollUp(), 0)
	testutil.Equal(t, tp.ScrollOffset(), 0)
}

// The control case: a replay pane whose recording IS line-oriented must keep
// browsing its history exactly as before. This is what stops the fix from
// degenerating into "replay panes can't scroll".
func TestTerminalPane_BUG081_MainScreenReplayPaneStillScrolls(t *testing.T) {
	tp, screen := replayPane(t, "bug081-main", mainScreenRecording(400))

	if tp.InAltScreen() {
		t.Fatal("line-oriented recording must not report alternate screen")
	}
	if tp.replayEmuMaxScroll <= 0 {
		t.Fatalf("line-oriented recording should offer scrollback, maxScroll=%d", tp.replayEmuMaxScroll)
	}

	tp.ScrollUp(mouseScrollStep)
	testutil.Equal(t, tp.ScrollOffset(), mouseScrollStep)
	tp.Draw(screen)
	waitReplayBuild(t, tp)
	if tp.ScrollOffset() == 0 {
		t.Error("a main-screen replay pane snapped back to the tail")
	}
}

// A live emulator, when present, stays the authority — the flag recorded from a
// stale replay build must never override the agent leaving the alternate screen.
func TestTerminalPane_BUG081_LiveEmulatorOutranksRecordedReplayFlag(t *testing.T) {
	tp := NewTerminalPane()
	tp.mu.Lock()
	tp.replayEmuAltScreen = true
	tp.mu.Unlock()

	testutil.Equal(t, tp.InAltScreen(), true) // no live emulator: replay flag answers

	// Attach a live emulator that is on the MAIN screen.
	tp.emu = tp.newTrackedEmulator(80, 24)
	_, _ = SafeEmuWrite(tp.emu, []byte("plain output\r\n"))
	testutil.Equal(t, tp.InAltScreen(), false)
}

func TestTerminalPane_BUG081_ResetVTClearsRecordedReplayFlag(t *testing.T) {
	tp, _ := replayPane(t, "bug081-reset", altScreenRecording(30, 24))
	testutil.Equal(t, tp.InAltScreen(), true)

	tp.ResetVT()
	testutil.Equal(t, tp.InAltScreen(), false)
}

func TestTerminalPane_BUG081_NoScrollbackHint(t *testing.T) {
	t.Run("live alt-screen agent points at the agent", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		tp := NewTerminalPane()
		tp.SetSession(&mockAdapter{alive: true, ptyCols: 80, ptyRows: 24})
		tp.emu = tp.newTrackedEmulator(80, 24)
		_, _ = SafeEmuWrite(tp.emu, []byte("\x1b[?1049h\x1b[H"))
		testutil.Contains(t, tp.NoScrollbackHint(), "scroll within the agent")
	})

	t.Run("replayed alt-screen recording says there is no scrollback", func(t *testing.T) {
		tp, _ := replayPane(t, "bug081-hint", altScreenRecording(30, 24))
		hint := tp.NoScrollbackHint()
		if hint == "" {
			t.Fatal("replayed alternate-screen content should surface a hint")
		}
		if strings.Contains(hint, "scroll within the agent") {
			t.Errorf("hint %q tells the user to scroll within an agent that is not running", hint)
		}
	})

	t.Run("scrollable pane has no hint", func(t *testing.T) {
		tp, _ := replayPane(t, "bug081-nohint", mainScreenRecording(400))
		testutil.Equal(t, tp.NoScrollbackHint(), "")
	})
}
