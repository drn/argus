package tui2

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	xvt "github.com/charmbracelet/x/vt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
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
}

// NewTaskPreviewPanel creates a task preview panel.
func NewTaskPreviewPanel() *TaskPreviewPanel {
	return &TaskPreviewPanel{
		Box:       tview.NewBox(),
		statusMsg: "No task selected",
	}
}

// SetTaskID sets which task to preview. Clears cached cells.
func (tp *TaskPreviewPanel) SetTaskID(id string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.taskID == id {
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
}

// TaskID returns the current task ID.
func (tp *TaskPreviewPanel) TaskID() string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.taskID
}

// RefreshOutput fetches session output and pre-renders cells.
// Called from a goroutine — never from the UI thread.
func (tp *TaskPreviewPanel) RefreshOutput(raw []byte, cols, rows int) {
	if cols < 10 {
		cols = 10
	}
	if rows < 3 {
		rows = 3
	}

	if len(raw) == 0 {
		tp.mu.Lock()
		tp.statusMsg = "Waiting for output..."
		tp.cells = nil
		tp.mu.Unlock()
		return
	}

	// Run VT emulation off the UI thread.
	emu := xvt.NewSafeEmulator(cols, rows)
	emu.Write(raw)

	grid := make([][]previewCell, rows)
	for vy := 0; vy < rows; vy++ {
		grid[vy] = make([]previewCell, cols)
		for vx := 0; vx < cols; vx++ {
			cell := emu.CellAt(vx, vy)
			ch := ' '
			style := tcell.StyleDefault
			if cell != nil {
				ch = cellRune(cell)
				style = uvCellToTcellStyle(cell)
			}
			grid[vy][vx] = previewCell{ch: ch, style: style}
		}
	}

	tp.mu.Lock()
	tp.cells = grid
	tp.cellCols = cols
	tp.cellRows = rows
	tp.statusMsg = ""
	tp.mu.Unlock()
}

// SetStatus sets a status message (clears cached cells).
func (tp *TaskPreviewPanel) SetStatus(msg string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.statusMsg = msg
	tp.cells = nil
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

	inner := drawBorderedPanel(screen, x, y, width, height, " Preview ", StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	tp.mu.Lock()
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
		drawText(screen, startX, row, w-(startX-x), line, StyleDimmed)
	}
}

// LoadSessionLog reads the session log file for a finished task.
// Call from a goroutine, then pass the result to RefreshOutput.
func LoadSessionLog(taskID string) []byte {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	logPath := filepath.Join(home, ".argus", "sessions", taskID+".log")
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
