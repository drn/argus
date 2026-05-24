package taskview

import (
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// rowKind identifies what kind of row is displayed. Underlying type is
// uint8 (not int) so `rowsSignature` can mix it directly into the FNV
// hash without a gosec G115 overflow check; there are only ~5 values.
type rowKind uint8

const (
	rowTask rowKind = iota
	rowProject
	rowArchiveHeader
	rowPinnedHeader
	rowSeparator
	// rowPinnedTrailingSep marks the boundary between the Pinned section and
	// the (header-less) Active section below it. `sectionAt` uses it as an
	// explicit anchor — without a dedicated kind, classifying separators
	// would devolve into a brittle "next-row-is-a-header" heuristic.
	rowPinnedTrailingSep
)

// rowSection identifies which section a row belongs to. The constant order
// matches the visual top-to-bottom render order so any future code that
// compares sections numerically gets sensible results.
type rowSection int

const (
	sectionPinned rowSection = iota
	sectionActive
	sectionArchive
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
	needsInput    map[string]bool // task IDs whose agent appears blocked on a user prompt
	animFrame     int             // current spinner frame (time-based, updated in Draw)

	cursor          int
	offset          int    // scroll offset
	expanded        string // currently expanded project
	archiveExpanded bool
	archiveProject  string // expanded project within archive
	// Pinned section is always fully expanded — pinning is an explicit
	// "keep this visible" action, so auto-collapsing it on cursor leave
	// would defeat the purpose. There are intentionally no
	// `pinnedExpanded` / `pinnedProject` fields.

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
	// Callback when user toggles pinned on a task via 'P' key.
	OnPin func(task *model.Task)
	// Callback when user presses 'r' to rename a task.
	OnRename func(task *model.Task)
	// Callback when user presses 'c' to copy task prompt.
	OnCopyPrompt func(task *model.Task)
	// Callback fired after buildRows when the row composition changes.
	// Used by App to force a tcell Sync — rows shifting under tview's
	// diff-based emit is a known source of bleed-through in tmux.
	OnLayoutChange func()
	// Callback fired when filter-input mode toggles. Distinct from
	// OnLayoutChange so the App can log a different reason — filter toggle
	// reserves/releases the bottom row without changing the row signature.
	// See gotchas/ui-threading.md.
	OnFilterToggle func()

	// Signature of the last buildRows output. Used to suppress
	// OnLayoutChange when the rebuild produced the same rows.
	// Initialized to ^uint64(0) so the very first build always fires,
	// even on the (astronomically unlikely) chance the first hash is 0.
	lastRowsSig uint64
}

// NewTaskListView creates a task list view.
func NewTaskListView() *TaskListView {
	tl := &TaskListView{
		Box:           tview.NewBox(),
		running:       make(map[string]bool),
		idle:          make(map[string]bool),
		idleUnvisited: make(map[string]bool),
		needsInput:    make(map[string]bool),
		lastRowsSig:   ^uint64(0), // sentinel — first build always fires OnLayoutChange
	}
	return tl
}

// SetTasks updates the task list and rebuilds rows.
func (tl *TaskListView) SetTasks(tasks []*model.Task) {
	// Remember current cursor target so we can restore after rebuild.
	hasPrev := tl.cursor >= 0 && tl.cursor < len(tl.rows)
	var prev taskRow
	sec := sectionActive
	if hasPrev {
		prev = tl.rows[tl.cursor]
		sec = tl.sectionAt(tl.cursor)
	}

	tl.tasks = tasks
	tl.buildRows()

	// Try to restore cursor to the same task/project.
	if hasPrev {
		tl.restoreCursor(prev, sec)
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

// SetNeedsInput updates the set of task IDs whose agent appears blocked on a
// user prompt (Claude permission dialog, AskUserQuestion, etc.). Detection
// runs on the app tick from a tail-of-PTY scan; see agent.DetectNeedsInput.
func (tl *TaskListView) SetNeedsInput(ids []string) {
	tl.needsInput = make(map[string]bool, len(ids))
	for _, id := range ids {
		tl.needsInput[id] = true
	}
}

// updateSpinnerFrame computes the current spinner frame from wall clock time.
func (tl *TaskListView) updateSpinnerFrame() {
	interval := widget.SpinnerTickInterval()
	if interval > 0 {
		tl.animFrame = int(time.Now().UnixMilli()/interval.Milliseconds()) % widget.SpinnerFrameCount()
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
// Filter terms are split by whitespace. All terms must match at least one
// of the project name or task name (case-insensitive substring). This allows
// queries like "forge download" to match a task "Download-this-video" in
// the "forge" project.
func (tl *TaskListView) matchesFilter(t *model.Task) bool {
	if tl.filter == "" {
		return true
	}
	terms := strings.Fields(strings.ToLower(tl.filter))
	name := strings.ToLower(t.Name)
	proj := strings.ToLower(t.Project)
	for _, term := range terms {
		if !strings.Contains(name, term) && !strings.Contains(proj, term) {
			return false
		}
	}
	return true
}

// buildRows flattens tasks into display rows grouped by project.
func (tl *TaskListView) buildRows() {
	tl.rows = nil

	// Separate pinned, active, and archived tasks, applying filter.
	// Pinned takes precedence over all other section flags so a pinned-and-archived
	// task surfaces at the top; the precedence order below archive > active is
	// preserved for unpinned tasks.
	var pinned, active, archived []*model.Task
	for _, t := range tl.tasks {
		if !tl.matchesFilter(t) {
			continue
		}
		switch {
		case t.Pinned:
			pinned = append(pinned, t)
		case t.Archived:
			archived = append(archived, t)
		default:
			active = append(active, t)
		}
	}

	filterActive := tl.filter != ""

	// Pinned section (above active). Always fully expanded — see note on
	// the pinnedExpanded comment in the struct. Active has no header, so we
	// emit an explicit `rowPinnedTrailingSep` to mark the Pinned/Active
	// boundary. The trailing marker is only needed when Active has content;
	// Archive emits its own leading separator below.
	if len(pinned) > 0 {
		tl.rows = append(tl.rows, taskRow{kind: rowPinnedHeader})
		pinOrder, pinTasks := groupByProject(pinned)
		for _, proj := range pinOrder {
			tl.rows = append(tl.rows, taskRow{kind: rowProject, project: proj})
			for _, t := range pinTasks[proj] {
				tl.rows = append(tl.rows, taskRow{kind: rowTask, task: t, project: proj})
			}
		}
		if len(active) > 0 {
			tl.rows = append(tl.rows, taskRow{kind: rowPinnedTrailingSep})
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

	// Notify on row composition change so the App can force a tcell Sync.
	// Rows shifting under tview's diff-based emit causes bleed-through in
	// tmux/Alacritty — see gotchas/ui-threading.md.
	sig := tl.rowsSignature()
	if sig != tl.lastRowsSig {
		tl.lastRowsSig = sig
		if tl.OnLayoutChange != nil {
			tl.OnLayoutChange()
		}
	}
}

// rowsSignature returns a 64-bit FNV-1a hash of the rendered row composition
// (kind + project + task ID + title + status per row). Title and status are
// included because auto-naming and status transitions change row width
// without changing structure — and tcell's diff emit leaves stale tail
// characters from the previous (longer) title on screen until forceRedraw
// runs. Cheap to compute. Casting `r.kind` through a byte avoids a switch
// and keeps any future rowKind value distinct without code changes here.
func (tl *TaskListView) rowsSignature() uint64 {
	h := fnv.New64a()
	for _, r := range tl.rows {
		_, _ = h.Write([]byte{byte(r.kind), 0})
		_, _ = io.WriteString(h, r.project)
		_, _ = h.Write([]byte{0})
		if r.task != nil {
			_, _ = io.WriteString(h, r.task.ID)
			_, _ = h.Write([]byte{0})
			_, _ = io.WriteString(h, r.task.Name)
			_, _ = h.Write([]byte{0})
			_, _ = io.WriteString(h, r.task.Status.String())
		}
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
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

	// On a separator (regular or Pinned-trailing) — skip it in the current direction.
	if tl.rows[c].kind == rowSeparator || tl.rows[c].kind == rowPinnedTrailingSep {
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

	// On a section header (pinned or archive) — skip it like a project header.
	if tl.rows[c].kind == rowArchiveHeader || tl.rows[c].kind == rowPinnedHeader {
		// Auto-expand section before skipping, so rows exist below the header.
		tl.autoExpand()
		c = tl.cursor
		if dir > 0 {
			if c+1 < len(tl.rows) {
				tl.cursor++
				tl.autoExpand()
				c = tl.cursor
				// May have landed on a project header within the section — skip that too.
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

// skipUpPastHeader moves the cursor up past header/separator rows (project,
// pinned, archive), landing on the last task of the previous expanded project.
// With three sections the cursor may need to chain through multiple headers
// when moving out of a lower section through a collapsed one. Falls back to
// prev if no task is reachable.
func (tl *TaskListView) skipUpPastHeader(prev int) {
	for {
		tl.cursor--
		if tl.cursor < 0 {
			tl.cursor = prev
			return
		}
		tl.autoExpand()
		c := tl.cursor
		if c < 0 || c >= len(tl.rows) {
			tl.cursor = prev
			return
		}
		switch tl.rows[c].kind {
		case rowTask:
			return
		case rowProject:
			tl.landOnLastTask(c, prev)
			return
		}
		// Separator or archive header — keep going up.
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

// autoExpand checks if the cursor moved to a different project or section and
// rebuilds the row list so exactly one project is expanded in the cursor's
// section, and the archive section is expanded only when the cursor is inside it.
func (tl *TaskListView) autoExpand() {
	if len(tl.rows) == 0 {
		return
	}
	c := tl.cursor
	if c < 0 || c >= len(tl.rows) {
		return
	}
	r := tl.rows[c]
	sec := tl.sectionAt(c)

	wantArchiveExpanded := sec == sectionArchive

	if tl.archiveExpanded != wantArchiveExpanded {
		tl.archiveExpanded = wantArchiveExpanded
		tl.buildRows()
		tl.restoreCursor(r, sec)
		c = tl.cursor
		if c < 0 || c >= len(tl.rows) {
			return
		}
		r = tl.rows[c]
		sec = tl.sectionAt(c)
	}

	// Section header or separator — don't change project expansion.
	if r.kind == rowArchiveHeader || r.kind == rowPinnedHeader || r.kind == rowSeparator || r.kind == rowPinnedTrailingSep {
		return
	}

	switch sec {
	case sectionPinned:
		// Pinned is always fully expanded — no per-project collapse to
		// auto-toggle. Cursor still tracks the active project for cosmetic
		// chevron purposes if any future code reads it.
	case sectionArchive:
		if r.project != tl.archiveProject {
			tl.archiveProject = r.project
			tl.buildRows()
			tl.restoreCursor(r, sec)
		}
	case sectionActive:
		if r.project != tl.expanded {
			tl.expanded = r.project
			tl.buildRows()
			tl.restoreCursor(r, sec)
		}
	}
}

// taskSection returns which section a task would appear in, using the same
// precedence as buildRows (pinned wins over archive).
func taskSection(t *model.Task) rowSection {
	switch {
	case t.Pinned:
		return sectionPinned
	case t.Archived:
		return sectionArchive
	default:
		return sectionActive
	}
}

// sectionAt returns which section the row at idx belongs to. It scans upward
// and returns the first section anchor encountered: a section header (Pinned /
// Archive) or `rowPinnedTrailingSep`. Plain `rowSeparator` rows lead INTO
// the Archive header below them, so they are transparent — keep scanning.
// `rowPinnedTrailingSep` is the explicit Pinned-to-Active boundary: anything
// at or below it (until the next header) belongs to Active.
func (tl *TaskListView) sectionAt(idx int) rowSection {
	for i := idx; i >= 0; i-- {
		switch tl.rows[i].kind {
		case rowPinnedHeader:
			return sectionPinned
		case rowArchiveHeader:
			return sectionArchive
		case rowPinnedTrailingSep:
			return sectionActive
		}
	}
	return sectionActive
}

// restoreCursor finds the row matching target in the rebuilt rows slice
// and positions the cursor there. sec restricts the search to a single
// section, preventing a project that exists in multiple sections from
// matching the wrong one.
func (tl *TaskListView) restoreCursor(target taskRow, sec rowSection) {
	for i, r := range tl.rows {
		if r.kind != target.kind {
			continue
		}
		if tl.sectionAt(i) != sec {
			continue
		}
		switch r.kind {
		case rowTask:
			if target.task != nil && r.task != nil && r.task.ID == target.task.ID {
				tl.cursor = i
				return
			}
		case rowProject:
			if r.project == target.project {
				tl.cursor = i
				return
			}
		case rowArchiveHeader, rowPinnedHeader, rowSeparator, rowPinnedTrailingSep:
			// Headers and section separators are unique within a section —
			// take the first match in the desired section.
			tl.cursor = i
			return
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

// setFiltering toggles filter-input mode. When the flag flips, the panel's
// bottom row swaps between a task row and the filter input — a layout shift
// that doesn't change `rowsSignature` (rows are unchanged, only `listH` is
// reduced by one). Without notifying OnFilterToggle the App can't Sync, and
// tcell's diff-based emit leaves stale cells from the previous bottom row.
// See gotchas/ui-threading.md. Fires OnFilterToggle (not OnLayoutChange) so
// the ux.log entry distinguishes filter-toggle from row-composition changes
// — a debugger reading the log can tell which class of event triggered Sync.
func (tl *TaskListView) setFiltering(v bool) {
	if tl.filtering == v {
		return
	}
	tl.filtering = v
	if tl.OnFilterToggle != nil {
		tl.OnFilterToggle()
	}
}

// ClearFilter clears the filter and rebuilds rows.
func (tl *TaskListView) ClearFilter() {
	tl.filter = ""
	tl.setFiltering(false)
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
	hasAlt := event.Modifiers()&tcell.ModAlt != 0
	switch event.Key() {
	case tcell.KeyEscape:
		tl.ClearFilter()
		return true
	case tcell.KeyEnter:
		// Confirm filter — keep filter text active, exit input mode.
		tl.setFiltering(false)
		return true
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if hasAlt {
			// Option+Delete: delete word left.
			runes := []rune(tl.filter)
			runes, _ = widget.DeleteWordLeft(runes, len(runes))
			tl.filter = string(runes)
			tl.applyFilter()
			return true
		}
		if len(tl.filter) > 0 {
			_, size := utf8.DecodeLastRuneInString(tl.filter)
			tl.filter = tl.filter[:len(tl.filter)-size]
			tl.applyFilter()
		}
		return true
	case tcell.KeyCtrlU:
		// Ctrl+U: clear entire filter text (Cmd+Delete on macOS).
		tl.filter = ""
		tl.applyFilter()
		return true
	case tcell.KeyCtrlW:
		// Ctrl+W: delete word left.
		runes := []rune(tl.filter)
		runes, _ = widget.DeleteWordLeft(runes, len(runes))
		tl.filter = string(runes)
		tl.applyFilter()
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
				tl.setFiltering(true)
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
					t.SetArchived(!t.Archived)
					if tl.OnArchive != nil {
						tl.OnArchive(t)
					}
				}
			case 'P':
				if t := tl.SelectedTask(); t != nil {
					t.SetPinned(!t.Pinned)
					if tl.OnPin != nil {
						tl.OnPin(t)
					}
				}
			case 'r':
				if t := tl.SelectedTask(); t != nil && tl.OnRename != nil {
					tl.OnRename(t)
				}
			case 'c':
				if t := tl.SelectedTask(); t != nil && t.Prompt != "" && tl.OnCopyPrompt != nil {
					tl.OnCopyPrompt(t)
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
	inner := widget.DrawBorderedPanel(screen, x, y, width, height, title, theme.StyleBorder)
	if tl.filter != "" || tl.filtering {
		filterStr := "/" + tl.filter
		col := x + 1 + ansi.StringWidth(title) // after the title text
		if col < x+width-1 {
			screen.SetContent(col, y, '[', nil, theme.StyleBorder)
			col++
		}
		for _, r := range filterStr {
			if col >= x+width-1 {
				break
			}
			screen.SetContent(col, y, r, nil, theme.StyleFilter)
			col++
		}
		if col < x+width-1 {
			screen.SetContent(col, y, ']', nil, theme.StyleBorder)
		}
	}
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
			tl.drawProjectRow(screen, inner.X, inner.Y+i, inner.W, row.project, tl.sectionAt(idx))
		case rowSeparator, rowPinnedTrailingSep:
			tl.drawSeparator(screen, inner.X, inner.Y+i, inner.W)
		case rowArchiveHeader:
			tl.drawArchiveHeader(screen, inner.X, inner.Y+i, inner.W)
		case rowPinnedHeader:
			tl.drawPinnedHeader(screen, inner.X, inner.Y+i, inner.W)
		case rowTask:
			tl.drawTaskRow(screen, inner.X, inner.Y+i, inner.W, row.task, isCursor)
		}
	}
}

// drawFilterInput renders the filter input line at the bottom of the task list.
func (tl *TaskListView) drawFilterInput(screen tcell.Screen, x, y, w int) {
	style := tcell.StyleDefault.Foreground(theme.ColorTitle)
	widget.DrawText(screen, x, y, 2, "/ ", style)
	inputStyle := tcell.StyleDefault.Foreground(theme.ColorNormal)
	widget.DrawText(screen, x+2, y, w-2, tl.filter, inputStyle)
	// Draw cursor after filter text.
	cursorCol := x + 2 + ansi.StringWidth(tl.filter)
	if cursorCol < x+w {
		screen.SetContent(cursorCol, y, ' ', nil, tcell.StyleDefault.Background(theme.ColorNormal))
	}
}

// projectStatusIcon returns the aggregated status icon and style for a project's tasks.
// Priority: any needs-input > any actively running > any in_review > idle in_progress > all complete > mixed > all pending.
// Needs-input outranks "actively running" so a single blocked task in a busy project still surfaces to the user.
func (tl *TaskListView) projectStatusIcon(tasks []*model.Task) (rune, tcell.Style) {
	var hasNeedsInput, hasActivelyRunning, hasIdleInProgress, hasInReview, hasPending, hasComplete bool

	for _, t := range tasks {
		switch t.Status {
		case model.StatusInProgress:
			if tl.needsInput[t.ID] {
				hasNeedsInput = true
			} else if tl.idleUnvisited[t.ID] {
				// Idle+unvisited InProgress tasks count as InReview at project level.
				hasInReview = true
			} else {
				hasIdleInProgress = true
				if tl.running[t.ID] && !tl.idle[t.ID] {
					hasActivelyRunning = true
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
	case hasNeedsInput:
		return theme.IconNeedsInput, theme.StyleNeedsInput
	case hasActivelyRunning:
		return widget.SpinnerFrame(tl.animFrame), theme.StyleInProgress
	case hasInReview:
		return theme.IconMoonStars, theme.StyleInReview
	case hasIdleInProgress:
		// All in-progress tasks are idle (waiting for input).
		return theme.IconMoonOutline, tcell.StyleDefault.Foreground(theme.ColorInReview)
	case hasComplete && !hasPending:
		return '✓', theme.StyleComplete
	case hasComplete && hasPending:
		return '✓', theme.StyleDimmed
	default:
		return '○', theme.StylePending
	}
}

func (tl *TaskListView) drawProjectRow(screen tcell.Screen, x, y, w int, proj string, sec rowSection) {
	// Find tasks for this project within the row's section. Aggregating across
	// all sections would leak status (and expansion state) from a same-named
	// project in a different section into this header.
	var projTasks []*model.Task
	for _, t := range tl.tasks {
		if taskSection(t) != sec {
			continue
		}
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
	widget.DrawText(screen, col, y, 2, "  ", theme.StyleDefault)
	col += 2

	// Status icon
	if len(projTasks) > 0 {
		icon, iconStyle := tl.projectStatusIcon(projTasks)
		screen.SetContent(col, y, icon, nil, iconStyle)
		col += 2
	}

	// Chevron — match only the expansion that corresponds to the row's section.
	chevron := '▸'
	switch sec {
	case sectionActive:
		if proj == tl.expanded {
			chevron = '▾'
		}
	case sectionArchive:
		if proj == tl.archiveProject {
			chevron = '▾'
		}
	case sectionPinned:
		// Pinned is always fully expanded.
		chevron = '▾'
	}
	screen.SetContent(col, y, chevron, nil, tcell.StyleDefault.Foreground(theme.ColorDimmed))
	col += 2

	// Project name
	nameStyle := tcell.StyleDefault.Foreground(theme.ColorProject).Bold(true)
	widget.DrawText(screen, col, y, w-(col-x), proj, nameStyle)
	col += len(proj)

	// Task count
	countStr := fmt.Sprintf(" (%d)", len(projTasks))
	if col-x+len(countStr) <= w {
		widget.DrawText(screen, col, y, len(countStr), countStr, tcell.StyleDefault.Foreground(theme.ColorDimmed))
	}
}

func (tl *TaskListView) drawSeparator(screen tcell.Screen, x, y, w int) {
	style := tcell.StyleDefault.Foreground(theme.ColorDimmed)
	for i := 0; i < w; i++ {
		screen.SetContent(x+i, y, '─', nil, style)
	}
}

func (tl *TaskListView) drawArchiveHeader(screen tcell.Screen, x, y, w int) {
	style := tcell.StyleDefault.Foreground(theme.ColorDimmed).Bold(true)
	indicator := "▸"
	if tl.archiveExpanded {
		indicator = "▾"
	}
	text := fmt.Sprintf("  %s Archive", indicator)
	widget.DrawText(screen, x, y, w, text, style)
}

func (tl *TaskListView) drawPinnedHeader(screen tcell.Screen, x, y, w int) {
	style := tcell.StyleDefault.Foreground(theme.ColorProject).Bold(true)
	// No collapse indicator — Pinned is always expanded.
	widget.DrawText(screen, x, y, w, "  ★ Pinned", style)
}

func (tl *TaskListView) drawTaskRow(screen tcell.Screen, x, y, w int, task *model.Task, cursor bool) {
	// Status indicator
	var statusChar rune
	var statusStyle tcell.Style
	switch task.Status {
	case model.StatusPending:
		statusChar = '○'
		statusStyle = theme.StylePending
	case model.StatusInProgress:
		switch {
		case tl.needsInput[task.ID]:
			// Agent rendered a blocking prompt — call attention regardless of
			// whether the user has visited since going idle.
			statusChar = theme.IconNeedsInput
			statusStyle = theme.StyleNeedsInput
		case tl.idleUnvisited[task.ID]:
			// Idle and not yet viewed since going idle — moon with stars.
			statusChar = theme.IconMoonStars
			statusStyle = theme.StyleInReview
		case !tl.running[task.ID] || tl.idle[task.ID]:
			// Session absent or idle (waiting for input) — moon icon.
			statusChar = theme.IconMoonOutline
			statusStyle = theme.StyleInReview
		default:
			// Actively running — animated spinner (nerd font progress spinner).
			statusChar = widget.SpinnerFrame(tl.animFrame)
			statusStyle = theme.StyleInProgress
		}
	case model.StatusInReview:
		statusChar = theme.IconMoonStars
		statusStyle = theme.StyleInReview
	case model.StatusComplete:
		statusChar = '✓'
		statusStyle = theme.StyleComplete
	default:
		statusChar = '○'
		statusStyle = theme.StylePending
	}

	// Build the row
	nameStyle := theme.StyleNormal
	if cursor {
		nameStyle = theme.StyleSelected
	}

	// Elapsed time
	elapsed := task.ElapsedString()

	// Layout: "    ● name              3m"
	prefix := "    "
	col := x
	widget.DrawText(screen, col, y, len(prefix), prefix, theme.StyleDefault)
	col += len(prefix)

	screen.SetContent(col, y, statusChar, nil, statusStyle)
	col += 2 // status char + space

	// Name gets priority; elapsed is right-aligned.
	nameStr := task.Name
	maxNameW := w - (col - x) - len(elapsed) - 2
	if maxNameW < 0 {
		maxNameW = 0
	}
	if len(nameStr) > maxNameW {
		nameStr = nameStr[:maxNameW]
	}
	widget.DrawText(screen, col, y, len(nameStr), nameStr, nameStyle)
	col += len(nameStr)

	// Right-align elapsed time. elapsedCol also limits cursor fill below.
	elapsedCol := -1
	if elapsed != "" {
		elapsedCol = x + w - len(elapsed) - 1
	}
	if elapsedCol > col {
		widget.DrawText(screen, elapsedCol, y, len(elapsed), elapsed, tcell.StyleDefault.Foreground(theme.ColorElapsed))
	}

	// Fill remaining cells on cursor row so the highlight extends to edge.
	// Stop before the elapsed time region so it doesn't get overwritten.
	if cursor {
		fillEnd := x + w
		if elapsedCol > col {
			fillEnd = elapsedCol
		}
		for c := col; c < fillEnd; c++ {
			screen.SetContent(c, y, ' ', nil, theme.StyleDefault)
		}
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
	// Pinned tasks need no expansion bookkeeping — that section is always open.
	for _, t := range tl.tasks {
		if t.ID == id {
			switch {
			case t.Pinned:
				// no-op
			case t.Archived:
				tl.archiveExpanded = true
				tl.archiveProject = t.Project
			default:
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
