package tui

import (
	"sync/atomic"
	"time"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/drn/argus/internal/uxlog"
)

const (
	// repaintInterval is the minimum gap between echo frames: draws within
	// echoWindow after a key event, where latency is what the user feels.
	repaintInterval = 8 * time.Millisecond
	// outputRepaintInterval is the minimum gap between output-only frames
	// (25fps). Nobody reads streamed text faster, and each frame costs a full
	// tview draw, cell diff and terminal flush.
	outputRepaintInterval = 40 * time.Millisecond
	// echoWindow covers a slow echo under load and renews with every key, so a
	// held key stays on the fast cap.
	echoWindow = 150 * time.Millisecond
)

// One goroutine queues at most one synchronous tview draw. Writers never wait
// for that goroutine, the UI mutex, or the event loop.
type repaintPump struct {
	done       <-chan struct{}
	wakes      chan struct{}
	stopped    chan struct{}
	draw       func()
	diagnostic bool
	wakeCount  atomic.Uint64
	epoch      time.Time     // monotonic anchor for lastKey
	lastKey    atomic.Int64  // 1 + monotonic ns since epoch of the latest key/paste, 0 = none
	keys       chan struct{} // size 1: a key arrived, recompute the throttle
	log        func(string, ...any)
	subs       map[*terminal.TerminalPane]*repaintSubscription // UI goroutine only
}

type repaintSubscription struct {
	session  agentview.TerminalAdapter
	source   outputSource
	writer   *repaintWriter
	cancel   chan struct{}
	finished chan struct{}
}

type repaintWriter struct {
	pump   *repaintPump
	active atomic.Bool
}

func (w *repaintWriter) Write(b []byte) (int, error) {
	if len(b) > 0 && w.active.Load() {
		w.pump.wake()
	}
	return len(b), nil
}

func newRepaintPump(done <-chan struct{}, draw func(), diagnostic bool) *repaintPump {
	return &repaintPump{epoch: time.Now(), done: done, wakes: make(chan struct{}, 1), keys: make(chan struct{}, 1), stopped: make(chan struct{}), draw: draw, diagnostic: diagnostic, log: uxlog.Log, subs: make(map[*terminal.TerminalPane]*repaintSubscription)}
}

func (p *repaintPump) wake() {
	select {
	case <-p.done:
		return
	default:
	}
	if p.diagnostic {
		p.wakeCount.Add(1)
	}
	select {
	case p.wakes <- struct{}{}:
	default:
	}
}

// noteKey records user input (key or paste) so the echo that follows paints at
// the fast cap, and cuts short an output-only throttle already in progress.
// It never blocks and is safe on a nil pump (an App built without a pump).
func (p *repaintPump) noteKey() {
	if p == nil {
		return
	}
	p.lastKey.Store(int64(time.Since(p.epoch)) + 1) // monotonic: immune to wall-clock steps and suspend
	select {
	case p.keys <- struct{}{}:
	default:
	}
}

func (p *repaintPump) inEchoWindow() bool {
	last := p.lastKey.Load()
	return last != 0 && time.Since(p.epoch)-time.Duration(last-1) < echoWindow
}

// interval is the minimum gap between draws right now.
func (p *repaintPump) interval() time.Duration {
	if p.inEchoWindow() {
		return repaintInterval
	}
	return outputRepaintInterval
}

func (p *repaintPump) run() {
	defer close(p.stopped)
	defer func() {
		if r := recover(); r != nil {
			p.log("[terminal-output] repaint pump failed: %v", r)
		}
		p.log("[terminal-output] repaint pump stopped")
	}()
	var report <-chan time.Time
	if p.diagnostic {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		report = ticker.C
	}
	var last time.Time
	var frames, echoFrames, outputFrames uint64
	for {
		select {
		case <-p.done:
			return
		case <-report:
			wakes := p.wakeCount.Swap(0)
			if wakes > 0 || frames > 0 {
				p.log("[repaint] wakes=%d frames=%d echo_frames=%d output_frames=%d window_s=30", wakes, frames, echoFrames, outputFrames)
			}
			frames, echoFrames, outputFrames = 0, 0, 0
			continue
		case <-p.wakes:
		}
		// Throttle from the previous draw's completion. A key during the wait
		// loops back to recompute with the (shorter) echo interval.
	throttle:
		for !last.IsZero() {
			delay := p.interval() - time.Since(last)
			if delay <= 0 {
				break
			}
			timer := time.NewTimer(delay)
			select {
			case <-p.done:
				timer.Stop()
				return
			case <-timer.C:
				break throttle
			case <-p.keys:
				timer.Stop()
			}
		}
		// Drain before drawing; a write DURING the draw must survive for the
		// next iteration. The single-slot channel coalesces the whole burst.
		select {
		case <-p.wakes:
		default:
		}
		select {
		case <-p.done:
			return
		default:
		}
		echo := p.inEchoWindow()
		p.draw()
		// Cap from completion, not enqueue time: a UI stall may have held
		// this queued draw for seconds, which must not permit a back-to-back frame.
		last = time.Now()
		frames++
		if echo {
			echoFrames++
		} else {
			outputFrames++
		}
	}
}

type paneFrame struct {
	pane    *terminal.TerminalPane
	session agentview.TerminalAdapter
	fed     uint64
	drawn   bool
}

func terminalFrames(panes ...*terminal.TerminalPane) []paneFrame {
	frames := make([]paneFrame, 0, len(panes))
	for _, pane := range panes {
		sess, fed, drawn := pane.TakePaint()
		frames = append(frames, paneFrame{pane, sess, fed, drawn})
	}
	return frames
}

func (p *repaintPump) reconcile(frames []paneFrame) {
	select {
	case <-p.done:
		p.close()
		return
	default:
	}
	for _, frame := range frames {
		sub := p.subs[frame.pane]
		if sub != nil && (!frame.drawn || sub.session != frame.session) {
			p.unsubscribe(frame.pane)
			sub = nil
		}
		if !frame.drawn || frame.session == nil || sub != nil {
			continue
		}
		source, ok := frame.session.(outputSource)
		if !ok {
			continue
		}
		writer := &repaintWriter{pump: p}
		writer.active.Store(true)
		sub = &repaintSubscription{session: frame.session, source: source, writer: writer, cancel: make(chan struct{}), finished: make(chan struct{})}
		p.subs[frame.pane] = sub
		source.AddWriterFrom(writer, frame.session.TotalWritten())
		go func(sub *repaintSubscription) {
			defer close(sub.finished)
			select {
			case <-sub.source.Done():
				if sub.writer.active.Load() {
					p.wake()
				}
			case <-sub.cancel:
			case <-p.done:
			}
		}(sub)
		p.wake() // cover output between the frame and subscription
	}
}

func (p *repaintPump) unsubscribe(pane *terminal.TerminalPane) {
	sub := p.subs[pane]
	if sub == nil {
		return
	}
	sub.writer.active.Store(false)
	close(sub.cancel)
	sub.source.RemoveWriter(sub.writer)
	delete(p.subs, pane)
}

func (p *repaintPump) close() {
	if p == nil {
		return
	}
	for pane := range p.subs {
		p.unsubscribe(pane)
	}
}

func (a *App) startOutputRepaint() {
	a.repaint = newRepaintPump(a.tickDone, func() { a.tapp.QueueUpdateDraw(func() {}) }, a.typing != nil)
	// Bracketed paste bypasses InputCapture, so the panes report it themselves.
	for _, pane := range []*terminal.TerminalPane{a.agentPane, a.heraPage.CoordPane(), a.heraPage.AgentPane()} {
		pane.OnUserInput = a.repaint.noteKey
	}
	go a.repaint.run()
}

func (a *App) stopLoops() {
	a.tickStopOnce.Do(func() {
		if a.tickDone != nil {
			close(a.tickDone)
		}
	})
}

// stopTUI leaves the event loop alive until its one queued output draw has
// returned. QueueUpdateDraw cannot be cancelled after tview accepts it; stopping
// the loop first would strand the pump at shutdown.
func (a *App) stopTUI() {
	a.quitOnce.Do(func() {
		a.stopLoops()
		if a.repaint == nil {
			a.tapp.Stop()
			return
		}
		go func() { <-a.repaint.stopped; a.tapp.Stop() }()
	})
}
