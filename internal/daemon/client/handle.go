package client

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/uxlog"
)

const defaultBufSize = 256 * 1024 // 256KB ring buffer; session log file handles full scrollback

// pasteEndBytes is the bracketed-paste end sequence (CSI 201~). The inputLoop
// drain treats it as a flush boundary so two back-to-back paste cycles never
// get merged into one PTY write — merging lets the receiving terminal's
// parser confuse them for a single paste, dropping the first cycle's
// contents (visible as only the last of N drag-dropped files registering
// in an interactive CLI).
var pasteEndBytes = []byte("\x1b[201~")

// Compile-time assertion.
var _ agent.SessionHandle = (*RemoteSession)(nil)

// RemoteSession implements agent.SessionHandle by proxying to the daemon.
type RemoteSession struct {
	taskID string
	client *Client

	mu            sync.Mutex
	buf           *agent.RingBuffer // local ring buffer, populated by stream reader
	writers       []io.Writer       // stream reader tees output to all attached writers (see fan-out note below)
	pid           int
	info          daemon.SessionInfo // cached session info
	done          chan struct{}      // closed when stream EOF
	closeOnce     sync.Once          // guards close(done) — see close()
	inputCh       chan []byte        // async input channel for WriteInput
	lastInput     time.Time          // wall-clock time of last input write (user or system)
	lastUserInput time.Time          // wall-clock time of last USER keystroke (WriteInput only; not WriteInputSystem) — BUG-034 clear-on-input source
}

func newRemoteSession(taskID string, c *Client) *RemoteSession {
	rs := &RemoteSession{
		taskID:  taskID,
		client:  c,
		buf:     agent.NewRingBuffer(defaultBufSize),
		done:    make(chan struct{}),
		inputCh: make(chan []byte, 64),
	}
	go rs.inputLoop()
	return rs
}

// inputLoop drains the input channel and sends coalesced bytes to the daemon
// via RPC. Runs until the done channel is closed.
func (rs *RemoteSession) inputLoop() {
	for {
		// Block until at least one input arrives or session closes.
		select {
		case b := <-rs.inputCh:
			buf := drainInput(b, rs.inputCh)
			var resp daemon.StatusResp
			if err := rs.client.call("Daemon.WriteInput", &daemon.WriteReq{
				TaskID: rs.taskID,
				Data:   buf,
			}, &resp); err != nil {
				uxlog.Log("[client] inputLoop WriteInput failed: task=%s err=%v", rs.taskID, err)
			}
		case <-rs.done:
			return
		}
	}
}

// drainInput coalesces additional pending messages from ch into initial,
// returning the combined buffer ready for one RPC. Drain stops as soon as
// either (a) the channel has no immediately-available message, or (b) the
// buffer ends with a bracketed-paste end sequence — coalescing across that
// boundary risks merging two `\x1b[200~..\x1b[201~` cycles into one PTY
// write, which the receiver may parse as a single paste.
func drainInput(initial []byte, ch <-chan []byte) []byte {
	buf := initial
	for !bytes.HasSuffix(buf, pasteEndBytes) {
		select {
		case more := <-ch:
			buf = append(buf, more...)
		default:
			return buf
		}
	}
	return buf
}

func (rs *RemoteSession) PID() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.pid
}

// WriteInput enqueues p onto the input channel; inputLoop drains the channel
// and sends one or more RPCs to the daemon.
//
// Invariant relied on by drainInput: only PasteHandler writes data ending in
// the bracketed-paste end sequence (\x1b[201~). drainInput uses that suffix
// as a flush boundary so two back-to-back paste cycles never get coalesced
// into one PTY write. Any future caller that writes bracketed-paste content
// must wrap the whole cycle in a single WriteInput call (start sequence,
// payload, and end sequence) — never split it across calls.
func (rs *RemoteSession) WriteInput(p []byte) (int, error) {
	return rs.writeInput(p, true)
}

// WriteInputSystem enqueues p like WriteInput but records only the work-cycle
// timestamp (lastInput), NOT the user-input timestamp (lastUserInput) — the
// system-delivery path (reliable-notify) so a delivered message never clears
// the needs-input "(?)" flag (BUG-034). The wire RPC is identical; the
// supervisor types the bytes the same way regardless.
func (rs *RemoteSession) WriteInputSystem(p []byte) (int, error) {
	return rs.writeInput(p, false)
}

func (rs *RemoteSession) writeInput(p []byte, user bool) (int, error) {
	// Copy so the caller can reuse the slice.
	cp := make([]byte, len(p))
	copy(cp, p)
	select {
	case rs.inputCh <- cp:
		now := time.Now()
		rs.mu.Lock()
		rs.lastInput = now
		if user {
			rs.lastUserInput = now
		}
		rs.mu.Unlock()
		return len(p), nil
	case <-rs.done:
		return 0, fmt.Errorf("session closed")
	}
}

// LastInput returns the wall-clock time of the most recent WriteInput call,
// or the zero time if WriteInput has never been called for this session.
//
// Tracked client-side so the SessionHandle interface contract holds, but
// note: the idle-push watcher runs in the daemon process against the
// in-process *agent.Session — so the value RemoteSession returns here is not
// what the watcher reads. It is correct for any local consumer that calls
// WriteInput on this handle.
func (rs *RemoteSession) LastInput() time.Time {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.lastInput
}

// LastUserInput returns the wall-clock time of the most recent USER keystroke
// written through this handle (WriteInput), or zero. WriteInputSystem does not
// advance it. Like LastInput, this is tracked client-side to satisfy the
// SessionHandle contract; the daemon watcher reads the in-process session.
func (rs *RemoteSession) LastUserInput() time.Time {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.lastUserInput
}

func (rs *RemoteSession) Resize(rows, cols uint16) error {
	var resp daemon.StatusResp
	err := rs.client.call("Daemon.Resize", &daemon.ResizeReq{
		TaskID: rs.taskID,
		Rows:   rows,
		Cols:   cols,
	}, &resp)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

func (rs *RemoteSession) RecentOutput() []byte {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.buf.Bytes()
}

func (rs *RemoteSession) RecentOutputTail(n int) []byte {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.buf.Tail(n)
}

// RecentOutputTailWithTotal mirrors Session.RecentOutputTailWithTotal — atomic
// snapshot of (tail, total) under the local stream-buffer lock. RemoteSession
// is only used by the TUI client (the API server runs in the daemon and talks
// to the in-process Session), so this implementation exists purely to satisfy
// SessionHandle; nothing in the TUI calls it.
func (rs *RemoteSession) RecentOutputTailWithTotal(n int) (tail []byte, total uint64) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.buf.Tail(n), rs.buf.TotalWritten()
}

func (rs *RemoteSession) TotalWritten() uint64 {
	return rs.buf.TotalWritten()
}

func (rs *RemoteSession) IsIdle() bool {
	rs.refreshInfo()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.info.Idle
}

func (rs *RemoteSession) Alive() bool {
	select {
	case <-rs.done:
		return false
	default:
		return true
	}
}

func (rs *RemoteSession) PTYSize() (cols, rows int) {
	rs.refreshInfo()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.info.Cols, rs.info.Rows
}

// InitialPTYSize returns the PTY dimensions the session was started with.
// Used by the TUI's narrow-stuck detector to spot sessions that need a
// kill+resume to re-flow their conversation history at a wider size.
func (rs *RemoteSession) InitialPTYSize() (cols, rows int) {
	rs.refreshInfo()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.info.InitialCols, rs.info.InitialRows
}

func (rs *RemoteSession) Done() <-chan struct{} {
	return rs.done
}

func (rs *RemoteSession) Err() error {
	return nil // errors are communicated via DB, not the handle
}

func (rs *RemoteSession) WorkDir() string {
	rs.refreshInfo()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.info.WorkDir
}

func (rs *RemoteSession) Stop() error {
	return rs.client.Stop(rs.taskID)
}

// updateInfo stores cached session info.
func (rs *RemoteSession) updateInfo(info daemon.SessionInfo) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.info = info
	if info.PID != 0 {
		rs.pid = info.PID
	}
}

// refreshInfo fetches session info from the daemon.
func (rs *RemoteSession) refreshInfo() {
	var info daemon.SessionInfo
	if err := rs.client.call("Daemon.SessionStatus", &daemon.TaskIDReq{TaskID: rs.taskID}, &info); err != nil {
		return
	}
	rs.updateInfo(info)
}

// Writer fan-out — the supervisor-client double-proxy (session-supervisor P2).
//
// When the daemon is the *consumer* of this client (supervisor mode), its own
// handleStream registers each TUI stream conn as a writer here and the stream
// reader (stream.go) tees supervisor bytes to them — supervisor.readLoop →
// daemon RemoteSession.buf + writers → TUI conn → x/vt. So these MUST fan out,
// mirroring agent.Session's writer set exactly (replay-then-attach, tolerant
// gap-not-duplicate ordering, errored-writer auto-removal).
//
// For the original TUI consumer (daemon mode, talking to the daemon directly)
// nothing registers writers — the terminalpane polls RecentOutputTail — so the
// fan-out is dormant and behavior is unchanged. The bytes still land in rs.buf
// for those pollers regardless of whether any writer is attached.

// AddWriter registers w to receive output: it replays the full local ring, then
// attaches for live output. Replay is sent BEFORE registering so live bytes
// can't race ahead of the replay (gap-not-duplicate; see Session.AddWriter).
func (rs *RemoteSession) AddWriter(w io.Writer) {
	rs.AddWriterFromTolerant(w, 0)
}

// AddWriterFrom registers w to receive output starting at byte `offset`,
// replaying [offset..currentTotal) under the lock so the live attach happens at
// exactly currentTotal — no gap, no duplicate. w.Write MUST NOT block (the lock
// is held through replay and the stream reader takes the same lock). Mirrors
// Session.AddWriterFrom; used by the daemon API server's SSE /stream channelWriter.
func (rs *RemoteSession) AddWriterFrom(w io.Writer, offset uint64) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	currentTotal := rs.buf.TotalWritten()
	if offset < currentTotal {
		gap := currentTotal - offset
		var replay []byte
		if gap > uint64(rs.buf.Len()) { //nolint:gosec // Len() is non-negative
			replay = rs.buf.Bytes()
		} else {
			replay = rs.buf.Tail(int(gap)) //nolint:gosec // gap <= Len() per branch guard
		}
		if len(replay) > 0 {
			if _, err := w.Write(replay); err != nil {
				return
			}
		}
	}
	rs.writers = append(rs.writers, w)
}

// AddWriterFromTolerant replays [offset..currentTotal) OUTSIDE the lock, then
// re-acquires it to attach — so a slow/blocking w.Write (e.g. a net.Conn under
// kernel flow control, like the daemon's TUI stream socket) cannot stall the
// stream reader. Accepts a small gap rather than a duplicate. `offset` of 0
// replays the full ring. Mirrors Session.AddWriterFromTolerant.
func (rs *RemoteSession) AddWriterFromTolerant(w io.Writer, offset uint64) {
	rs.mu.Lock()
	currentTotal := rs.buf.TotalWritten()
	var replay []byte
	if offset < currentTotal {
		gap := currentTotal - offset
		if gap > uint64(rs.buf.Len()) { //nolint:gosec // Len() is non-negative
			replay = rs.buf.Bytes()
		} else {
			replay = rs.buf.Tail(int(gap)) //nolint:gosec // gap <= Len() per branch guard
		}
	}
	rs.mu.Unlock()

	if len(replay) > 0 {
		if _, err := w.Write(replay); err != nil {
			return
		}
	}

	rs.mu.Lock()
	rs.writers = append(rs.writers, w)
	rs.mu.Unlock()
}

// RemoveWriter unregisters a writer. Safe to call concurrently.
func (rs *RemoteSession) RemoveWriter(w io.Writer) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.removeWriterLocked(w)
}

// removeWriterLocked removes a writer from the slice. Caller must hold rs.mu.
func (rs *RemoteSession) removeWriterLocked(w io.Writer) {
	for i, existing := range rs.writers {
		if existing == w {
			rs.writers = append(rs.writers[:i], rs.writers[i+1:]...)
			return
		}
	}
}

// close shuts down the remote session.
// close is idempotent + concurrency-safe via closeOnce. Multiple goroutines
// race to close a session's done channel: the session's own connectStream
// goroutine on exit/stream-loss, AND Client.Close() iterating every session on
// shutdown (test t.Cleanup, restartDaemon client swap, supervisor-mode daemon
// cleanup). The old `select{<-done: default: close(done)}` was a non-atomic
// check-then-close: two racers both took the default and double-closed → "close
// of closed channel" panic (surfaced under parallel -race load). Mirrors the
// Client.closeOnce fix one level down.
func (rs *RemoteSession) close() {
	rs.closeOnce.Do(func() { close(rs.done) })
}
