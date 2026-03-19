package tui2

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/drn/argus/internal/model"
)

// rowKind identifies what kind of row is displayed.
type rowKind int

const (
	rowTask rowKind = iota
	rowProject
	rowArchiveHeader
)

// taskRow is a flattened display row in the task list.
type taskRow struct {
	kind    rowKind
	task    *model.Task
	project string
}

// TaskListView displays tasks grouped by project with cursor navigation.
// One project expanded at a time,
// cursor skips headers, archive section at the bottom.
type TaskListView struct {
	*tview.Box
	tasks   []*model.Task
	rows    []taskRow
	running map[string]bool
	idle    map[string]bool

	cursor   int
	offset   int // scroll offset
	expanded string // currently expanded project
	archiveExpanded bool
	archiveProject  string // expanded project within archive

	// Callback when user selects a task (Enter key).
	OnSelect func(task *model.Task)
	// Callback when user presses 'n' (new task).
	OnNew func()
	// Callback when cursor moves to a different task.
	OnCursorChange func(task *model.Task)
}

// NewTaskListView creates a task list view.
func NewTaskListView() *TaskListView {
	tl := &TaskListView{
		Box:     tview.NewBox(),
		running: make(map[string]bool),
		idle:    make(map[string]bool),
	}
	return tl
}

// SetTasks updates the task list and rebuilds rows.
func (tl *TaskListView) SetTasks(tasks []*model.Task) {
	tl.tasks = tasks
	tl.buildRows()
	tl.clampCursor()
}

// SetRunning updates the set of running task IDs.
func (tl *TaskListView) SetRunning(ids []string) {
	tl.running = make(map[string]bool, len(ids))
	for _, id := range ids {
		tl.running[id] = true
	}
}

// SetIdle updates the set of idle (finished but not visited) task IDs.
func (tl *TaskListView) SetIdle(ids []string) {
	tl.idle = make(map[string]bool, len(ids))
	for _, id := range ids {
		tl.idle[id] = true
	}
}

// SelectedTask returns the task at the current cursor, or nil.
func (tl *TaskListView) SelectedTask() *model.Task {
	if tl.cursor < 0 || tl.cursor >= len(tl.rows) {
		return nil
	}
	r := tl.rows[tl.cursor]
	if r.kind != rowTask {
		return nil
	}
	return r.task
}

// buildRows flattens tasks into display rows grouped by project.
func (tl *TaskListView) buildRows() {
	tl.rows = nil

	// Separate active and archived tasks
	var active, archived []*model.Task
	for _, t := range tl.tasks {
		if t.Archived {
			archived = append(archived, t)
		} else {
			active = append(active, t)
		}
	}

	// Group active tasks by project
	projectOrder, projectTasks := groupByProject(active)

	// Auto-expand first project if none is expanded
	if tl.expanded == "" && len(projectOrder) > 0 {
		tl.expanded = projectOrder[0]
	}

	for _, proj := range projectOrder {
		tl.rows = append(tl.rows, taskRow{kind: rowProject, project: proj})
		if proj == tl.expanded {
			for _, t := range projectTasks[proj] {
				tl.rows = append(tl.rows, taskRow{kind: rowTask, task: t, project: proj})
			}
		}
	}

	// Archive section
	if len(archived) > 0 {
		tl.rows = append(tl.rows, taskRow{kind: rowArchiveHeader})
		if tl.archiveExpanded {
			archOrder, archTasks := groupByProject(archived)
			for _, proj := range archOrder {
				tl.rows = append(tl.rows, taskRow{kind: rowProject, project: proj})
				if proj == tl.archiveProject {
					for _, t := range archTasks[proj] {
						tl.rows = append(tl.rows, taskRow{kind: rowTask, task: t, project: proj})
					}
				}
			}
		}
	}
}

// groupByProject groups tasks by project name, preserving insertion order.
func groupByProject(tasks []*model.Task) ([]string, map[string][]*model.Task) {
	order := []string{}
	groups := map[string][]*model.Task{}
	seen := map[string]bool{}
	for _, t := range tasks {
		proj := t.Project
		if proj == "" {
			proj = "(no project)"
		}
		if !seen[proj] {
			seen[proj] = true
			order = append(order, proj)
		}
		groups[proj] = append(groups[proj], t)
	}
	return order, groups
}

func (tl *TaskListView) clampCursor() {
	if len(tl.rows) == 0 {
		tl.cursor = 0
		return
	}
	if tl.cursor >= len(tl.rows) {
		tl.cursor = len(tl.rows) - 1
	}
	if tl.cursor < 0 {
		tl.cursor = 0
	}
	// Skip to nearest task row
	tl.skipToTask(1)
}

// skipToTask moves the cursor to the nearest task row in the given direction.
func (tl *TaskListView) skipToTask(dir int) {
	for tl.cursor >= 0 && tl.cursor < len(tl.rows) {
		if tl.rows[tl.cursor].kind == rowTask {
			return
		}
		tl.cursor += dir
	}
	// If we went past bounds, search the other way
	if dir > 0 {
		tl.cursor = len(tl.rows) - 1
	} else {
		tl.cursor = 0
	}
	for tl.cursor >= 0 && tl.cursor < len(tl.rows) {
		if tl.rows[tl.cursor].kind == rowTask {
			return
		}
		tl.cursor -= dir
	}
}

// CursorDown moves the cursor down. When landing on a project header,
// autoExpand will expand it and advance the cursor to the first task.
func (tl *TaskListView) CursorDown() {
	if len(tl.rows) == 0 {
		return
	}
	tl.cursor++
	if tl.cursor >= len(tl.rows) {
		tl.cursor = len(tl.rows) - 1
	}
	tl.autoExpand()
	tl.notifyCursorChange()
}

// CursorUp moves the cursor up. When landing on a project header,
// autoExpand will expand it and move the cursor to the last task.
func (tl *TaskListView) CursorUp() {
	if len(tl.rows) == 0 {
		return
	}
	tl.cursor--
	if tl.cursor < 0 {
		tl.cursor = 0
	}
	tl.autoExpand()
	tl.notifyCursorChange()
}

// notifyCursorChange fires the OnCursorChange callback with the current task.
func (tl *TaskListView) notifyCursorChange() {
	if tl.OnCursorChange != nil {
		tl.OnCursorChange(tl.SelectedTask())
	}
}

// autoExpand expands the project the cursor is in, collapses others.
func (tl *TaskListView) autoExpand() {
	if tl.cursor < 0 || tl.cursor >= len(tl.rows) {
		return
	}
	row := tl.rows[tl.cursor]

	switch row.kind {
	case rowProject:
		// Cursor landed on a project header — expand it
		inArchive := tl.isInArchive(tl.cursor)
		if inArchive {
			tl.archiveExpanded = true
			if row.project != tl.archiveProject {
				tl.archiveProject = row.project
				tl.buildRows()
				// Move cursor to the first task in this project
				tl.skipToTask(1)
			}
		} else {
			if row.project != tl.expanded {
				tl.expanded = row.project
				tl.buildRows()
				// Move cursor to the first task in this project
				tl.skipToTask(1)
			}
		}
	case rowArchiveHeader:
		tl.archiveExpanded = !tl.archiveExpanded
		tl.buildRows()
	case rowTask:
		inArchive := tl.isInArchive(tl.cursor)
		if inArchive {
			tl.archiveExpanded = true
			if row.project != tl.archiveProject {
				tl.archiveProject = row.project
				tl.buildRows()
				tl.restoreCursorToTask(row.task)
			}
		} else {
			tl.archiveExpanded = false
			if row.project != tl.expanded {
				tl.expanded = row.project
				tl.buildRows()
				tl.restoreCursorToTask(row.task)
			}
		}
	}
}

// isInArchive checks if the row at idx is within the archive section.
func (tl *TaskListView) isInArchive(idx int) bool {
	for i := idx; i >= 0; i-- {
		if tl.rows[i].kind == rowArchiveHeader {
			return true
		}
	}
	return false
}

// restoreCursorToTask moves the cursor to the row matching the given task.
func (tl *TaskListView) restoreCursorToTask(task *model.Task) {
	if task == nil {
		return
	}
	for i, r := range tl.rows {
		if r.kind == rowTask && r.task != nil && r.task.ID == task.ID {
			tl.cursor = i
			return
		}
	}
}

// InputHandler handles key events for the task list.
func (tl *TaskListView) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return tl.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyUp:
			tl.CursorUp()
		case tcell.KeyDown:
			tl.CursorDown()
		case tcell.KeyEnter:
			if t := tl.SelectedTask(); t != nil && tl.OnSelect != nil {
				tl.OnSelect(t)
			}
		case tcell.KeyRune:
			switch event.Rune() {
			case 'j':
				tl.CursorDown()
			case 'k':
				tl.CursorUp()
			case 'n':
				if tl.OnNew != nil {
					tl.OnNew()
				}
			}
		}
	})
}

// Draw renders the task list.
func (tl *TaskListView) Draw(screen tcell.Screen) {
	tl.Box.DrawForSubclass(screen, tl)
	x, y, width, height := tl.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	inner := drawBorderedPanel(screen, x, y, width, height, " Tasks ", StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	if len(tl.rows) == 0 {
		return
	}

	// Ensure scroll offset keeps cursor visible
	if tl.cursor < 0 {
		tl.cursor = 0
	}
	if tl.cursor < tl.offset {
		tl.offset = tl.cursor
	}
	if tl.cursor >= tl.offset+inner.H {
		tl.offset = tl.cursor - inner.H + 1
	}

	for i := 0; i < inner.H; i++ {
		idx := tl.offset + i
		if idx >= len(tl.rows) {
			break
		}
		row := tl.rows[idx]
		isCursor := idx == tl.cursor

		switch row.kind {
		case rowProject:
			tl.drawProjectRow(screen, inner.X, inner.Y+i, inner.W, row.project)
		case rowArchiveHeader:
			tl.drawArchiveHeader(screen, inner.X, inner.Y+i, inner.W)
		case rowTask:
			tl.drawTaskRow(screen, inner.X, inner.Y+i, inner.W, row.task, isCursor)
		}
	}
}

func (tl *TaskListView) drawProjectRow(screen tcell.Screen, x, y, w int, proj string) {
	style := tcell.StyleDefault.Foreground(ColorProject).Bold(true)
	text := fmt.Sprintf("  %s", proj)
	drawText(screen, x, y, w, text, style)
}

func (tl *TaskListView) drawArchiveHeader(screen tcell.Screen, x, y, w int) {
	style := tcell.StyleDefault.Foreground(ColorDimmed).Bold(true)
	indicator := "▸"
	if tl.archiveExpanded {
		indicator = "▾"
	}
	text := fmt.Sprintf("  %s Archive", indicator)
	drawText(screen, x, y, w, text, style)
}

func (tl *TaskListView) drawTaskRow(screen tcell.Screen, x, y, w int, task *model.Task, cursor bool) {
	// Status indicator
	var statusChar rune
	var statusStyle tcell.Style
	switch task.Status {
	case model.StatusPending:
		statusChar = '○'
		statusStyle = StylePending
	case model.StatusInProgress:
		if tl.running[task.ID] {
			statusChar = '●'
			statusStyle = StyleInProgress
		} else if tl.idle[task.ID] {
			statusChar = '◉'
			statusStyle = tcell.StyleDefault.Foreground(tcell.Color78) // green
		} else {
			statusChar = '◉'
			statusStyle = StyleInProgress
		}
	case model.StatusInReview:
		statusChar = '◎'
		statusStyle = StyleInReview
	case model.StatusComplete:
		statusChar = '✓'
		statusStyle = StyleComplete
	default:
		statusChar = '○'
		statusStyle = StylePending
	}

	// Build the row
	nameStyle := StyleNormal
	if cursor {
		nameStyle = StyleSelected
	}

	// Elapsed time
	elapsed := task.ElapsedString()

	// Layout: "    ● name                    3m"
	prefix := "    "
	col := x
	drawText(screen, col, y, len(prefix), prefix, StyleDefault)
	col += len(prefix)

	screen.SetContent(col, y, statusChar, nil, statusStyle)
	col += 2 // status char + space

	nameStr := task.Name
	maxNameW := w - (col - x) - len(elapsed) - 2
	if maxNameW < 0 {
		maxNameW = 0
	}
	if len(nameStr) > maxNameW {
		nameStr = nameStr[:maxNameW]
	}
	drawText(screen, col, y, len(nameStr), nameStr, nameStyle)
	col += len(nameStr)

	// Right-align elapsed time
	if elapsed != "" {
		elapsedCol := x + w - len(elapsed) - 1
		if elapsedCol > col {
			drawText(screen, elapsedCol, y, len(elapsed), elapsed, tcell.StyleDefault.Foreground(ColorElapsed))
		}
	}

	// Fill remaining cells on cursor row so the highlight extends to edge
	if cursor {
		for c := col; c < x+w; c++ {
			screen.SetContent(c, y, ' ', nil, StyleDefault)
		}
	}
}

// drawText writes a string at position, clipped to maxWidth.
func drawText(screen tcell.Screen, x, y, maxWidth int, text string, style tcell.Style) {
	col := x
	for _, r := range text {
		if col-x >= maxWidth {
			break
		}
		screen.SetContent(col, y, r, nil, style)
		col++
	}
}

// HasTasks returns whether there are any tasks.
func (tl *TaskListView) HasTasks() bool {
	return len(tl.tasks) > 0
}

// SetExpanded sets which project is expanded.
func (tl *TaskListView) SetExpanded(proj string) {
	tl.expanded = proj
	tl.buildRows()
	tl.clampCursor()
}

// Empty returns placeholder text for when there are no tasks.
func (tl *TaskListView) Empty() string {
	return strings.Repeat(" ", 20) + "No tasks yet. Press 'n' to create one."
}
