package agent

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/creack/pty"

	"github.com/drn/argus/internal/testutil"
)

// newViewerTestSession builds a Session wired with a spy sizer so the
// viewer-registry logic can be exercised without a real PTY/process. The
// returned counter increments once per apply-to-PTY (resizeLocked → setSize),
// letting tests assert that an unchanged min issues NO resize. SaveSessionSize
// resolves through $HOME, so callers MUST t.Setenv("HOME", t.TempDir()) first
// (done here).
func newViewerTestSession(t *testing.T, cols, rows uint16) (*Session, *int32) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	// A pipe write-end is a valid *os.File for the field; the spy sizer never
	// touches it (no real ioctl), so the fd content is irrelevant.
	_, w, err := os.Pipe()
	testutil.NoError(t, err)
	t.Cleanup(func() { w.Close() })

	var calls int32
	s := &Session{
		TaskID:      "viewer-test",
		ptmx:        w,
		ptyCols:     cols,
		ptyRows:     rows,
		initialCols: cols,
		initialRows: rows,
		viewers:     make(map[string]viewerSize),
		setSize: func(_ *os.File, _ *pty.Winsize) error {
			atomic.AddInt32(&calls, 1)
			return nil
		},
	}
	return s, &calls
}

func TestSession_SetViewerSize_SmallestWins(t *testing.T) {
	s, calls := newViewerTestSession(t, 80, 24)

	s.SetViewerSize("tui", 180, 50)
	cols, rows := s.PTYSize()
	testutil.Equal(t, cols, 180)
	testutil.Equal(t, rows, 50)
	testutil.Equal(t, atomic.LoadInt32(calls), int32(1))

	// A second, smaller viewer constrains the min down to 80x24.
	s.SetViewerSize("web", 80, 24)
	cols, rows = s.PTYSize()
	testutil.Equal(t, cols, 80)
	testutil.Equal(t, rows, 24)
	testutil.Equal(t, atomic.LoadInt32(calls), int32(2))
}

func TestSession_SetViewerSize_PerDimensionMin(t *testing.T) {
	s, _ := newViewerTestSession(t, 80, 24)

	// Min is taken per dimension independently: narrow-but-tall vs wide-but-short
	// yields the narrow cols and the short rows.
	s.SetViewerSize("a", 100, 30)
	s.SetViewerSize("b", 200, 20)
	cols, rows := s.PTYSize()
	testutil.Equal(t, cols, 100)
	testutil.Equal(t, rows, 20)
}

func TestSession_RemoveViewer_GrowsBack(t *testing.T) {
	s, calls := newViewerTestSession(t, 80, 24)

	s.SetViewerSize("big", 180, 50)
	s.SetViewerSize("small", 80, 24)
	cols, rows := s.PTYSize()
	testutil.Equal(t, cols, 80)
	testutil.Equal(t, rows, 24)

	before := atomic.LoadInt32(calls)
	// Removing the smallest viewer recomputes the min over the survivors and
	// grows the PTY back up.
	s.RemoveViewer("small")
	cols, rows = s.PTYSize()
	testutil.Equal(t, cols, 180)
	testutil.Equal(t, rows, 50)
	testutil.Equal(t, atomic.LoadInt32(calls), before+1)
}

func TestSession_SetViewerSize_UnchangedMinNoResize(t *testing.T) {
	s, calls := newViewerTestSession(t, 80, 24)

	s.SetViewerSize("tui", 120, 40)
	testutil.Equal(t, atomic.LoadInt32(calls), int32(1))

	// Re-posting the exact same size for the same viewer leaves the min
	// unchanged → no resize, no SIGWINCH.
	s.SetViewerSize("tui", 120, 40)
	testutil.Equal(t, atomic.LoadInt32(calls), int32(1))

	// A second viewer LARGER than the current min does not change the min
	// either → still no resize.
	s.SetViewerSize("web", 200, 60)
	testutil.Equal(t, atomic.LoadInt32(calls), int32(1))
	cols, rows := s.PTYSize()
	testutil.Equal(t, cols, 120)
	testutil.Equal(t, rows, 40)
}

func TestSession_RemoveViewer_LastViewerKeepsLastSize(t *testing.T) {
	s, calls := newViewerTestSession(t, 80, 24)

	s.SetViewerSize("only", 150, 45)
	cols, rows := s.PTYSize()
	testutil.Equal(t, cols, 150)
	testutil.Equal(t, rows, 45)
	after := atomic.LoadInt32(calls)

	// Removing the last active viewer leaves an empty registry: keep the last
	// applied size, never resize toward zero.
	s.RemoveViewer("only")
	cols, rows = s.PTYSize()
	testutil.Equal(t, cols, 150)
	testutil.Equal(t, rows, 45)
	testutil.Equal(t, atomic.LoadInt32(calls), after) // no extra apply
}

func TestSession_SetViewerSize_ZeroDimensionIgnored(t *testing.T) {
	s, calls := newViewerTestSession(t, 80, 24)

	// A viewer that hasn't been laid out yet (0 in a dimension) must not
	// collapse the PTY toward zero: with no positive claim in a dimension the
	// last size is kept.
	s.SetViewerSize("unlaid", 0, 0)
	cols, rows := s.PTYSize()
	testutil.Equal(t, cols, 80)
	testutil.Equal(t, rows, 24)
	testutil.Equal(t, atomic.LoadInt32(calls), int32(0))
}

func TestSession_SetViewerSize_EmptyIDIgnored(t *testing.T) {
	s, calls := newViewerTestSession(t, 80, 24)
	s.SetViewerSize("", 200, 60)
	testutil.Equal(t, atomic.LoadInt32(calls), int32(0))
	testutil.Equal(t, len(s.viewers), 0)
}

func TestSession_RemoveViewer_UnknownIDNoResize(t *testing.T) {
	s, calls := newViewerTestSession(t, 80, 24)
	s.SetViewerSize("a", 100, 30)
	before := atomic.LoadInt32(calls)
	s.RemoveViewer("ghost") // not registered
	testutil.Equal(t, atomic.LoadInt32(calls), before)
	cols, rows := s.PTYSize()
	testutil.Equal(t, cols, 100)
	testutil.Equal(t, rows, 30)
}

func TestSession_Viewer_AfterExitNoOp(t *testing.T) {
	s, calls := newViewerTestSession(t, 80, 24)

	// Simulate the process having exited and the PTY closed.
	s.mu.Lock()
	s.ptmxClosed = true
	s.mu.Unlock()

	// Registry still mutates, the reported size still updates, but no real
	// resize (setSize) is ever issued on a closed PTY — a safe no-op success.
	s.SetViewerSize("tui", 200, 60)
	cols, rows := s.PTYSize()
	testutil.Equal(t, cols, 200)
	testutil.Equal(t, rows, 60)
	testutil.Equal(t, atomic.LoadInt32(calls), int32(0))

	s.RemoveViewer("tui")
	testutil.Equal(t, atomic.LoadInt32(calls), int32(0))
}
