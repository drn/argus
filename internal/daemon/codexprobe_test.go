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
	// Probes now run once at startup (fix-backend-usage-routing); this test only
	// pins that an absent [backend_routing] table never blocks Serve.
	d.codexProbe = func(context.Context) error { return nil }

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

// TestCodexProbePoller_ProbesOnceAtStartup pins fix-backend-usage-routing's
// "Usage probes run at daemon startup" scenario for the Codex probe.
func TestCodexProbePoller_ProbesOnceAtStartup(t *testing.T) {
	d, _ := testDaemon(t)
	called := make(chan struct{}, 1)
	d.codexProbe = func(context.Context) error {
		select {
		case called <- struct{}{}:
		default:
		}
		return nil
	}

	stopped := make(chan struct{})
	go func() {
		d.runCodexProbePoller()
		close(stopped)
	}()
	t.Cleanup(func() {
		close(d.done)
		<-stopped
	})

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("codex probe did not run at poller startup")
	}
}
