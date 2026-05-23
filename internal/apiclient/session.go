package apiclient

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/uxlog"
)

// Compile-time assertion.
var _ agent.SessionHandle = (*Session)(nil)

// defaultRingBufSize matches daemon-client's RemoteSession — 256 KiB of
// scrollback in-process, full history available via /api/tasks/{id}/output
// from the on-disk session log on the server.
const defaultRingBufSize = 256 * 1024

// Session implements agent.SessionHandle over the REST API. A long-lived
// SSE goroutine reads /api/tasks/{id}/stream into a local ring buffer; the
// TUI consumes the buffer via RecentOutput*. WriteInput, Resize and Stop
// are short REST calls. PID / size / work-dir are cached in cached and
// refreshed on demand.
type Session struct {
	taskID string
	p      *Provider

	mu        sync.Mutex
	buf       *agent.RingBuffer
	pid       int
	cols      int
	rows      int
	initCols  int
	initRows  int
	workDir   string
	idle      bool
	lastInput time.Time
	done      chan struct{}

	// streamCtx is cancelled to stop the SSE reader. Distinct from p.closed
	// so callers can stop a single session without nuking the whole provider.
	streamCtx    context.Context
	streamCancel context.CancelFunc
}

func newSession(taskID string, p *Provider) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{
		taskID:       taskID,
		p:            p,
		buf:          agent.NewRingBuffer(defaultRingBufSize),
		done:         make(chan struct{}),
		streamCtx:    ctx,
		streamCancel: cancel,
	}
}

// PID returns the cached agent PID. Populated by refreshTask().
func (s *Session) PID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pid
}

// WriteInput POSTs to /api/tasks/{id}/input. p is copied — caller may reuse.
func (s *Session) WriteInput(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	if err := s.p.c.WriteInput(context.Background(), s.taskID, cp); err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.lastInput = time.Now()
	s.mu.Unlock()
	return len(p), nil
}

// LastInput is local-only state — see RemoteSession.LastInput for the
// process-boundary rationale. The idle-push watcher reads the in-process
// Session, not this client side, but the interface contract requires it.
func (s *Session) LastInput() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastInput
}

// Resize POSTs to /api/tasks/{id}/resize.
func (s *Session) Resize(rows, cols uint16) error {
	resp, err := s.p.c.Resize(context.Background(), s.taskID, rows, cols)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cols = resp.Cols
	s.rows = resp.Rows
	s.mu.Unlock()
	return nil
}

// RecentOutput returns the full ring buffer contents.
func (s *Session) RecentOutput() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Bytes()
}

// RecentOutputTail returns the last n bytes from the ring buffer.
func (s *Session) RecentOutputTail(n int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Tail(n)
}

// RecentOutputTailWithTotal atomically snapshots (tail, total).
func (s *Session) RecentOutputTailWithTotal(n int) (tail []byte, total uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Tail(n), s.buf.TotalWritten()
}

// TotalWritten returns the high-water mark of bytes ever written to the ring.
func (s *Session) TotalWritten() uint64 {
	return s.buf.TotalWritten()
}

// IsIdle queries the server's sessions/state for the task. The "idle" flag
// reflects the agent's PTY readiness — true when the session is waiting
// for input.
//
// This is one HTTP request per call; the TUI's idle polling already runs
// at ~1 Hz so this is acceptable. For tighter loops, callers should cache
// the cached value via the mu-protected idle field.
func (s *Session) IsIdle() bool {
	state, err := s.p.c.GetSessionState(context.Background())
	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.idle
	}
	for _, id := range state.Idle {
		if id == s.taskID {
			s.mu.Lock()
			s.idle = true
			s.mu.Unlock()
			return true
		}
	}
	s.mu.Lock()
	s.idle = false
	s.mu.Unlock()
	return false
}

// Alive reports whether the stream is still attached. Closed on EOF.
func (s *Session) Alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// PTYSize fetches /size from the server.
func (s *Session) PTYSize() (cols, rows int) {
	sz, err := s.p.c.GetSize(context.Background(), s.taskID)
	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.cols, s.rows
	}
	s.mu.Lock()
	s.cols = sz.Cols
	s.rows = sz.Rows
	s.mu.Unlock()
	return sz.Cols, sz.Rows
}

// InitialPTYSize returns the dimensions captured the first time the stream
// observed a size. The TUI uses this to detect "started narrow" sessions
// whose scrollback won't re-flow on SIGWINCH. Cached on first sync.
func (s *Session) InitialPTYSize() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initCols == 0 && s.initRows == 0 {
		return s.cols, s.rows
	}
	return s.initCols, s.initRows
}

// Done is closed when the SSE stream terminates.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err is always nil — errors are surfaced via SessionExitInfo on the
// provider's onSessionExit callback.
func (s *Session) Err() error { return nil }

// WorkDir is the worktree path. Cached from the task GET.
func (s *Session) WorkDir() string {
	s.mu.Lock()
	wd := s.workDir
	s.mu.Unlock()
	if wd != "" {
		return wd
	}
	t, err := s.p.c.GetTask(context.Background(), s.taskID)
	if err != nil || t == nil {
		return ""
	}
	s.mu.Lock()
	s.workDir = t.WorktreePath
	s.mu.Unlock()
	return t.WorktreePath
}

// Stop ends the session via REST.
func (s *Session) Stop() error { return s.p.c.StopTask(context.Background(), s.taskID) }

// AddWriter, AddWriterFrom, AddWriterFromTolerant, RemoveWriter are no-ops.
// The server fans bytes to /stream; the TUI reads RecentOutput* off the
// local ring buffer. Same contract as daemon-client RemoteSession.
func (s *Session) AddWriter(_ io.Writer)                       {}
func (s *Session) AddWriterFrom(_ io.Writer, _ uint64)         {}
func (s *Session) AddWriterFromTolerant(_ io.Writer, _ uint64) {}
func (s *Session) RemoveWriter(_ io.Writer)                    {}

// close terminates the SSE reader and signals consumers of Done().
func (s *Session) close() {
	s.streamCancel()
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

// runStream is the SSE reader goroutine. Connects, decodes events, writes
// decoded bytes to the ring buffer, and exits on EOF. Reconnects once on
// transient drops via reconnectOnce — if the second attempt also drops the
// session is considered exited and removeSession fires.
//
// Each event is `data: <base64>\n\n` for output, `event: exit\ndata: …\n\n`
// at end-of-stream, or `event: clipboard\ndata: …\n\n` for clipboard
// notifications (ignored here — clipboard polling is in the API layer).
func (s *Session) runStream() {
	const maxRetries = 2
	for attempt := 0; attempt <= maxRetries; attempt++ {
		exited, err := s.streamOnce()
		if exited {
			s.close()
			s.p.removeSession(s.taskID, SessionExitInfo{Err: errString(err), Stopped: err == nil})
			return
		}
		select {
		case <-s.streamCtx.Done():
			s.close()
			s.p.removeSession(s.taskID, SessionExitInfo{StreamLost: true})
			return
		case <-s.p.closed:
			s.close()
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	uxlog.Log("[apiclient] stream task=%s exhausted retries, treating as stream lost", s.taskID)
	s.close()
	s.p.removeSession(s.taskID, SessionExitInfo{StreamLost: true})
}

// streamOnce opens one SSE connection and reads until EOF or context cancel.
// Returns (exited, err):
//   - exited == true  → server reported `event: exit` → process gone, fire exit
//   - exited == false → connection dropped without exit → caller decides
//     whether to retry
func (s *Session) streamOnce() (bool, error) {
	resp, err := s.p.c.StreamOutput(s.streamCtx, s.taskID, s.buf.TotalWritten())
	if err != nil {
		var apiErr *Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			// Server has no session for this task. Treat as exited.
			return true, err
		}
		uxlog.Log("[apiclient] stream task=%s connect failed: %v", s.taskID, err)
		return false, err
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	var event string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return false, err
			}
			uxlog.Log("[apiclient] stream task=%s read error: %v", s.taskID, err)
			return false, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			event = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			// SSE comment (server keepalive).
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(line[len("event:"):])
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		switch event {
		case "exit":
			return true, nil
		case "clipboard":
			// Ignored — handled separately by the apiclient consumer if any.
			continue
		default:
			payload, derr := base64.StdEncoding.DecodeString(data)
			if derr != nil {
				continue
			}
			s.mu.Lock()
			s.buf.Write(payload)
			s.mu.Unlock()
		}
	}
}

// errString is a nil-safe Error().
func errString(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
