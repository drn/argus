package tui2

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"image/color"

	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/uxlog"
)

// Cursor colors — high-contrast, theme-independent.
var (
	cursorFG = tcell.PaletteColor(17)  // dark blue
	cursorBG = tcell.PaletteColor(153) // light blue
)

// TerminalPane renders PTY output natively to a tcell screen via x/vt.
// No ANSI string intermediary — x/vt cells map directly to tcell cells.
// No activeInputBG or findInputRow — the native surface shows upstream
// PTY output without Argus-injected highlights.
type TerminalPane struct {
	*tview.Box
	mu      sync.Mutex
	session agentview.TerminalAdapter
	taskID  string
	taskPR  string
	focused bool

	// Persistent x/vt emulator for live incremental rendering.
	emu        *xvt.SafeEmulator
	emuFedTotal uint64
	emuCols     int
	emuRows     int

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

// SetSession attaches a live session. Resets emulator state only when the
// session pointer actually changes — the tick calls this every second with
// the same session, and resetting the emulator each time would destroy
// incremental rendering state.
func (tp *TerminalPane) SetSession(sess agentview.TerminalAdapter) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.session == sess {
		return // same session, skip reset
	}
	if sess != nil {
		uxlog.Log("[terminalpane] SetSession: sess=%p totalWritten=%d", sess, sess.TotalWritten())
	} else {
		uxlog.Log("[terminalpane] SetSession: nil")
	}
	tp.session = sess
	tp.emu = nil
	tp.emuFedTotal = 0
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
	tp.emu = nil
	tp.emuFedTotal = 0
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

func (tp *TerminalPane) ScrollUp(n int)    { tp.scrollOffset += n }
func (tp *TerminalPane) ScrollOffset() int  { return tp.scrollOffset }
func (tp *TerminalPane) ResetScroll()       { tp.scrollOffset = 0 }

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
	drawBorderedPanel(screen, x-1, y-1, width+2, height+2, "", borderStyle)

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

		// Resize PTY to match panel dimensions if they've changed.
		wantRows := max(height, 5)
		wantCols := max(width, 20)
		if alive && (ptyCols != wantCols || ptyRows != wantRows) {
			sess.Resize(uint16(wantRows), uint16(wantCols))
			ptyCols, ptyRows = wantCols, wantRows
		}

		raw = sess.RecentOutput()
	} else if len(tp.replayData) > 0 {
		raw = tp.replayData
	}

	if len(raw) == 0 {
		if sess != nil {
			uxlog.Log("[terminalpane] Draw: sess=%p alive=%v ptyCols=%d ptyRows=%d raw=0 totalWritten=%d",
				sess, alive, ptyCols, ptyRows, sess.TotalWritten())
			msg := "Waiting for output..."
			drawText(screen, x+(width-len(msg))/2, y+height/2, width, msg, StyleDimmed)
		}
		return
	}

	uxlog.Log("[terminalpane] Draw: rendering raw=%d ptyCols=%d ptyRows=%d alive=%v scroll=%d",
		len(raw), ptyCols, ptyRows, alive, tp.scrollOffset)

	if ptyCols < 20 {
		ptyCols = width
	}
	if ptyRows < 5 {
		ptyRows = height
	}

	if tp.scrollOffset > 0 || !alive {
		tp.renderReplay(screen, x, y, width, height, raw, ptyCols, ptyRows)
	} else {
		tp.renderLive(screen, x, y, width, height, raw, ptyCols, ptyRows)
	}
}

// renderLive feeds only new bytes to persistent x/vt emulator and paints cells to tcell.
func (tp *TerminalPane) renderLive(screen tcell.Screen, x, y, w, h int, raw []byte, ptyCols, ptyRows int) {
	tp.mu.Lock()
	sess := tp.session
	tp.mu.Unlock()

	totalWritten := uint64(0)
	if sess != nil {
		totalWritten = sess.TotalWritten()
	}

	if tp.emu == nil || tp.emuCols != ptyCols || tp.emuRows != ptyRows {
		tp.emu = xvt.NewSafeEmulator(ptyCols, ptyRows)
		tp.emuFedTotal = 0
		tp.emuCols = ptyCols
		tp.emuRows = ptyRows
	}

	newBytes := totalWritten - tp.emuFedTotal
	if newBytes > uint64(len(raw)) {
		// Ring buffer wrapped — full reset and replay.
		tp.emu = xvt.NewSafeEmulator(ptyCols, ptyRows)
		tp.emu.Write(raw)
	} else if newBytes > 0 {
		tp.emu.Write(raw[len(raw)-int(newBytes):])
	}
	tp.emuFedTotal = totalWritten

	tp.paintEmu(screen, x, y, w, h, tp.emu, ptyCols, ptyRows, true)
}

// renderReplay uses x/vt scrollback for finished sessions and scroll mode.
// Feeds full buffer into a fresh emulator and uses scrollback for history.
func (tp *TerminalPane) renderReplay(screen tcell.Screen, x, y, w, h int, raw []byte, ptyCols, ptyRows int) {
	emu := xvt.NewSafeEmulator(ptyCols, ptyRows)
	emu.Write(raw)

	tp.paintEmu(screen, x, y, w, h, emu, ptyCols, ptyRows, tp.scrollOffset == 0)
}

// paintEmu renders x/vt emulator cells to the tcell screen with content trimming and scrollback.
func (tp *TerminalPane) paintEmu(screen tcell.Screen, x, y, w, h int, emu *xvt.SafeEmulator, emuCols, emuRows int, showCursor bool) {
	cur := emu.CursorPosition()
	sbLen := emu.ScrollbackLen()

	// Find content bounds in the main screen area.
	lastContentRow := findLastContentRowEmu(emu, emuCols, emuRows)
	if cur.Y > lastContentRow {
		lastContentRow = cur.Y
	}

	// Total addressable lines = scrollback + visible content.
	totalLines := sbLen + lastContentRow + 1
	firstContentRow := 0
	if sbLen == 0 {
		firstContentRow = findFirstContentRowEmu(emu, emuCols, lastContentRow)
		totalLines = lastContentRow - firstContentRow + 1
	}

	if totalLines <= 0 {
		return
	}

	// Clamp scroll offset.
	maxScroll := totalLines - h
	if maxScroll < 0 {
		maxScroll = 0
	}
	if tp.scrollOffset > maxScroll {
		tp.scrollOffset = maxScroll
	}

	renderCols := min(emuCols, w)

	// Render visible rows. Row index is in "unified" space:
	// rows 0..sbLen-1 are scrollback, rows sbLen..sbLen+emuRows-1 are main screen.
	endLine := totalLines - 1 - tp.scrollOffset
	startLine := endLine - h + 1
	if startLine < 0 {
		startLine = 0
	}

	for screenRow := 0; screenRow < h; screenRow++ {
		lineIdx := startLine + screenRow
		if lineIdx > endLine {
			break
		}

		for col := 0; col < renderCols; col++ {
			var cell *uv.Cell
			isMainScreen := false
			mainRow := 0

			if sbLen > 0 && lineIdx < sbLen {
				// Scrollback region.
				cell = emu.ScrollbackCellAt(col, lineIdx)
			} else {
				// Main screen region.
				if sbLen > 0 {
					mainRow = lineIdx - sbLen
				} else {
					mainRow = firstContentRow + lineIdx
				}
				isMainScreen = true
				cell = emu.CellAt(col, mainRow)
			}

			ch := ' '
			style := tcell.StyleDefault
			if cell != nil {
				if cell.Content != "" {
					runes := []rune(cell.Content)
					if len(runes) > 0 {
						ch = runes[0]
					}
				}
				style = uvCellToTcellStyle(cell)
			}

			// Cursor — always render regardless of CursorVisible().
			if showCursor && isMainScreen && mainRow == cur.Y && col == cur.X {
				style = tcell.StyleDefault.Foreground(cursorFG).Background(cursorBG)
			}

			screen.SetContent(x+col, y+screenRow, ch, nil, style)
		}
	}

	// Scroll indicator.
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

// --- x/vt → tcell helpers ---

// uvColorToTcell converts an image/color.Color (as used by x/vt) to a tcell.Color.
// nil → tcell.ColorDefault (inherits terminal theme).
// ansi.BasicColor/IndexedColor → tcell.PaletteColor for exact palette match.
// Everything else → tcell.FromImageColor for RGB conversion.
func uvColorToTcell(c color.Color) tcell.Color {
	if c == nil {
		return tcell.ColorDefault
	}
	switch v := c.(type) {
	case ansi.BasicColor:
		return tcell.PaletteColor(int(v))
	case ansi.IndexedColor:
		return tcell.PaletteColor(int(v))
	default:
		return tcell.FromImageColor(c)
	}
}

// uvCellToTcellStyle converts a *uv.Cell to a tcell.Style.
func uvCellToTcellStyle(cell *uv.Cell) tcell.Style {
	if cell == nil {
		return tcell.StyleDefault
	}
	style := tcell.StyleDefault.
		Foreground(uvColorToTcell(cell.Style.Fg)).
		Background(uvColorToTcell(cell.Style.Bg))

	attrs := cell.Style.Attrs
	if attrs&uv.AttrBold != 0 {
		style = style.Bold(true)
	}
	if attrs&uv.AttrItalic != 0 {
		style = style.Italic(true)
	}
	if attrs&uv.AttrReverse != 0 {
		style = style.Reverse(true)
	}
	if attrs&uv.AttrStrikethrough != 0 {
		style = style.StrikeThrough(true)
	}
	// Underline styles.
	ul := cell.Style.Underline
	if ul != 0 {
		style = style.Underline(true)
	}
	return style
}

// findLastContentRowEmu scans backwards to find the last row with visible content.
func findLastContentRowEmu(emu *xvt.SafeEmulator, cols, rows int) int {
	for row := rows - 1; row >= 0; row-- {
		if rowHasContentEmu(emu, row, cols) {
			return row
		}
	}
	return -1
}

// findFirstContentRowEmu scans forward to find the first row with content.
func findFirstContentRowEmu(emu *xvt.SafeEmulator, cols, maxRow int) int {
	for row := 0; row <= maxRow; row++ {
		if rowHasContentEmu(emu, row, cols) {
			return row
		}
	}
	return 0
}

// rowHasContentEmu returns true if any cell in the row has visible content.
func rowHasContentEmu(emu *xvt.SafeEmulator, row, cols int) bool {
	for x := 0; x < cols; x++ {
		cell := emu.CellAt(x, row)
		if cell == nil {
			continue
		}
		if cell.Content != "" && cell.Content != " " {
			return true
		}
		if cell.Style.Fg != nil || cell.Style.Bg != nil || cell.Style.Attrs != 0 {
			return true
		}
	}
	return false
}
