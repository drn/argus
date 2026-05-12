package tui

import (
	"os"
	"strings"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// previewCell is a pre-rendered cell for the preview panel.
type previewCell struct {
	ch    rune
	style tcell.Style
}

// TaskPreviewPanel renders a small terminal snapshot of the selected task's agent output.
// All heavy work (RPC, file I/O, VT emulation) happens in RefreshOutput(), called from
// the tick goroutine. Draw() only paints cached cells — zero blocking.
type TaskPreviewPanel struct {
	*tview.Box
	mu     sync.Mutex
	taskID string

	// Pre-rendered cell grid, updated by RefreshOutput().
	cells     [][]previewCell
	cellCols  int
	cellRows  int
	statusMsg string // shown when cells is nil ("No task selected", etc.)

	// Cached inner dimensions from Draw() — safe for tick goroutine to read.
	drawCols int
	drawRows int

	// OnBranchChange fires when Draw() will paint a different rendering
	// branch than the previous frame: the cells==nil "centered status text"
	// branch swap to/from the cellCols×cellRows paint, viewport-size shifts
	// that change the painted rect, and statusMsg changes (different
	// length text in the centered placeholder). App wires this to
	// forceRedraw, which is now log-only (does NOT trigger Sync) —
	// DrawBorderedPanel's FillArea covers the inner rect every frame and
	// tcell.Show()'s diff handles the branch transition correctly.
	// See gotchas/ui-threading.md.
	OnBranchChange func()

	// Snapshot of last-rendered shape, used to suppress callback when a
	// mutator left the cell SET unchanged. cellsNil distinguishes
	// "centered status" from "grid paint"; cols/rows track grid dimensions.
	lastCellsNil  bool
	lastCellCols  int
	lastCellRows  int
	lastStatusMsg string
}

// NewTaskPreviewPanel creates a task preview panel.
func NewTaskPreviewPanel() *TaskPreviewPanel {
	return &TaskPreviewPanel{
		Box:          tview.NewBox(),
		statusMsg:    "No task selected",
		lastCellsNil: true, // initial Draw is the centered "No task selected"
	}
}

// SetTaskID sets which task to preview. Clears cached cells. Fires
// OnBranchChange when the rendered shape changes — switching to a different
// task always clears `cells` (centered placeholder branch) and updates
// statusMsg, both shape inputs.
func (tp *TaskPreviewPanel) SetTaskID(id string) {
	tp.mu.Lock()
	if tp.taskID == id {
		tp.mu.Unlock()
		return
	}
	tp.taskID = id
	tp.cells = nil
	tp.cellCols = 0
	tp.cellRows = 0
	if id == "" {
		tp.statusMsg = "No task selected"
	} else {
		tp.statusMsg = "Loading..."
	}
	changed := tp.snapshotShapeLocked()
	tp.mu.Unlock()
	if changed {
		tp.notifyBranchChange()
	}
}

// snapshotShapeLocked records the current rendered shape and returns true if
// it differs from the prior snapshot. Caller must hold tp.mu.
func (tp *TaskPreviewPanel) snapshotShapeLocked() bool {
	cellsNil := tp.cells == nil
	changed := cellsNil != tp.lastCellsNil ||
		tp.cellCols != tp.lastCellCols ||
		tp.cellRows != tp.lastCellRows ||
		(cellsNil && tp.statusMsg != tp.lastStatusMsg)
	tp.lastCellsNil = cellsNil
	tp.lastCellCols = tp.cellCols
	tp.lastCellRows = tp.cellRows
	tp.lastStatusMsg = tp.statusMsg
	return changed
}

// notifyBranchChange fires OnBranchChange if set. Call AFTER releasing tp.mu —
// the callback may invoke app-level code that takes other locks (mirrors the
// TerminalPane pattern in gotchas/ui-threading.md).
func (tp *TaskPreviewPanel) notifyBranchChange() {
	if tp.OnBranchChange != nil {
		tp.OnBranchChange()
	}
}

// TaskID returns the current task ID.
func (tp *TaskPreviewPanel) TaskID() string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.taskID
}

// DrawSize returns the cached inner dimensions from the last Draw() call.
// Safe to call from any goroutine.
func (tp *TaskPreviewPanel) DrawSize() (cols, rows int) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.drawCols, tp.drawRows
}

// RefreshOutput fetches session output and pre-renders cells.
// Called from a goroutine — never from the UI thread.
// emuCols/emuRows are the VT emulator dimensions (should match PTY size for correct
// cursor positioning). viewCols/viewRows are the viewport dimensions for the output grid.
func (tp *TaskPreviewPanel) RefreshOutput(raw []byte, emuCols, emuRows, viewCols, viewRows int) {
	if emuCols < 10 {
		emuCols = 10
	}
	if emuRows < 3 {
		emuRows = 3
	}
	if viewCols < 10 {
		viewCols = 10
	}
	if viewRows < 3 {
		viewRows = 3
	}

	if len(raw) == 0 {
		tp.mu.Lock()
		tp.statusMsg = "Waiting for output..."
		tp.cells = nil
		changed := tp.snapshotShapeLocked()
		tp.mu.Unlock()
		if changed {
			tp.notifyBranchChange()
		}
		return
	}

	// Run VT emulation off the UI thread.
	// Use drained emulator to prevent hangs on terminal query sequences.
	// Tail slices (ring buffer / 64KB log tail) routinely begin mid-CSI;
	// AlignToEscBoundary skips any partial CSI/OSC prefix that would
	// otherwise render as a smudge of orphan digits/punctuation at the
	// top of the emulator.
	emu := terminal.NewDrainedEmulator(emuCols, emuRows)
	if _, err := terminal.SafeEmuWrite(emu, terminal.AlignToEscBoundary(raw)); err != nil {
		tp.mu.Lock()
		tp.statusMsg = "Preview unavailable"
		tp.cells = nil
		changed := tp.snapshotShapeLocked()
		tp.mu.Unlock()
		if changed {
			tp.notifyBranchChange()
		}
		return
	}

	lastContentRow := terminal.FindLastContentRowEmu(emu, emuCols, emuRows)
	sbLen := emu.ScrollbackLen()
	totalLines := sbLen + lastContentRow + 1
	firstContentRow := 0
	if sbLen == 0 {
		firstContentRow = terminal.FindFirstContentRowEmu(emu, emuCols, lastContentRow)
		totalLines = lastContentRow - firstContentRow + 1
	}

	grid := make([][]previewCell, viewRows)
	for vy := 0; vy < viewRows; vy++ {
		grid[vy] = make([]previewCell, viewCols)
	}

	if totalLines <= 0 {
		tp.mu.Lock()
		tp.cells = grid
		tp.cellCols = viewCols
		tp.cellRows = viewRows
		tp.statusMsg = ""
		changed := tp.snapshotShapeLocked()
		tp.mu.Unlock()
		if changed {
			tp.notifyBranchChange()
		}
		return
	}

	endLine := totalLines - 1
	startLine := endLine - viewRows + 1
	if startLine < 0 {
		startLine = 0
	}

	// Clip to whichever is narrower: emulator width or viewport width.
	renderCols := min(emuCols, viewCols)
	for vy := 0; vy < viewRows; vy++ {
		lineIdx := startLine + vy
		if lineIdx > endLine {
			break
		}
		for vx := 0; vx < renderCols; vx++ {
			var cell *uv.Cell
			if sbLen > 0 && lineIdx < sbLen {
				cell = emu.ScrollbackCellAt(vx, lineIdx)
			} else {
				mainRow := lineIdx - sbLen
				if sbLen == 0 {
					mainRow = firstContentRow + lineIdx
				}
				cell = emu.CellAt(vx, mainRow)
			}
			ch := ' '
			style := tcell.StyleDefault
			if cell != nil {
				ch = cellRune(cell)
				style = terminal.UvCellToTcellStyle(cell)
			}
			grid[vy][vx] = previewCell{ch: ch, style: style}
		}
	}

	tp.mu.Lock()
	tp.cells = grid
	tp.cellCols = viewCols
	tp.cellRows = viewRows
	tp.statusMsg = ""
	changed := tp.snapshotShapeLocked()
	tp.mu.Unlock()
	if changed {
		tp.notifyBranchChange()
	}
	// Same-shape content updates (cells differ, cols/rows unchanged) flow
	// through tcell.Show()'s per-cell diff. No explicit notification needed
	// — tcell handles content changes correctly on both bare terminals and
	// inside tmux (DECSET 2026 wraps each draw atomically when XTermLike).
}

// SetStatus sets a status message (clears cached cells). Fires
// OnBranchChange when the rendered shape changes (cells transition to nil,
// or the centered message text differs in width).
func (tp *TaskPreviewPanel) SetStatus(msg string) {
	tp.mu.Lock()
	tp.statusMsg = msg
	tp.cells = nil
	changed := tp.snapshotShapeLocked()
	tp.mu.Unlock()
	if changed {
		tp.notifyBranchChange()
	}
}

// cellRune extracts the display rune from a uv.Cell.
func cellRune(cell *uv.Cell) rune {
	if cell.Content != "" {
		runes := []rune(cell.Content)
		if len(runes) > 0 {
			return runes[0]
		}
	}
	return ' '
}

// Draw renders the preview panel from cached cells — no blocking work.
func (tp *TaskPreviewPanel) Draw(screen tcell.Screen) {
	tp.Box.DrawForSubclass(screen, tp)
	x, y, width, height := tp.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	inner := widget.DrawBorderedPanel(screen, x, y, width, height, " Preview ", theme.StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	// Cache inner dimensions for tick goroutine (avoids calling GetInnerRect off UI thread).
	tp.mu.Lock()
	tp.drawCols = inner.W
	tp.drawRows = inner.H
	cells := tp.cells
	cellCols := tp.cellCols
	cellRows := tp.cellRows
	statusMsg := tp.statusMsg
	tp.mu.Unlock()

	if cells == nil {
		tp.drawCentered(screen, inner.X, inner.Y, inner.W, inner.H, statusMsg)
		return
	}

	// Paint cached cells
	renderCols := min(cellCols, inner.W)
	renderRows := min(cellRows, inner.H)
	for vy := 0; vy < renderRows; vy++ {
		for vx := 0; vx < renderCols; vx++ {
			c := cells[vy][vx]
			screen.SetContent(inner.X+vx, inner.Y+vy, c.ch, nil, c.style)
		}
	}
}

// drawCentered renders centered dimmed text in the panel.
func (tp *TaskPreviewPanel) drawCentered(screen tcell.Screen, x, y, w, h int, msg string) {
	if msg == "" {
		return
	}
	lines := strings.Split(msg, "\n")
	startY := y + (h-len(lines))/2
	for i, line := range lines {
		row := startY + i
		if row < y || row >= y+h {
			continue
		}
		startX := x + (w-len(line))/2
		if startX < x {
			startX = x
		}
		widget.DrawText(screen, startX, row, w-(startX-x), line, theme.StyleDimmed)
	}
}

// statSessionLog returns the file size of a session log without reading it.
// Returns 0 if the file doesn't exist. Used to skip redundant reads in refreshPreview.
func statSessionLog(taskID string) int64 {
	logPath := agent.SessionLogPath(taskID)
	fi, err := os.Stat(logPath)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// LoadSessionLog reads the session log file for a finished task.
// Call from a goroutine, then pass the result to RefreshOutput.
func LoadSessionLog(taskID string) []byte {
	logPath := agent.SessionLogPath(taskID)
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}
	// Only use the last 64KB for preview rendering.
	if len(data) > 64*1024 {
		data = data[len(data)-64*1024:]
	}
	return data
}
