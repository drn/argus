package tui

import (
	"os"
	"strings"
)

// detectMultiplexer reports whether the TUI is running inside a terminal
// multiplexer (tmux, GNU screen) where tcell.Show()'s SGR/cursor-move
// optimization can desync from the multiplexer's pane backing after the
// multiplexer redraws from its own state (status-bar tick, copy-mode
// exit, pane refresh on window return). When detected, the App enables
// `multiplexerMode`, which causes `forceContentSync` to flag a Sync on
// content-only cell updates (preview RefreshOutput, terminal pane PTY
// streaming) — paths that bypass the OnBranchChange contract by design.
// Outside a multiplexer, `forceContentSync` is a no-op and content
// updates flow through tcell.Show()'s diff with no Sync (no flash).
//
// NOTE: this is NOT "Sync every frame." An earlier revision (commit
// `9d0a56c`) did that and caused per-keystroke flashing in tmux. The
// current contract Syncs only when a content-streaming widget actually
// updated cells — coarser than per-frame, finer than per-keystroke.
//
// ARGUS_FORCE_SYNC overrides detection:
//   - "1"/"true"/"yes" (case-insensitive) forces multiplexerMode ON
//   - "0"/"false"/"no" forces it OFF
//   - any other value (including unset) falls through to env-based detection
//
// The env var name predates the current semantics (originally meant
// "force Sync every frame"); kept for compatibility with anything users
// have scripted.
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
