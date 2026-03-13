package agent

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// AttachCmd implements tea.ExecCommand for attaching to a running session.
// When executed by Bubble Tea, it takes over the terminal and splices I/O
// with the session's PTY. Returns nil on detach, process error on exit.
type AttachCmd struct {
	Session *Session
	origTTY *unix.Termios
}

// Run is called by Bubble Tea's tea.Exec. It puts the terminal into raw mode,
// resizes the PTY, and splices stdin/stdout until detach or process exit.
func (a *AttachCmd) Run() error {
	// Put terminal into raw mode
	fd := int(os.Stdin.Fd())
	orig, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err == nil {
		a.origTTY = orig
		raw := *orig
		raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
		raw.Oflag &^= unix.OPOST
		raw.Cflag |= unix.CS8
		raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
		raw.Cc[unix.VMIN] = 1
		raw.Cc[unix.VTIME] = 0
		unix.IoctlSetTermios(fd, unix.TIOCSETA, &raw)
	}

	// Match PTY size to current terminal
	if ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ); err == nil {
		a.Session.Resize(ws.Row, ws.Col)
	}

	// Use a detach-aware reader that watches for ctrl+a d
	dr := &detachReader{
		reader:  os.Stdin,
		session: a.Session,
	}

	return a.Session.Attach(dr, os.Stdout)
}

// SetStdin is required by tea.ExecCommand but we use os.Stdin directly
// since we need the raw file descriptor.
func (a *AttachCmd) SetStdin(_ io.Reader) {}

// SetStdout is required by tea.ExecCommand.
func (a *AttachCmd) SetStdout(_ io.Writer) {}

// SetStderr is required by tea.ExecCommand.
func (a *AttachCmd) SetStderr(_ io.Writer) {}

// detachReader wraps stdin and intercepts the detach sequence (ctrl+a then d).
type detachReader struct {
	reader    io.Reader
	session   *Session
	sawCtrlA  bool
}

func (dr *detachReader) Read(p []byte) (int, error) {
	n, err := dr.reader.Read(p)
	if n > 0 {
		// Scan for detach sequence: ctrl+a (0x01) followed by 'd'
		for i := 0; i < n; i++ {
			if dr.sawCtrlA {
				dr.sawCtrlA = false
				if p[i] == 'd' {
					// Detach: remove the ctrl+a and d from output,
					// signal detach, and return what we have so far
					dr.session.Detach()
					// Return bytes before ctrl+a (which was in previous read)
					copy(p[i:], p[i+1:])
					return n - 1, io.EOF
				}
			}
			if p[i] == 0x01 { // ctrl+a
				dr.sawCtrlA = true
				// Remove ctrl+a from buffer, pass through on next read if not 'd'
				copy(p[i:], p[i+1:])
				n--
				i--
			}
		}
	}
	return n, err
}
