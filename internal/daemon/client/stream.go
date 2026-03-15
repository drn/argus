package client

import (
	"encoding/json"
	"net"

	"github.com/drn/argus/internal/daemon"
)

// connectStream opens a stream connection to the daemon for this session's output.
// Reads raw bytes and writes them to the local ring buffer. Closes rs.done on EOF.
func (rs *RemoteSession) connectStream(sockPath string) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		rs.client.removeSession(rs.taskID)
		return
	}
	defer conn.Close()

	// Send stream prefix byte.
	if _, err := conn.Write([]byte("S")); err != nil {
		rs.client.removeSession(rs.taskID)
		return
	}

	// Send stream header to subscribe to this session's output.
	enc := json.NewEncoder(conn)
	if err := enc.Encode(daemon.StreamHeader{
		TaskID: rs.taskID,
	}); err != nil {
		rs.client.removeSession(rs.taskID)
		return
	}

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
			break // EOF or connection error — treat as session exit
		}
	}

	// Stream ended — session exited or daemon shut down.
	rs.close()
	rs.client.removeSession(rs.taskID)
}
