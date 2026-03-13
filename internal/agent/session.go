package agent

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

const defaultBufSize = 256 * 1024 // 256KB ring buffer

// Session manages a single agent process with PTY.
type Session struct {
	TaskID string
	Cmd    *exec.Cmd
	ptmx   *os.File // PTY master

	mu       sync.Mutex
	buf      *ringBuffer
	done     chan struct{} // closed when process exits
	err      error        // exit error
	attached bool
	detachCh chan struct{} // signal to stop splice
}

// StartSession allocates a PTY, starts the command, and begins
// reading output into a ring buffer.
func StartSession(taskID string, cmd *exec.Cmd) (*Session, error) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	s := &Session{
		TaskID:   taskID,
		Cmd:      cmd,
		ptmx:     ptmx,
		buf:      newRingBuffer(defaultBufSize),
		done:     make(chan struct{}),
		detachCh: make(chan struct{}),
	}

	// Background reader: PTY output → ring buffer
	go s.readLoop()

	// Background waiter: process exit
	go s.waitLoop()

	return s, nil
}

// readLoop copies PTY output into the ring buffer until EOF.
func (s *Session) readLoop() {
	tmp := make([]byte, 4096)
	for {
		n, err := s.ptmx.Read(tmp)
		if n > 0 {
			s.mu.Lock()
			s.buf.Write(tmp[:n])
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// waitLoop waits for the process to exit and cleans up.
func (s *Session) waitLoop() {
	s.err = s.Cmd.Wait()
	s.ptmx.Close()
	close(s.done)
}

// Done returns a channel that is closed when the process exits.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// Err returns the process exit error (nil if exited 0).
func (s *Session) Err() error {
	return s.err
}

// Alive returns true if the process is still running.
func (s *Session) Alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// PID returns the process ID, or 0 if not started.
func (s *Session) PID() int {
	if s.Cmd.Process != nil {
		return s.Cmd.Process.Pid
	}
	return 0
}

// Attach splices the PTY to the given stdin/stdout.
// Blocks until detach is called, the process exits, or an error occurs.
// Returns nil on detach, the process error on exit.
func (s *Session) Attach(stdin io.Reader, stdout io.Writer) error {
	s.mu.Lock()
	if s.attached {
		s.mu.Unlock()
		return ErrAlreadyAttached
	}
	s.attached = true
	s.detachCh = make(chan struct{})
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.attached = false
		s.mu.Unlock()
	}()

	// Replay buffered output so user sees recent context
	s.mu.Lock()
	replay := s.buf.Bytes()
	s.mu.Unlock()
	if len(replay) > 0 {
		stdout.Write(replay)
	}

	errCh := make(chan error, 2)

	// PTY → stdout
	go func() {
		_, err := io.Copy(stdout, s.ptmx)
		errCh <- err
	}()

	// stdin → PTY
	go func() {
		_, err := io.Copy(s.ptmx, stdin)
		errCh <- err
	}()

	select {
	case <-s.detachCh:
		return nil
	case <-s.done:
		return s.err
	case err := <-errCh:
		// Check if process exited
		select {
		case <-s.done:
			return s.err
		default:
			return err
		}
	}
}

// Detach stops an active Attach splice. Safe to call if not attached.
func (s *Session) Detach() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attached {
		select {
		case <-s.detachCh:
		default:
			close(s.detachCh)
		}
	}
}

// Signal sends a signal to the process.
func (s *Session) Signal(sig os.Signal) error {
	if s.Cmd.Process == nil {
		return ErrNotRunning
	}
	return s.Cmd.Process.Signal(sig)
}

// Stop sends SIGTERM, then SIGKILL if still alive after the channel fires.
func (s *Session) Stop() error {
	if !s.Alive() {
		return nil
	}
	return s.Signal(syscall.SIGTERM)
}

// Resize sets the PTY window size.
func (s *Session) Resize(rows, cols uint16) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}
