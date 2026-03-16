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
// exited or retries are exhausted.
func (rs *RemoteSession) connectStream(sockPath string) {
	for attempt := 0; attempt <= maxStreamRetries; attempt++ {
		if attempt > 0 {
			uxlog.Log("stream: retry %d/%d task=%s", attempt, maxStreamRetries, rs.taskID)
			time.Sleep(500 * time.Millisecond)
		}

		exited := rs.streamOnce(sockPath)
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
			// Session was closed (e.g., client shutdown) — don't retry.
			uxlog.Log("stream: session closed externally task=%s, not retrying", rs.taskID)
			rs.client.removeSession(rs.taskID)
			return
		default:
		}
	}

	// Exhausted retries — treat as exit to avoid silently losing the session.
	uxlog.Log("stream: exhausted %d retries task=%s, treating as exit", maxStreamRetries, rs.taskID)
	rs.close()
	rs.client.removeSession(rs.taskID)
}

// streamOnce opens a single stream connection and reads until EOF or error.
// Returns true if the process has exited (should fire exit callback),
// false if the stream dropped but the process is still alive (should retry).
func (rs *RemoteSession) streamOnce(sockPath string) (processExited bool) {
	uxlog.Log("stream: connecting task=%s", rs.taskID)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		uxlog.Log("stream: DIAL FAILED task=%s err=%v", rs.taskID, err)
		return !rs.isSessionAlive()
	}
	defer conn.Close()

	// Send stream prefix byte.
	if _, err := conn.Write([]byte("S")); err != nil {
		uxlog.Log("stream: WRITE PREFIX FAILED task=%s err=%v", rs.taskID, err)
		return !rs.isSessionAlive()
	}

	// Send stream header to subscribe to this session's output.
	enc := json.NewEncoder(conn)
	if err := enc.Encode(daemon.StreamHeader{
		TaskID: rs.taskID,
	}); err != nil {
		uxlog.Log("stream: ENCODE HEADER FAILED task=%s err=%v", rs.taskID, err)
		return !rs.isSessionAlive()
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
	return !rs.isSessionAlive()
}

// isSessionAlive checks with the daemon whether the session's process is
// still running. Returns false if the RPC fails (daemon may be down).
func (rs *RemoteSession) isSessionAlive() bool {
	var info daemon.SessionInfo
	if err := rs.client.call("Daemon.SessionStatus", &daemon.TaskIDReq{TaskID: rs.taskID}, &info); err != nil {
		uxlog.Log("stream: SessionStatus RPC failed task=%s err=%v (assuming dead)", rs.taskID, err)
		return false
	}
	uxlog.Log("stream: SessionStatus task=%s alive=%v pid=%d", rs.taskID, info.Alive, info.PID)
	return info.Alive
}
