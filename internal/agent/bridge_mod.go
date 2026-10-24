package agent

import "github.com/drn/argus/internal/bridgemod"

// ensureBridgeModFn materializes the embedded argus-bridge Claude Code mod and
// returns the directory for Claude's --plugin-dir flag. A package var so tests
// can stub it; the real implementation is isTestBinary()-gated to ("", nil)
// under `go test` (like ensureBuiltinRoutingFn), so it never touches the real
// ~/.argus and BuildCmd's exact-command-string tests are unaffected.
var ensureBridgeModFn = bridgemod.EnsureBridgeMod

// SetEnsureBridgeModForTest overrides the bridge-mod materialization function
// BuildCmd calls. Returns a restore func.
func SetEnsureBridgeModForTest(fn func() (string, error)) func() {
	old := ensureBridgeModFn
	ensureBridgeModFn = fn
	return func() { ensureBridgeModFn = old }
}
