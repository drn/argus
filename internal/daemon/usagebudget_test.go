package daemon

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestUsageBudgetPoller_GoroutineStopsOnShutdown(t *testing.T) {
	d, _ := testDaemon(t)

	stopped := make(chan struct{})
	go func() {
		d.runUsageBudgetPoller()
		close(stopped)
	}()

	close(d.done)

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("runUsageBudgetPoller did not return after d.done closed (goroutine stuck)")
	}
}

func TestD_UsageBudgetAbsentCfg(t *testing.T) {
	d, sockPath := testDaemon(t)
	// Probes now run once at startup (fix-backend-usage-routing); this test only
	// pins that an absent [backend_routing]/usage config never blocks Serve.
	d.usageBudgetProbe = func(context.Context) error { return nil }

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

// TestUsageBudgetPoller_ProbesOnceAtStartup pins fix-backend-usage-routing's
// "Usage probes run at daemon startup" scenario: the first probe is attempted
// immediately, not only after the first 30-minute tick.
func TestUsageBudgetPoller_ProbesOnceAtStartup(t *testing.T) {
	d, _ := testDaemon(t)
	called := make(chan struct{}, 1)
	d.usageBudgetProbe = func(context.Context) error {
		select {
		case called <- struct{}{}:
		default:
		}
		return nil
	}

	stopped := make(chan struct{})
	go func() {
		d.runUsageBudgetPoller()
		close(stopped)
	}()
	t.Cleanup(func() {
		close(d.done)
		<-stopped
	})

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("usage budget probe did not run at poller startup")
	}
}
