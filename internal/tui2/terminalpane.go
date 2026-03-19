package tui2

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/hinshun/vt10x"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/uxlog"
)

// vt10x attribute bit flags (redefined locally — no BT dependency).
const (
	vtAttrReverse   = 1 << 0
	vtAttrUnderline = 1 << 1
	vtAttrBold      = 1 << 2
	vtAttrItalic    = 1 << 4
)

// Cursor colors — high-contrast, theme-independent.
var (
	cursorFG = tcell.PaletteColor(17)  // dark blue
	cursorBG = tcell.PaletteColor(153) // light blue
)

// TerminalPane renders PTY output natively to a tcell screen via vt10x.
// No ANSI string intermediary — vt10x cells map directly to tcell cells.
// No activeInputBG or findInputRow — the native surface shows upstream
// PTY output without Argus-injected highlights.
type TerminalPane struct {
	*tview.Box
	mu      sync.Mutex
	session agentview.TerminalAdapter
	taskID  string
	taskPR  string
	focused bool

	// Persistent vt10x for live incremental rendering.
	vtTerm     vt10x.Terminal
	vtFedTotal uint64
	vtCols     int
	vtRows     int

	// Scrollback.
	scrollOffset int

	// Replay data for finished sessions (loaded from session log file).
	replayData []byte

	// Diff mode.
	diffMode    bool
	diffContent []diffLine
	diffSplit   bool
	diffScroll  int
	diffFile    string
}

// diffLine is a single line in the diff view with its type.
type diffLine struct {
	text     string
	lineType diffLineType
}

type diffLineType int

const (
	diffContext diffLineType = iota
	diffAdded
	diffRemoved
	diffHeader
)

// NewTerminalPane creates a native terminal rendering pane.
func NewTerminalPane() *TerminalPane {
	return &TerminalPane{
		Box: tview.NewBox(),
	}
}

// SetSession attaches a live session. Resets vt10x state.
func (tp *TerminalPane) SetSession(sess agentview.TerminalAdapter) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.session = sess
	tp.vtTerm = nil
	tp.vtFedTotal = 0
	tp.scrollOffset = 0
}

// Session returns the current session (thread-safe).
func (tp *TerminalPane) Session() agentview.TerminalAdapter {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.session
}

// SetTaskID sets the current task ID and loads session log if no live session.
func (tp *TerminalPane) SetTaskID(id string) {
	tp.taskID = id
	tp.replayData = nil
	if id != "" && tp.Session() == nil {
		tp.loadSessionLog(id)
	}
}

// loadSessionLog reads the session log file for finished-session replay.
func (tp *TerminalPane) loadSessionLog(taskID string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	logPath := filepath.Join(home, ".argus", "sessions", taskID+".log")
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return
	}
	uxlog.Log("[tui2] loaded session log for %s (%d bytes)", taskID, len(data))
	tp.replayData = data
}

// SetPRURL sets the PR URL for the current task.
func (tp *TerminalPane) SetPRURL(url string) {
	tp.taskPR = url
}

// SetFocused sets the focus state for border rendering.
func (tp *TerminalPane) SetFocused(f bool) {
	tp.focused = f
}

// ResetVT clears all terminal state (on resize or task switch).
func (tp *TerminalPane) ResetVT() {
	tp.vtTerm = nil
	tp.vtFedTotal = 0
	tp.scrollOffset = 0
	tp.replayData = nil
	tp.ExitDiffMode()
}

// HasContent returns true if there is something to render.
func (tp *TerminalPane) HasContent() bool {
	tp.mu.Lock()
	sess := tp.session
	tp.mu.Unlock()
	if sess != nil {
		return sess.TotalWritten() > 0
	}
	return len(tp.replayData) > 0
}

// --- Scrollback ---

func (tp *TerminalPane) ScrollUp(n int)  { tp.scrollOffset += n }
func (tp *TerminalPane) ScrollOffset() int { return tp.scrollOffset }
func (tp *TerminalPane) ResetScroll()     { tp.scrollOffset = 0 }

func (tp *TerminalPane) ScrollDown(n int) {
	tp.scrollOffset -= n
	if tp.scrollOffset < 0 {
		tp.scrollOffset = 0
	}
}

// --- Diff mode ---

// EnterDiffMode activates diff display in the center panel.
func (tp *TerminalPane) EnterDiffMode(diff, fileName string) {
	tp.diffMode = true
	tp.diffScroll = 0
	tp.diffFile = fileName
	tp.diffContent = parseDiffLines(diff)
}

// ExitDiffMode returns to terminal display.
func (tp *TerminalPane) ExitDiffMode() {
	tp.diffMode = false
	tp.diffContent = nil
	tp.diffScroll = 0
	tp.diffFile = ""
}

// InDiffMode returns true if viewing a diff.
func (tp *TerminalPane) InDiffMode() bool { return tp.diffMode }

// ToggleDiffSplit switches between side-by-side and unified views.
func (tp *TerminalPane) ToggleDiffSplit() {
	tp.diffSplit = !tp.diffSplit
	tp.diffScroll = 0
}

// DiffScrollUp scrolls the diff view up.
func (tp *TerminalPane) DiffScrollUp(n int) {
	tp.diffScroll -= n
	if tp.diffScroll < 0 {
		tp.diffScroll = 0
	}
}

// DiffScrollDown scrolls the diff view down.
func (tp *TerminalPane) DiffScrollDown(n int) {
	tp.diffScroll += n
}

// --- PR ---

func (tp *TerminalPane) OpenPR() {
	if tp.taskPR == "" {
		return
	}
	exec.Command("open", tp.taskPR).Start() //nolint:errcheck
}

// --- Draw ---

func (tp *TerminalPane) Draw(screen tcell.Screen) {
	tp.Box.DrawForSubclass(screen, tp)
	x, y, width, height := tp.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	borderStyle := StyleBorder
	if tp.focused {
		borderStyle = StyleFocusedBorder
	}
	drawBorder(screen, x-1, y-1, width+2, height+2, borderStyle)

	if tp.diffMode {
		tp.renderDiff(screen, x, y, width, height)
		return
	}

	tp.mu.Lock()
	sess := tp.session
	tp.mu.Unlock()

	if sess == nil && !tp.HasContent() {
		msg := "No active session"
		if tp.taskID != "" {
			msg = "Session not running - press Enter to start"
		}
		midY := y + height/2
		midX := x + (width-len(msg))/2
		if midX < x {
			midX = x
		}
		drawText(screen, midX, midY, width, msg, StyleDimmed)
		return
	}

	var raw []byte
	var ptyCols, ptyRows int
	alive := false

	if sess != nil {
		ptyCols, ptyRows = sess.PTYSize()
		alive = sess.Alive()
		raw = sess.RecentOutput()
	} else if len(tp.replayData) > 0 {
		raw = tp.replayData
	}

	if len(raw) == 0 {
		if sess != nil {
			msg := "Waiting for output..."
			drawText(screen, x+(width-len(msg))/2, y+height/2, width, msg, StyleDimmed)
		}
		return
	}

	if ptyCols < 20 {
		ptyCols = width
	}
	if ptyRows < 5 {
		ptyRows = height
	}

	if tp.scrollOffset > 0 || !alive {
		tp.renderReplay(screen, x, y, width, height, raw, ptyCols)
	} else {
		tp.renderLive(screen, x, y, width, height, raw, ptyCols, ptyRows)
	}
}

// renderLive feeds only new bytes to persistent vt10x and paints cells to tcell.
func (tp *TerminalPane) renderLive(screen tcell.Screen, x, y, w, h int, raw []byte, ptyCols, ptyRows int) {
	tp.mu.Lock()
	sess := tp.session
	tp.mu.Unlock()

	totalWritten := uint64(0)
	if sess != nil {
		totalWritten = sess.TotalWritten()
	}

	if tp.vtTerm == nil || tp.vtCols != ptyCols || tp.vtRows != ptyRows {
		tp.vtTerm = vt10x.New(vt10x.WithSize(ptyCols, ptyRows))
		tp.vtFedTotal = 0
		tp.vtCols = ptyCols
		tp.vtRows = ptyRows
	}

	newBytes := totalWritten - tp.vtFedTotal
	if newBytes > uint64(len(raw)) {
		tp.vtTerm = vt10x.New(vt10x.WithSize(ptyCols, ptyRows))
		tp.vtTerm.Write(raw)
	} else if newBytes > 0 {
		tp.vtTerm.Write(raw[len(raw)-int(newBytes):])
	}
	tp.vtFedTotal = totalWritten

	tp.vtTerm.Lock()
	defer tp.vtTerm.Unlock()

	tp.paintVT(screen, x, y, w, h, tp.vtTerm, ptyCols, ptyRows, true)
}

// renderReplay replays full buffer through a tall vt10x and renders a window.
// Direct vt10x→tcell — no ANSI string intermediary.
func (tp *TerminalPane) renderReplay(screen tcell.Screen, x, y, w, h int, raw []byte, vtCols int) {
	vtRows := estimateVTRows(raw, vtCols, h)
	vt := vt10x.New(vt10x.WithSize(vtCols, vtRows))
	vt.Write(raw)

	vt.Lock()
	defer vt.Unlock()

	tp.paintVT(screen, x, y, w, h, vt, vtCols, vtRows, tp.scrollOffset == 0)
}

// paintVT renders vt10x cells to the tcell screen with content trimming and scrollback.
func (tp *TerminalPane) paintVT(screen tcell.Screen, x, y, w, h int, vt vt10x.Terminal, vtCols, vtRows int, showCursor bool) {
	cur := vt.Cursor()

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
	if tp.scrollOffset > maxScroll {
		tp.scrollOffset = maxScroll
	}

	// Compute visible window
	endRow := lastContentRow - tp.scrollOffset
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

			// Cursor — always render regardless of CursorVisible()
			if showCursor && vtRow == cur.Y && col == cur.X {
				style = tcell.StyleDefault.Foreground(cursorFG).Background(cursorBG)
			}

			screen.SetContent(x+col, y+screenRow, ch, nil, style)
		}
	}

	// Scroll indicator
	if tp.scrollOffset > 0 {
		indicator := "   [SCROLL]   "
		style := tcell.StyleDefault.Foreground(tcell.PaletteColor(214)).Bold(true)
		midX := x + (w-len(indicator))/2
		if midX < x {
			midX = x
		}
		for i, r := range indicator {
			if midX+i < x+w {
				screen.SetContent(midX+i, y, r, nil, style)
			}
		}
	}
}

// --- Diff rendering ---

func (tp *TerminalPane) renderDiff(screen tcell.Screen, x, y, w, h int) {
	if len(tp.diffContent) == 0 {
		msg := "No diff available"
		drawText(screen, x+(w-len(msg))/2, y+h/2, w, msg, StyleDimmed)
		return
	}

	// Header
	headerText := " " + tp.diffFile + " "
	headerStyle := tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
	for i, r := range headerText {
		if i >= w {
			break
		}
		screen.SetContent(x+i, y, r, nil, headerStyle)
	}

	visibleH := h - 1
	maxScroll := len(tp.diffContent) - visibleH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if tp.diffScroll > maxScroll {
		tp.diffScroll = maxScroll
	}

	for i := range visibleH {
		lineIdx := tp.diffScroll + i
		if lineIdx >= len(tp.diffContent) {
			break
		}
		line := tp.diffContent[lineIdx]
		style := diffLineStyle(line.lineType)
		text := line.text
		if len(text) > w {
			text = text[:w]
		}
		drawText(screen, x, y+1+i, w, text, style)
	}
}

func diffLineStyle(t diffLineType) tcell.Style {
	switch t {
	case diffAdded:
		return tcell.StyleDefault.Foreground(tcell.PaletteColor(78)) // green
	case diffRemoved:
		return tcell.StyleDefault.Foreground(tcell.PaletteColor(203)) // red
	case diffHeader:
		return tcell.StyleDefault.Foreground(tcell.PaletteColor(87)).Bold(true)
	default:
		return tcell.StyleDefault.Foreground(tcell.PaletteColor(245))
	}
}

func parseDiffLines(diff string) []diffLine {
	if diff == "" {
		return nil
	}
	var lines []diffLine
	start := 0
	for i := 0; i <= len(diff); i++ {
		if i == len(diff) || diff[i] == '\n' {
			line := diff[start:i]
			start = i + 1
			lt := diffContext
			if len(line) > 0 {
				switch line[0] {
				case '+':
					lt = diffAdded
				case '-':
					lt = diffRemoved
				case '@':
					lt = diffHeader
				}
			}
			lines = append(lines, diffLine{text: line, lineType: lt})
		}
	}
	return lines
}

// --- vt10x helpers (no BT dependency) ---

// vtColorToTcell maps a vt10x color to a tcell color.
// DefaultFG/BG → tcell.ColorDefault so terminal theme is inherited.
func vtColorToTcell(c vt10x.Color) tcell.Color {
	if c == vt10x.DefaultFG || c == vt10x.DefaultBG {
		return tcell.ColorDefault
	}
	n := uint32(c)
	if n < 256 {
		return tcell.PaletteColor(int(n))
	}
	r := int32((n >> 16) & 0xFF)
	g := int32((n >> 8) & 0xFF)
	b := int32(n & 0xFF)
	return tcell.NewRGBColor(r, g, b)
}

// cellStyle converts a vt10x glyph to a tcell.Style.
// No activeInputBG — the native surface shows upstream PTY output as-is.
func cellStyle(cell vt10x.Glyph) tcell.Style {
	style := tcell.StyleDefault.
		Foreground(vtColorToTcell(cell.FG)).
		Background(vtColorToTcell(cell.BG))

	if cell.Mode&vtAttrBold != 0 {
		style = style.Bold(true)
	}
	if cell.Mode&vtAttrItalic != 0 {
		style = style.Italic(true)
	}
	if cell.Mode&vtAttrUnderline != 0 {
		style = style.Underline(true)
	}
	if cell.Mode&vtAttrReverse != 0 {
		style = style.Reverse(true)
	}
	return style
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
// all output. Reimplemented locally to avoid importing internal/ui (BT package).
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

