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

	// Set raw input (no echo, no canonical, no signals) but keep output
	// processing (OPOST) so \n → \r\n conversion works. term.MakeRaw
	// clears OPOST which causes garbled output from PTY children.
	fd := int(os.Stdin.Fd())
	orig, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err == nil {
		raw := *orig
		// Raw input flags
		raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
		raw.Cflag |= unix.CS8
		raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
		raw.Cc[unix.VMIN] = 1
		raw.Cc[unix.VTIME] = 0
		// Keep OPOST in Oflag — do NOT clear it
		unix.IoctlSetTermios(fd, ioctlSetTermios, &raw)
		defer unix.IoctlSetTermios(fd, ioctlSetTermios, orig)
	}

	// Clear screen before attaching
	a.stdout.Write([]byte("\x1b[2J\x1b[H"))

	// Match PTY size to current terminal
	if ws, wsErr := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ); wsErr == nil {
		a.Session.Resize(ws.Row, ws.Col)
	}

	// Use a detach-aware reader that watches for ctrl+q
	dr := &detachReader{
		reader:  a.stdin,
		session: a.Session,
	}

	return a.Session.Attach(dr, a.stdout)
}

// detachReader wraps stdin and intercepts ctrl+q (0x11) to detach.
type detachReader struct {
	reader  io.Reader
	session *Session
}

func (dr *detachReader) Read(p []byte) (int, error) {
	n, err := dr.reader.Read(p)
	if n > 0 {
		for i := 0; i < n; i++ {
			if p[i] == 0x11 { // ctrl+q
				dr.session.Detach()
				copy(p[i:], p[i+1:])
				return n - 1, io.EOF
			}
		}
	}
	return n, err
}
