package agent

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const defaultBufSize = 0 // unbounded: keep all output for full scrollback

// idleThreshold is how long without output before a session is considered idle.
const idleThreshold = 3 * time.Second

// DefaultTermRows and DefaultTermCols are fallback PTY dimensions.
const (
	DefaultTermRows uint16 = 24
	DefaultTermCols uint16 = 80
)

// Session manages a single agent process with PTY.
type Session struct {
	TaskID string
	Cmd    *exec.Cmd
	ptmx   *os.File // PTY master

	mu         sync.Mutex
	buf        *RingBuffer
	writers    []io.Writer // readLoop tees output to all attached writers
	done       chan struct{}
	err        error
	attached   bool
	detachCh   chan struct{}
	ptyCols    uint16    // current PTY width
	ptyRows    uint16    // current PTY height
	lastOutput time.Time // last time output was received from PTY

	logFile *os.File // PTY output log for post-session scrollback; nil if unavailable
}

// SessionLogPath returns the path to the PTY output log file for a task.
func SessionLogPath(taskID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".argus", "sessions", taskID+".log")
}

// StartSession allocates a PTY with the given initial size, starts the command,
// and begins reading output into a ring buffer.
func StartSession(taskID string, cmd *exec.Cmd, rows, cols uint16) (*Session, error) {
	size := &pty.Winsize{Rows: rows, Cols: cols}
	if rows == 0 || cols == 0 {
		size = &pty.Winsize{Rows: DefaultTermRows, Cols: DefaultTermCols}
		rows = DefaultTermRows
		cols = DefaultTermCols
	}

	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return nil, err
	}

	// Open log file for PTY output — best effort, nil if unavailable.
	var logFile *os.File
	logPath := SessionLogPath(taskID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
		logFile, _ = os.Create(logPath)
	}

	s := &Session{
		TaskID:   taskID,
		Cmd:      cmd,
		ptmx:     ptmx,
		buf:      NewRingBuffer(defaultBufSize),
		done:     make(chan struct{}),
		detachCh: make(chan struct{}),
		ptyCols:  cols,
		ptyRows:  rows,
		logFile:  logFile,
	}

	// Single reader: PTY → ring buffer (+ attached writer when set)
	go s.readLoop()

	// Background waiter: process exit
	go s.waitLoop()

	return s, nil
}

// readLoop is the sole reader of the PTY. It always writes to the ring buffer,
// tees output to all attached writers, and appends to the session log file.
func (s *Session) readLoop() {
	if s.logFile != nil {
		defer s.logFile.Close()
	}
	tmp := make([]byte, 4096)
	for {
		n, err := s.ptmx.Read(tmp)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, tmp[:n])
			s.mu.Lock()
			s.buf.Write(chunk)
			s.lastOutput = time.Now()
			// Copy writer slice under lock, iterate outside lock.
			ws := make([]io.Writer, len(s.writers))
			copy(ws, s.writers)
			s.mu.Unlock()
			// Write to log file — sole writer, no lock needed.
			if s.logFile != nil {
				s.logFile.Write(chunk) //nolint:errcheck // best-effort
			}
			// Write to all attached writers, collect any that error.
			var failed []io.Writer
			for _, w := range ws {
				if _, werr := w.Write(chunk); werr != nil {
					failed = append(failed, w)
				}
			}
			if len(failed) > 0 {
				s.mu.Lock()
				for _, f := range failed {
					s.removeWriterLocked(f)
				}
				s.mu.Unlock()
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

// IsIdle returns true if the session is alive but has not produced output recently,
// indicating the agent is likely waiting for user input.
func (s *Session) IsIdle() bool {
	if !s.Alive() {
		return false
	}
	s.mu.Lock()
	last := s.lastOutput
	s.mu.Unlock()
	if last.IsZero() {
		return false // still starting up
	}
	return time.Since(last) >= idleThreshold
}

// PID returns the process ID, or 0 if not started.
func (s *Session) PID() int {
	if s.Cmd.Process != nil {
		return s.Cmd.Process.Pid
	}
	return 0
}

// AddWriter registers a writer to receive PTY output. The writer immediately
// receives a replay of the ring buffer contents, then receives live output.
// Replay is sent before registering the writer to avoid duplicate bytes:
// if we registered first, readLoop could deliver live bytes to w before
// the replay is sent, causing the same bytes to appear twice.
// Safe to call concurrently.
func (s *Session) AddWriter(w io.Writer) {
	s.mu.Lock()
	replay := s.buf.Bytes()
	s.mu.Unlock()

	// Send replay outside lock but BEFORE registering the writer.
	// Any bytes produced by readLoop during this window are missed
	// (they're in the ring buffer but not yet in replay and w isn't
	// registered yet). This creates a small gap rather than a duplicate.
	// The gap is acceptable: the vt10x terminal handles missing bytes
	// gracefully (partial escape sequences are ignored), whereas
	// duplicate bytes cause visible rendering corruption.
	if len(replay) > 0 {
		w.Write(replay)
	}

	// Now register for live output. Any bytes that arrived during
	// replay will be in the ring buffer for future RecentOutput() calls.
	s.mu.Lock()
	s.writers = append(s.writers, w)
	s.mu.Unlock()
}

// RemoveWriter unregisters a writer. Safe to call concurrently.
func (s *Session) RemoveWriter(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeWriterLocked(w)
}

// removeWriterLocked removes a writer from the slice. Caller must hold s.mu.
func (s *Session) removeWriterLocked(w io.Writer) {
	for i, existing := range s.writers {
		if existing == w {
			s.writers = append(s.writers[:i], s.writers[i+1:]...)
			return
		}
	}
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

	// AddWriter replays buffered output and registers for live output.
	s.AddWriter(stdout)

	defer func() {
		s.RemoveWriter(stdout)
		s.mu.Lock()
		s.attached = false
		s.mu.Unlock()
	}()

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

// TotalWritten returns the monotonic count of bytes written to the ring buffer.
// Safe to call concurrently. Used to detect new output without copying the buffer.
func (s *Session) TotalWritten() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.TotalWritten()
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

// WriteInput writes raw bytes to the PTY master (stdin of the child process).
// Used by the agent view to forward keyboard input without full Attach.
func (s *Session) WriteInput(p []byte) (int, error) {
	return s.ptmx.Write(p)
}
