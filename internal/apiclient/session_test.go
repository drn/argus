package apiclient

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/agent"

	"github.com/drn/argus/internal/testutil"
)

type sessionWriterFunc func([]byte) (int, error)

func (f sessionWriterFunc) Write(b []byte) (int, error) { return f(b) }

func TestSessionWriterStreamFanout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: YWJj\n\ndata: ZGVm\n\nevent: exit\ndata: {}\n\n")
	}))
	defer srv.Close()
	c := New(srv.URL, "token")
	p := NewProvider(c)
	defer p.Close()
	s := newSession("task", p)
	defer s.close()
	var output bytes.Buffer
	s.AddWriterFrom(&output, 0)
	failures := 0
	bad := &sessionWriterAdapter{write: func(b []byte) (int, error) {
		failures++
		// The stream must append to the ring first, and release s.mu before
		// notifying writers. RecentOutput takes that mutex itself.
		testutil.Equal(t, string(s.RecentOutput()), "abc")
		return 0, errors.New("writer failed")
	}}
	s.AddWriterFrom(bad, 0)
	exited, err := s.streamOnce()
	testutil.NoError(t, err)
	testutil.Equal(t, exited, true)
	testutil.Equal(t, output.String(), "abcdef")
	testutil.Equal(t, failures, 1)
	testutil.Equal(t, len(s.writers), 1)
	s.RemoveWriter(&output)
	s.RemoveWriter(&output)
	testutil.Equal(t, len(s.writers), 0)
}

// A pointer writer is comparable, matching io.Writer registration/removal.
type sessionWriterAdapter struct{ write sessionWriterFunc }

func (w *sessionWriterAdapter) Write(b []byte) (int, error) { return w.write(b) }

func TestSessionWriterReplayOffsets(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		offset   uint64
		tolerant bool
		want     string
	}{
		{"current", 6, false, ""},
		{"delta", 3, false, "def"},
		{"before ring", 0, false, "cdef"},
		{"tolerant", 3, true, "def"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{buf: agent.NewRingBuffer(4)}
			s.buf.Write([]byte("abcdef"))
			var out bytes.Buffer
			if tc.tolerant {
				s.AddWriterFromTolerant(&out, tc.offset)
			} else {
				s.AddWriterFrom(&out, tc.offset)
			}
			testutil.Equal(t, out.String(), tc.want)
			testutil.Equal(t, len(s.writers), 1)
		})
	}
	for _, tolerant := range []bool{false, true} {
		t.Run(fmt.Sprint("failure-", tolerant), func(t *testing.T) {
			s := &Session{buf: agent.NewRingBuffer(4)}
			s.buf.Write([]byte("data"))
			if tolerant {
				s.AddWriterFromTolerant(failingSessionWriter{}, 0)
			} else {
				s.AddWriterFrom(failingSessionWriter{}, 0)
			}
			testutil.Equal(t, len(s.writers), 0)
		})
	}
}

type failingSessionWriter struct{}

func (failingSessionWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
