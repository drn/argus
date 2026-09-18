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
	d.usageBudgetProbe = func(context.Context) error {
		t.Fatal("usage budget probe should not run before the first interval")
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
