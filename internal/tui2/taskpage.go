package tui2

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const emptyHint = "Press [n] to create your first task"

// TaskPage wraps the task list 3-panel layout and shows the banner
// as an empty state when there are no tasks.
type TaskPage struct {
	*tview.Box
	inner    *tview.Flex // the 3-panel layout
	tasklist *TaskListView
}

// NewTaskPage creates a task page wrapping the given layout and task list.
func NewTaskPage(inner *tview.Flex, tasklist *TaskListView) *TaskPage {
	return &TaskPage{
		Box:      tview.NewBox(),
		inner:    inner,
		tasklist: tasklist,
	}
}

func (tp *TaskPage) Draw(screen tcell.Screen) {
	tp.Box.DrawForSubclass(screen, tp)
	x, y, width, height := tp.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	if tp.tasklist.HasTasks() {
		tp.inner.SetRect(x, y, width, height)
		tp.inner.Draw(screen)
		return
	}

	// Empty state: draw banner centered vertically.
	bh := bannerHeight()
	hintRow := 1 // "Press [n]..." line
	totalH := bh + hintRow
	topPad := max((height-totalH)/2, 0)

	drawBanner(screen, x, y+topPad, width)

	// Draw hint below the banner.
	hintY := y + topPad + bh
	hintPad := max((width-len(emptyHint))/2, 0)
	drawText(screen, x+hintPad, hintY, width-hintPad, emptyHint, tcell.StyleDefault.Foreground(ColorDimmed))
}

// Children returns the inner flex for tview focus routing.
func (tp *TaskPage) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return tp.inner.InputHandler()
}

// MouseHandler delegates to the inner flex.
func (tp *TaskPage) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return tp.inner.MouseHandler()
}
