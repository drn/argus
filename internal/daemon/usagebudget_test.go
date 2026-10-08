package daemon

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
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

// TestDefaultProbe_NoOpUnderTestBinary pins the structural guard: under
// `go test` the daemon's default probe wiring never calls the real probe, so
// a test that builds a Daemon via New (including callers outside this
// package that cannot reach the seams) never spawns `claude` or reads
// ~/.codex.
func TestDefaultProbe_NoOpUnderTestBinary(t *testing.T) {
	testutil.Equal(t, isTestBinary(), true)

	called := false
	probe := defaultProbe(func(context.Context) error {
		called = true
		return nil
	})
	testutil.NoError(t, probe(context.Background()))
	testutil.Equal(t, called, false)
}

// TestNew_DefaultProbesAreInert drives the seams New wires by default and
// confirms they return immediately without error under go test.
func TestNew_DefaultProbesAreInert(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	d := New(database)
	testutil.NoError(t, d.usageBudgetProbe(context.Background()))
	testutil.NoError(t, d.codexProbe(context.Background()))
}

// TestProbePoller_ShutdownCancelsInFlightStartupProbe pins that a startup
// probe still running when the daemon shuts down sees its ctx cancelled and
// the poller goroutine exits.
func TestProbePoller_ShutdownCancelsInFlightStartupProbe(t *testing.T) {
	d, _ := testDaemon(t)
	started := make(chan struct{})
	d.usageBudgetProbe = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}

	stopped := make(chan struct{})
	go func() {
		d.runUsageBudgetPoller()
		close(stopped)
	}()

	<-started
	close(d.done)

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("poller did not exit after shutdown cancelled the startup probe")
	}
}

// TestProbePoller_SkipsStartupProbeAfterShutdown pins that a poller started
// after shutdown never runs its startup probe.
func TestProbePoller_SkipsStartupProbeAfterShutdown(t *testing.T) {
	d, _ := testDaemon(t)
	called := false
	d.codexProbe = func(context.Context) error {
		called = true
		return nil
	}
	close(d.done)

	d.runCodexProbePoller()

	testutil.Equal(t, called, false)
}
