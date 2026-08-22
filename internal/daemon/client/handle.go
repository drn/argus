package client

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
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
	inputCh       chan inputItem     // async input channel for WriteInput
	lastInput     time.Time          // wall-clock time of last input write (either origin)
	lastUserInput time.Time          // wall-clock time of last agentview.OriginUser write — BUG-034 clear-on-input source
}

// inputItem is one queued WriteInput call awaiting its RPC.
type inputItem struct {
	data   []byte
	origin agentview.InputOrigin
}

func newRemoteSession(taskID string, c *Client) *RemoteSession {
	rs := &RemoteSession{
		taskID:  taskID,
		client:  c,
		buf:     agent.NewRingBuffer(defaultBufSize),
		done:    make(chan struct{}),
		inputCh: make(chan inputItem, 64),
	}
	go rs.inputLoop()
	return rs
}

// inputLoop drains the input channel and sends coalesced bytes to the daemon
// via RPC, one Origin per RPC call. Runs until the done channel is closed.
//
// carry holds an item drainInput already dequeued but could not merge into
// the batch just sent (an origin boundary) — inputLoop must send it next
// instead of blocking on <-rs.inputCh, or it would be lost.
func (rs *RemoteSession) inputLoop() {
	var carry *inputItem
	for {
		var item inputItem
		if carry != nil {
			// carry is unconditionally overwritten by drainInput below, so
			// clearing it here would be an ineffectual assignment.
			item = *carry
		} else {
			select {
			case item = <-rs.inputCh:
			case <-rs.done:
				return
			}
		}
		var batch inputItem
		batch, carry = drainInput(item, rs.inputCh)
		var resp daemon.StatusResp
		if err := rs.client.call("Daemon.WriteInput", &daemon.WriteReq{
			TaskID: rs.taskID,
			Data:   batch.data,
			Origin: batch.origin,
		}, &resp); err != nil {
			uxlog.Log("[client] inputLoop WriteInput failed: task=%s err=%v", rs.taskID, err)
		}
	}
}

// drainInput coalesces additional pending items from ch into initial,
// returning the combined batch ready for one RPC plus an optional carry item
// that could not be merged in (returned, not lost, for the next RPC). Drain
// stops as soon as any of: (a) the channel has no immediately-available
// item, (b) the batch ends with a bracketed-paste end sequence — coalescing
// across that boundary risks merging two `\x1b[200~..\x1b[201~` cycles into
// one PTY write, which the receiver may parse as a single paste — or (c) the
// next queued item has a DIFFERENT origin than the batch being built: origin
// is a per-RPC attribute (WriteReq.Origin), so merging a System-origin write
// and a User-origin write into one call would misattribute one of them —
// e.g. a hera bounce instruction queued back-to-back with a real keystroke
// must not stamp the keystroke's bytes (or vice versa) with the wrong origin.
func drainInput(initial inputItem, ch <-chan inputItem) (batch inputItem, carry *inputItem) {
	buf := initial.data
	for !bytes.HasSuffix(buf, pasteEndBytes) {
		select {
		case more := <-ch:
			if more.origin != initial.origin {
				return inputItem{data: buf, origin: initial.origin}, &more
			}
			buf = append(buf, more.data...)
		default:
			return inputItem{data: buf, origin: initial.origin}, nil
		}
	}
	return inputItem{data: buf, origin: initial.origin}, nil
}

func (rs *RemoteSession) PID() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.pid
}

// WriteInput enqueues p onto the input channel; inputLoop drains the channel
// and sends one or more RPCs to the daemon. origin is carried on the wire via
// WriteReq.Origin (see drainInput for why differently-origined items are
// never coalesced into the same RPC) and, locally, decides whether
// lastUserInput advances (see agentview.InputOrigin).
//
// Invariant relied on by drainInput: only PasteHandler writes data ending in
// the bracketed-paste end sequence (\x1b[201~). drainInput uses that suffix
// as a flush boundary so two back-to-back paste cycles never get coalesced
// into one PTY write. Any future caller that writes bracketed-paste content
// must wrap the whole cycle in a single WriteInput call (start sequence,
// payload, and end sequence) — never split it across calls.
func (rs *RemoteSession) WriteInput(p []byte, origin agentview.InputOrigin) (int, error) {
	// Copy so the caller can reuse the slice.
	cp := make([]byte, len(p))
	copy(cp, p)
	select {
	case rs.inputCh <- inputItem{data: cp, origin: origin}:
		now := time.Now()
		rs.mu.Lock()
		rs.lastInput = now
		if origin == agentview.OriginUser {
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

// LastUserInput returns the wall-clock time of the most recent WriteInput
// call made through this handle with agentview.OriginUser, or zero. A call
// made with agentview.OriginSystem does not advance it. Like LastInput, this
// is tracked client-side to satisfy the SessionHandle contract; the daemon
// watcher reads the in-process session.
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
