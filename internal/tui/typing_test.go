package tui

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/gdamore/tcell/v2"
)

func TestTypingParts(t *testing.T) {
	t.Parallel()
	base := time.Unix(100, 0)
	now := base
	var lines []string
	tr := newTypingTracker(true)
	tr.now = func() time.Time { return now }
	tr.log = func(f string, args ...any) { lines = append(lines, fmt.Sprintf(f, args...)) }
	p := newOutputProbe(tr.now)
	p.last.Store(base.Add(-100 * time.Millisecond).UnixNano())
	k := tr.begin("task", "tasks", p, 10, base, base.Add(20*time.Millisecond))
	now = base.Add(25 * time.Millisecond)
	tr.queued(k)
	tr.tick(base.Add(-10*time.Millisecond), base.Add(20*time.Millisecond))
	now = base.Add(30 * time.Millisecond)
	_, err := p.Write([]byte("private output"))
	testutil.NoError(t, err)
	tr.painted(p, 11)
	now = base.Add(40 * time.Millisecond)
	tr.flushed()
	testutil.Contains(t, lines[1], "total_ms=40.000 queue_ms=20.000 arrive_ms=5.000 paint_ms=10.000 stall_ms=20.000 quiet_ms=100.000 overlap=0")
	testutil.Equal(t, strings.Contains(strings.Join(lines, ""), "private"), false)
}

// Writes and draws run on the UI goroutine in these integration tests. The
// separate concurrent-writer test below covers the actual atomic probe seam.
type typingSession struct {
	altScreenAdapter
	writers map[io.Writer]bool
	offset  uint64
}

func (s *typingSession) AddWriterFrom(w io.Writer, total uint64) {
	if s.writers == nil {
		s.writers = make(map[io.Writer]bool)
	}
	s.writers[w] = true
	s.offset = total
}
func (s *typingSession) RemoveWriter(w io.Writer) { delete(s.writers, w) }
func (s *typingSession) Done() <-chan struct{}    { return nil }
func (s *typingSession) WriteInput(b []byte, _ agentview.InputOrigin) (int, error) {
	s.out = append(s.out, b...)
	for w := range s.writers {
		_, _ = w.Write(b)
	}
	return len(b), nil
}

func TestTypingSubscriptions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tr := newTypingTracker(true)
	pane := terminal.NewTerminalPane()
	pane.SetRect(0, 0, 80, 24)
	pane.SetTaskID("typing-test")
	first := &typingSession{altScreenAdapter: altScreenAdapter{out: []byte("first")}}
	pane.SetSession(first)
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(80, 24)
	pane.Draw(sim)
	tr.reconcile(pane)
	testutil.Equal(t, len(first.writers), 1)
	testutil.Equal(t, first.offset, uint64(5))
	queued := tr.forward(pane, tcell.NewEventKey(tcell.KeyRune, 'x', 0), time.Now(), "tasks")
	_, err := first.WriteInput([]byte("x"), agentview.OriginUser)
	testutil.NoError(t, err)
	queued()
	// A frame with no Draw of this pane unsubscribes it.
	tr.reconcile(pane)
	testutil.Equal(t, len(first.writers), 0)
	second := &typingSession{}
	pane.SetSession(second)
	tr.forward(pane, tcell.NewEventKey(tcell.KeyRune, 'x', 0), time.Now(), "tasks")()
	testutil.Equal(t, len(tr.pending), 2) // a first key after re-resolution is measured
	pane.Draw(sim)
	tr.reconcile(pane)
	testutil.Equal(t, len(second.writers), 1)
	tr.close()
	testutil.Equal(t, len(second.writers), 0)
	tr.expire(time.Now().Add(3 * time.Second))
	testutil.Equal(t, len(tr.pending), 0) // hidden/rebound keys cannot accumulate
}

func TestSmoke_TypingFlushAfterShow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app, sim, ls := simApp(t)
	tr := newTypingTracker(true)
	sess := &typingSession{}
	pane := terminal.NewTerminalPane()
	pane.SetSession(sess)
	pane.SetTaskID("smoke")
	app.SetRoot(pane, true)
	app.SetAfterDrawFunc(func(tcell.Screen) { tr.reconcile(pane) })
	logged := make(chan string, 1)
	tr.log = func(f string, args ...any) {
		// The front buffer must already contain the echo when the log fires.
		cells, _, _ := sim.GetContents()
		found := false
		for _, cell := range cells {
			if len(cell.Runes) > 0 && cell.Runes[0] == 'Z' {
				found = true
			}
		}
		testutil.Equal(t, found, true)
		logged <- fmt.Sprintf(f, args...)
	}
	ls.onShow = tr.flushed
	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		queued := tr.forward(pane, ev, time.Now(), "tasks")
		_, _ = sess.WriteInput([]byte("Z"), agentview.OriginUser)
		queued()
		return nil
	})
	stop := runApp(t, app)
	defer stop()
	app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'Z', 0))
	select {
	case line := <-logged:
		testutil.Contains(t, line, "task=smoke pane=tasks total_ms=")
	case <-time.After(uiTimeout):
		t.Fatal("echo was not logged after the flushed frame")
	}
	readUI(t, app, tr.close)
}

func TestTypingProbeConcurrentOutput(t *testing.T) {
	t.Parallel()
	tr := newTypingTracker(true)
	p := newOutputProbe(time.Now)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			_, _ = p.Write([]byte("output"))
		}
	}()
	for range 100 {
		now := time.Now()
		tr.queued(tr.begin("task", "tasks", p, 0, now, now))
		tr.painted(p, 1)
		tr.flushed()
	}
	<-done
}

func TestTypingOutputBeforeDeadlinePaintsLater(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0)
	tr := newTypingTracker(true)
	tr.now = func() time.Time { return now }
	var line string
	tr.log = func(f string, args ...any) { line = fmt.Sprintf(f, args...) }
	p := newOutputProbe(tr.now)
	tr.queued(tr.begin("task", "tasks", p, 5, now, now))
	now = now.Add(time.Second)
	_, _ = p.Write([]byte("echo"))
	tr.painted(p, 5)
	tr.flushed()
	testutil.Equal(t, line, "") // output must actually reach the emulator
	now = now.Add(2 * time.Second)
	tr.painted(p, 6)
	tr.flushed()
	testutil.Contains(t, line, "total_ms=3000.000")
}

func TestTypingRealKeyPaths(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    tcell.Key
		r      rune
		global bool
	}{
		{"rune", tcell.KeyRune, 'x', false},
		{"escape", tcell.KeyEscape, 0, false},
		{"interrupt", tcell.KeyCtrlC, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			a := New(testDB(t), agent.NewRunner(nil), false)
			a.EnableTypingLog(true)
			sess := &typingSession{}
			a.agentPane.SetSession(sess)
			a.agentPane.SetTaskID("keys")
			a.agentPane.SetRect(0, 0, 80, 24)
			sim := tcell.NewSimulationScreen("UTF-8")
			testutil.NoError(t, sim.Init())
			t.Cleanup(sim.Fini)
			sim.SetSize(80, 24)
			a.agentPane.Draw(sim)
			a.typing.reconcile(a.agentPane)
			a.mode = modeAgent
			ev := tcell.NewEventKey(tc.key, tc.r, 0)
			if tc.global {
				a.handleGlobalKey(ev)
			} else {
				a.handleAgentKey(ev)
			}
			testutil.Equal(t, len(a.typing.pending), 1)
			testutil.Equal(t, a.typing.pending[0].queued.IsZero(), false)
			testutil.Equal(t, a.typing.pending[0].output.Load() != 0, true)
			// The legacy follow-up goroutine is intentionally still present for
			// the baseline. Its count comparison sees no later output in this test.
			a.typing.close()
		})
	}
}

func TestTypingOffHasNoProbe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := New(testDB(t), agent.NewRunner(nil), false)
	a.EnableTypingLog(false)
	testutil.Nil(t, a.typing)
	testutil.Equal(t, a.heraPage.OnForwardKey == nil, true)
	sess := &typingSession{}
	a.agentPane.SetSession(sess)
	testutil.Equal(t, len(sess.writers), 0)
}

func TestTypingTimeoutOverlapAndNil(t *testing.T) {
	t.Parallel()
	var disabled *typingTracker
	disabled.queued(nil)
	disabled.flushed()
	testutil.Nil(t, newTypingTracker(false))
	base := time.Unix(100, 0)
	now := base
	var lines []string
	tr := newTypingTracker(true)
	tr.now = func() time.Time { return now }
	tr.log = func(f string, args ...any) { lines = append(lines, fmt.Sprintf(f, args...)) }
	p := newOutputProbe(tr.now)
	tr.begin("a", "tasks", p, 0, base, base)
	tr.begin("a", "tasks", p, 0, base, base)
	now = base.Add(2 * time.Second)
	tr.flushed()
	testutil.Equal(t, len(lines), 2)
	testutil.Contains(t, lines[1], "overlap=1 outcome=no-echo")
	_, _ = p.Write([]byte("late echo"))
	tr.painted(p, 1)
	tr.flushed()
	testutil.Equal(t, len(lines), 2)
	tr.tick(base, base.Add(time.Millisecond))
	testutil.Equal(t, len(lines), 2)
}
