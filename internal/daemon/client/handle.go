package client

import (
	"fmt"
	"io"
	"sync"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/uxlog"
)

const defaultBufSize = 256 * 1024 // 256KB ring buffer; session log file handles full scrollback

// Compile-time assertion.
var _ agent.SessionHandle = (*RemoteSession)(nil)

// RemoteSession implements agent.SessionHandle by proxying to the daemon.
type RemoteSession struct {
	taskID string
	client *Client

	mu      sync.Mutex
	buf     *agent.RingBuffer // local ring buffer, populated by stream reader
	pid     int
	info    daemon.SessionInfo // cached session info
	done    chan struct{}       // closed when stream EOF
	inputCh chan []byte         // async input channel for WriteInput
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
			// Drain any additional pending bytes to coalesce into one RPC.
			buf := b
			for {
				select {
				case more := <-rs.inputCh:
					buf = append(buf, more...)
				default:
					goto send
				}
			}
		send:
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

func (rs *RemoteSession) PID() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.pid
}

func (rs *RemoteSession) WriteInput(p []byte) (int, error) {
	// Copy so the caller can reuse the slice.
	cp := make([]byte, len(p))
	copy(cp, p)
	select {
	case rs.inputCh <- cp:
		return len(p), nil
	case <-rs.done:
		return 0, fmt.Errorf("session closed")
	}
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
