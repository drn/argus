package agent

import "github.com/drn/argus/internal/routing"

// ensureBuiltinRoutingFn materializes argus's builtin hera/argus-task routing
// content and returns the path to the concatenated system-prompt file, for
// Claude Code's --append-system-prompt-file flag. A package var (rather than
// a direct routing.EnsureBuiltinRouting call) so tests can stub it — mirrors
// ensurePrelaunchFn (prelaunch.go) and autoRenameFn (autorename.go).
//
// The real implementation is isTestBinary()-gated (see routing.go), so it
// always returns ("", nil) under `go test` — necessary so the dozens of
// TestBuildCmd_* cases asserting exact command strings don't break, but it
// also means BuildCmd's routing-flag injection can't be observed by calling
// the real function from a test. SetEnsureBuiltinRoutingForTest is the seam.
var ensureBuiltinRoutingFn = routing.EnsureBuiltinRouting

// SetEnsureBuiltinRoutingForTest overrides the routing materialization
// function BuildCmd calls. Returns a restore func.
func SetEnsureBuiltinRoutingForTest(fn func() (string, error)) func() {
	old := ensureBuiltinRoutingFn
	ensureBuiltinRoutingFn = fn
	return func() { ensureBuiltinRoutingFn = old }
}
