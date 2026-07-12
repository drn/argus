package api

import (
	"os"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// TestStaticIndex_CtrlZGuard is the CI-enforced regression guard for the
// web/PWA terminal's Ctrl+Z interception.
//
// A bare Ctrl+Z (0x1a / SIGTSTP) reaching Claude Code's stdin makes its CLI
// background the session into its own supervisor, reparenting it out of
// argus's process tree permanently — orphaning the worker. The SPA terminal
// swallows Ctrl+Z at the xterm.js key layer (via attachCustomKeyEventHandler)
// so the byte never reaches the PTY, mirroring the TUI's guard.
//
// The behavioral test lives in web-tests/tests/terminal.spec.ts, but Playwright
// is NOT wired into the Makefile or CI (`make pre-pr` is Go-only), so removing
// the guard would otherwise be invisible to CI. This test asserts the guard is
// present in the served app shell. Modeled on TestSPAJSReferencesResolve's
// file-read approach.
func TestStaticIndex_CtrlZGuard(t *testing.T) {
	data, err := os.ReadFile("static/index.html")
	testutil.NoError(t, err)

	js := extractInlineScript(string(data))
	if js == "" {
		t.Fatal("expected an inline <script> block in static/index.html")
	}

	// The xterm key-layer intercept (before any byte is emitted to onData).
	testutil.Contains(t, js, "attachCustomKeyEventHandler")
	// The bare-Ctrl+Z predicate.
	testutil.Contains(t, js, "isCtrlZ")
	// The explanatory notice (never a silent dead key).
	testutil.Contains(t, js, "background the agent")
}
