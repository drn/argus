package agent

import (
	"io"
	"os"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// AttachCmd implements tea.ExecCommand for attaching to a running session.
// When executed by Bubble Tea, it takes over the terminal and splices I/O
// with the session's PTY. Returns nil on detach, process error on exit.
type AttachCmd struct {
	Session *Session
	stdin   io.Reader
	stdout  io.Writer
}

// SetStdin captures the reader provided by Bubble Tea.
func (a *AttachCmd) SetStdin(r io.Reader) { a.stdin = r }

// SetStdout captures the writer provided by Bubble Tea.
func (a *AttachCmd) SetStdout(w io.Writer) { a.stdout = w }

// SetStderr is required by tea.ExecCommand.
func (a *AttachCmd) SetStderr(_ io.Writer) {}

// Run is called by Bubble Tea's tea.Exec. It puts the terminal into raw mode,
// resizes the PTY, and splices stdin/stdout until detach or process exit.
func (a *AttachCmd) Run() error {
	// Fall back to os std streams if Bubble Tea didn't provide them
	if a.stdin == nil {
		a.stdin = os.Stdin
	}
	if a.stdout == nil {
		a.stdout = os.Stdout
	}

	// Put terminal into raw mode so keypresses pass through immediately
	fd := os.Stdin.Fd()
	prev, err := term.MakeRaw(fd)
	if err == nil {
		defer term.Restore(fd, prev)
	}

	// Match PTY size to current terminal
	if ws, wsErr := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ); wsErr == nil {
		a.Session.Resize(ws.Row, ws.Col)
	}

	// Use a detach-aware reader that watches for ctrl+a d
	dr := &detachReader{
		reader:  a.stdin,
		session: a.Session,
	}

	return a.Session.Attach(dr, a.stdout)
}

// detachReader wraps stdin and intercepts the detach sequence (ctrl+a then d).
type detachReader struct {
	reader   io.Reader
	session  *Session
	sawCtrlA bool
}

func (dr *detachReader) Read(p []byte) (int, error) {
	n, err := dr.reader.Read(p)
	if n > 0 {
		// Scan for detach sequence: ctrl+a (0x01) followed by 'd'
		for i := 0; i < n; i++ {
			if dr.sawCtrlA {
				dr.sawCtrlA = false
				if p[i] == 'd' {
					dr.session.Detach()
					copy(p[i:], p[i+1:])
					return n - 1, io.EOF
				}
			}
			if p[i] == 0x01 { // ctrl+a
				dr.sawCtrlA = true
				copy(p[i:], p[i+1:])
				n--
				i--
			}
		}
	}
	return n, err
}
