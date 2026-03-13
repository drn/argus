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
	attachW  io.Writer // when non-nil, readLoop tees output here
	done     chan struct{}
	err      error
	attached bool
	detachCh chan struct{}
	ptyCols  uint16 // current PTY width
	ptyRows  uint16 // current PTY height
}

// StartSession allocates a PTY with the given initial size, starts the command,
// and begins reading output into a ring buffer.
func StartSession(taskID string, cmd *exec.Cmd, rows, cols uint16) (*Session, error) {
	size := &pty.Winsize{Rows: rows, Cols: cols}
	if rows == 0 || cols == 0 {
		size = &pty.Winsize{Rows: 24, Cols: 80}
		rows = 24
		cols = 80
	}

	ptmx, err := pty.StartWithSize(cmd, size)
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
		ptyCols:  cols,
		ptyRows:  rows,
	}

	// Single reader: PTY → ring buffer (+ attached writer when set)
	go s.readLoop()

	// Background waiter: process exit
	go s.waitLoop()

	return s, nil
}

// readLoop is the sole reader of the PTY. It always writes to the ring buffer,
// and when a writer is attached, also tees output there.
func (s *Session) readLoop() {
	tmp := make([]byte, 4096)
	for {
		n, err := s.ptmx.Read(tmp)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, tmp[:n])
			s.mu.Lock()
			s.buf.Write(chunk)
			w := s.attachW
			s.mu.Unlock()
			if w != nil {
				w.Write(chunk)
			}
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

	// Replay buffered output so user sees recent context,
	// then set tee writer — all under one lock so no data is lost.
	replay := s.buf.Bytes()
	s.attachW = stdout
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.attachW = nil
		s.attached = false
		s.mu.Unlock()
	}()

	if len(replay) > 0 {
		stdout.Write(replay)
	}

	errCh := make(chan error, 1)

	// stdin → PTY (only direction we need a goroutine for)
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

// Stop sends SIGTERM.
func (s *Session) Stop() error {
	if !s.Alive() {
		return nil
	}
	return s.Signal(syscall.SIGTERM)
}

// RecentOutput returns the last n bytes from the ring buffer.
// Safe to call concurrently.
func (s *Session) RecentOutput() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Bytes()
}

// WorkDir returns the effective working directory of the session.
// Returns Cmd.Dir if set, otherwise falls back to the process's inherited cwd.
func (s *Session) WorkDir() string {
	if s.Cmd.Dir != "" {
		return s.Cmd.Dir
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return ""
}

// Resize sets the PTY window size.
func (s *Session) Resize(rows, cols uint16) error {
	s.mu.Lock()
	s.ptyCols = cols
	s.ptyRows = rows
	s.mu.Unlock()
	return pty.Setsize(s.ptmx, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}

// PTYSize returns the current PTY dimensions (cols, rows).
func (s *Session) PTYSize() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int(s.ptyCols), int(s.ptyRows)
}
