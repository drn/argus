package terminalpane

import (
	"bytes"

	"github.com/gdamore/tcell/v2"
	"github.com/hinshun/vt10x"
)

// TerminalSession is the narrow interface the terminal pane needs to display
// a running agent session. Satisfied by agent.SessionHandle.
type TerminalSession interface {
	RecentOutput() []byte
	TotalWritten() uint64
	Alive() bool
	PTYSize() (cols, rows int)
}

// Pane renders a vt10x terminal directly to a tcell.Screen region.
// This is the native terminal passthrough: PTY output goes through vt10x
// for state tracking, then cells are painted directly to the screen
// without any ANSI string intermediary.
//
// Pane is NOT a tview.Primitive — it renders into a caller-specified region.
// The caller (AgentPane) owns the tview.Box, border, and placeholder logic.
type Pane struct {
	session TerminalSession

	// Persistent vt10x for live mode (follow tail)
	vt       vt10x.Terminal
	vtCols   int
	vtRows   int
	fedTotal uint64

	// Scrollback
	scrollOffset int

	// Replay data for finished sessions (loaded from session log file)
	replayData []byte
}

// New creates a terminal pane.
func New() *Pane {
	return &Pane{}
}

// SetSession attaches a session for display. Resets vt10x state.
func (p *Pane) SetSession(sess TerminalSession) {
	p.session = sess
	p.vt = nil
	p.fedTotal = 0
	p.scrollOffset = 0
}

// Session returns the current session, or nil.
func (p *Pane) Session() TerminalSession {
	return p.session
}

// SetReplayData sets raw PTY output for replay rendering (finished sessions).
func (p *Pane) SetReplayData(data []byte) {
	p.replayData = data
}

// ScrollUp scrolls the terminal view up by n lines.
func (p *Pane) ScrollUp(n int) {
	p.scrollOffset += n
}

// ScrollDown scrolls toward the tail by n lines.
func (p *Pane) ScrollDown(n int) {
	p.scrollOffset -= n
	if p.scrollOffset < 0 {
		p.scrollOffset = 0
	}
}

// ScrollToBottom resets scroll to follow tail.
func (p *Pane) ScrollToBottom() {
	p.scrollOffset = 0
}

// ScrollOffset returns the current scroll offset.
func (p *Pane) ScrollOffset() int {
	return p.scrollOffset
}

// HasContent returns true if there is something to render.
func (p *Pane) HasContent() bool {
	if p.session != nil {
		return p.session.TotalWritten() > 0
	}
	return len(p.replayData) > 0
}

// Render paints the terminal content onto the given screen region.
// (x, y) is the top-left corner, w and h are the usable content dimensions.
func (p *Pane) Render(screen tcell.Screen, x, y, w, h int) {
	if w <= 0 || h <= 0 {
		return
	}

	var raw []byte
	var ptyCols, ptyRows int
	alive := false

	if p.session != nil {
		ptyCols, ptyRows = p.session.PTYSize()
		alive = p.session.Alive()
		raw = p.session.RecentOutput()
	} else if len(p.replayData) > 0 {
		raw = p.replayData
	}

	if len(raw) == 0 {
		return
	}

	if ptyCols < 20 {
		ptyCols = w
	}
	if ptyRows < 5 {
		ptyRows = h
	}

	if p.scrollOffset > 0 || !alive {
		p.renderReplay(screen, x, y, w, h, raw, ptyCols)
	} else {
		p.renderLive(screen, x, y, w, h, raw, ptyCols, ptyRows)
	}
}

// renderLive feeds only new bytes to a persistent vt10x terminal and renders
// the current screen state directly to tcell cells. This is the core
// passthrough path — O(screen_size) per render, not O(buffer_size).
func (p *Pane) renderLive(screen tcell.Screen, x, y, w, h int, raw []byte, ptyCols, ptyRows int) {
	totalWritten := uint64(0)
	if p.session != nil {
		totalWritten = p.session.TotalWritten()
	}

	// Initialize or reset vt10x if dimensions changed
	if p.vt == nil || p.vtCols != ptyCols || p.vtRows != ptyRows {
		p.vt = vt10x.New(vt10x.WithSize(ptyCols, ptyRows))
		p.fedTotal = 0
		p.vtCols = ptyCols
		p.vtRows = ptyRows
	}

	// Feed only new bytes
	newBytes := totalWritten - p.fedTotal
	if newBytes > uint64(len(raw)) {
		// Ring buffer wrapped — full reset
		p.vt = vt10x.New(vt10x.WithSize(ptyCols, ptyRows))
		p.vt.Write(raw)
	} else if newBytes > 0 {
		p.vt.Write(raw[len(raw)-int(newBytes):])
	}
	p.fedTotal = totalWritten

	// Render vt10x screen to tcell
	p.vt.Lock()
	defer p.vt.Unlock()

	cur := p.vt.Cursor()

	// Find content bounds — trim trailing empty rows
	lastContentRow := findLastContentRow(p.vt, ptyCols, ptyRows)
	if cur.Y > lastContentRow {
		lastContentRow = cur.Y
	}

	// Find first content row (e.g. Codex positions content in lower portion)
	firstContentRow := findFirstContentRow(p.vt, ptyCols, lastContentRow)

	// If content fits in display, show from first content row.
	// If content exceeds display, show tail (follow).
	startRow := firstContentRow
	contentRows := lastContentRow - firstContentRow + 1
	if contentRows > h {
		startRow = lastContentRow - h + 1
	}

	renderCols := min(ptyCols, w)
	for screenRow := 0; screenRow < h; screenRow++ {
		vtRow := startRow + screenRow
		if vtRow > lastContentRow {
			break
		}
		for col := 0; col < renderCols; col++ {
			cell := p.vt.Cell(col, vtRow)
			ch := cell.Char
			if ch == 0 {
				ch = ' '
			}

			style := cellStyle(cell)

			// Cursor — always render regardless of CursorVisible() since
			// TUI agents like Claude Code hide the hardware cursor.
			if vtRow == cur.Y && col == cur.X {
				style = tcell.StyleDefault.
					Foreground(CursorFG).
					Background(CursorBG)
			}

			screen.SetContent(x+col, y+screenRow, ch, nil, style)
		}
	}
}

// renderReplay replays the full ring buffer through a tall vt10x terminal
// and renders a scrollable window. Used for scrollback and finished sessions.
func (p *Pane) renderReplay(screen tcell.Screen, x, y, w, h int, raw []byte, vtCols int) {
	vtRows := estimateVTRows(raw, vtCols, h)
	vt := vt10x.New(vt10x.WithSize(vtCols, vtRows))
	vt.Write(raw)

	vt.Lock()
	defer vt.Unlock()

	cur := vt.Cursor()

	// Find content bounds
	lastContentRow := findLastContentRow(vt, vtCols, vtRows)
	if cur.Y > lastContentRow {
		lastContentRow = cur.Y
	}
	if lastContentRow < 0 {
		return
	}

	firstContentRow := findFirstContentRow(vt, vtCols, lastContentRow)
	totalLines := lastContentRow - firstContentRow + 1

	// Clamp scroll offset
	maxScroll := totalLines - h
	if maxScroll < 0 {
		maxScroll = 0
	}
	if p.scrollOffset > maxScroll {
		p.scrollOffset = maxScroll
	}

	// Compute visible window
	endRow := lastContentRow - p.scrollOffset
	if endRow < firstContentRow {
		endRow = firstContentRow
	}
	startRow := endRow - h + 1
	if startRow < firstContentRow {
		startRow = firstContentRow
	}

	renderCols := min(vtCols, w)
	for screenRow := 0; screenRow < h; screenRow++ {
		vtRow := startRow + screenRow
		if vtRow > endRow {
			break
		}
		for col := 0; col < renderCols; col++ {
			cell := vt.Cell(col, vtRow)
			ch := cell.Char
			if ch == 0 {
				ch = ' '
			}

			style := cellStyle(cell)

			// Only show cursor at bottom (not scrolled back)
			if p.scrollOffset == 0 && vtRow == cur.Y && col == cur.X {
				style = tcell.StyleDefault.
					Foreground(CursorFG).
					Background(CursorBG)
			}

			screen.SetContent(x+col, y+screenRow, ch, nil, style)
		}
	}
}

// findLastContentRow scans backwards to find the last row with visible content.
func findLastContentRow(vt vt10x.Terminal, cols, rows int) int {
	for row := rows - 1; row >= 0; row-- {
		if rowHasContent(vt, row, cols) {
			return row
		}
	}
	return -1
}

// findFirstContentRow scans forward to find the first row with content.
func findFirstContentRow(vt vt10x.Terminal, cols, maxRow int) int {
	for row := 0; row <= maxRow; row++ {
		if rowHasContent(vt, row, cols) {
			return row
		}
	}
	return 0
}

// rowHasContent returns true if any cell in the row has visible content.
func rowHasContent(vt vt10x.Terminal, row, cols int) bool {
	for x := 0; x < cols; x++ {
		cell := vt.Cell(x, row)
		if cell.Char != 0 && cell.Char != ' ' {
			return true
		}
		if cell.FG != vt10x.DefaultFG || cell.BG != vt10x.DefaultBG || cell.Mode != 0 {
			return true
		}
	}
	return false
}

// estimateVTRows estimates how many rows a vt10x terminal needs to capture
// all output, given the raw bytes and display dimensions.
func estimateVTRows(raw []byte, vtCols, dispH int) int {
	vtRows := dispH
	if n := bytes.Count(raw, []byte{'\n'}); n > vtRows {
		vtRows = n + dispH
	}
	if vtCols > 0 {
		wrappedEstimate := len(raw)/vtCols + dispH
		if wrappedEstimate > vtRows {
			vtRows = wrappedEstimate
		}
	}
	return vtRows
}
