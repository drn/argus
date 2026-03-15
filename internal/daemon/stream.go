package daemon

import (
	"encoding/json"
	"net"
)

// handleStream processes a stream connection. The client sends a JSON
// StreamHeader, then the daemon registers the connection as a writer on
// the session. Output flows as raw bytes until the session exits or the
// client disconnects.
func (d *Daemon) handleStream(conn net.Conn) {
	var header StreamHeader
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&header); err != nil {
		return
	}

	sess := d.runner.Get(header.TaskID)
	if sess == nil {
		return
	}

	d.registerStream(header.TaskID, conn)
	defer d.unregisterStream(header.TaskID, conn)

	// AddWriter replays the ring buffer and registers for live output.
	sess.AddWriter(conn)
	defer sess.RemoveWriter(conn)

	// Block until the session exits or the client disconnects.
	// We detect client disconnect by trying to read from the connection.
	// The client doesn't send anything on the stream after the header,
	// so a read will block until the connection is closed.
	select {
	case <-sess.Done():
		// Session exited — the onFinish callback closes the connection.
	case <-d.done:
		// Daemon shutting down.
	case <-waitForClose(conn):
		// Client disconnected.
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
