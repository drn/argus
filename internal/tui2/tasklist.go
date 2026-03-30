package tui2

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
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
	rowSeparator
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
	tasks         []*model.Task
	rows          []taskRow
	running       map[string]bool
	idle          map[string]bool
	idleUnvisited map[string]bool // task IDs idle since user last viewed the agent view
	animFrame     int             // current spinner frame (time-based, updated in Draw)

	cursor   int
	offset   int // scroll offset
	expanded string // currently expanded project
	archiveExpanded bool
	archiveProject  string // expanded project within archive

	// Filter state: `/` activates filter input, typing narrows visible tasks.
	filtering bool   // true while the filter input is focused
	filter    string // current filter text (case-insensitive substring match)

	// Callback when user selects a task (Enter key).
	OnSelect func(task *model.Task)
	// Callback when user presses 'n' (new task).
	OnNew func()
	// Callback when cursor moves to a different task.
	OnCursorChange func(task *model.Task)
	// Callback when user changes task status via s/S keys.
	OnStatusChange func(task *model.Task)
	// Callback when user toggles archive on a task via 'a' key.
	OnArchive func(task *model.Task)
	// Callback when user presses 'p' to open PR URL.
	OnOpenPR func(task *model.Task)
	// Callback when user presses 'r' to rename a task.
	OnRename func(task *model.Task)
}

// NewTaskListView creates a task list view.
func NewTaskListView() *TaskListView {
	tl := &TaskListView{
		Box:           tview.NewBox(),
		running:       make(map[string]bool),
		idle:          make(map[string]bool),
		idleUnvisited: make(map[string]bool),
	}
	return tl
}

// SetTasks updates the task list and rebuilds rows.
func (tl *TaskListView) SetTasks(tasks []*model.Task) {
	// Remember current cursor target so we can restore after rebuild.
	hasPrev := tl.cursor >= 0 && tl.cursor < len(tl.rows)
	var prev taskRow
	inArchive := false
	if hasPrev {
		prev = tl.rows[tl.cursor]
		inArchive = tl.isInArchive(tl.cursor)
	}

	tl.tasks = tasks
	tl.buildRows()

	// Try to restore cursor to the same task/project.
	if hasPrev {
		tl.restoreCursor(prev, inArchive)
	}
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

// IdleSet returns a snapshot of the current idle map (for diffing newly-idle tasks).
func (tl *TaskListView) IdleSet() map[string]bool {
	cp := make(map[string]bool, len(tl.idle))
	for id := range tl.idle {
		cp[id] = true
	}
	return cp
}

// SetIdleUnvisited updates the set of idle+unvisited task IDs.
func (tl *TaskListView) SetIdleUnvisited(ids []string) {
	tl.idleUnvisited = make(map[string]bool, len(ids))
	for _, id := range ids {
		tl.idleUnvisited[id] = true
	}
}

// updateSpinnerFrame computes the current spinner frame from wall clock time.
func (tl *TaskListView) updateSpinnerFrame() {
	interval := model.SpinnerTickInterval()
	if interval > 0 {
		tl.animFrame = int(time.Now().UnixMilli()/interval.Milliseconds()) % model.SpinnerFrameCount()
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

// SelectedProject returns the project name at the current cursor position,
// whether the cursor is on a task row or a project header row.
func (tl *TaskListView) SelectedProject() string {
	if tl.cursor < 0 || tl.cursor >= len(tl.rows) {
		return ""
	}
	return tl.rows[tl.cursor].project
}

// matchesFilter returns true if the task matches the current filter.
func (tl *TaskListView) matchesFilter(t *model.Task) bool {
	if tl.filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(t.Name), strings.ToLower(tl.filter)) ||
		strings.Contains(strings.ToLower(t.Project), strings.ToLower(tl.filter))
}

// buildRows flattens tasks into display rows grouped by project.
func (tl *TaskListView) buildRows() {
	tl.rows = nil

	// Separate active and archived tasks, applying filter.
	var active, archived []*model.Task
	for _, t := range tl.tasks {
		if !tl.matchesFilter(t) {
			continue
		}
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

	filterActive := tl.filter != ""
	for _, proj := range projectOrder {
		tl.rows = append(tl.rows, taskRow{kind: rowProject, project: proj})
		if filterActive || proj == tl.expanded {
			for _, t := range projectTasks[proj] {
				tl.rows = append(tl.rows, taskRow{kind: rowTask, task: t, project: proj})
			}
		}
	}

	// Archive section
	if len(archived) > 0 {
		tl.rows = append(tl.rows, taskRow{kind: rowSeparator})
		tl.rows = append(tl.rows, taskRow{kind: rowArchiveHeader})
		if filterActive || tl.archiveExpanded {
			archOrder, archTasks := groupByProject(archived)
			for _, proj := range archOrder {
				tl.rows = append(tl.rows, taskRow{kind: rowProject, project: proj})
				if filterActive || proj == tl.archiveProject {
					for _, t := range archTasks[proj] {
						tl.rows = append(tl.rows, taskRow{kind: rowTask, task: t, project: proj})
					}
				}
			}
		}
	}
}

// groupByProject groups tasks by project name, sorted alphabetically.
func groupByProject(tasks []*model.Task) ([]string, map[string][]*model.Task) {
	groups := map[string][]*model.Task{}
	for _, t := range tasks {
		proj := t.Project
		if proj == "" {
			proj = "(no project)"
		}
		groups[proj] = append(groups[proj], t)
	}
	order := make([]string, 0, len(groups))
	for proj := range groups {
		order = append(order, proj)
	}
	sort.Strings(order)
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

// CursorDown moves the cursor down, skipping headers.
func (tl *TaskListView) CursorDown() {
	tl.moveCursor(1)
}

// CursorUp moves the cursor up, skipping headers.
func (tl *TaskListView) CursorUp() {
	tl.moveCursor(-1)
}

// moveCursor moves the cursor in the given direction (+1 down, -1 up),
// skipping project header and archive header rows so the cursor always
// lands on a task. When navigating up past a project header, the cursor
// lands on the last task of the previous project.
func (tl *TaskListView) moveCursor(dir int) {
	if len(tl.rows) == 0 {
		return
	}

	prev := tl.cursor
	defer func() {
		// Only notify when the cursor actually moved to a different position.
		if tl.cursor != prev {
			tl.notifyCursorChange()
		}
	}()

	// Step 1: Move one position in the given direction.
	tl.cursor += dir
	if tl.cursor < 0 {
		tl.cursor = 0
	}
	if tl.cursor >= len(tl.rows) {
		tl.cursor = len(tl.rows) - 1
	}
	tl.autoExpand()

	c := tl.cursor
	if c < 0 || c >= len(tl.rows) {
		return
	}

	// Already on a task row — done.
	if tl.rows[c].kind == rowTask {
		return
	}

	// On the separator — skip it in the current direction.
	if tl.rows[c].kind == rowSeparator {
		if dir > 0 {
			tl.cursor++
		} else {
			tl.skipUpPastHeader(prev)
			return
		}
		if tl.cursor < 0 {
			tl.cursor = 0
		}
		if tl.cursor >= len(tl.rows) {
			tl.cursor = len(tl.rows) - 1
		}
		c = tl.cursor
	}

	// On the archive header — skip it like a project header.
	if tl.rows[c].kind == rowArchiveHeader {
		// Auto-expand archive before skipping, so rows exist below the header.
		tl.autoExpand()
		c = tl.cursor
		if dir > 0 {
			if c+1 < len(tl.rows) {
				tl.cursor++
				tl.autoExpand()
				c = tl.cursor
				// May have landed on a project header within archive — skip that too.
				if c >= 0 && c < len(tl.rows) && tl.rows[c].kind == rowProject {
					if c+1 < len(tl.rows) && tl.rows[c+1].kind == rowTask {
						tl.cursor++
					}
				}
			}
		} else {
			tl.skipUpPastHeader(prev)
		}
		return
	}

	// On a project header — skip it.
	if dir > 0 {
		// Going down: move to the first task below this header.
		if c+1 < len(tl.rows) && tl.rows[c+1].kind == rowTask {
			tl.cursor++
		}
	} else {
		if c > 0 {
			tl.skipUpPastHeader(prev)
		} else {
			// At the top (row 0) and it's a header — stay on the previous task.
			tl.cursor = prev
		}
	}
}

// skipUpPastHeader moves the cursor up past a header row (project or archive),
// landing on the last task of the previous expanded project. If it lands on
// another header (e.g., archive header above a project header), it chains
// through one additional header. Falls back to prev if no task is reachable.
func (tl *TaskListView) skipUpPastHeader(prev int) {
	tl.cursor--
	if tl.cursor < 0 {
		tl.cursor = prev
		return
	}
	tl.autoExpand()
	c := tl.cursor

	if c >= 0 && c < len(tl.rows) && tl.rows[c].kind == rowProject {
		tl.landOnLastTask(c, prev)
		return
	}

	// Landed on separator — skip it too.
	if c >= 0 && c < len(tl.rows) && tl.rows[c].kind == rowSeparator {
		tl.cursor--
		if tl.cursor < 0 {
			tl.cursor = prev
			return
		}
		c = tl.cursor
	}

	// Landed on archive header after skipping a project header — skip it too.
	if c >= 0 && c < len(tl.rows) && tl.rows[c].kind == rowArchiveHeader {
		tl.cursor--
		if tl.cursor < 0 {
			tl.cursor = prev
			return
		}
		tl.autoExpand()
		c = tl.cursor
		// May land on separator before archive header.
		if c >= 0 && c < len(tl.rows) && tl.rows[c].kind == rowSeparator {
			tl.cursor--
			if tl.cursor < 0 {
				tl.cursor = prev
				return
			}
			c = tl.cursor
		}
		if c >= 0 && c < len(tl.rows) && tl.rows[c].kind == rowProject {
			tl.landOnLastTask(c, prev)
			return
		}
	}

	// Couldn't find a task — revert.
	if c < 0 || c >= len(tl.rows) || tl.rows[c].kind != rowTask {
		tl.cursor = prev
	}
}

// landOnLastTask sets the cursor to the last consecutive task row after
// the project header at idx. Falls back to prev if no tasks follow.
func (tl *TaskListView) landOnLastTask(idx, prev int) {
	lastTask := -1
	for i := idx + 1; i < len(tl.rows) && tl.rows[i].kind == rowTask; i++ {
		lastTask = i
	}
	if lastTask >= 0 {
		tl.cursor = lastTask
	} else {
		tl.cursor = prev
	}
}

// notifyCursorChange fires the OnCursorChange callback with the current task.
func (tl *TaskListView) notifyCursorChange() {
	if tl.OnCursorChange != nil {
		tl.OnCursorChange(tl.SelectedTask())
	}
}

// autoExpand checks if the cursor moved to a different project and rebuilds
// the row list with that project expanded. Also auto-expands the archive
// section when the cursor enters it and auto-collapses when the cursor leaves.
func (tl *TaskListView) autoExpand() {
	if len(tl.rows) == 0 {
		return
	}
	c := tl.cursor
	if c < 0 || c >= len(tl.rows) {
		return
	}
	r := tl.rows[c]

	// Determine if cursor is in the archive section.
	inArchive := tl.isInArchive(c)

	// Auto-expand/collapse archive section based on cursor position.
	if inArchive && !tl.archiveExpanded {
		tl.archiveExpanded = true
		tl.buildRows()
		// Cursor stays valid — archive rows are appended after current position.
		c = tl.cursor
		if c >= 0 && c < len(tl.rows) {
			r = tl.rows[c]
		}
	} else if !inArchive && tl.archiveExpanded {
		tl.archiveExpanded = false
		tl.buildRows()
		// Cursor is above archive section — rows above haven't changed.
	}

	// Archive header and separator — don't change project expansion.
	if r.kind == rowArchiveHeader || r.kind == rowSeparator {
		return
	}

	if inArchive {
		// Expand the project within the archive section.
		if r.project != tl.archiveProject {
			currentRow := tl.rows[c]
			tl.archiveProject = r.project
			tl.buildRows()
			tl.restoreCursor(currentRow, true)
		}
	} else {
		// Expand the project in the active section.
		if r.project != tl.expanded {
			currentRow := tl.rows[c]
			tl.expanded = r.project
			tl.buildRows()
			tl.restoreCursor(currentRow, false)
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

// restoreCursor finds the row matching target in the rebuilt rows slice
// and positions the cursor there. inArchive restricts the search to the
// archive section (or main section), preventing a project that exists in both
// sections from matching the wrong header.
func (tl *TaskListView) restoreCursor(target taskRow, inArchive bool) {
	for i, r := range tl.rows {
		if r.kind == target.kind && r.project == target.project {
			if tl.isInArchive(i) != inArchive {
				continue
			}
			if r.kind == rowProject || (r.task != nil && target.task != nil && r.task.ID == target.task.ID) {
				tl.cursor = i
				return
			}
		}
	}
	tl.clampCursor()
}

// Filtering returns whether the filter input is currently active.
func (tl *TaskListView) Filtering() bool {
	return tl.filtering
}

// Filter returns the current filter text.
func (tl *TaskListView) Filter() string {
	return tl.filter
}

// ClearFilter clears the filter and rebuilds rows.
func (tl *TaskListView) ClearFilter() {
	tl.filter = ""
	tl.filtering = false
	tl.buildRows()
	tl.clampCursor()
	tl.notifyCursorChange()
}

// applyFilter sets the filter string and rebuilds rows.
func (tl *TaskListView) applyFilter() {
	tl.buildRows()
	tl.clampCursor()
	tl.notifyCursorChange()
}

// handleFilterInput processes key events while the filter input is active.
// Returns true if the event was consumed.
func (tl *TaskListView) handleFilterInput(event *tcell.EventKey) bool {
	switch event.Key() {
	case tcell.KeyEscape:
		tl.ClearFilter()
		return true
	case tcell.KeyEnter:
		// Confirm filter — keep filter text active, exit input mode.
		tl.filtering = false
		return true
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(tl.filter) > 0 {
			_, size := utf8.DecodeLastRuneInString(tl.filter)
			tl.filter = tl.filter[:len(tl.filter)-size]
			tl.applyFilter()
		}
		return true
	case tcell.KeyUp:
		tl.CursorUp()
		return true
	case tcell.KeyDown:
		tl.CursorDown()
		return true
	case tcell.KeyRune:
		tl.filter += string(event.Rune())
		tl.applyFilter()
		return true
	}
	return false
}

// InputHandler handles key events for the task list.
func (tl *TaskListView) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return tl.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		// When filter input is active, route all keys through filter handler.
		if tl.filtering {
			tl.handleFilterInput(event)
			return
		}

		switch event.Key() {
		case tcell.KeyUp:
			tl.CursorUp()
		case tcell.KeyDown:
			tl.CursorDown()
		case tcell.KeyEnter:
			if t := tl.SelectedTask(); t != nil && t.Status != model.StatusComplete && tl.OnSelect != nil {
				tl.OnSelect(t)
			}
		case tcell.KeyEscape:
			// Clear active filter if one exists.
			if tl.filter != "" {
				tl.ClearFilter()
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
			case '/':
				tl.filtering = true
			case 's':
				if t := tl.SelectedTask(); t != nil {
					t.SetStatus(t.Status.Next())
					if tl.OnStatusChange != nil {
						tl.OnStatusChange(t)
					}
				}
			case 'S':
				if t := tl.SelectedTask(); t != nil {
					t.SetStatus(t.Status.Prev())
					if tl.OnStatusChange != nil {
						tl.OnStatusChange(t)
					}
				}
			case 'a':
				if t := tl.SelectedTask(); t != nil {
					t.Archived = !t.Archived
					if tl.OnArchive != nil {
						tl.OnArchive(t)
					}
				}
			case 'p':
				if t := tl.SelectedTask(); t != nil && t.PRURL != "" && tl.OnOpenPR != nil {
					tl.OnOpenPR(t)
				}
			case 'r':
				if t := tl.SelectedTask(); t != nil && tl.OnRename != nil {
					tl.OnRename(t)
				}
			}
		}
	})
}

// PasteHandler handles bracketed paste events in filter mode.
func (tl *TaskListView) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return tl.WrapPasteHandler(func(pastedText string, setFocus func(p tview.Primitive)) {
		if !tl.filtering {
			return
		}
		tl.filter += pastedText
		tl.applyFilter()
	})
}

// Draw renders the task list.
func (tl *TaskListView) Draw(screen tcell.Screen) {
	tl.updateSpinnerFrame()
	tl.Box.DrawForSubclass(screen, tl)
	x, y, width, height := tl.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Show filter text in panel title when active.
	title := " Tasks "
	if tl.filter != "" || tl.filtering {
		title = " Tasks [/" + tl.filter + "] "
	}

	inner := drawBorderedPanel(screen, x, y, width, height, title, StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	// Reserve bottom row for filter input when in filter mode.
	listH := inner.H
	if tl.filtering {
		listH--
		if listH < 0 {
			listH = 0
		}
		// Draw filter input on the last inner row.
		tl.drawFilterInput(screen, inner.X, inner.Y+inner.H-1, inner.W)
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
	if listH > 0 && tl.cursor >= tl.offset+listH {
		tl.offset = tl.cursor - listH + 1
	}

	for i := 0; i < listH; i++ {
		idx := tl.offset + i
		if idx >= len(tl.rows) {
			break
		}
		row := tl.rows[idx]
		isCursor := idx == tl.cursor

		switch row.kind {
		case rowProject:
			tl.drawProjectRow(screen, inner.X, inner.Y+i, inner.W, row.project)
		case rowSeparator:
			tl.drawSeparator(screen, inner.X, inner.Y+i, inner.W)
		case rowArchiveHeader:
			tl.drawArchiveHeader(screen, inner.X, inner.Y+i, inner.W)
		case rowTask:
			tl.drawTaskRow(screen, inner.X, inner.Y+i, inner.W, row.task, isCursor)
		}
	}
}

// drawFilterInput renders the filter input line at the bottom of the task list.
func (tl *TaskListView) drawFilterInput(screen tcell.Screen, x, y, w int) {
	style := tcell.StyleDefault.Foreground(ColorTitle)
	drawText(screen, x, y, 2, "/ ", style)
	inputStyle := tcell.StyleDefault.Foreground(ColorNormal)
	drawText(screen, x+2, y, w-2, tl.filter, inputStyle)
	// Draw cursor after filter text.
	cursorCol := x + 2 + ansi.StringWidth(tl.filter)
	if cursorCol < x+w {
		screen.SetContent(cursorCol, y, ' ', nil, tcell.StyleDefault.Background(ColorNormal))
	}
}

// projectStatusIcon returns the aggregated status icon and style for a project's tasks.
// Priority: in_progress > in_review > all complete > mixed > all pending.
func (tl *TaskListView) projectStatusIcon(tasks []*model.Task) (rune, tcell.Style) {
	var hasInProgress, hasInReview, hasPending, hasComplete bool
	allInProgressIdle := true

	for _, t := range tasks {
		switch t.Status {
		case model.StatusInProgress:
			if tl.idleUnvisited[t.ID] {
				// Idle+unvisited InProgress tasks count as InReview at project level.
				hasInReview = true
			} else {
				hasInProgress = true
				if tl.running[t.ID] && !tl.idle[t.ID] {
					allInProgressIdle = false
				}
			}
		case model.StatusInReview:
			hasInReview = true
		case model.StatusComplete:
			hasComplete = true
		default:
			hasPending = true
		}
	}

	switch {
	case hasInProgress:
		if allInProgressIdle {
			return '☾', tcell.StyleDefault.Foreground(ColorInProgress)
		}
		return model.SpinnerFrame(tl.animFrame), StyleInProgress
	case hasInReview:
		return '◎', StyleInReview
	case hasComplete && !hasPending:
		return '✓', StyleComplete
	case hasComplete && hasPending:
		return '✓', StyleDimmed
	default:
		return '○', StylePending
	}
}

func (tl *TaskListView) drawProjectRow(screen tcell.Screen, x, y, w int, proj string) {
	// Find tasks for this project to compute the aggregated status icon.
	var projTasks []*model.Task
	for _, t := range tl.tasks {
		p := t.Project
		if p == "" {
			p = "(no project)"
		}
		if p == proj {
			projTasks = append(projTasks, t)
		}
	}

	col := x
	// "  " prefix
	drawText(screen, col, y, 2, "  ", StyleDefault)
	col += 2

	// Status icon
	if len(projTasks) > 0 {
		icon, iconStyle := tl.projectStatusIcon(projTasks)
		screen.SetContent(col, y, icon, nil, iconStyle)
		col += 2
	}

	// Chevron
	chevron := '▸'
	if proj == tl.expanded || proj == tl.archiveProject {
		chevron = '▾'
	}
	screen.SetContent(col, y, chevron, nil, tcell.StyleDefault.Foreground(ColorDimmed))
	col += 2

	// Project name
	nameStyle := tcell.StyleDefault.Foreground(ColorProject).Bold(true)
	drawText(screen, col, y, w-(col-x), proj, nameStyle)
	col += len(proj)

	// Task count
	countStr := fmt.Sprintf(" (%d)", len(projTasks))
	if col-x+len(countStr) <= w {
		drawText(screen, col, y, len(countStr), countStr, tcell.StyleDefault.Foreground(ColorDimmed))
	}
}

func (tl *TaskListView) drawSeparator(screen tcell.Screen, x, y, w int) {
	style := tcell.StyleDefault.Foreground(ColorDimmed)
	for i := 0; i < w; i++ {
		screen.SetContent(x+i, y, '─', nil, style)
	}
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
		if tl.idleUnvisited[task.ID] {
			// Idle and not yet viewed since going idle — show as in-review.
			statusChar = '◎'
			statusStyle = StyleInReview
		} else if !tl.running[task.ID] || tl.idle[task.ID] {
			// Session absent or idle (waiting for input) — moon icon.
			statusChar = '☾'
			statusStyle = StyleInProgress
		} else {
			// Actively running — animated spinner (nerd font progress spinner).
			statusChar = model.SpinnerFrame(tl.animFrame)
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

	// Right-align elapsed time. elapsedCol also limits cursor fill below.
	elapsedCol := -1
	if elapsed != "" {
		elapsedCol = x + w - len(elapsed) - 1
	}
	if elapsedCol > col {
		drawText(screen, elapsedCol, y, len(elapsed), elapsed, tcell.StyleDefault.Foreground(ColorElapsed))
	}

	// Fill remaining cells on cursor row so the highlight extends to edge.
	// Stop before the elapsed time region so it doesn't get overwritten.
	if cursor {
		fillEnd := x + w
		if elapsedCol > col {
			fillEnd = elapsedCol
		}
		for c := col; c < fillEnd; c++ {
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

// AdjacentTask returns the next (+1) or previous (-1) task relative to the
// given task ID. Scans the full task list (not just visible/expanded rows).
// Returns nil if there is no adjacent task in that direction.
func (tl *TaskListView) AdjacentTask(currentID string, direction int) *model.Task {
	currentIdx := -1
	for i, t := range tl.tasks {
		if t.ID == currentID {
			currentIdx = i
			break
		}
	}
	if currentIdx < 0 {
		return nil
	}
	next := currentIdx + direction
	if next < 0 || next >= len(tl.tasks) {
		return nil
	}
	return tl.tasks[next]
}

// SelectByID moves the cursor to the row matching the given task ID.
// If the task is in a collapsed project, expands it first.
func (tl *TaskListView) SelectByID(id string) {
	// Find the task to get its project, then expand it so the row exists.
	for _, t := range tl.tasks {
		if t.ID == id {
			if t.Archived {
				tl.archiveExpanded = true
				tl.archiveProject = t.Project
			} else {
				tl.expanded = t.Project
			}
			tl.buildRows()
			break
		}
	}
	for i, r := range tl.rows {
		if r.kind == rowTask && r.task.ID == id {
			tl.cursor = i
			tl.notifyCursorChange()
			return
		}
	}
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
