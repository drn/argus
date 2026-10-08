package daemon

import (
	"context"
	"os"
	"strings"
)

// defaultProbe returns the production usage probe that daemon.New wires into
// the usageBudgetProbe / codexProbe seams — or a no-op under `go test`
// (fix-backend-usage-routing).
//
// The pollers probe immediately at startup, so every test that constructs a
// Daemon via New and calls Serve would otherwise spawn the real `claude`
// binary in a PTY and read the real ~/.codex. That includes callers outside
// this package (cmd/argus, internal/daemon/client) that cannot reach the
// unexported seams. Guarding here — the daemon's DEFAULT wiring only — rather
// than inside usagebudget.Probe / backendtier.Probe keeps those packages'
// own unit tests able to drive Probe end to end through their internal
// seams, while a daemon-package test that wants a probe still injects one by
// assigning the seam after New.
func defaultProbe(probe func(context.Context) error) func(context.Context) error {
	if isTestBinary() {
		return func(context.Context) error { return nil }
	}
	return probe
}

// isTestBinary returns true when the current process is a Go test binary.
// Keep in sync with the identical copies in internal/agent/cleanup.go,
// internal/daemon/client/client.go, and internal/api/selfupdate.go.
func isTestBinary() bool {
	return strings.HasSuffix(os.Args[0], ".test") ||
		strings.Contains(os.Args[0], "/_test/")
}
