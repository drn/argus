package tui

import (
	"os"
	"strings"
)

// detectMultiplexer reports whether the TUI is running inside a terminal
// multiplexer (tmux, GNU screen) where tcell's per-cell diff cannot be
// trusted. The multiplexer maintains its own pane backing store that can
// drift from tcell's belief about on-screen state — particularly across
// layout shifts, alt-screen toggles, and bracketed-paste boundaries —
// producing the visible tearing we've been chasing via per-widget branch-
// change callbacks. When detected, the App switches to syncing every frame
// (full repaint) instead of diff-emit, trading the diff optimization for
// guaranteed correctness.
//
// ARGUS_FORCE_SYNC overrides detection:
//   - "1"/"true"/"yes" (case-insensitive) forces sync-every-frame ON
//   - "0"/"false"/"no" forces it OFF
//   - any other value (including unset) falls through to env-based detection
//
// Detection signals (any one is enough):
//   - $TMUX is set (tmux exports this in every spawned shell)
//   - $STY is set (GNU screen exports this)
//   - $TERM begins with "tmux" or "screen" (covers iTerm2 tmux integration
//     and configurations where TMUX/STY were stripped but TERM survived)
func detectMultiplexer() bool {
	switch strings.ToLower(os.Getenv("ARGUS_FORCE_SYNC")) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	}
	if os.Getenv("TMUX") != "" {
		return true
	}
	if os.Getenv("STY") != "" {
		return true
	}
	term := os.Getenv("TERM")
	if strings.HasPrefix(term, "tmux") || strings.HasPrefix(term, "screen") {
		return true
	}
	return false
}
