package tui

import (
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// capHarness runs a real pump under synctest and records draw times relative
// to the harness start.
type capHarness struct {
	p     *repaintPump
	done  chan struct{}
	start time.Time
	mu    sync.Mutex
	draws []time.Duration
	logs  []string
}

func newCapHarness(diagnostic bool) *capHarness {
	h := &capHarness{done: make(chan struct{}), start: time.Now()}
	h.p = newRepaintPump(h.done, func() {
		h.mu.Lock()
		h.draws = append(h.draws, time.Since(h.start))
		h.mu.Unlock()
	}, diagnostic)
	h.p.log = func(f string, a ...any) {
		h.mu.Lock()
		h.logs = append(h.logs, fmt.Sprintf(f, a...))
		h.mu.Unlock()
	}
	go h.p.run()
	return h
}

func (h *capHarness) stop() {
	close(h.done)
	synctest.Wait()
}

func (h *capHarness) snapshot() []time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]time.Duration(nil), h.draws...)
}

func TestRepaintOutputOnlyFramesCoalesceAtOutputInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCapHarness(false)
		for range 500 { // a wake every millisecond for half a second
			h.p.wake()
			time.Sleep(time.Millisecond)
			synctest.Wait()
		}
		h.stop()
		draws := h.snapshot()
		testutil.Equal(t, draws[0], time.Duration(0)) // leading edge is immediate
		for i := 1; i < len(draws); i++ {
			if gap := draws[i] - draws[i-1]; gap < outputRepaintInterval {
				t.Fatalf("output-only draws %d and %d are %v apart, want >= %v", i-1, i, gap, outputRepaintInterval)
			}
		}
		if len(draws) < 10 || len(draws) > 14 {
			t.Fatalf("got %d draws in 500ms, want about 12 (25fps)", len(draws))
		}
	})
}

func TestRepaintEchoKeepsFastCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCapHarness(false)
		h.p.wake() // draw at 0
		synctest.Wait()
		time.Sleep(time.Millisecond)
		h.p.noteKey()
		h.p.wake() // the echo
		synctest.Wait()
		time.Sleep(repaintInterval)
		synctest.Wait()
		h.stop()
		draws := h.snapshot()
		testutil.DeepEqual(t, draws, []time.Duration{0, repaintInterval})
	})
}

func TestRepaintKeyShortensThrottleInProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCapHarness(false)
		h.p.wake() // draw at 0
		synctest.Wait()
		time.Sleep(time.Millisecond)
		h.p.wake() // output-only: throttled until 40ms
		synctest.Wait()
		time.Sleep(2 * time.Millisecond) // t=3ms, still sleeping out the output interval
		testutil.Equal(t, len(h.snapshot()), 1)
		h.p.noteKey()
		synctest.Wait()
		time.Sleep(repaintInterval) // past 8ms
		synctest.Wait()
		draws := h.snapshot()
		testutil.DeepEqual(t, draws, []time.Duration{0, repaintInterval}) // not 40ms
		h.stop()
	})
}

func TestRepaintKeyAfterEchoIntervalDrawsImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCapHarness(false)
		h.p.wake()
		synctest.Wait()
		time.Sleep(time.Millisecond)
		h.p.wake() // pending, throttled
		time.Sleep(14 * time.Millisecond)
		synctest.Wait()
		h.p.noteKey() // 8ms already elapsed since the last draw: no more waiting
		synctest.Wait()
		draws := h.snapshot()
		testutil.DeepEqual(t, draws, []time.Duration{0, 15 * time.Millisecond})
		h.stop()
	})
}

func TestRepaintKeyWhileIdleDoesNotDraw(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCapHarness(false)
		h.p.wake()
		synctest.Wait()
		time.Sleep(time.Second)
		for range 5 {
			h.p.noteKey()
		}
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		testutil.Equal(t, len(h.snapshot()), 1)
		h.stop()
	})
}

func TestRepaintFirstByteAfterQuietIsImmediate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCapHarness(false)
		h.p.wake() // a prior draw, so the leading edge is decided by the quiet gap, not by "never drew"
		synctest.Wait()
		h.p.noteKey()
		time.Sleep(echoWindow + time.Second) // key window closed, quiet far longer than either interval
		h.p.wake()
		synctest.Wait()
		testutil.DeepEqual(t, h.snapshot(), []time.Duration{0, echoWindow + time.Second})
		h.stop()
	})
}

func TestRepaintSingleWriteDuringThrottlePaintsAtIntervalEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCapHarness(false)
		h.p.wake() // draw at 0
		synctest.Wait()
		time.Sleep(5 * time.Millisecond)
		h.p.wake() // the only write of the burst lands mid-throttle
		synctest.Wait()
		time.Sleep(outputRepaintInterval - 5*time.Millisecond - time.Nanosecond)
		synctest.Wait()
		testutil.Equal(t, len(h.snapshot()), 1) // not early
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		testutil.DeepEqual(t, h.snapshot(), []time.Duration{0, outputRepaintInterval}) // not lost
		h.stop()
	})
}

func TestRepaintEchoWindowBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCapHarness(false)
		h.p.wake() // draw at 0
		synctest.Wait()
		h.p.noteKey()
		time.Sleep(echoWindow - time.Millisecond)
		synctest.Wait()
		testutil.Equal(t, h.p.interval(), repaintInterval) // inside the window
		time.Sleep(2 * time.Millisecond)
		testutil.Equal(t, h.p.interval(), outputRepaintInterval) // outside it
		h.stop()
	})
}

func TestRepaintFinalWriteOfBurstAlwaysPaints(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCapHarness(false)
		for range 30 {
			h.p.wake()
			time.Sleep(time.Millisecond)
			synctest.Wait()
		}
		lastWrite := time.Since(h.start)
		h.p.wake() // the final chunk lands mid-throttle and the producer then stops
		synctest.Wait()
		time.Sleep(outputRepaintInterval)
		synctest.Wait()
		draws := h.snapshot()
		final := draws[len(draws)-1]
		if final < lastWrite || final > lastWrite+outputRepaintInterval {
			t.Fatalf("final draw at %v, want within one output interval after the last write at %v", final, lastWrite)
		}
		// And the pump then goes quiet.
		n := len(draws)
		time.Sleep(time.Second)
		synctest.Wait()
		testutil.Equal(t, len(h.snapshot()), n)
		h.stop()
	})
}

func TestRepaintWriteDuringDrawStillRewakesAtOutputInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		var p *repaintPump
		var mu sync.Mutex
		var times []time.Duration
		start := time.Now()
		p = newRepaintPump(done, func() {
			mu.Lock()
			times = append(times, time.Since(start))
			first := len(times) == 1
			mu.Unlock()
			if first {
				p.wake() // output lands while drawing
			}
		}, false)
		go p.run()
		p.wake()
		synctest.Wait()
		time.Sleep(outputRepaintInterval)
		synctest.Wait()
		mu.Lock()
		testutil.DeepEqual(t, times, []time.Duration{0, outputRepaintInterval})
		mu.Unlock()
		close(done)
		synctest.Wait()
	})
}

func TestRepaintCountersSplitEchoAndOutputFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCapHarness(true)
		h.p.wake() // output frame at 0
		synctest.Wait()
		time.Sleep(time.Second)
		h.p.noteKey()
		h.p.wake() // echo frame
		synctest.Wait()
		time.Sleep(time.Second)
		h.p.wake() // output frame, key window long closed
		synctest.Wait()
		time.Sleep(30 * time.Second)
		synctest.Wait()
		h.stop()
		h.mu.Lock()
		defer h.mu.Unlock()
		testutil.Contains(t, fmt.Sprint(h.logs), "[repaint] wakes=3 frames=3 echo_frames=1 output_frames=2 window_s=30")
	})
}

func TestRepaintNoteKeyNilPumpIsSafe(t *testing.T) {
	var p *repaintPump
	p.noteKey() // handleGlobalKey runs before the pump exists in unit-built Apps
}

func TestApp_HandleGlobalKeyStampsRepaintPump(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, typing := range []bool{false, true} { // independent of ARGUS_TYPING_LOG
		a := New(testDB(t), agent.NewRunner(nil), false)
		a.EnableTypingLog(typing)
		a.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0)) // no pump yet: must not panic
		a.repaint = newRepaintPump(make(chan struct{}), func() {}, false)
		testutil.Equal(t, a.repaint.lastKey.Load(), int64(0))
		a.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
		testutil.Equal(t, a.repaint.inEchoWindow(), true)
		testutil.Equal(t, len(a.repaint.keys), 1)
	}
}

// Wiring: startOutputRepaint must hook every pane that can receive a paste, and
// a paste through the real PasteHandler must open the echo window.
func TestApp_PasteStampsRepaintPump(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := New(testDB(t), agent.NewRunner(nil), false)
	tapp, _, _ := simApp(t)
	a.tapp = tapp
	a.tickDone = make(chan struct{})
	a.startOutputRepaint()
	t.Cleanup(func() { close(a.tickDone); <-a.repaint.stopped; a.repaint.close() })
	for name, pane := range map[string]*terminal.TerminalPane{"tasks": a.agentPane, "coord": a.heraPage.CoordPane(), "agent": a.heraPage.AgentPane()} {
		if pane.OnUserInput == nil {
			t.Fatalf("%s pane is not wired to the repaint pump", name)
		}
		pane.SetSession(newRepaintSession())
		a.repaint.lastKey.Store(0)
		testutil.Equal(t, a.repaint.inEchoWindow(), false)
		pane.PasteHandler()("pasted", func(tview.Primitive) {})
		if !a.repaint.inEchoWindow() {
			t.Fatalf("a paste into the %s pane did not open the echo window", name)
		}
	}
	// A paste with no live session is not input.
	dead := newRepaintSession()
	close(dead.ended)
	a.agentPane.SetSession(dead)
	a.repaint.lastKey.Store(0)
	a.agentPane.PasteHandler()("lost", func(tview.Primitive) {})
	testutil.Equal(t, a.repaint.inEchoWindow(), false)
}

// A click forwarded through the real MouseHandler of a pump-wired pane opens the
// echo window; wheel scrolling does not.
func TestApp_ForwardedClickStampsRepaintPump(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := New(testDB(t), agent.NewRunner(nil), false)
	tapp, _, _ := simApp(t)
	a.tapp = tapp
	a.tickDone = make(chan struct{})
	a.startOutputRepaint()
	t.Cleanup(func() { close(a.tickDone); <-a.repaint.stopped; a.repaint.close() })
	pane := a.agentPane
	pane.SetSession(newRepaintSession())
	pane.SetFocused(true)
	pane.SetRect(0, 0, 80, 24)
	h := pane.MouseHandler()
	a.repaint.lastKey.Store(0)
	h(tview.MouseLeftDown, tcell.NewEventMouse(10, 5, tcell.Button1, tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, a.repaint.inEchoWindow(), true)
	a.repaint.lastKey.Store(0)
	h(tview.MouseScrollUp, tcell.NewEventMouse(10, 5, tcell.WheelUp, tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, a.repaint.inEchoWindow(), false)
}
