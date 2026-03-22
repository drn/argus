package tui2

import "github.com/gdamore/tcell/v2"

// lazyScreen wraps a tcell.Screen and allows skipping Clear() calls.
// When skipClear is set, the next Clear() becomes a no-op. This avoids
// a full terminal repaint for keystrokes that are simply forwarded to
// the PTY with no UI state change: without Clear, cells retain their
// previous values, widgets redraw identical content, and tcell's
// dirty-tracking in Show() detects nothing changed → zero terminal I/O.
//
// Both skipClear and Clear are called from the tview main goroutine
// (InputCapture → draw), so no synchronization is needed.
type lazyScreen struct {
	tcell.Screen
	skipClear bool
}

func (s *lazyScreen) Clear() {
	if s.skipClear {
		s.skipClear = false
		return
	}
	s.Screen.Clear()
}
