package daemon

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestCodexCostSweepFn_NilUnderTest(t *testing.T) {
	testutil.True(t, codexCostSweepFn == nil)
	d := &Daemon{done: make(chan struct{})}
	d.runCodexCostSweeper() // returns immediately rather than blocking
}

func TestRunCodexCostSweeper(t *testing.T) {
	origFn, origInterval := codexCostSweepFn, codexCostSweepInterval
	t.Cleanup(func() { codexCostSweepFn, codexCostSweepInterval = origFn, origInterval })

	var calls, built atomic.Int32
	codexCostSweepInterval = 5 * time.Millisecond
	codexCostSweepFn = func(*Daemon) func() {
		built.Add(1)
		return func() {
			if calls.Add(1) == 2 {
				panic("sweep blew up") // recovered; the loop keeps ticking
			}
		}
	}
	d := &Daemon{done: make(chan struct{})}
	finished := make(chan struct{})
	go func() {
		d.runCodexCostSweeper()
		close(finished)
	}()
	deadline := time.After(5 * time.Second)
	for calls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("sweeper ran %d times, want >= 3", calls.Load())
		case <-time.After(time.Millisecond):
		}
	}
	close(d.done)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("sweeper did not stop on done")
	}
	testutil.Equal(t, built.Load(), int32(1)) // one Sweeper per daemon run: backfill runs once
}
