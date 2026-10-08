package usagebudget

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

// fakeUsageSession is a scripted stand-in for a live `claude -- /usage` PTY
// session. It yields queued output chunks one Read at a time; once the queue
// is drained it behaves like the real interactive session — it never exits on
// its own — and blocks until Terminate is called (Read then returns io.EOF).
// As a safety net against a probe that never terminates it, a drained Read
// gives up after selfExitAfter and returns io.EOF, recording selfExited so
// the test can assert the probe did NOT rely on the process exiting.
type fakeUsageSession struct {
	mu            sync.Mutex
	pending       []byte
	chunks        chan []byte
	term          chan struct{}
	termOnce      sync.Once
	terminated    bool
	selfExited    bool
	exitWhenEmpty bool
	selfExitAfter time.Duration
	writes        bytes.Buffer
	onWrite       func(f *fakeUsageSession, p []byte)
}

func newFakeSession(chunks ...[]byte) *fakeUsageSession {
	f := &fakeUsageSession{
		chunks:        make(chan []byte, 4096),
		term:          make(chan struct{}),
		selfExitAfter: 3 * time.Second,
	}
	f.push(chunks...)
	return f
}

func (f *fakeUsageSession) push(chunks ...[]byte) {
	for _, c := range chunks {
		f.chunks <- c
	}
}

func (f *fakeUsageSession) Read(p []byte) (int, error) {
	f.mu.Lock()
	if len(f.pending) > 0 {
		n := copy(p, f.pending)
		f.pending = f.pending[n:]
		f.mu.Unlock()
		return n, nil
	}
	exitWhenEmpty := f.exitWhenEmpty
	f.mu.Unlock()

	var c []byte
	select {
	case c = <-f.chunks:
	default:
		if exitWhenEmpty {
			return 0, io.EOF
		}
		select {
		case c = <-f.chunks:
		case <-f.term:
			return 0, io.EOF
		case <-time.After(f.selfExitAfter):
			f.mu.Lock()
			f.selfExited = true
			f.mu.Unlock()
			return 0, io.EOF
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n := copy(p, c)
	f.pending = append(f.pending[:0], c[n:]...)
	return n, nil
}

func (f *fakeUsageSession) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.writes.Write(p)
	hook := f.onWrite
	f.mu.Unlock()
	if hook != nil {
		hook(f, p)
	}
	return len(p), nil
}

func (f *fakeUsageSession) Terminate() error {
	f.termOnce.Do(func() {
		f.mu.Lock()
		f.terminated = true
		f.mu.Unlock()
		close(f.term)
	})
	return nil
}

func (f *fakeUsageSession) state() (terminated, selfExited bool, written string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.terminated, f.selfExited, f.writes.String()
}

// chunked splits raw into n-byte pieces, simulating PTY output arriving
// incrementally (splits may fall mid-escape-sequence or mid-rune on purpose).
func chunked(raw []byte, n int) [][]byte {
	var out [][]byte
	for len(raw) > 0 {
		k := min(n, len(raw))
		out = append(out, append([]byte(nil), raw[:k]...))
		raw = raw[k:]
	}
	return out
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	testutil.NoError(t, err)
	return b
}

// installFakeSession routes the probe's session launch to f and its probe
// directory to dir, recording the directory the probe asked to run in.
func installFakeSession(t *testing.T, f *fakeUsageSession, dir string) *string {
	t.Helper()
	origStart, origDir := startUsageSession, probeDirFunc
	var gotDir string
	var mu sync.Mutex
	startUsageSession = func(_ context.Context, d string) (usageSession, error) {
		mu.Lock()
		gotDir = d
		mu.Unlock()
		return f, nil
	}
	probeDirFunc = func() (string, error) { return dir, nil }
	t.Cleanup(func() { startUsageSession, probeDirFunc = origStart, origDir })
	return &gotDir
}

// captureSlog swaps the default slog logger for one writing into a buffer
// (the daemon's slog output is what lands in daemon.log) and restores it on
// cleanup.
func captureSlog(t *testing.T) func() string {
	t.Helper()
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, w: &buf}, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
