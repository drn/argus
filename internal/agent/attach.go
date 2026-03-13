package agent

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

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

// headerWriter wraps a writer. It draws the Argus header once at attach time
// but does NOT redraw on every write — doing so causes cursor-jumping
// interference that corrupts the display, especially when the child process
// uses an alternate screen buffer (e.g. Claude Code).
type headerWriter struct {
	inner    io.Writer
	taskName string
	cols     int
	rows     int
}

func (hw *headerWriter) Write(p []byte) (int, error) {
	return hw.inner.Write(p)
}

func (hw *headerWriter) drawHeader() {
	// Layout: " <task> ··· ◁〈❮ ARGUS ❯〉▷ ··· ctrl+q detach "
	// ARGUS is centered with flame-like symbols on each side.
	task := hw.taskName
	hint := "ctrl+q detach"

	// Flame symbols flanking ARGUS (outer → inner)
	flameL := "░▒▓▞▚"
	flameR := "▚▞▓▒░"
	center := flameL + " ARGUS " + flameR
	centerW := utf8.RuneCountInString(center)

	// Left side: " <task> " — right side: " <hint> "
	leftFixed := 2  // leading space + trailing space around task
	rightFixed := 3 // spaces around hint + trailing space

	// Width available for task and hint after center and padding
	availSides := hw.cols - centerW - 2 // 2 for spaces flanking center
	if availSides < 0 {
		availSides = 0
	}

	// Split available space: left gets ~60%, right gets ~40%
	leftAvail := availSides*6/10 - leftFixed
	rightAvail := availSides - leftAvail - leftFixed - rightFixed
	if leftAvail < 0 {
		leftAvail = 0
	}
	if rightAvail < 0 {
		rightAvail = 0
	}

	// Truncate task name
	taskRunes := []rune(task)
	if len(taskRunes) > leftAvail {
		if leftAvail > 3 {
			task = string(taskRunes[:leftAvail-3]) + "..."
		} else {
			task = string(taskRunes[:leftAvail])
		}
	}

	// Truncate hint
	if utf8.RuneCountInString(hint) > rightAvail {
		if rightAvail > 3 {
			hintRunes := []rune(hint)
			hint = string(hintRunes[:rightAvail-3]) + "..."
		} else {
			hint = string([]rune(hint)[:rightAvail])
		}
	}

	// Build left and right sections with padding
	leftContent := " " + task + " "
	rightContent := " " + hint + " "

	leftPad := availSides/2 + 1 - utf8.RuneCountInString(leftContent)
	rightPad := hw.cols - utf8.RuneCountInString(leftContent) - leftPad - centerW - utf8.RuneCountInString(rightContent)
	if leftPad < 0 {
		leftPad = 0
	}
	if rightPad < 0 {
		rightPad = 0
	}

	leftSide := leftContent + strings.Repeat(" ", leftPad)
	rightSide := strings.Repeat(" ", rightPad) + rightContent

	// ANSI 256-color: bg=235
	// task in 252, flames gradient (208→196→87→196→208), ARGUS in bold cyan(87), hint in 241
	line := fmt.Sprintf(
		"\x1b[1;1H\x1b[2K"+ // move to row 1, clear line
			"\x1b[48;5;235m"+ // bg
			"\x1b[38;5;252m%s"+ // task (left side)
			"\x1b[38;5;208m░▒\x1b[38;5;202m▓\x1b[38;5;196m▞\x1b[38;5;87m▚"+ // left flames (orange→red→cyan)
			"\x1b[1;38;5;87m ARGUS "+ // bold cyan ARGUS
			"\x1b[22;38;5;87m▚\x1b[38;5;196m▞\x1b[38;5;202m▓\x1b[38;5;208m▒░"+ // right flames (cyan→red→orange)
			"\x1b[38;5;241m%s"+ // hint (right side)
			"\x1b[K"+ // fill rest of line with bg color
			"\x1b[0m", // reset
		leftSide, rightSide,
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
