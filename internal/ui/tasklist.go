package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/model"
)

// rowKind distinguishes project header rows from task rows in the flattened list.
type rowKind int

const (
	rowProject rowKind = iota
	rowTask
)

// row represents a single navigable row in the task list — either a project
// header or a task nested under it.
type row struct {
	kind    rowKind
	project string
	task    *model.Task // nil for project headers
}

const uncategorized = "Uncategorized"

// TaskList renders the task list view with collapsible project folders.
type TaskList struct {
	tasks    []*model.Task
	scroll   ScrollState
	theme    Theme
	width    int
	height   int
	filter   string
	filtered []*model.Task
	running  map[string]bool // task IDs with active agent sessions
	idle     map[string]bool // task IDs with sessions waiting for input
	rows     []row           // flattened display rows (headers + tasks)
	expanded string          // currently expanded project name
	tickEven bool            // toggles each tick for status icon animation
}

func NewTaskList(theme Theme) TaskList {
	return TaskList{theme: theme, running: make(map[string]bool), idle: make(map[string]bool)}
}

func (tl *TaskList) Tick() {
	tl.tickEven = !tl.tickEven
}

func (tl *TaskList) SetRunning(ids []string) {
	tl.running = toStringSet(ids)
}

func (tl *TaskList) SetIdle(ids []string) {
	tl.idle = toStringSet(ids)
}

func (tl *TaskList) SetTasks(tasks []*model.Task) {
	tl.tasks = tasks
	tl.applyFilter()
	tl.buildRows()
	tl.scroll.ClampCursor(len(tl.rows))
	tl.skipToFirstTask()
}

func (tl *TaskList) SetSize(w, h int) {
	tl.width = w
	tl.height = h
}

func (tl *TaskList) CursorUp() {
	tl.moveCursor(-1)
}

func (tl *TaskList) CursorDown() {
	tl.moveCursor(1)
}

// moveCursor moves the cursor in the given direction (+1 down, -1 up),
// skipping project header rows so the cursor always lands on a task.
// When navigating up into a new project, the cursor lands on the last task
// of that project rather than the first.
func (tl *TaskList) moveCursor(dir int) {
	if len(tl.rows) == 0 {
		return
	}

	prev := tl.scroll.Cursor()

	// Step 1: Move one position in the given direction.
	if dir > 0 {
		tl.scroll.CursorDown(len(tl.rows), tl.visibleRows())
	} else {
		tl.scroll.CursorUp()
	}
	tl.autoExpand()

	c := tl.scroll.Cursor()
	if c < 0 || c >= len(tl.rows) || tl.rows[c].kind == rowTask {
		return // Already on a task row.
	}

	// On a project header — skip it.
	if dir > 0 {
		// Going down: move to the first task below this header.
		if c+1 < len(tl.rows) && tl.rows[c+1].kind == rowTask {
			tl.scroll.CursorDown(len(tl.rows), tl.visibleRows())
		}
	} else {
		// Going up: expand the project above and land on its last task.
		if c > 0 {
			tl.scroll.CursorUp()
			tl.autoExpand()
			c = tl.scroll.Cursor()
			if c >= 0 && c < len(tl.rows) && tl.rows[c].kind == rowProject {
				// Find the last task row in this expanded project.
				lastTask := -1
				for i := c + 1; i < len(tl.rows) && tl.rows[i].kind == rowTask; i++ {
					lastTask = i
				}
				if lastTask >= 0 {
					tl.scroll.SetCursor(lastTask)
					visible := tl.visibleRows()
					if lastTask >= tl.scroll.Offset()+visible {
						tl.scroll.SetOffset(lastTask - visible + 1)
					}
				}
			}
		} else {
			// At the top (row 0) and it's a project header — stay on the previous task.
			tl.scroll.SetCursor(prev)
		}
	}
}

// skipToFirstTask moves the cursor to the first task row if it's currently on
// a project header. Used after building/resetting the row list.
func (tl *TaskList) skipToFirstTask() {
	c := tl.scroll.Cursor()
	if c >= 0 && c < len(tl.rows) && tl.rows[c].kind == rowProject {
		for i := c; i < len(tl.rows); i++ {
			if tl.rows[i].kind == rowTask {
				tl.scroll.SetCursor(i)
				return
			}
		}
	}
}

func (tl *TaskList) Selected() *model.Task {
	if len(tl.rows) == 0 {
		return nil
	}
	c := tl.scroll.Cursor()
	if c < 0 || c >= len(tl.rows) {
		return nil
	}
	r := tl.rows[c]
	if r.kind == rowTask {
		return r.task
	}
	// On a project header — return the first task in the expanded project.
	if c+1 < len(tl.rows) && tl.rows[c+1].kind == rowTask {
		return tl.rows[c+1].task
	}
	return nil
}

// AdjacentTask returns the next (dir=+1) or previous (dir=-1) task relative
// to the given task ID, using the filtered task ordering. Returns nil if there
// is no adjacent task in that direction.
func (tl *TaskList) AdjacentTask(taskID string, dir int) *model.Task {
	if len(tl.filtered) == 0 {
		return nil
	}
	idx := -1
	for i, t := range tl.filtered {
		if t.ID == taskID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	next := idx + dir
	if next < 0 || next >= len(tl.filtered) {
		return nil
	}
	return tl.filtered[next]
}

func (tl *TaskList) SetFilter(f string) {
	tl.filter = f
	tl.applyFilter()
	// Reset expanded if the currently expanded project has no tasks after filtering.
	if tl.expanded != "" {
		found := false
		for _, t := range tl.filtered {
			p := t.Project
			if p == "" {
				p = uncategorized
			}
			if p == tl.expanded {
				found = true
				break
			}
		}
		if !found {
			tl.expanded = ""
		}
	}
	tl.buildRows()
	tl.scroll.Reset()
	tl.skipToFirstTask()
}

func (tl *TaskList) applyFilter() {
	if tl.filter == "" {
		tl.filtered = tl.tasks
		return
	}
	f := strings.ToLower(tl.filter)
	tl.filtered = nil
	for _, t := range tl.tasks {
		if strings.Contains(strings.ToLower(t.Name), f) ||
			strings.Contains(strings.ToLower(t.Project), f) {
			tl.filtered = append(tl.filtered, t)
		}
	}
}

// projectGroup holds tasks belonging to a single project along with a
// priority used for sort ordering.
type projectGroup struct {
	name     string
	tasks    []*model.Task
	priority int // lower = higher in the list
}

// buildRows groups filtered tasks by project and builds the flattened row list.
func (tl *TaskList) buildRows() {
	// Group tasks by project.
	groupMap := make(map[string][]*model.Task)
	var order []string
	for _, t := range tl.filtered {
		proj := t.Project
		if proj == "" {
			proj = uncategorized
		}
		if _, exists := groupMap[proj]; !exists {
			order = append(order, proj)
		}
		groupMap[proj] = append(groupMap[proj], t)
	}

	// Build sortable groups with priority.
	groups := make([]projectGroup, 0, len(order))
	for _, name := range order {
		tasks := groupMap[name]
		pri := projectPriority(tasks)
		if name == uncategorized {
			pri += 100 // push to bottom within its tier
		}
		groups = append(groups, projectGroup{name: name, tasks: tasks, priority: pri})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].priority != groups[j].priority {
			return groups[i].priority < groups[j].priority
		}
		return groups[i].name < groups[j].name
	})

	// Reset expanded if the project no longer exists (e.g. all its tasks were pruned).
	if tl.expanded != "" {
		found := false
		for _, g := range groups {
			if g.name == tl.expanded {
				found = true
				break
			}
		}
		if !found {
			tl.expanded = ""
		}
	}

	// If nothing is expanded, expand the first project.
	if tl.expanded == "" && len(groups) > 0 {
		tl.expanded = groups[0].name
	}

	tl.rows = nil
	for _, g := range groups {
		tl.rows = append(tl.rows, row{kind: rowProject, project: g.name})
		if g.name == tl.expanded {
			for _, t := range g.tasks {
				tl.rows = append(tl.rows, row{kind: rowTask, project: g.name, task: t})
			}
		}
	}
}

// projectPriority returns a sort key: 0 for in-progress, 1 for pending, 2 for all-complete.
func projectPriority(tasks []*model.Task) int {
	hasInProgress := false
	hasPending := false
	for _, t := range tasks {
		switch t.Status {
		case model.StatusInProgress:
			hasInProgress = true
		case model.StatusPending:
			hasPending = true
		}
	}
	if hasInProgress {
		return 0
	}
	if hasPending {
		return 1
	}
	return 2
}

// autoExpand checks if the cursor moved to a different project and rebuilds
// the row list with that project expanded.
func (tl *TaskList) autoExpand() {
	if len(tl.rows) == 0 {
		return
	}
	c := tl.scroll.Cursor()
	if c < 0 || c >= len(tl.rows) {
		return
	}
	target := tl.rows[c].project
	if target == tl.expanded {
		return
	}

	// Remember what the cursor is pointing at so we can restore it.
	currentRow := tl.rows[c]
	tl.expanded = target
	tl.buildRows()
	tl.restoreCursor(currentRow)
}

// restoreCursor finds the row matching currentRow in the rebuilt rows slice
// and positions the cursor there.
func (tl *TaskList) restoreCursor(target row) {
	for i, r := range tl.rows {
		if r.kind == target.kind && r.project == target.project {
			if r.kind == rowProject || r.task == target.task {
				tl.scroll.SetCursor(i)
				// Ensure offset is reasonable after cursor jump.
				visible := tl.visibleRows()
				if i < tl.scroll.Offset() {
					tl.scroll.SetOffset(i)
				} else if i >= tl.scroll.Offset()+visible {
					tl.scroll.SetOffset(i - visible + 1)
				}
				return
			}
		}
	}
	tl.scroll.ClampCursor(len(tl.rows))
}

func (tl *TaskList) visibleRows() int {
	// Each row takes 1 line.
	return max(tl.height, 1)
}

func (tl TaskList) View() string {
	if len(tl.rows) == 0 {
		return "\n" + tl.theme.Dimmed.Render("    No tasks yet. Press [n] to create one.")
	}

	var b strings.Builder
	visible := tl.visibleRows()
	offset := tl.scroll.Offset()
	cursor := tl.scroll.Cursor()
	end := min(offset+visible, len(tl.rows))

	for i := offset; i < end; i++ {
		r := tl.rows[i]
		selected := i == cursor

		if r.kind == rowProject {
			tl.renderProjectHeader(&b, r.project, selected)
		} else {
			tl.renderTaskRow(&b, r.task, selected)
		}
	}

	return b.String()
}

// projectTasks returns the filtered tasks belonging to the given project name.
func (tl TaskList) projectTasks(project string) []*model.Task {
	var tasks []*model.Task
	for _, t := range tl.filtered {
		p := t.Project
		if p == "" {
			p = uncategorized
		}
		if p == project {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

// taskStatusIcon returns the styled status icon for a task, including
// animation logic for in-progress tasks (running/idle/tick).
func (tl TaskList) taskStatusIcon(t *model.Task) string {
	displayText := t.Status.Display()
	if t.Status == model.StatusInProgress {
		if !tl.running[t.ID] || tl.idle[t.ID] {
			displayText = "\uF186" // moon: idle
		} else if tl.tickEven {
			displayText = t.Status.DisplayAlt()
		}
	}
	return tl.statusStyle(t.Status).Render(displayText)
}

// projectStatusIcon returns a single styled icon summarizing the aggregate
// status of all tasks in a project. Priority: in_progress > in_review > all
// complete > mixed (partial) > all pending.
func (tl TaskList) projectStatusIcon(tasks []*model.Task) string {
	var hasInProgress, hasInReview, hasPending, hasComplete bool
	var allInProgressIdle bool = true

	for _, t := range tasks {
		switch t.Status {
		case model.StatusInProgress:
			hasInProgress = true
			if tl.running[t.ID] && !tl.idle[t.ID] {
				allInProgressIdle = false
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
		displayText := model.StatusInProgress.Display()
		if allInProgressIdle {
			displayText = "\uF186" // moon: all in-progress tasks idle
		} else if tl.tickEven {
			displayText = model.StatusInProgress.DisplayAlt()
		}
		return tl.statusStyle(model.StatusInProgress).Render(displayText)
	case hasInReview:
		return tl.statusStyle(model.StatusInReview).Render(model.StatusInReview.Display())
	case hasComplete && !hasPending:
		return tl.statusStyle(model.StatusComplete).Render(model.StatusComplete.Display())
	case hasComplete && hasPending:
		return tl.theme.Dimmed.Render(model.StatusComplete.Display())
	default:
		return tl.statusStyle(model.StatusPending).Render(model.StatusPending.Display())
	}
}

func (tl TaskList) renderProjectHeader(b *strings.Builder, project string, selected bool) {
	chevron := "▸"
	if project == tl.expanded {
		chevron = "▾"
	}

	tasks := tl.projectTasks(project)
	count := len(tasks)

	nameStyle := tl.theme.Section
	chevronStyle := tl.theme.Dimmed
	cursorStr := "  "
	if selected {
		nameStyle = tl.theme.Selected
		chevronStyle = tl.theme.Selected
		cursorStr = tl.theme.Selected.Render(" >")
	}

	icon := tl.projectStatusIcon(tasks)
	countStr := tl.theme.Dimmed.Render(fmt.Sprintf(" (%d)", count))
	b.WriteString(fmt.Sprintf("%s %s %s %s%s\n", cursorStr, icon, chevronStyle.Render(chevron), nameStyle.Render(project), countStr))
}

func (tl TaskList) renderTaskRow(b *strings.Builder, t *model.Task, selected bool) {
	icon := tl.taskStatusIcon(t)

	nameStyle := tl.theme.Normal
	if selected {
		nameStyle = tl.theme.Selected
	}
	if t.Status == model.StatusComplete {
		nameStyle = tl.theme.Dimmed
	}
	name := nameStyle.Render(t.Name)

	cursorStr := "    "
	if selected {
		cursorStr = tl.theme.Selected.Render("   >")
	}

	// Duration in parentheses immediately after name
	elapsed := ""
	if e := t.ElapsedString(); e != "" {
		elapsed = " " + tl.theme.Elapsed.Render("("+e+")")
	}

	b.WriteString(fmt.Sprintf("%s %s  %s%s\n", cursorStr, icon, name, elapsed))
}

func (tl TaskList) statusStyle(s model.Status) lipgloss.Style {
	switch s {
	case model.StatusInProgress:
		return tl.theme.InProgress
	case model.StatusInReview:
		return tl.theme.InReview
	case model.StatusComplete:
		return tl.theme.Complete
	default:
		return tl.theme.Pending
	}
}
