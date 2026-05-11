package daemon

import (
	"encoding/json"
	"log/slog"
	"net"
	"time"
)

// streamPendingRestartWaitInterval is how often handleStream checks whether
// a kick-restart's new session has appeared while waiting at the gap.
const streamPendingRestartWaitInterval = 50 * time.Millisecond

// streamPendingRestartMaxWait is the upper bound on how long handleStream
// will block waiting for a kick-restart's new session to appear before giving
// up. Tuned to comfortably cover sandboxed Claude/Codex cold starts (~1-3s)
// without making truly-dead sessions linger.
const streamPendingRestartMaxWait = 10 * time.Second

// handleStream processes a stream connection. The client sends a JSON
// StreamHeader, then the daemon registers the connection as a writer on
// the session. Output flows as raw bytes until the session exits or the
// client disconnects.
func (d *Daemon) handleStream(conn net.Conn) {
	var header StreamHeader
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&header); err != nil {
		slog.Error("stream header decode error", "err", err)
		return
	}

	sess := d.runner.Get(header.TaskID)
	// If a kick-restart is in flight (between the old session's exit and the
	// new session's slot being filled), wait briefly for the new session to
	// land instead of rejecting the client immediately. Without this, TUI
	// clients reconnecting during a kick gap exhaust their retry budget
	// (3×500ms = 1.5s) and tear down the local handle. The wait is bounded
	// and short-circuits as soon as the new session appears or the kick is
	// no longer in flight.
	if sess == nil && d.runner.HasPendingRestart(header.TaskID) {
		deadline := time.Now().Add(streamPendingRestartMaxWait)
	wait:
		for time.Now().Before(deadline) {
			select {
			case <-d.done:
				return // daemon shutting down — abandon the wait
			case <-time.After(streamPendingRestartWaitInterval):
			}
			sess = d.runner.Get(header.TaskID)
			if sess != nil {
				slog.Info("stream: attached to resumed session after kick gap", "task", header.TaskID)
				break wait
			}
			if !d.runner.HasPendingRestart(header.TaskID) {
				break wait // restart abandoned (failed Start) — no new session coming
			}
		}
	}
	if sess == nil {
		slog.Warn("stream: session not found", "task", header.TaskID)
		return
	}

	slog.Info("stream connected", "task", header.TaskID, "since", header.Since)
	d.registerStream(header.TaskID, conn)
	defer d.unregisterStream(header.TaskID, conn)

	// AddWriterFromTolerant replays only [Since, currentTotal) before attaching
	// live — reconnects whose client ring already contains bytes ≤ Since don't
	// see the daemon ring replayed on top. Tolerant variant: conn.Write may
	// block on kernel socket flow control, so the replay runs outside the
	// session mutex (accepting a tiny gap rather than stalling readLoop).
	sess.AddWriterFromTolerant(conn, header.Since)
	defer sess.RemoveWriter(conn)

	// Block until the session exits or the client disconnects.
	// We detect client disconnect by trying to read from the connection.
	// The client doesn't send anything on the stream after the header,
	// so a read will block until the connection is closed.
	select {
	case <-sess.Done():
		slog.Info("stream: session exited", "task", header.TaskID)
	case <-d.done:
		slog.Info("stream: daemon shutting down", "task", header.TaskID)
	case <-waitForClose(conn):
		slog.Info("stream: client disconnected", "task", header.TaskID)
	}
}

// waitForClose returns a channel that closes when the connection is closed.
func waitForClose(conn net.Conn) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		conn.Read(buf) // blocks until close or error
		close(ch)
	}()
	return ch
}
