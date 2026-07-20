package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// taskSwitcherEntry is one row in the task switcher: a task the user can jump
// to from the agent view. NeedsInput flags tasks blocked on a user prompt so
// they can be surfaced (and visually marked) at the top of the list.
type taskSwitcherEntry struct {
	ID         string
	Name       string
	Project    string
	Status     model.Status
	NeedsInput bool
	// Running/Idle/IdleUnvisited mirror the task list's per-task state maps so
	// the switcher's status icon (widget.TaskStatusIcon) resolves to the same
	// in_progress sub-state glyph the task list shows (spinner / moon variants).
	Running       bool
	Idle          bool
	IdleUnvisited bool
	// HeraManaged marks a task that holds a live hera binding (worker or
	// coordinator role). Selecting such an entry jumps into the native Hera
	// view (rail + pane) instead of the classic per-task agent view, since the
	// entry represents a role within Hera's own rail structure.
	HeraManaged bool
}

// statusIcon resolves this entry's status glyph + style via the shared helper,
// so the switcher's indicator column stays identical to the task list's.
func (e taskSwitcherEntry) statusIcon() (rune, tcell.Style) {
	return widget.TaskStatusIcon(e.Status, widget.TaskStatusInputs{
		NeedsInput:    e.NeedsInput,
		IdleUnvisited: e.IdleUnvisited,
		Running:       e.Running,
		Idle:          e.Idle,
	}, widget.CurrentSpinnerFrame())
}

// switcherRowKind distinguishes a project-folder header from a task row in the
// grouped (folder-structure) rendering used by the Ctrl+J switcher.
type switcherRowKind uint8

const (
	switcherRowProjectHeader switcherRowKind = iota
	switcherRowTaskItem
)

// switcherRow is one rendered line in grouped mode: either a project header
// (folder) or a task nested under it.
type switcherRow struct {
	kind    switcherRowKind
	project string
	entry   taskSwitcherEntry // valid when kind == switcherRowTaskItem
	count   int               // tasks in this project in the current (filtered) view; valid when kind == switcherRowProjectHeader
}

// TaskSwitcherModal presents a filterable list of tasks (and, unified,
// hera-managed roles) for jumping directly to another task's agent view or
// role within the Projects tab (Ctrl+J).
//
// It has two rendering modes:
//
//   - Flat (default): a single fuzzy-filtered list, pre-sorted by the caller
//     (needs-input first). Used by the Hera link/unlink parent picker.
//   - Grouped (SetGrouped(true)): a folder structure mirroring the task list —
//     tasks nested under collapsible project headers, one project expanded at a
//     time (all expanded while filtering). Filtering uses the same whitespace-
//     split, all-terms-substring match across project + task name as the task
//     list. Used by the Ctrl+J task switcher.
type TaskSwitcherModal struct {
	*tview.Box
	all      []taskSwitcherEntry // full set, pre-sorted (needs-input first)
	filtered []taskSwitcherEntry // matches current query (flat mode + grouped count/empty checks)
	query    []rune
	qCursor  int
	cursor   int // flat: index into filtered; grouped: index into rows
	selected bool
	canceled bool
	title    string // centered title bar text (default " Switch task ")
	help     string // footer hint (default switch wording)

	// Grouped (folder-structure) mode state.
	grouped  bool
	rows     []switcherRow // grouped rendering rows (headers + tasks)
	expanded string        // currently expanded project (grouped, no filter)
}

// NewTaskSwitcherModal creates a task switcher over the given entries in flat
// (fuzzy) mode.
func NewTaskSwitcherModal(entries []taskSwitcherEntry) *TaskSwitcherModal {
	return &TaskSwitcherModal{
		Box:      tview.NewBox(),
		all:      entries,
		filtered: entries,
		title:    " Switch task ",
		help:     "↑/↓ select  Enter switch  Esc cancel",
	}
}

// SetGrouped switches the modal into the folder-structure rendering used by the
// Ctrl+J task switcher: tasks nested under project headers with task-list-style
// search. It rebuilds the row projection and parks the cursor on the first task.
func (m *TaskSwitcherModal) SetGrouped(v bool) {
	m.grouped = v
	if v {
		m.buildGroupedRows()
		m.cursor = 0
		m.skipToTaskRow(1)
	}
}

// SetTitles overrides the modal title bar and footer hint so the same widget can
// drive the Hera DAG link/unlink parent picker (M7) without reading "Switch
// task". Empty strings keep the current value.
func (m *TaskSwitcherModal) SetTitles(title, help string) {
	if title != "" {
		m.title = title
	}
	if help != "" {
		m.help = help
	}
}

// Selected reports whether the user picked a task.
func (m *TaskSwitcherModal) Selected() bool { return m.selected }

// Canceled reports whether the user dismissed the modal.
func (m *TaskSwitcherModal) Canceled() bool { return m.canceled }

// SelectedTask returns the chosen task's ID (empty if none).
func (m *TaskSwitcherModal) SelectedTask() string {
	e := m.selectedEntry()
	if e == nil {
		return ""
	}
	return e.ID
}

// SelectedHeraManaged reports whether the chosen entry is hera-managed (should
// jump into the Hera view rather than the classic per-task agent view).
func (m *TaskSwitcherModal) SelectedHeraManaged() bool {
	e := m.selectedEntry()
	return e != nil && e.HeraManaged
}

// selectedEntry resolves the entry under the cursor in either render mode, or
// nil when the cursor isn't parked on a task row.
func (m *TaskSwitcherModal) selectedEntry() *taskSwitcherEntry {
	if m.grouped {
		if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].kind == switcherRowTaskItem {
			return &m.rows[m.cursor].entry
		}
		return nil
	}
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		return &m.filtered[m.cursor]
	}
	return nil
}

// PasteHandler handles bracketed paste into the filter field.
func (m *TaskSwitcherModal) PasteHandler() func(string, func(tview.Primitive)) {
	return m.WrapPasteHandler(func(pastedText string, _ func(tview.Primitive)) {
		runes := []rune(pastedText)
		if len(runes) == 0 {
			return
		}
		newQ := make([]rune, 0, len(m.query)+len(runes))
		newQ = append(newQ, m.query[:m.qCursor]...)
		newQ = append(newQ, runes...)
		newQ = append(newQ, m.query[m.qCursor:]...)
		m.query = newQ
		m.qCursor += len(runes)
		m.refilter()
	})
}

// InputHandler handles key events for the task switcher.
func (m *TaskSwitcherModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.canceled = true
		case tcell.KeyEnter:
			if m.hasSelectableTask() {
				m.selected = true
			}
		case tcell.KeyUp:
			m.moveSelection(-1)
		case tcell.KeyDown:
			m.moveSelection(1)
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if event.Modifiers()&tcell.ModAlt != 0 {
				m.query, m.qCursor = widget.DeleteWordLeft(m.query, m.qCursor)
			} else if m.qCursor > 0 {
				m.query = append(m.query[:m.qCursor-1], m.query[m.qCursor:]...)
				m.qCursor--
			}
			m.refilter()
		case tcell.KeyCtrlW:
			m.query, m.qCursor = widget.DeleteWordLeft(m.query, m.qCursor)
			m.refilter()
		case tcell.KeyCtrlU:
			m.query = m.query[m.qCursor:]
			m.qCursor = 0
			m.refilter()
		case tcell.KeyLeft:
			if event.Modifiers()&tcell.ModAlt != 0 {
				m.qCursor = widget.WordLeftPos(m.query, m.qCursor)
			} else if m.qCursor > 0 {
				m.qCursor--
			}
		case tcell.KeyRight:
			if event.Modifiers()&tcell.ModAlt != 0 {
				m.qCursor = widget.WordRightPos(m.query, m.qCursor)
			} else if m.qCursor < len(m.query) {
				m.qCursor++
			}
		case tcell.KeyDelete:
			if event.Modifiers()&tcell.ModAlt != 0 {
				m.query, m.qCursor = widget.DeleteWordRight(m.query, m.qCursor)
			} else if m.qCursor < len(m.query) {
				m.query = append(m.query[:m.qCursor], m.query[m.qCursor+1:]...)
			}
			m.refilter()
		case tcell.KeyHome, tcell.KeyCtrlA:
			m.qCursor = 0
		case tcell.KeyEnd, tcell.KeyCtrlE:
			m.qCursor = len(m.query)
		case tcell.KeyRune:
			r := event.Rune()
			if event.Modifiers()&tcell.ModAlt != 0 {
				switch r {
				case 'b', 'B':
					m.qCursor = widget.WordLeftPos(m.query, m.qCursor)
				case 'f', 'F':
					m.qCursor = widget.WordRightPos(m.query, m.qCursor)
				case 'd', 'D':
					m.query, m.qCursor = widget.DeleteWordRight(m.query, m.qCursor)
					m.refilter()
				}
				return
			}
			m.query = append(m.query[:m.qCursor], append([]rune{r}, m.query[m.qCursor:]...)...)
			m.qCursor++
			m.refilter()
		}
	})
}

// hasSelectableTask reports whether Enter would pick a task right now.
func (m *TaskSwitcherModal) hasSelectableTask() bool {
	if m.grouped {
		return m.SelectedTask() != ""
	}
	return len(m.filtered) > 0
}

// moveSelection moves the cursor by dir (+1 down, -1 up), dispatching to the
// header-skipping grouped walker or the simple flat clamp.
func (m *TaskSwitcherModal) moveSelection(dir int) {
	if m.grouped {
		m.moveGroupedCursor(dir)
		return
	}
	if dir < 0 {
		if m.cursor > 0 {
			m.cursor--
		}
		return
	}
	if m.cursor < len(m.filtered)-1 {
		m.cursor++
	}
}

// refilter updates the match set from the current query. In flat mode matching
// is fuzzy across name and project and preserves the caller's order. In grouped
// mode it uses the task list's whitespace-split, all-terms-substring match and
// rebuilds the folder rows, parking the cursor on the first match.
func (m *TaskSwitcherModal) refilter() {
	if m.grouped {
		m.buildGroupedRows()
		m.cursor = 0
		m.skipToTaskRow(1)
		return
	}
	q := string(m.query)
	if q == "" {
		m.filtered = m.all
	} else {
		var matches []taskSwitcherEntry
		for _, e := range m.all {
			if fuzzyMatch(q, e.Name) || fuzzyMatch(q, e.Project) {
				matches = append(matches, e)
			}
		}
		m.filtered = matches
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(len(m.filtered)-1, 0)
	}
}

// switcherEntryMatches mirrors TaskListView.matchesFilter: terms are split on
// whitespace and EVERY term must appear (case-insensitive substring) in the
// task name OR the project name.
func switcherEntryMatches(e taskSwitcherEntry, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	name := strings.ToLower(e.Name)
	proj := strings.ToLower(e.Project)
	for _, term := range terms {
		if !strings.Contains(name, term) && !strings.Contains(proj, term) {
			return false
		}
	}
	return true
}

// groupSwitcherEntries groups entries by project name (alphabetical), mirroring
// TaskListView.groupByProject. Empty project names collapse to "(no project)".
// Within a project the incoming order is preserved (needs-input first).
func groupSwitcherEntries(entries []taskSwitcherEntry) ([]string, map[string][]taskSwitcherEntry) {
	groups := map[string][]taskSwitcherEntry{}
	for _, e := range entries {
		proj := e.Project
		if proj == "" {
			proj = "(no project)"
		}
		groups[proj] = append(groups[proj], e)
	}
	order := make([]string, 0, len(groups))
	for proj := range groups {
		order = append(order, proj)
	}
	sort.Strings(order)
	return order, groups
}

// buildGroupedRows rebuilds the folder-structure rows from the current query.
// One project is expanded at a time (m.expanded); while a query is active every
// project is expanded so all matches are visible — exactly like the task list.
// This only rebuilds m.rows; cursor positioning is the caller's responsibility.
func (m *TaskSwitcherModal) buildGroupedRows() {
	terms := strings.Fields(strings.ToLower(string(m.query)))
	var matched []taskSwitcherEntry
	for _, e := range m.all {
		if switcherEntryMatches(e, terms) {
			matched = append(matched, e)
		}
	}
	m.filtered = matched

	order, groups := groupSwitcherEntries(matched)
	if m.expanded == "" && len(order) > 0 {
		m.expanded = order[0]
	}
	filterActive := len(terms) > 0

	rows := make([]switcherRow, 0, len(matched)+len(order))
	for _, proj := range order {
		rows = append(rows, switcherRow{kind: switcherRowProjectHeader, project: proj, count: len(groups[proj])})
		if filterActive || proj == m.expanded {
			for _, e := range groups[proj] {
				rows = append(rows, switcherRow{kind: switcherRowTaskItem, project: proj, entry: e})
			}
		}
	}
	m.rows = rows
}

// skipToTaskRow advances the cursor in direction dir until it lands on a task
// row, then falls back the other way if it ran off the end. Mirrors
// TaskListView.skipToTask. With no rows at all the cursor parks at 0 (the
// inert empty-state index) rather than the -1 the two-loop fallthrough would
// otherwise leave behind — every reader guards `cursor >= 0 && < len(rows)`,
// so 0 and -1 are both safe, but 0 keeps the sentinel uniform with flat mode.
func (m *TaskSwitcherModal) skipToTaskRow(dir int) {
	if len(m.rows) == 0 {
		m.cursor = 0
		return
	}
	for m.cursor >= 0 && m.cursor < len(m.rows) {
		if m.rows[m.cursor].kind == switcherRowTaskItem {
			return
		}
		m.cursor += dir
	}
	if dir > 0 {
		m.cursor = len(m.rows) - 1
	} else {
		m.cursor = 0
	}
	for m.cursor >= 0 && m.cursor < len(m.rows) {
		if m.rows[m.cursor].kind == switcherRowTaskItem {
			return
		}
		m.cursor -= dir
	}
}

// moveGroupedCursor moves one row in direction dir, auto-expanding the project
// the cursor enters and skipping project-header rows so the cursor always lands
// on a task. Mirrors the active-section logic of TaskListView.moveCursor.
func (m *TaskSwitcherModal) moveGroupedCursor(dir int) {
	if len(m.rows) == 0 {
		return
	}
	prev := m.cursor
	m.cursor += dir
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	m.autoExpandGrouped()

	c := m.cursor
	if c < 0 || c >= len(m.rows) {
		return
	}
	if m.rows[c].kind == switcherRowTaskItem {
		return
	}
	// On a project header — skip it.
	if dir > 0 {
		if c+1 < len(m.rows) && m.rows[c+1].kind == switcherRowTaskItem {
			m.cursor++
		} else {
			// Header with no task directly below (degenerate: groupSwitcherEntries
			// never emits an empty folder, so every header has ≥1 task while its
			// project is expanded — and moving down always lands on a header whose
			// project we just auto-expanded). Stay defensive anyway: never strand
			// the cursor on a header where Enter would no-op — fall back to the
			// last task we were on.
			m.cursor = m.clampToTaskRow(prev)
		}
	} else {
		m.skipUpGrouped(prev)
	}
}

// autoExpandGrouped expands the project the cursor is currently on (collapsing
// the previous one) when no query is active, then restores the cursor onto the
// same logical row after the rebuild shifts indices. With a query active every
// project is already expanded, so this is a no-op.
func (m *TaskSwitcherModal) autoExpandGrouped() {
	if len(m.query) > 0 {
		return
	}
	c := m.cursor
	if c < 0 || c >= len(m.rows) {
		return
	}
	target := m.rows[c]
	if target.project != "" && target.project != m.expanded {
		m.expanded = target.project
		m.buildGroupedRows()
		m.restoreGroupedCursor(target)
	}
}

// restoreGroupedCursor repositions the cursor onto the row matching target
// (by task ID, or by project for a header) after a rebuild. Falls back to the
// nearest task row if the target vanished.
func (m *TaskSwitcherModal) restoreGroupedCursor(target switcherRow) {
	for i, r := range m.rows {
		if r.kind != target.kind {
			continue
		}
		switch r.kind {
		case switcherRowTaskItem:
			if r.entry.ID == target.entry.ID {
				m.cursor = i
				return
			}
		case switcherRowProjectHeader:
			if r.project == target.project {
				m.cursor = i
				return
			}
		}
	}
	m.skipToTaskRow(1)
}

// clampToTaskRow returns idx clamped into the current rows and snapped to the
// nearest task row. Callers carry a `prev` index captured before an
// autoExpandGrouped rebuild may have shrunk m.rows, so a raw `prev` can be
// stale (out of range or pointing at a header). Routing every prev-fallback
// through here guarantees the cursor lands on a real task, never off the list
// or on a header. It uses m.cursor as scratch (skipToTaskRow mutates it) and
// restores it before returning — safe because all navigation runs on the
// single tview goroutine, so there is no concurrent reader to observe the
// transient value.
func (m *TaskSwitcherModal) clampToTaskRow(idx int) int {
	if len(m.rows) == 0 {
		return 0
	}
	saved := m.cursor
	m.cursor = max(min(idx, len(m.rows)-1), 0)
	m.skipToTaskRow(1)
	res := m.cursor
	m.cursor = saved
	return res
}

// skipUpGrouped moves the cursor up past header rows, landing on the last task
// of the previous expanded project. Mirrors TaskListView.skipUpPastHeader.
func (m *TaskSwitcherModal) skipUpGrouped(prev int) {
	for {
		m.cursor--
		if m.cursor < 0 {
			m.cursor = m.clampToTaskRow(prev)
			return
		}
		m.autoExpandGrouped()
		c := m.cursor
		if c < 0 || c >= len(m.rows) {
			m.cursor = m.clampToTaskRow(prev)
			return
		}
		switch m.rows[c].kind {
		case switcherRowTaskItem:
			return
		case switcherRowProjectHeader:
			m.landOnLastTaskGrouped(c, prev)
			return
		}
	}
}

// landOnLastTaskGrouped sets the cursor to the last consecutive task row after
// the project header at idx, falling back to the nearest task to prev if none
// follow (prev may be stale after a rebuild, so it is clamped+snapped).
func (m *TaskSwitcherModal) landOnLastTaskGrouped(idx, prev int) {
	last := -1
	for i := idx + 1; i < len(m.rows) && m.rows[i].kind == switcherRowTaskItem; i++ {
		last = i
	}
	if last >= 0 {
		m.cursor = last
	} else {
		m.cursor = m.clampToTaskRow(prev)
	}
}

// taskSwitcherRowText renders the non-icon portion of a flat-mode row: the task
// name followed by its project. The status is now conveyed by the leading icon
// (matching the task list), so it is no longer spelled out here.
func taskSwitcherRowText(e taskSwitcherEntry) string {
	if e.Project != "" {
		return e.Name + "  ·  " + e.Project
	}
	return e.Name
}

// switcherTaskRowText renders a grouped task row: just the task name. The status
// is shown by the leading icon and the project lives in the folder header.
func switcherTaskRowText(e taskSwitcherEntry) string {
	return e.Name
}

// switcherHeaderText renders a grouped project-folder header with its chevron
// and task count.
func switcherHeaderText(r switcherRow, expanded bool) string {
	chevron := '▸'
	if expanded {
		chevron = '▾'
	}
	return fmt.Sprintf("%c %s (%d)", chevron, r.project, r.count)
}

// Draw renders the task switcher as a centered modal.
func (m *TaskSwitcherModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Compute modal width from the widest row (+2 for the marker column).
	maxDisplayW := 30
	if m.grouped {
		for _, r := range m.rows {
			var w int
			if r.kind == switcherRowProjectHeader {
				w = utf8.RuneCountInString(switcherHeaderText(r, true))
			} else {
				w = utf8.RuneCountInString(switcherTaskRowText(r.entry)) + 4
			}
			if w > maxDisplayW {
				maxDisplayW = w
			}
		}
	} else {
		for _, e := range m.all {
			if w := utf8.RuneCountInString(taskSwitcherRowText(e)) + 2; w > maxDisplayW {
				maxDisplayW = w
			}
		}
	}
	modalW := max(maxDisplayW+6, 44)
	modalW = min(modalW, width-4)
	innerW := modalW - 4

	// Height: border + title + filter + gap + items + gap + help + border.
	rowCount := len(m.all)
	if m.grouped {
		rowCount = len(m.rows)
	}
	maxItems := max(min(rowCount, height-8), 1)
	modalH := maxItems + 7
	if modalH > height {
		modalH = height
		maxItems = max(modalH-7, 1)
	}

	mx := x + (width-modalW)/2
	my := y + (height-modalH)/2

	clearStyle := tcell.StyleDefault.Background(tcell.ColorDefault)
	for row := my; row < my+modalH; row++ {
		for col := mx; col < mx+modalW; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}

	widget.DrawBorder(screen, mx, my, modalW, modalH, theme.StyleFocusedBorder)

	title := m.title
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	titleStyle := tcell.StyleDefault.Foreground(theme.ColorTitle).Bold(true)
	// Iterate the []rune (not the byte-indexed range over the string) so a
	// multi-byte title rune — e.g. an arrow in the link-picker title — doesn't
	// leave gap cells (the rune-vs-byte placement bug; see gotchas/dag-rendering.md).
	for i, r := range []rune(title) {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2

	// Filter input row.
	filterY := my + 2
	widget.DrawText(screen, innerX, filterY, 2, "› ", theme.StyleFilter)
	before := string(m.query[:m.qCursor])
	after := string(m.query[m.qCursor:])
	fieldW := innerW - 2
	// On a very narrow terminal innerW can be ≤ 2, making fieldW ≤ 0. A
	// negative fieldW underflows the scroll-truncation slice below
	// (runes[len-fieldW:]) into an out-of-range panic, so clamp to ≥ 1.
	fieldW = max(fieldW, 1)
	val := before + "█" + after
	if runes := []rune(val); len(runes) > fieldW {
		val = string(runes[len(runes)-fieldW:])
	}
	widget.DrawText(screen, innerX+2, filterY, fieldW, val, theme.StyleNormal)

	itemsY := my + 4
	if m.grouped {
		m.drawGroupedItems(screen, innerX, itemsY, innerW, maxItems)
	} else {
		m.drawFlatItems(screen, innerX, itemsY, innerW, maxItems)
	}

	helpRow := my + modalH - 2
	widget.DrawText(screen, innerX, helpRow, innerW, m.help, theme.StyleDimmed)
}

// drawFlatItems renders the flat (fuzzy) item list used by the Hera picker.
func (m *TaskSwitcherModal) drawFlatItems(screen tcell.Screen, innerX, itemsY, innerW, maxItems int) {
	if len(m.all) == 0 {
		widget.DrawText(screen, innerX, itemsY, innerW, "No other tasks to switch to.", theme.StyleDimmed)
		return
	}
	if len(m.filtered) == 0 {
		widget.DrawText(screen, innerX, itemsY, innerW, "No matches", theme.StyleDimmed)
		return
	}
	offset := 0
	if m.cursor >= maxItems {
		offset = m.cursor - maxItems + 1
	}
	maxVisible := min(maxItems, len(m.filtered))
	for i := range maxVisible {
		idx := offset + i
		if idx >= len(m.filtered) {
			break
		}
		e := m.filtered[idx]
		rowY := itemsY + i
		selected := idx == m.cursor
		style := theme.StyleNormal
		if selected {
			style = theme.StyleSelected
		}
		// Status icon to the left of the name — identical to the task list. The
		// icon keeps its status colour even on the selected row (only the name
		// adopts the selected style), matching drawTaskRow.
		icon, iconStyle := e.statusIcon()
		screen.SetContent(innerX, rowY, icon, nil, iconStyle)
		display := taskSwitcherRowText(e)
		textW := innerW - 2
		if utf8.RuneCountInString(display) > textW && textW > 3 {
			display = string([]rune(display)[:textW-1]) + "…"
		}
		widget.DrawText(screen, innerX+2, rowY, textW, display, style)
	}
}

// drawGroupedItems renders the folder structure: project headers with nested,
// indented task rows. Cursor scrolling keeps the selected task visible.
func (m *TaskSwitcherModal) drawGroupedItems(screen tcell.Screen, innerX, itemsY, innerW, maxItems int) {
	if len(m.all) == 0 {
		widget.DrawText(screen, innerX, itemsY, innerW, "No other tasks to switch to.", theme.StyleDimmed)
		return
	}
	if len(m.rows) == 0 {
		widget.DrawText(screen, innerX, itemsY, innerW, "No matches", theme.StyleDimmed)
		return
	}
	offset := 0
	if m.cursor >= maxItems {
		offset = m.cursor - maxItems + 1
	}
	headerStyle := tcell.StyleDefault.Foreground(theme.ColorProject).Bold(true)
	maxVisible := min(maxItems, len(m.rows))
	for i := range maxVisible {
		idx := offset + i
		if idx >= len(m.rows) {
			break
		}
		r := m.rows[idx]
		rowY := itemsY + i
		if r.kind == switcherRowProjectHeader {
			text := switcherHeaderText(r, len(m.query) > 0 || r.project == m.expanded)
			if utf8.RuneCountInString(text) > innerW && innerW > 3 {
				text = string([]rune(text)[:innerW-1]) + "…"
			}
			widget.DrawText(screen, innerX, rowY, innerW, text, headerStyle)
			continue
		}
		// Task row, indented under its folder.
		e := r.entry
		selected := idx == m.cursor
		style := theme.StyleNormal
		if selected {
			style = theme.StyleSelected
		}
		// Status icon to the left of the name (indented under its folder),
		// identical to the task list. The icon keeps its status colour even on
		// the selected row, matching drawTaskRow.
		icon, iconStyle := e.statusIcon()
		screen.SetContent(innerX+2, rowY, icon, nil, iconStyle)
		display := switcherTaskRowText(e)
		textW := max(innerW-4, 1)
		if utf8.RuneCountInString(display) > textW && textW > 3 {
			display = string([]rune(display)[:textW-1]) + "…"
		}
		widget.DrawText(screen, innerX+4, rowY, textW, display, style)
	}
}
