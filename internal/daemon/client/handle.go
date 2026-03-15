package client

import (
	"fmt"
	"io"
	"sync"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/daemon"
)

const defaultBufSize = 256 * 1024 // 256KB ring buffer, same as agent

// Compile-time assertion.
var _ agent.SessionHandle = (*RemoteSession)(nil)

// RemoteSession implements agent.SessionHandle by proxying to the daemon.
type RemoteSession struct {
	taskID string
	client *Client

	mu   sync.Mutex
	buf  *agent.RingBuffer // local ring buffer, populated by stream reader
	pid  int
	info daemon.SessionInfo // cached session info
	done chan struct{}       // closed when stream EOF
}

func newRemoteSession(taskID string, c *Client) *RemoteSession {
	return &RemoteSession{
		taskID: taskID,
		client: c,
		buf:    agent.NewRingBuffer(defaultBufSize),
		done:   make(chan struct{}),
	}
}

func (rs *RemoteSession) PID() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.pid
}

func (rs *RemoteSession) WriteInput(p []byte) (int, error) {
	var resp daemon.StatusResp
	err := rs.client.rpc.Call("Daemon.WriteInput", &daemon.WriteReq{
		TaskID: rs.taskID,
		Data:   p,
	}, &resp)
	if err != nil {
		return 0, err
	}
	if resp.Error != "" {
		return 0, fmt.Errorf("%s", resp.Error)
	}
	return len(p), nil
}

func (rs *RemoteSession) Resize(rows, cols uint16) error {
	var resp daemon.StatusResp
	err := rs.client.rpc.Call("Daemon.Resize", &daemon.ResizeReq{
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

func (rs *RemoteSession) TotalWritten() uint64 {
	rs.mu.Lock()
	defer rs.mu.Unlock()
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
	if err := rs.client.rpc.Call("Daemon.SessionStatus", &daemon.TaskIDReq{TaskID: rs.taskID}, &info); err != nil {
		return
	}
	rs.updateInfo(info)
}

// AddWriter is a no-op on RemoteSession. The daemon manages writers directly;
// the client receives output via its stream connection.
func (rs *RemoteSession) AddWriter(_ io.Writer) {}

// RemoveWriter is a no-op on RemoteSession.
func (rs *RemoteSession) RemoveWriter(_ io.Writer) {}

// close shuts down the remote session.
func (rs *RemoteSession) close() {
	select {
	case <-rs.done:
	default:
		close(rs.done)
	}
}
