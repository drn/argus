package tui

import (
	"io"
	"sync/atomic"
	"time"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
)

// The stream reader only touches atomics. Everything else belongs to the UI
// goroutine, including subscription reconciliation and the after-flush hook.
type outputProbe struct {
	now  func() time.Time
	last atomic.Int64
	keys atomic.Pointer[probeKeys]
}
type probeKeys struct{ keys []*typingKey }

func newOutputProbe(now func() time.Time) *outputProbe { return &outputProbe{now: now} }

func (p *outputProbe) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n := p.now().UnixNano()
	p.last.Store(n)
	if batch := p.keys.Load(); batch != nil {
		for _, key := range batch.keys {
			key.output.CompareAndSwap(0, n)
		}
	}
	return len(b), nil
}

type typingKey struct {
	task, pane               string
	probe                    *outputProbe
	startTotal               uint64
	pressed, handled, queued time.Time
	quiet                    time.Duration
	overlap                  bool
	output                   atomic.Int64
	painted                  bool
	cancelled                bool // pane was hidden/rebound before its echo painted
}
type tickInterval struct{ start, end time.Time }
type typingTracker struct {
	now     func() time.Time
	log     func(string, ...any)
	pending []*typingKey
	ticks   []tickInterval
	subs    map[*terminal.TerminalPane]*typingSubscription
}
type outputSource interface {
	AddWriterFrom(io.Writer, uint64)
	RemoveWriter(io.Writer)
	Done() <-chan struct{}
}
type typingSubscription struct {
	session agentview.TerminalAdapter
	source  outputSource
	probe   *outputProbe
}

func newTypingTracker(enabled bool) *typingTracker {
	if !enabled {
		return nil
	}
	return &typingTracker{now: time.Now, log: uxlog.Log, subs: make(map[*terminal.TerminalPane]*typingSubscription)}
}

func (tr *typingTracker) begin(task, pane string, probe *outputProbe, total uint64, pressed, handled time.Time) *typingKey {
	tr.expire(handled)
	k := &typingKey{task: task, pane: pane, probe: probe, startTotal: total, pressed: pressed, handled: handled}
	if last := probe.last.Load(); last != 0 {
		k.quiet = max(0, pressed.Sub(time.Unix(0, last)))
	}
	for _, old := range tr.pending {
		if old.probe == probe {
			k.overlap = true
		}
	}
	tr.pending = append(tr.pending, k)
	tr.publish(probe)
	return k
}

func (tr *typingTracker) publish(p *outputProbe) {
	batch := &probeKeys{}
	for _, key := range tr.pending {
		if key.probe == p {
			batch.keys = append(batch.keys, key)
		}
	}
	p.keys.Store(batch)
}

func (tr *typingTracker) queued(k *typingKey) {
	if tr != nil && k != nil {
		k.queued = tr.now()
	}
}

func (tr *typingTracker) painted(p *outputProbe, total uint64) {
	for _, key := range tr.pending {
		if key.probe == p && total > key.startTotal && key.output.Load() != 0 {
			key.painted = true
		}
	}
}

func (tr *typingTracker) expire(now time.Time) {
	kept := tr.pending[:0]
	var removed []*outputProbe
	for _, key := range tr.pending {
		output := key.output.Load()
		deadline := key.handled.Add(2 * time.Second)
		if !now.Before(deadline) && (key.cancelled || output == 0 || !time.Unix(0, output).Before(deadline)) {
			tr.log("[typing] task=%s pane=%s quiet_ms=%.3f overlap=%d outcome=no-echo", key.task, key.pane, ms(key.quiet), boolInt(key.overlap))
			removed = append(removed, key.probe)
		} else {
			kept = append(kept, key)
		}
	}
	tr.pending = kept
	for _, p := range removed {
		tr.publish(p)
	}
}

func (tr *typingTracker) flushed() {
	if tr == nil {
		return
	}
	now := tr.now()
	tr.expire(now)
	kept := tr.pending[:0]
	var removed []*outputProbe
	for _, key := range tr.pending {
		if !key.painted {
			kept = append(kept, key)
			continue
		}
		output := time.Unix(0, key.output.Load())
		var stall time.Duration
		for _, tick := range tr.ticks {
			start, end := key.pressed, now
			if tick.start.After(start) {
				start = tick.start
			}
			if tick.end.Before(end) {
				end = tick.end
			}
			if end.After(start) {
				stall += end.Sub(start)
			}
		}
		tr.log("[typing] task=%s pane=%s total_ms=%.3f queue_ms=%.3f arrive_ms=%.3f paint_ms=%.3f stall_ms=%.3f quiet_ms=%.3f overlap=%d", key.task, key.pane, ms(now.Sub(key.pressed)), ms(key.handled.Sub(key.pressed)), ms(max(0, output.Sub(key.queued))), ms(now.Sub(output)), ms(stall), ms(key.quiet), boolInt(key.overlap))
		removed = append(removed, key.probe)
	}
	tr.pending = kept
	for _, p := range removed {
		tr.publish(p)
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (tr *typingTracker) tick(start, end time.Time) {
	tr.ticks = append(tr.ticks, tickInterval{start, end})
	if len(tr.ticks) > 32 {
		tr.ticks = tr.ticks[len(tr.ticks)-32:]
	}
	if end.Sub(start) >= 16*time.Millisecond {
		tr.log("[tick] closure_ms=%.3f", ms(end.Sub(start)))
	}
}

func (tr *typingTracker) forward(tp *terminal.TerminalPane, ev *tcell.EventKey, handled time.Time, label string) func() {
	// A key can re-resolve a dead session before the next Draw. Attach now in
	// that case so its first echo is measured too; afterDraw still owns visibility.
	sub := tr.subscription(tp, tp.Session())
	if sub == nil {
		return func() {}
	}
	k := tr.begin(tp.TaskID(), label, sub.probe, sub.session.TotalWritten(), ev.When(), handled)
	return func() { tr.queued(k) }
}

func (tr *typingTracker) unsubscribe(tp *terminal.TerminalPane) {
	sub := tr.subs[tp]
	if sub == nil {
		return
	}
	sub.source.RemoveWriter(sub.probe)
	for _, key := range tr.pending {
		if key.probe == sub.probe && !key.painted {
			key.cancelled = true
		}
	}
	delete(tr.subs, tp)
}

func (tr *typingTracker) subscription(tp *terminal.TerminalPane, sess agentview.TerminalAdapter) *typingSubscription {
	sub := tr.subs[tp]
	if sub != nil && sub.session != sess {
		tr.unsubscribe(tp)
		sub = nil
	}
	if sub == nil && sess != nil {
		if source, ok := sess.(outputSource); ok {
			sub = &typingSubscription{session: sess, source: source, probe: newOutputProbe(tr.now)}
			tr.subs[tp] = sub
			source.AddWriterFrom(sub.probe, sess.TotalWritten())
		}
	}
	return sub
}

func (tr *typingTracker) reconcile(panes ...*terminal.TerminalPane) {
	for _, tp := range panes {
		sess, total, drawn := tp.TakePaint()
		if !drawn {
			tr.unsubscribe(tp)
			continue
		}
		if sub := tr.subscription(tp, sess); sub != nil {
			tr.painted(sub.probe, total)
		}
	}
}

func (tr *typingTracker) close() {
	if tr == nil {
		return
	}
	for pane := range tr.subs {
		tr.unsubscribe(pane)
	}
}

// EnableTypingLog is called once by the launch entry point, before Run.
// Keeping the environment read there makes the disabled path testable.
func (a *App) EnableTypingLog(enabled bool) {
	a.typing = newTypingTracker(enabled)
	if a.typing != nil {
		a.heraPage.OnForwardKey = func(tp *terminal.TerminalPane, ev *tcell.EventKey, entered time.Time) func() {
			label := "hera-agent"
			if tp == a.heraPage.CoordPane() {
				label = "hera-coord"
			}
			return a.typing.forward(tp, ev, entered, label)
		}
	}
}
