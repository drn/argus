package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/terminal"
)

type repaintSession struct {
	fakeKickSession
	mu      sync.Mutex
	ring    *agent.RingBuffer
	writers map[io.Writer]bool
	ended   chan struct{}
	resizes atomic.Int64
}

func newRepaintSession() *repaintSession {
	return &repaintSession{ring: agent.NewRingBuffer(4096), writers: make(map[io.Writer]bool), ended: make(chan struct{})}
}
func (s *repaintSession) Alive() bool {
	select {
	case <-s.ended:
		return false
	default:
		return true
	}
}
func (s *repaintSession) Done() <-chan struct{}         { return s.ended }
func (s *repaintSession) Resize(uint16, uint16) error   { s.resizes.Add(1); return nil }
func (s *repaintSession) TotalWritten() uint64          { return s.ring.TotalWritten() }
func (s *repaintSession) RecentOutput() []byte          { return s.ring.Bytes() }
func (s *repaintSession) RecentOutputTail(n int) []byte { return s.ring.Tail(n) }
func (s *repaintSession) RecentOutputTailWithTotal(n int) ([]byte, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ring.Tail(n), s.ring.TotalWritten()
}
func (s *repaintSession) AddWriterFrom(w io.Writer, offset uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if total := s.ring.TotalWritten(); offset < total {
		_, _ = w.Write(s.ring.Tail(int(min(total-offset, 4096)))) //nolint:gosec // Bounded by the fixture's 4096-byte ring.
	}
	s.writers[w] = true
}
func (s *repaintSession) RemoveWriter(w io.Writer) { s.mu.Lock(); delete(s.writers, w); s.mu.Unlock() }
func (s *repaintSession) WriteInput(b []byte, _ agentview.InputOrigin) (int, error) {
	s.emit(b)
	return len(b), nil
}
func (s *repaintSession) emit(b []byte) {
	s.mu.Lock()
	s.ring.Write(b)
	ws := make([]io.Writer, 0, len(s.writers))
	for w := range s.writers {
		ws = append(ws, w)
	}
	s.mu.Unlock()
	for _, w := range ws {
		_, _ = w.Write(b)
	}
}
func (s *repaintSession) writerCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.writers) }

func TestRepaintSubscriptionEndAndUnsubscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		defer close(done)
		p := newRepaintPump(done, func() {}, false)
		pane := terminal.NewTerminalPane()
		sess := newRepaintSession()
		p.reconcile([]paneFrame{{pane: pane, session: sess, drawn: true}})
		sub := p.subs[pane]
		testutil.Equal(t, sess.writerCount(), 1)
		<-p.wakes // initial subscription covers the frame/attach gap
		_, _ = sub.writer.Write(nil)
		testutil.Equal(t, len(p.wakes), 0)
		close(sess.ended)
		synctest.Wait()
		testutil.Equal(t, len(p.wakes), 1)
		<-p.wakes
		select {
		case <-sub.finished:
		default:
			t.Fatal("Done watcher did not exit")
		}
		p.reconcile([]paneFrame{{pane: pane, drawn: false}})
		testutil.Equal(t, sess.writerCount(), 0)
		_, _ = sub.writer.Write([]byte("late snapshot"))
		testutil.Equal(t, len(p.wakes), 0)
		next := newRepaintSession()
		p.reconcile([]paneFrame{{pane: pane, session: next, drawn: true}})
		watcher := p.subs[pane]
		p.close()
		p.close()
		synctest.Wait()
		testutil.Equal(t, next.writerCount(), 0)
		select {
		case <-watcher.finished:
		default:
			t.Fatal("unsubscribe left a Done watcher")
		}
	})
}

func TestRepaintDiagnosticsAndFailure(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				done := make(chan struct{})
				lines := make(chan string, 10)
				p := newRepaintPump(done, func() {}, enabled)
				p.log = func(f string, args ...any) { lines <- fmt.Sprintf(f, args...) }
				go p.run()
				p.wake()
				synctest.Wait()
				time.Sleep(30 * time.Second)
				synctest.Wait()
				want := 0
				if enabled {
					want = 1
				}
				testutil.Equal(t, len(lines), want)
				if enabled {
					testutil.Equal(t, <-lines, "[repaint] wakes=1 frames=1 window_s=30")
				}
				time.Sleep(30 * time.Second)
				synctest.Wait()
				testutil.Equal(t, len(lines), 0) // idle window is silent
				close(done)
				synctest.Wait()
				testutil.Equal(t, <-lines, "[terminal-output] repaint pump stopped")
				p.wake()
				testutil.Equal(t, len(p.wakes), 0)
			})
		})
	}
	synctest.Test(t, func(t *testing.T) {
		p := newRepaintPump(make(chan struct{}), func() { panic("draw failed") }, false)
		lines := make(chan string, 10)
		p.log = func(f string, args ...any) { lines <- fmt.Sprintf(f, args...) }
		go p.run()
		p.wake()
		synctest.Wait()
		testutil.Equal(t, strings.Contains(<-lines, "repaint pump failed: draw failed"), true)
		<-p.stopped
	})
}

func TestRepaintRebindCancelsOldSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		defer close(done)
		p := newRepaintPump(done, func() {}, false)
		pane := terminal.NewTerminalPane()
		old, next := newRepaintSession(), newRepaintSession()
		p.reconcile([]paneFrame{{pane: pane, session: old, drawn: true}})
		watcher := p.subs[pane]
		<-p.wakes
		p.reconcile([]paneFrame{{pane: pane, session: next, drawn: true}})
		<-p.wakes
		synctest.Wait()
		testutil.Equal(t, old.writerCount(), 0)
		testutil.Equal(t, next.writerCount(), 1)
		select {
		case <-watcher.finished:
		default:
			t.Fatal("rebind kept old watcher")
		}
		old.emit([]byte("hidden"))
		testutil.Equal(t, len(p.wakes), 0)
		next.emit([]byte("visible"))
		testutil.Equal(t, len(p.wakes), 1)
		p.close()
		synctest.Wait()
	})
}
