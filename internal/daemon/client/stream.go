package client

import (
	"encoding/json"
	"net"
	"time"

	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/uxlog"
)

// maxStreamRetries is the maximum number of times to retry the stream
// connection when it drops but the process is still alive on the daemon.
const maxStreamRetries = 3

// connectStream opens a stream connection to the daemon for this session's output.
// Reads raw bytes and writes them to the local ring buffer. If the stream drops
// but the daemon reports the session is still alive, retries up to maxStreamRetries
// times before giving up. Calls removeSession only when the process has actually
// exited. Calls removeSessionStreamLost when retries are exhausted, the daemon
// is unreachable, or the session was closed externally (e.g., client shutdown).
func (rs *RemoteSession) connectStream(sockPath string) {
	for attempt := 0; attempt <= maxStreamRetries; attempt++ {
		// Check if the client was closed (e.g., daemon restart replaced it).
		// This stops stale goroutines from flooding the new daemon with
		// stream requests after a restart cascade. No removeSession* call
		// needed — Client.Close() iterates c.sessions to clean up.
		select {
		case <-rs.client.closed:
			uxlog.Log("stream: client closed, stopping retries task=%s", rs.taskID)
			rs.close()
			return
		case <-rs.done:
			uxlog.Log("stream: session closed externally, stopping retries task=%s", rs.taskID)
			rs.close()
			return
		default:
		}

		if attempt > 0 {
			uxlog.Log("stream: retry %d/%d task=%s", attempt, maxStreamRetries, rs.taskID)
			time.Sleep(500 * time.Millisecond)
		}

		exited, daemonDown := rs.streamOnce(sockPath)
		if daemonDown {
			// Can't reach daemon — stream lost, not a confirmed process exit.
			uxlog.Log("stream: daemon unreachable task=%s, treating as stream lost", rs.taskID)
			rs.close()
			rs.client.removeSessionStreamLost(rs.taskID)
			return
		}
		if exited {
			// Process actually exited — fire the exit callback.
			rs.close()
			rs.client.removeSession(rs.taskID)
			return
		}

		// Stream dropped but process is still alive — check if we
		// should retry or if we've been closed externally.
		select {
		case <-rs.done:
			// Session was closed externally (e.g., client shutdown during
			// daemon restart) — we don't know if the process actually
			// exited, so treat as stream lost to avoid incorrectly
			// marking the task Complete.
			uxlog.Log("stream: session closed externally task=%s, treating as stream lost", rs.taskID)
			rs.close() // no-op (done already closed), but consistent with other exit paths
			rs.client.removeSessionStreamLost(rs.taskID)
			return
		default:
		}
	}

	// Exhausted retries — stream lost, not a confirmed process exit.
	uxlog.Log("stream: exhausted %d retries task=%s, treating as stream lost", maxStreamRetries, rs.taskID)
	rs.close()
	rs.client.removeSessionStreamLost(rs.taskID)
}

// streamOnce opens a single stream connection and reads until EOF or error.
// Returns (processExited, daemonDown):
//   - (true, false)  — process has exited, fire exit callback
//   - (false, false) — stream dropped but process still alive, should retry
//   - (false, true)  — daemon is unreachable, treat as stream lost
func (rs *RemoteSession) streamOnce(sockPath string) (processExited, daemonDown bool) {
	// Early exit if closed (e.g., client shutdown during daemon restart).
	// Avoids dialing the new daemon's socket with stale session IDs.
	select {
	case <-rs.done:
		return false, true
	default:
	}
	uxlog.Log("stream: connecting task=%s", rs.taskID)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		uxlog.Log("stream: DIAL FAILED task=%s err=%v", rs.taskID, err)
		alive, reachable := rs.isSessionAlive()
		if !reachable {
			return false, true
		}
		return !alive, false
	}
	defer conn.Close()

	// Send stream prefix byte.
	if _, err := conn.Write([]byte("S")); err != nil {
		uxlog.Log("stream: WRITE PREFIX FAILED task=%s err=%v", rs.taskID, err)
		alive, reachable := rs.isSessionAlive()
		if !reachable {
			return false, true
		}
		return !alive, false
	}

	// Send stream header to subscribe to this session's output.
	enc := json.NewEncoder(conn)
	if err := enc.Encode(daemon.StreamHeader{
		TaskID: rs.taskID,
	}); err != nil {
		uxlog.Log("stream: ENCODE HEADER FAILED task=%s err=%v", rs.taskID, err)
		alive, reachable := rs.isSessionAlive()
		if !reachable {
			return false, true
		}
		return !alive, false
	}

	uxlog.Log("stream: connected task=%s", rs.taskID)

	// Read output stream into local ring buffer.
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			rs.mu.Lock()
			rs.buf.Write(buf[:n])
			rs.mu.Unlock()
		}
		if err != nil {
			uxlog.Log("stream: ended task=%s err=%v", rs.taskID, err)
			break
		}
	}

	// Stream ended — check if the process actually exited.
	alive, reachable := rs.isSessionAlive()
	if !reachable {
		return false, true
	}
	return !alive, false
}

// isSessionAlive checks with the daemon whether the session's process is
// still running. Returns (alive, daemonReachable). If the RPC fails, daemon
// may be down — returns (false, false).
func (rs *RemoteSession) isSessionAlive() (alive bool, daemonReachable bool) {
	var info daemon.SessionInfo
	if err := rs.client.call("Daemon.SessionStatus", &daemon.TaskIDReq{TaskID: rs.taskID}, &info); err != nil {
		uxlog.Log("stream: SessionStatus RPC failed task=%s err=%v (daemon unreachable)", rs.taskID, err)
		return false, false
	}
	uxlog.Log("stream: SessionStatus task=%s alive=%v pid=%d", rs.taskID, info.Alive, info.PID)
	return info.Alive, true
}
