package agent

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// headerHeight is the number of terminal rows reserved for the Argus header.
const headerHeight = 1

// AttachCmd implements tea.ExecCommand for attaching to a running session.
// When executed by Bubble Tea, it takes over the terminal and splices I/O
// with the session's PTY. Returns nil on detach, process error on exit.
type AttachCmd struct {
	Session  *Session
	TaskName string
	stdin    io.Reader
	stdout   io.Writer
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

	// Get terminal size
	var rows, cols uint16
	if ws, wsErr := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ); wsErr == nil {
		rows = ws.Row
		cols = ws.Col
	} else {
		rows = 24
		cols = 80
	}

	// Clear screen, draw header, set scroll region below header
	hw := &headerWriter{
		inner:    a.stdout,
		taskName: a.TaskName,
		cols:     int(cols),
		rows:     int(rows),
	}
	a.stdout.Write([]byte("\x1b[2J\x1b[H")) // clear screen
	hw.drawHeader()
	// Set scroll region from row 2 to bottom, position cursor there
	a.stdout.Write([]byte(fmt.Sprintf("\x1b[%d;%dr\x1b[%d;1H", headerHeight+1, rows, headerHeight+1)))

	// Resize PTY to fit below header
	a.Session.Resize(rows-headerHeight, cols)

	// Restore full scroll region on exit
	defer func() {
		a.stdout.Write([]byte("\x1b[r"))
	}()

	// Use a detach-aware reader that watches for ctrl+q
	dr := &detachReader{
		reader:  a.stdin,
		session: a.Session,
	}

	return a.Session.Attach(dr, hw)
}

// headerWriter wraps a writer and redraws the Argus header bar after each
// write, ensuring the header persists even if the child clears the screen.
type headerWriter struct {
	inner    io.Writer
	taskName string
	cols     int
	rows     int
}

func (hw *headerWriter) Write(p []byte) (int, error) {
	n, err := hw.inner.Write(p)
	// Save cursor, redraw header, restore scroll region, restore cursor
	hw.inner.Write([]byte("\x1b7"))       // save cursor
	hw.drawHeader()                       // redraw header at row 1
	hw.inner.Write([]byte(fmt.Sprintf(    // re-establish scroll region
		"\x1b[%d;%dr", headerHeight+1, hw.rows)))
	hw.inner.Write([]byte("\x1b8")) // restore cursor
	return n, err
}

func (hw *headerWriter) drawHeader() {
	// Build header: " ARGUS │ <task> ····· ctrl+q detach "
	label := " ARGUS "
	sep := " │ "
	task := hw.taskName
	hint := " ctrl+q detach "

	// Truncate task name if needed
	fixed := len(label) + len(sep) + len(hint)
	maxTask := hw.cols - fixed
	if maxTask < 0 {
		maxTask = 0
	}
	if len(task) > maxTask {
		if maxTask > 3 {
			task = task[:maxTask-3] + "..."
		} else {
			task = task[:maxTask]
		}
	}

	// Pad middle to fill width
	middle := task
	padLen := hw.cols - fixed - len(task)
	if padLen > 0 {
		middle = task + strings.Repeat(" ", padLen)
	}

	// ANSI 256-color: bg=235, ARGUS in bold cyan(87), sep/task in 245, hint in 241
	line := fmt.Sprintf(
		"\x1b[1;1H\x1b[2K"+ // move to row 1, clear line
			"\x1b[48;5;235m"+ // bg
			"\x1b[1;38;5;87m%s"+ // bold cyan ARGUS
			"\x1b[22;38;5;240m%s"+ // dim separator
			"\x1b[38;5;252m%s"+ // normal task name
			"\x1b[38;5;241m%s"+ // dim hint
			"\x1b[0m", // reset
		label, sep, middle, hint,
	)
	hw.inner.Write([]byte(line))
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
