package client

import (
	"bytes"
	"net"
	"net/rpc/jsonrpc"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/testutil"
)

// syncWriter is a concurrency-safe io.Writer for asserting fan-out delivery.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// errWriter always fails — used to prove errored writers are auto-removed.
type errWriter struct{ n int }

func (w *errWriter) Write(p []byte) (int, error) {
	w.n++
	return 0, errFakeWrite
}

var errFakeWrite = &writeErr{}

type writeErr struct{}

func (*writeErr) Error() string { return "fake write error" }

// TestRSReplay pins the replay-then-attach behavior of
// the generalized RemoteSession fan-out: a freshly attached writer immediately
// receives the contents of the local ring (the supervisor-client double-proxy
// relies on this so a late TUI stream conn replays missed bytes).
func TestRSReplay(t *testing.T) {
	rs := newRemoteSession("replay", &Client{closed: make(chan struct{})})

	// Seed the local ring as if the stream reader had populated it.
	rs.mu.Lock()
	rs.buf.Write([]byte("hello world"))
	rs.mu.Unlock()

	t.Run("AddWriter replays full ring", func(t *testing.T) {
		var w syncWriter
		rs.AddWriter(&w)
		testutil.Equal(t, w.String(), "hello world")
	})

	t.Run("AddWriterFromTolerant replays only the delta past offset", func(t *testing.T) {
		var w syncWriter
		// offset 6 → skip "hello ", replay "world".
		rs.AddWriterFromTolerant(&w, 6)
		testutil.Equal(t, w.String(), "world")
	})

	t.Run("AddWriterFrom replays only the delta past offset", func(t *testing.T) {
		var w syncWriter
		rs.AddWriterFrom(&w, 6)
		testutil.Equal(t, w.String(), "world")
	})

	t.Run("offset at or beyond total replays nothing", func(t *testing.T) {
		var w syncWriter
		rs.AddWriterFromTolerant(&w, 11)
		testutil.Equal(t, w.String(), "")
	})
}

// TestRSTee proves the stream reader tees live bytes
// to attached writers — the supervisor→daemon→TUI fan-out hop. A writer attached
// before the stream sends should receive the streamed payload live (not just via
// the ring), which is exactly how the daemon's handleStream relays to TUI conns.
func TestRSTee(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.mu.Lock()
	fd.alive = false
	fd.streamMsg = []byte("live-tee-payload")
	fd.mu.Unlock()

	conn, err := net.Dial("unix", fd.sock)
	testutil.NoError(t, err)
	conn.Write([]byte("R")) //nolint:errcheck
	c := &Client{
		rpc:      jsonrpc.NewClient(conn),
		sockPath: fd.sock,
		sessions: make(map[string]*RemoteSession),
		closed:   make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-c.closed:
		default:
			close(c.closed)
		}
		c.rpc.Close() //nolint:errcheck
	})
	c.OnSessionExit(func(string, daemon.ExitInfo) {})

	rs := newRemoteSession("tee", c)
	c.mu.Lock()
	c.sessions["tee"] = rs
	c.mu.Unlock()

	// Attach a live writer + an always-erroring writer BEFORE the stream sends,
	// so the tee path (and errored-writer removal) fires on the live bytes.
	var live syncWriter
	bad := &errWriter{}
	rs.AddWriter(&live)
	rs.AddWriter(bad)

	done := make(chan struct{})
	go func() {
		rs.connectStream(fd.sock)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("connectStream did not return")
	}

	testutil.Contains(t, live.String(), "live-tee-payload")
	// The erroring writer was dropped from the set after its first failed Write.
	rs.mu.Lock()
	nWriters := len(rs.writers)
	rs.mu.Unlock()
	if nWriters != 1 {
		t.Fatalf("expected 1 writer after errored-writer removal, got %d", nWriters)
	}
}

// TestRSRmWriter pins that a removed writer stops receiving
// replay/live output.
func TestRSRmWriter(t *testing.T) {
	rs := newRemoteSession("rm", &Client{closed: make(chan struct{})})
	var w syncWriter
	rs.AddWriter(&w)
	rs.mu.Lock()
	got := len(rs.writers)
	rs.mu.Unlock()
	testutil.Equal(t, got, 1)

	rs.RemoveWriter(&w)
	rs.mu.Lock()
	got = len(rs.writers)
	rs.mu.Unlock()
	testutil.Equal(t, got, 0)

	// RemoveWriter on an absent writer is a no-op.
	rs.RemoveWriter(&w)
}
