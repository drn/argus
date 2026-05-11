package taskview

import (
	"fmt"
	"hash/fnv"
	"io"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// TaskDetailPanel displays metadata for the selected task in the right panel.
type TaskDetailPanel struct {
	*tview.Box
	task    *model.Task
	running bool

	// OnBranchChange fires when Draw() will paint a different rendering
	// branch than the previous frame: the task==nil "No task selected"
	// swap, swapping to a different task (different conditional rows
	// render: Project/Branch/Backend/Worktree/Created/Elapsed/Prompt are
	// each gated on field presence), status string width change (e.g.
	// "In Progress (running)" → "In Progress (idle)"), running flag flip,
	// and prompt text changes (different number of wrapped lines).
	// App wires this to forceRedraw so afterDraw runs Sync.
	// See gotchas/ui-threading.md.
	OnBranchChange func()

	// lastShape captures the rendered-shape signature emitted by
	// taskShape. The callback fires only when it changes — e.g. tick
	// re-renders that update elapsed time but leave the field set
	// unchanged don't spam forceRedraw.
	lastShape uint64
}

// NewTaskDetailPanel creates a task detail panel.
func NewTaskDetailPanel() *TaskDetailPanel {
	return &TaskDetailPanel{
		Box: tview.NewBox(),
		// Sentinel — the first SetTask always fires, even if the
		// computed signature happens to be 0.
		lastShape: ^uint64(0),
	}
}

// SetTask updates the displayed task. Fires OnBranchChange when the rendered
// shape changes (different task ID, different field-presence flags, running
// flip, status flip, or prompt text change).
func (td *TaskDetailPanel) SetTask(t *model.Task, running bool) {
	td.task = t
	td.running = running
	shape := td.taskShape()
	if shape == td.lastShape {
		return
	}
	td.lastShape = shape
	if td.OnBranchChange != nil {
		td.OnBranchChange()
	}
}

// taskShape returns a 64-bit FNV-1a hash of the inputs that determine which
// rows Draw paints and at what widths. Fields that DON'T affect the cell SET
// (e.g. elapsed time string that grows by one char per minute — the row is
// always present, just wider) are intentionally omitted to avoid firing the
// callback on every tick. Caller is responsible for invoking this only on
// state transitions.
func (td *TaskDetailPanel) taskShape() uint64 {
	h := fnv.New64a()
	if td.task == nil {
		_, _ = h.Write([]byte{0}) // nil-task branch
		return h.Sum64()
	}
	_, _ = h.Write([]byte{1})
	_, _ = io.WriteString(h, td.task.ID)
	_, _ = h.Write([]byte{0})
	_, _ = io.WriteString(h, td.task.Name)
	_, _ = h.Write([]byte{0})
	_, _ = io.WriteString(h, td.task.Status.String())
	_, _ = h.Write([]byte{0})
	flag := byte(0)
	if td.running {
		flag |= 1
	}
	if td.task.Sandboxed {
		flag |= 2
	}
	if !td.task.CreatedAt.IsZero() {
		flag |= 4
	}
	_, _ = h.Write([]byte{flag, 0})
	_, _ = io.WriteString(h, td.task.Project)
	_, _ = h.Write([]byte{0})
	_, _ = io.WriteString(h, td.task.Branch)
	_, _ = h.Write([]byte{0})
	_, _ = io.WriteString(h, td.task.Backend)
	_, _ = h.Write([]byte{0})
	_, _ = io.WriteString(h, td.task.Worktree)
	_, _ = h.Write([]byte{0})
	// Hashing the full Prompt is correct (different prompt text = different
	// wrapped line count = different row count) but cheap — fnv-1a streams
	// without allocation. The prompt is at most a few KB.
	_, _ = io.WriteString(h, td.task.Prompt)
	return h.Sum64()
}

// Draw renders the task detail panel.
func (td *TaskDetailPanel) Draw(screen tcell.Screen) {
	td.Box.DrawForSubclass(screen, td)
	x, y, width, height := td.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	inner := widget.DrawBorderedPanel(screen, x, y, width, height, " Details ", theme.StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	if td.task == nil {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "No task selected", theme.StyleDimmed)
		return
	}

	t := td.task
	row := inner.Y

	// Task name (title)
	name := t.Name
	if len(name) > inner.W-1 {
		name = name[:inner.W-4] + "..."
	}
	widget.DrawText(screen, inner.X, row, inner.W, name, theme.StyleTitle)
	row += 2

	// Status
	statusLabel := t.Status.DisplayName()
	if t.Status == model.StatusInProgress {
		if td.running {
			statusLabel += " (running)"
		} else {
			statusLabel += " (idle)"
		}
	}
	statusStyle := td.statusStyle(t.Status)
	row = td.drawField(screen, inner.X, row, inner.W, "Status", statusLabel, statusStyle)

	// Project
	if t.Project != "" {
		row = td.drawField(screen, inner.X, row, inner.W, "Project", t.Project, theme.StyleNormal)
	}

	// Branch
	if t.Branch != "" {
		row = td.drawField(screen, inner.X, row, inner.W, "Branch", t.Branch, theme.StyleNormal)
	}

	// Backend
	if t.Backend != "" {
		row = td.drawField(screen, inner.X, row, inner.W, "Backend", t.Backend, theme.StyleNormal)
	}

	// Sandbox
	if t.Sandboxed {
		row = td.drawField(screen, inner.X, row, inner.W, "Sandbox", "Yes", theme.StyleComplete)
	} else {
		row = td.drawField(screen, inner.X, row, inner.W, "Sandbox", "No", theme.StyleDimmed)
	}

	// Worktree
	if t.Worktree != "" {
		wt := t.Worktree
		maxLen := inner.W - 11
		if maxLen > 3 && len(wt) > maxLen {
			wt = "..." + wt[len(wt)-maxLen+3:]
		}
		row = td.drawField(screen, inner.X, row, inner.W, "Worktree", wt, theme.StyleNormal)
	}

	// Created date
	if !t.CreatedAt.IsZero() {
		row = td.drawField(screen, inner.X, row, inner.W, "Created", t.CreatedAt.Format(time.DateOnly), theme.StyleNormal)
	}

	// Elapsed
	if elapsed := t.ElapsedString(); elapsed != "" {
		row = td.drawField(screen, inner.X, row, inner.W, "Elapsed", elapsed, tcell.StyleDefault.Foreground(theme.ColorElapsed))
	}

	// Prompt
	maxRow := inner.Y + inner.H
	if t.Prompt != "" && row < maxRow-1 {
		row++
		widget.DrawText(screen, inner.X, row, inner.W, "PROMPT", theme.StyleTitle)
		row++
		remaining := maxRow - row
		promptLines := td.wrapText(t.Prompt, inner.W-1)
		for i, line := range promptLines {
			if i >= remaining {
				break
			}
			widget.DrawText(screen, inner.X, row, inner.W, line, theme.StyleNormal)
			row++
		}
	}
}

// drawField renders "Label: Value" and returns the next row.
func (td *TaskDetailPanel) drawField(screen tcell.Screen, x, row, w int, label, value string, valStyle tcell.Style) int {
	labelStr := fmt.Sprintf("%s: ", label)
	widget.DrawText(screen, x, row, len(labelStr), labelStr, theme.StyleDimmed)
	widget.DrawText(screen, x+len(labelStr), row, w-len(labelStr), value, valStyle)
	return row + 1
}

// statusStyle returns the style for a given status.
func (td *TaskDetailPanel) statusStyle(s model.Status) tcell.Style {
	switch s {
	case model.StatusPending:
		return theme.StylePending
	case model.StatusInProgress:
		return theme.StyleInProgress
	case model.StatusInReview:
		return theme.StyleInReview
	case model.StatusComplete:
		return theme.StyleComplete
	default:
		return theme.StyleNormal
	}
}

// wrapText wraps text to fit within maxWidth at word boundaries.
func (td *TaskDetailPanel) wrapText(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > maxWidth {
			lines = append(lines, line)
			line = w
		} else {
			line += " " + w
		}
	}
	lines = append(lines, line)
	return lines
}
