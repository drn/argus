package daemon

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

// TestProbeCodexOnce_CallsThroughAndSurvivesConfigReflect pins the
// fix-backend-routing-semantics wiring: probeCodexOnce reads the live
// BackendRouting.CodexPTYFallbackEnabled flag and reflects it into
// backendtier (SetCodexPTYFallbackEnabled) before invoking the injected
// probe seam every tick, regardless of the flag's value — the opt-in
// semantics themselves are internal/backendtier's own, thoroughly tested
// concern; this only pins that probeCodexOnce still calls through.
func TestProbeCodexOnce_CallsThroughAndSurvivesConfigReflect(t *testing.T) {
	d, _ := testDaemon(t)
	called := false
	d.codexProbe = func(context.Context) error {
		called = true
		return nil
	}

	d.probeCodexOnce(context.Background())

	testutil.Equal(t, called, true)
}

func TestCodexProbePoller_GoroutineStopsOnShutdown(t *testing.T) {
	d, _ := testDaemon(t)

	stopped := make(chan struct{})
	go func() {
		d.runCodexProbePoller()
		close(stopped)
	}()

	close(d.done)

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("runCodexProbePoller did not return after d.done closed (goroutine stuck)")
	}
}

// TestD_BackendRoutingAbsentCfg is tasks.md 6.2: the daemon must start cleanly
// with no [backend_routing] table configured, mirroring
// TestD_UsageBudgetAbsentCfg's shape. Backend-tier routing being unconfigured
// is the common, fully-inactive default state, and must never block startup.
func TestD_BackendRoutingAbsentCfg(t *testing.T) {
	d, sockPath := testDaemon(t)
	d.codexProbe = func(context.Context) error {
		t.Fatal("codex probe should not run before the first interval")
		return nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Serve(sockPath)
	}()
	t.Cleanup(func() { d.Shutdown() })

	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-errCh:
			t.Fatalf("Serve returned before startup: %v", err)
		case <-deadline:
			t.Fatalf("socket %s did not appear", sockPath)
		case <-tick.C:
			if _, err := os.Stat(sockPath); err == nil {
				return
			}
		}
	}
}
