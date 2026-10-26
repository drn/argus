package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/drn/argus/internal/tui/taskview"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// SwitcherColumn identifies one of the jump switcher's two columns.
type SwitcherColumn int

const (
	// SwitcherColumnTasks is the left column: the plain Tasks list.
	SwitcherColumnTasks SwitcherColumn = iota
	// SwitcherColumnProjects is the right column: the Projects (Hera) rail.
	SwitcherColumnProjects
)

func (c SwitcherColumn) String() string {
	if c == SwitcherColumnProjects {
		return "projects"
	}
	return "tasks"
}

// switcherFooterHint renders the popup's bottom key-hint line, naming the
// key currently bound to the next-needs-input cycle (default ctrl+g).
func switcherFooterHint(nextKey string) string {
	return "↑↓ move · ←→ column · " + nextKey + " next (?) · enter go · esc clear/close"
}

// switcherRemotePlaceholder replaces the Projects column in remote mode.
const switcherRemotePlaceholder = "Projects unavailable in remote mode"

// SwitcherFeed is the data snapshot the App hands the jump switcher at open
// time (NewSwitcherPopup) and on every tick while it is open (Refeed). It
// mirrors what the real Tasks tab list and Projects rail are fed.
type SwitcherFeed struct {
	// CoordinatorBoard enables unread-board attention in the private rail.
	CoordinatorBoard bool
	// Tasks is the full task snapshot (a.tasks). Archived tasks and every
	// hera-managed task (Managed ∪ HeraWorkers — the real list's own
	// isHeraSpawnedWorker || managed hide rule with `H` on) are dropped from
	// the Tasks column regardless of the real list's `H` toggle.
	Tasks []*model.Task
	// Managed is the set of task IDs holding a live coordinator/worker binding
	// (the real list's SetManagedTasks input).
	Managed map[string]bool
	// HeraWorkers / HeraCoords are the task_meta hera.role=worker/coordinator
	// sets (the real list's SetHeraWorkers / SetHeraCoordinators inputs).
	// HeraCoords is display-only and NEVER a hide signal: coordinator meta is
	// not cleared when the binding ends, so hiding on it would make an
	// ex-coordinator unreachable in both columns.
	HeraWorkers map[string]bool
	HeraCoords  map[string]bool

	// Per-task session state, as fed to the real Tasks list.
	Running       []string
	Idle          []string
	IdleUnvisited []string
	NeedsInput    []string
	PRStates      map[string]model.PRState

	// HeraModel is the Projects rail's model — typically
	// heraPage.Rail().Model() right after heraPage.Refresh(). The popup
	// deep-copies it, so the real rail's model is never mutated.
	HeraModel heramodel.Model
	// PRMeta is the real rail's "pr" namespace cache (Rail.SetPRMeta input).
	PRMeta map[string]map[string]string
	// Remote is heraPage.IsRemote(): the Projects column shows a placeholder
	// and is skipped by ←/→, ctrl+g and the empty-column handoff.
	Remote bool
}

// SwitcherSeed carries the open-time-only inputs: where the cursor should
// fall back to, and the real widgets whose folds the private copies start
// from. The real widgets are only READ.
type SwitcherSeed struct {
	// CurrentTaskID is the currently-viewed task (agent view task, or the
	// Projects rail's selected task) — the start cursor when nothing needs
	// input.
	CurrentTaskID string
	// TasksExpanded is the real Tasks list's expanded project (Expanded()).
	TasksExpanded string
	// Rail is the real Projects rail to copy folds from (nil → defaults).
	Rail *hera.Rail
}

// SwitcherSelection is what Enter chose: the column, the task to open (a Tasks
// row's ID, or a Projects row's Selection().FocusTaskID()), and — for a
// Projects row — the row's identity: RoleID for a role row (0 for an
// orchestrator header) and its orchestrator. The App lands the real rail on
// that exact row (HeraPage.JumpToRow), since TaskID alone is ambiguous for a
// task bound to roles in several orchestrators.
type SwitcherSelection struct {
	Column SwitcherColumn
	TaskID string
	RoleID int64
	OrchID int64
}

// switcherRect is a drawn region, kept for mouse hit-testing.
type switcherRect struct{ x, y, w, h int }

func (r switcherRect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// SwitcherPopup is the ctrl+j jump switcher (add-two-column-switcher): a
// centered overlay with a shared filter line, the Tasks list on the left and
// the Projects rail on the right, each rendered by a PRIVATE instance of the
// real widget (design D1) so rows, folds and (?) markers look identical while
// navigation, folding and filtering never touch the real tabs. The App reads
// Canceled()/Selected() after each key and routes Selection().
type SwitcherPopup struct {
	*tview.Box

	tasks *taskview.TaskListView
	rail  *hera.Rail

	active SwitcherColumn
	remote bool
	query  []rune
	// taskIDs is the set of task IDs fed to the Tasks column (after the
	// archived / hera-managed exclusion).
	taskIDs map[string]bool
	// preFilterActive is the active column when the query went from empty to
	// non-empty; clearing the query with Esc restores it (D7).
	preFilterActive SwitcherColumn

	canceled  bool
	selected  bool
	selection SwitcherSelection

	colRects [2]switcherRect

	// nextKey is the key that cycles to the next needs-input row: the active
	// global.jump_needs_input binding (SetNextNeedsInputKey), default ctrl+g.
	nextKey keymap.Binding
}

// NewSwitcherPopup builds the popup from feed, seeds both columns' folds from
// seed, and places the start cursor (D6): the first needs-input row in ctrl+g
// order, else seed.CurrentTaskID's row in whichever column holds it, else the
// top of the Tasks column.
func NewSwitcherPopup(feed SwitcherFeed, seed SwitcherSeed) *SwitcherPopup {
	p := &SwitcherPopup{
		Box:   tview.NewBox(),
		tasks: taskview.NewTaskListView(),
		rail:  hera.NewRail(),
	}
	p.nextKey, _ = keymap.DefaultKeymap().BindingFor(keymap.CtxGlobal, keymap.ActGlobalJumpNeedsInput)
	p.rail.SetPanelTitle("Projects")
	p.tasks.SetHideHeraManaged(true)
	p.tasks.SetExpanded(seed.TasksExpanded)
	p.rail.CopyFoldStateFrom(seed.Rail)
	p.feed(feed)
	if p.tasks.SelectedTask() == nil && len(p.tasks.VisibleTaskIDs()) == 0 {
		// A stale expanded seed (project gone or fully hidden) leaves no task
		// row; fall back to the auto-expanded first project.
		p.tasks.SetExpanded("")
	}
	p.placeStartCursor(seed.CurrentTaskID)
	uxlog.Log("[switcher] open tasks=%d task_needs_input=%d projects_rows=%d remote=%v active=%s",
		len(p.tasks.VisibleTaskIDs()), len(p.tasks.NeedsInputTaskIDs()), p.rail.Rows(), p.remote, p.active)
	return p
}

// Refeed re-snapshots both columns (called on each App tick while open). The
// widgets restore their cursors by identity and keep the applied query; if the
// active column is left with no selectable row, the other column takes over.
func (p *SwitcherPopup) Refeed(feed SwitcherFeed) {
	p.feed(feed)
	p.handoffIfEmpty()
}

func (p *SwitcherPopup) feed(f SwitcherFeed) {
	p.remote = f.Remote
	hidden := make(map[string]bool, len(f.Managed)+len(f.HeraWorkers))
	for _, set := range []map[string]bool{f.Managed, f.HeraWorkers} {
		for id, v := range set {
			if v {
				hidden[id] = true
			}
		}
	}
	tasks := make([]*model.Task, 0, len(f.Tasks))
	p.taskIDs = make(map[string]bool, len(f.Tasks))
	for _, t := range f.Tasks {
		if t == nil || t.Archived || hidden[t.ID] {
			continue
		}
		tasks = append(tasks, t)
		p.taskIDs[t.ID] = true
	}
	p.tasks.SetManagedTasks(hidden)
	p.tasks.SetHeraWorkers(f.HeraWorkers)
	p.tasks.SetHeraCoordinators(f.HeraCoords)
	p.tasks.SetRunning(f.Running)
	p.tasks.SetIdle(f.Idle)
	p.tasks.SetIdleUnvisited(f.IdleUnvisited)
	p.tasks.SetNeedsInput(f.NeedsInput)
	p.tasks.SetPRStates(f.PRStates)
	p.tasks.SetTasks(tasks)
	if !p.remote {
		p.rail.SetPRMeta(f.PRMeta)
		p.rail.SetBoardAuto(f.CoordinatorBoard)
		p.rail.SetModel(f.HeraModel.Clone())
	}
}

// SetNextNeedsInputKey sets the key that cycles the cursor to the next
// needs-input row (and the footer hint naming it) — the App passes the active
// global.jump_needs_input binding so a rebind applies inside the popup too.
func (p *SwitcherPopup) SetNextNeedsInputKey(b keymap.Binding) { p.nextKey = b }

// Canceled reports whether Esc (with no query) or ctrl+q closed the popup.
func (p *SwitcherPopup) Canceled() bool { return p.canceled }

// Selected reports whether Enter chose a target (see Selection).
func (p *SwitcherPopup) Selected() bool { return p.selected }

// Selection returns the target Enter chose, snapshotted at the keypress.
func (p *SwitcherPopup) Selection() SwitcherSelection { return p.selection }

// ActiveColumn returns the column that currently owns the cursor.
func (p *SwitcherPopup) ActiveColumn() SwitcherColumn { return p.active }

// Query returns the shared filter query.
func (p *SwitcherPopup) Query() string { return string(p.query) }

func (p *SwitcherPopup) projectsAvailable() bool { return !p.remote }

// hasSelectable reports whether column c can hold the cursor right now.
func (p *SwitcherPopup) hasSelectable(c SwitcherColumn) bool {
	if c == SwitcherColumnProjects {
		return p.projectsAvailable() && p.rail.HasSelectableRow()
	}
	return p.tasks.SelectedTask() != nil
}

func (p *SwitcherPopup) other(c SwitcherColumn) SwitcherColumn {
	if c == SwitcherColumnTasks {
		return SwitcherColumnProjects
	}
	return SwitcherColumnTasks
}

// setActive switches the active column (Projects only when available).
func (p *SwitcherPopup) setActive(c SwitcherColumn, why string) {
	if c == SwitcherColumnProjects && !p.projectsAvailable() {
		return
	}
	if c == p.active {
		return
	}
	p.active = c
	uxlog.Log("[switcher] column -> %s (%s)", c, why)
}

// handoffIfEmpty moves the cursor to the other column when the active one has
// no selectable row and the other does (D5).
func (p *SwitcherPopup) handoffIfEmpty() {
	if !p.hasSelectable(p.active) && p.hasSelectable(p.other(p.active)) {
		p.setActive(p.other(p.active), "empty handoff")
	}
}

// placeStartCursor implements D6's start-position rule.
func (p *SwitcherPopup) placeStartCursor(current string) {
	if p.firstNeedsInput() {
		return
	}
	if current != "" {
		if p.taskIDs[current] {
			p.tasks.SelectByID(current)
			p.active = SwitcherColumnTasks
			return
		}
		if p.projectsAvailable() && p.rail.SelectByTaskID(current) {
			p.active = SwitcherColumnProjects
			return
		}
	}
	p.active = SwitcherColumnTasks
	p.handoffIfEmpty()
}

// firstNeedsInput lands on the first needs-input row in ctrl+g order (Tasks
// top-to-bottom, then Projects top-to-bottom). Returns false when none.
func (p *SwitcherPopup) firstNeedsInput() bool {
	if ids := p.tasks.NeedsInputTaskIDs(); len(ids) > 0 {
		p.tasks.SelectByID(ids[0])
		p.active = SwitcherColumnTasks
		return true
	}
	if p.projectsAvailable() && p.rail.CursorToNextNeedsInput(true) {
		p.active = SwitcherColumnProjects
		return true
	}
	return false
}

// nextNeedsInput is ctrl+g (D6): the next needs-input row after the cursor in
// the Tasks-then-Projects ring, wrapping, switching column as needed. Returns
// false (cursor unchanged) when no row needs input.
func (p *SwitcherPopup) nextNeedsInput() bool {
	selectTask := func(id string) bool {
		p.tasks.SelectByID(id)
		p.setActive(SwitcherColumnTasks, "ctrl+g")
		return true
	}
	firstTask := func() bool {
		if ids := p.tasks.NeedsInputTaskIDs(); len(ids) > 0 {
			return selectTask(ids[0])
		}
		return false
	}
	railFrom := func(fromStart bool) bool {
		if p.projectsAvailable() && p.rail.CursorToNextNeedsInput(fromStart) {
			p.setActive(SwitcherColumnProjects, "ctrl+g")
			return true
		}
		return false
	}
	if p.active == SwitcherColumnTasks {
		cur := ""
		if t := p.tasks.SelectedTask(); t != nil {
			cur = t.ID
		}
		if ids := p.tasks.NeedsInputTaskIDsAfter(cur); len(ids) > 0 {
			return selectTask(ids[0])
		}
		return railFrom(true) || firstTask()
	}
	return railFrom(false) || firstTask() || railFrom(true)
}

// applyQuery pushes the shared query into both columns and hands off when the
// active column empties (D5).
func (p *SwitcherPopup) applyQuery() {
	q := string(p.query)
	p.tasks.SetFilter(q)
	if p.projectsAvailable() {
		p.rail.SetFilter(q)
	}
	p.handoffIfEmpty()
}

// setQuery replaces the query, remembering the active column when filtering
// starts and restoring it when the query is emptied.
func (p *SwitcherPopup) setQuery(q []rune) {
	was := len(p.query) > 0
	if !was && len(q) > 0 {
		p.preFilterActive = p.active
	}
	p.query = q
	p.applyQuery()
	if was && len(q) == 0 {
		p.setActive(p.preFilterActive, "filter cleared")
		p.handoffIfEmpty()
	}
}

// InputHandler routes every key while the popup is open (D4–D7).
func (p *SwitcherPopup) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return p.WrapInputHandler(func(event *tcell.EventKey, _ func(tview.Primitive)) {
		if p.nextKey.Matches(event) {
			if p.nextNeedsInput() {
				uxlog.Log("[switcher] next (?) hit column=%s", p.active)
			} else {
				uxlog.Log("[switcher] next (?) miss (nothing needs input)")
			}
			return
		}
		switch event.Key() {
		case tcell.KeyEscape:
			if len(p.query) > 0 {
				p.setQuery(nil)
				uxlog.Log("[switcher] esc: query cleared")
				return
			}
			p.canceled = true
			uxlog.Log("[switcher] cancel (esc)")
		case tcell.KeyCtrlQ:
			p.canceled = true
			uxlog.Log("[switcher] cancel (ctrl+q)")
		case tcell.KeyEnter:
			p.enter()
		case tcell.KeyUp:
			p.cursorStep(-1)
		case tcell.KeyDown:
			p.cursorStep(1)
		case tcell.KeyLeft:
			p.setActive(SwitcherColumnTasks, "left")
		case tcell.KeyRight:
			p.setActive(SwitcherColumnProjects, "right")
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if len(p.query) == 0 {
				return
			}
			if event.Modifiers()&tcell.ModAlt != 0 {
				q, _ := widget.DeleteWordLeft(p.query, len(p.query))
				p.setQuery(q)
				return
			}
			p.setQuery(p.query[:len(p.query)-1])
		case tcell.KeyCtrlW:
			q, _ := widget.DeleteWordLeft(p.query, len(p.query))
			p.setQuery(q)
		case tcell.KeyCtrlU:
			p.setQuery(nil)
		case tcell.KeyRune:
			if event.Modifiers()&(tcell.ModAlt|tcell.ModCtrl) != 0 {
				return
			}
			r := event.Rune()
			if r == ' ' {
				// Space is structural (D4): fold in Projects, never filter text.
				if p.active == SwitcherColumnProjects {
					p.rail.ToggleCollapse()
				}
				return
			}
			p.setQuery(append(append([]rune(nil), p.query...), r))
		}
	})
}

// cursorStep moves the active column's cursor (dir -1 up, +1 down).
func (p *SwitcherPopup) cursorStep(dir int) {
	p.stepColumn(p.active, dir)
}

func (p *SwitcherPopup) stepColumn(c SwitcherColumn, dir int) {
	switch {
	case c == SwitcherColumnProjects && p.projectsAvailable():
		if dir < 0 {
			p.rail.CursorUp()
		} else {
			p.rail.CursorDown()
		}
	case c == SwitcherColumnTasks:
		if dir < 0 {
			p.tasks.CursorUp()
		} else {
			p.tasks.CursorDown()
		}
	}
}

// enter resolves the active column's row into a Selection (D7). A Projects
// fold-only row (Freelance / Archive expando) has no target and toggles its
// fold instead; an empty column is a no-op.
func (p *SwitcherPopup) enter() {
	if p.active == SwitcherColumnTasks {
		t := p.tasks.SelectedTask()
		if t == nil {
			uxlog.Log("[switcher] enter: no task row (ignored)")
			return
		}
		p.choose(SwitcherSelection{Column: SwitcherColumnTasks, TaskID: t.ID})
		return
	}
	if !p.hasSelectable(SwitcherColumnProjects) {
		uxlog.Log("[switcher] enter: no projects row (ignored)")
		return
	}
	sel := p.rail.Selection()
	out := SwitcherSelection{Column: SwitcherColumnProjects, TaskID: sel.FocusTaskID()}
	if sel.Role != nil {
		out.RoleID = sel.Role.RoleID
	}
	if sel.Orch != nil {
		out.OrchID = sel.Orch.ID
	}
	if out.TaskID == "" && out.OrchID == 0 {
		p.rail.ToggleCollapse()
		uxlog.Log("[switcher] enter on fold row: toggled")
		return
	}
	p.choose(out)
}

func (p *SwitcherPopup) choose(sel SwitcherSelection) {
	p.selection = sel
	p.selected = true
	uxlog.Log("[switcher] select column=%s task=%s role=%d orch=%d", sel.Column, sel.TaskID, sel.RoleID, sel.OrchID)
}

// PasteHandler appends pasted text (line breaks and other control characters
// stripped) to the shared query.
func (p *SwitcherPopup) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return p.WrapPasteHandler(func(pastedText string, _ func(tview.Primitive)) {
		add := []rune(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, pastedText))
		if len(add) == 0 {
			return
		}
		p.setQuery(append(append([]rune(nil), p.query...), add...))
	})
}

// MouseHandler consumes every mouse event so tview never moves focus off the
// popup (or onto a child widget — the page-wrapper focus-guard rule). The
// wheel moves the cursor of the column under the pointer, with the same
// "natural" direction as the real widgets; clicks are ignored.
func (p *SwitcherPopup) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return p.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, _ func(tview.Primitive)) (bool, tview.Primitive) {
		x, y := event.Position()
		dir := 0
		switch action {
		case tview.MouseScrollUp:
			dir = 1
		case tview.MouseScrollDown:
			dir = -1
		}
		if dir != 0 {
			for _, c := range []SwitcherColumn{SwitcherColumnTasks, SwitcherColumnProjects} {
				if p.colRects[c].contains(x, y) {
					p.stepColumn(c, dir)
				}
			}
		}
		return true, nil
	})
}

// overlayRect returns the centered ~90% overlay inside the popup's rect.
func (p *SwitcherPopup) overlayRect() (x, y, w, h int) {
	bx, by, bw, bh := p.GetInnerRect()
	w = min(bw, max(bw*9/10, 40))
	h = min(bh, max(bh*9/10, 10))
	return bx + (bw-w)/2, by + (bh-h)/2, w, h
}

// Draw paints the overlay: a bordered frame, the filter line, the two headed
// columns (each private widget draws its own bordered panel; the active one
// focused, the other with a dimmed cursor), and the footer hint. Every cell of
// the overlay rect is overwritten (no stale cells, no Sync).
func (p *SwitcherPopup) Draw(screen tcell.Screen) {
	p.DrawForSubclass(screen, p)
	mx, my, mw, mh := p.overlayRect()
	if mw <= 0 || mh <= 0 {
		return
	}
	widget.FillArea(screen, mx, my, mw, mh, ' ', tcell.StyleDefault)
	if mw < 4 || mh < 3 {
		return
	}
	widget.DrawBorder(screen, mx, my, mw, mh, theme.StyleFocusedBorder)
	const title = " Jump "
	titleX := mx + (mw-utf8.RuneCountInString(title))/2
	widget.DrawText(screen, titleX, my, mw-(titleX-mx)-1, title, theme.StyleTitle)

	innerX, innerW := mx+1, mw-2
	p.drawFilterLine(screen, innerX, my+1, innerW)
	if mh < 5 {
		return
	}
	footerY := my + mh - 2
	widget.DrawText(screen, innerX+1, footerY, innerW-1, switcherFooterHint(p.nextKey.String()), theme.StyleDimmed)

	colY, colH := my+2, mh-4
	if colH < 2 {
		return
	}
	leftW := innerW / 2
	p.colRects[SwitcherColumnTasks] = switcherRect{innerX, colY, leftW, colH}
	p.colRects[SwitcherColumnProjects] = switcherRect{innerX + leftW, colY, innerW - leftW, colH}

	tasksActive := p.active == SwitcherColumnTasks
	p.tasks.SetFocused(tasksActive)
	p.tasks.SetCursorDimmed(!tasksActive)
	lr := p.colRects[SwitcherColumnTasks]
	p.tasks.SetRect(lr.x, lr.y, lr.w, lr.h)
	p.tasks.Draw(screen)

	rr := p.colRects[SwitcherColumnProjects]
	if !p.projectsAvailable() {
		inner := widget.DrawBorderedPanel(screen, rr.x, rr.y, rr.w, rr.h, " Projects ", theme.StyleBorder)
		if inner.W > 0 && inner.H > 0 {
			widget.DrawText(screen, inner.X+1, inner.Y+inner.H/2, inner.W-1, switcherRemotePlaceholder, theme.StyleDimmed)
		}
		return
	}
	p.rail.SetFocused(!tasksActive)
	p.rail.SetCursorDimmed(tasksActive)
	p.rail.SetRect(rr.x, rr.y, rr.w, rr.h)
	p.rail.Draw(screen)
}

// drawFilterLine renders "› <query>█", or a dim hint while the query is empty.
func (p *SwitcherPopup) drawFilterLine(screen tcell.Screen, x, y, w int) {
	if w <= 2 {
		return
	}
	widget.DrawText(screen, x+1, y, 2, "› ", theme.StyleFilter)
	fieldW := max(w-3, 1)
	if len(p.query) == 0 {
		widget.DrawText(screen, x+3, y, fieldW, "█ type to filter", theme.StyleDimmed)
		return
	}
	val := []rune(string(p.query) + "█")
	if len(val) > fieldW {
		val = val[len(val)-fieldW:]
	}
	widget.DrawText(screen, x+3, y, fieldW, string(val), theme.StyleNormal)
}
