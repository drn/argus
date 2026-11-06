package tui

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestRepaintLeadingEdgeCapAndRewake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		starts := make(chan time.Time, 10)
		var count atomic.Int64
		var p *repaintPump
		p = newRepaintPump(done, func() {
			starts <- time.Now()
			if count.Add(1) == 1 {
				for range 100 {
					p.wake()
				}
			}
		}, false)
		go p.run()
		p.wake()
		synctest.Wait()
		testutil.Equal(t, len(starts), 1) // the first byte after quiet draws immediately
		first := <-starts
		time.Sleep(repaintInterval - time.Nanosecond)
		synctest.Wait()
		testutil.Equal(t, len(starts), 0)
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		testutil.Equal(t, len(starts), 1) // output during Draw survives, burst collapses
		testutil.Equal(t, (<-starts).Sub(first), repaintInterval)
		time.Sleep(time.Second)
		synctest.Wait()
		testutil.Equal(t, len(starts), 0) // no periodic draws while quiet
		close(done)
		synctest.Wait()
		select {
		case <-p.stopped:
		default:
			t.Fatal("pump did not stop")
		}
	})
}

func TestRepaintOneQueuedDrawAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int64
		p := newRepaintPump(done, func() { calls.Add(1); <-release }, false)
		go p.run()
		p.wake()
		synctest.Wait()
		for range 1000 {
			p.wake()
		}
		synctest.Wait()
		testutil.Equal(t, calls.Load(), int64(1))
		close(done)
		close(release)
		synctest.Wait()
		testutil.Equal(t, calls.Load(), int64(1))
	})
}

func TestRepaintStopDuringThrottle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		calls := 0
		p := newRepaintPump(done, func() { calls++ }, false)
		go p.run()
		p.wake()
		synctest.Wait()
		p.wake()
		synctest.Wait()
		close(done)
		synctest.Wait()
		testutil.Equal(t, calls, 1)
		select {
		case <-p.stopped:
		default:
			t.Fatal("throttled pump did not stop")
		}
	})
}

func TestRepaintCapAfterSlowDraw(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		starts := make(chan time.Time, 10)
		var p *repaintPump
		var calls atomic.Int64
		p = newRepaintPump(done, func() {
			starts <- time.Now()
			if calls.Add(1) == 1 {
				p.wake()
				time.Sleep(100 * time.Millisecond)
			}
		}, false)
		go p.run()
		p.wake()
		synctest.Wait()
		first := <-starts
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		testutil.Equal(t, len(starts), 0)
		time.Sleep(repaintInterval)
		synctest.Wait()
		testutil.Equal(t, (<-starts).Sub(first), 100*time.Millisecond+repaintInterval)
		close(done)
		synctest.Wait()
	})
}
