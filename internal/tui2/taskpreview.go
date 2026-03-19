package tui2

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	xvt "github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
)

// TaskPreviewPanel renders a small terminal snapshot of the selected task's agent output.
type TaskPreviewPanel struct {
	*tview.Box
	mu     sync.Mutex
	runner agent.SessionProvider
	taskID string
}

// NewTaskPreviewPanel creates a task preview panel.
func NewTaskPreviewPanel() *TaskPreviewPanel {
	return &TaskPreviewPanel{
		Box: tview.NewBox(),
	}
}

// SetRunner sets the session provider for looking up task sessions.
func (tp *TaskPreviewPanel) SetRunner(runner agent.SessionProvider) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.runner = runner
}

// SetTaskID sets which task to preview.
func (tp *TaskPreviewPanel) SetTaskID(id string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.taskID = id
}

// Draw renders the preview panel.
func (tp *TaskPreviewPanel) Draw(screen tcell.Screen) {
	tp.Box.DrawForSubclass(screen, tp)
	x, y, width, height := tp.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Draw border
	drawBorder(screen, x-1, y-1, width+2, height+2, StyleBorder)

	// Title in border
	title := " Preview "
	for i, r := range title {
		if x+i < x+width {
			screen.SetContent(x+i, y-1, r, nil, StyleBorder.Bold(true))
		}
	}

	tp.mu.Lock()
	taskID := tp.taskID
	runner := tp.runner
	tp.mu.Unlock()

	if taskID == "" || runner == nil {
		tp.drawCentered(screen, x, y, width, height, "No task selected")
		return
	}

	sess := runner.Get(taskID)
	if sess == nil {
		// Try loading from session log file
		logData := tp.loadSessionLog(taskID)
		if len(logData) > 0 {
			tp.renderVTOutput(screen, x, y, width, height, logData)
			return
		}
		tp.drawCentered(screen, x, y, width, height, "No active agent")
		return
	}

	raw := sess.RecentOutput()
	if len(raw) == 0 {
		tp.drawCentered(screen, x, y, width, height, "Waiting for output...")
		return
	}

	tp.renderVTOutput(screen, x, y, width, height, raw)
}

// renderVTOutput replays raw PTY bytes through x/vt and paints the last h rows to screen.
func (tp *TaskPreviewPanel) renderVTOutput(screen tcell.Screen, x, y, w, h int, raw []byte) {
	cols := w
	rows := h
	if cols < 10 {
		cols = 10
	}
	if rows < 3 {
		rows = 3
	}

	emu := xvt.NewSafeEmulator(cols, rows)
	emu.Write(raw)

	renderCols := min(cols, w)
	renderRows := min(rows, h)

	for vy := 0; vy < renderRows; vy++ {
		for vx := 0; vx < renderCols; vx++ {
			cell := emu.CellAt(vx, vy)
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
			screen.SetContent(x+vx, y+vy, ch, nil, style)
		}
	}
}

// drawCentered renders centered dimmed text in the panel.
func (tp *TaskPreviewPanel) drawCentered(screen tcell.Screen, x, y, w, h int, msg string) {
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

// loadSessionLog reads the session log file for a finished task.
func (tp *TaskPreviewPanel) loadSessionLog(taskID string) []byte {
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
