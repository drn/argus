package hera

import (
	"fmt"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// railRowKind enumerates the flattened display-row types. uint8 keeps it small;
// there are only a handful of values.
type railRowKind uint8

const (
	rrRule           railRowKind = iota // non-selectable separator
	rrSectionHeader                     // "Pinned" / "Freelance" label
	rrOrch                              // orchestrator header (selectable, collapsible)
	rrRole                              // role under an orchestrator (selectable)
	rrFreelanceRole                     // freelance-kind role (selectable)
	rrArchiveExpando                    // "Archive (N)" fold (selectable, collapsible)
	rrEmpty                             // empty-state placeholder (non-selectable)
)

// railRow is one flattened display line. orch/role point into the Model (never
// copied) so selection can hand the live projection back to 6b.
type railRow struct {
	kind  railRowKind
	label string
	orch  *OrchView
	role  *RoleView
	depth int
	dim   bool // archived placement → dimmed style

	// Collapse target (only one is set, and only when collapsible).
	collOrchID    int64 // >0 → toggle collapsed[collOrchID]
	collFreelance bool
	collArchive   bool
}

func (r railRow) selectable() bool {
	switch r.kind {
	case rrOrch, rrRole, rrFreelanceRole, rrArchiveExpando:
		return true
	case rrSectionHeader:
		// The Freelance fold header is selectable so the cursor can land on it
		// to collapse/expand; the plain "Pinned" label is not.
		return r.collFreelance
	default:
		return false
	}
}

// heraIconCoord is the coordinator/orchestrator marker (Nerd Font md, same
// codepoint Hera's rail used for iconCoord).
const heraIconCoord = rune(0x0F0E7B) // 󰹻

// Rail is the left navigation widget of the Hera view. It renders the Model
// read-only: sections, cursor, and per-orchestrator collapse. No mutation
// verbs live here (6c).
type Rail struct {
	*tview.Box

	model  Model
	rows   []railRow
	cursor int
	offset int

	// Collapse state survives rebuilds. Orchestrators default expanded; the
	// Freelance section defaults expanded; the Archive section defaults
	// collapsed.
	collapsed        map[int64]bool
	freelanceCollap  bool
	archiveCollapsed bool

	focused bool // drives the border-highlight style

	// onSelectionChanged fires (with the selected row's ref) whenever the
	// cursor lands on a different selectable row. 6b binds the panes off it;
	// 6a wires it only for the focus-guard smoke test. May be nil.
	onSelectionChanged func()
}

// NewRail builds an empty rail. Archive starts collapsed (matches the task
// list's archive default).
func NewRail() *Rail {
	return &Rail{
		Box:              tview.NewBox(),
		collapsed:        make(map[int64]bool),
		archiveCollapsed: true,
	}
}

// SetOnSelectionChanged registers the selection callback.
func (r *Rail) SetOnSelectionChanged(fn func()) { r.onSelectionChanged = fn }

// SetFocused records whether the rail holds keyboard focus, switching its
// border between the focused and unfocused palette.
func (r *Rail) SetFocused(v bool) { r.focused = v }

// SetModel replaces the snapshot and rebuilds rows, preserving the cursor's
// selectable target where possible.
func (r *Rail) SetModel(m Model) {
	prev := r.currentRef()
	r.model = m
	r.buildRows()
	r.restoreCursor(prev)
	r.clampCursor()
}

// Model returns the current snapshot (read-only; for tests/inspection).
func (r *Rail) Model() Model { return r.model }

// currentRef returns a stable identity for the row under the cursor so the
// cursor can be re-pinned after a rebuild. RoleID for roles, OrchID (negated
// to avoid colliding with role ids) for orch headers, 0 otherwise.
func (r *Rail) currentRef() int64 {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return 0
	}
	row := r.rows[r.cursor]
	switch {
	case row.role != nil:
		return row.role.RoleID
	case row.orch != nil:
		return -row.orch.ID
	default:
		return 0
	}
}

func (r *Rail) restoreCursor(ref int64) {
	if ref == 0 {
		return
	}
	for i, row := range r.rows {
		switch {
		case row.role != nil && row.role.RoleID == ref:
			r.cursor = i
			return
		case row.orch != nil && -row.orch.ID == ref:
			r.cursor = i
			return
		}
	}
}

// --- row building ---

func (r *Rail) buildRows() {
	r.rows = nil

	if r.model.IsEmpty() {
		r.rows = append(r.rows, railRow{kind: rrEmpty, label: "No hera orchestrators"})
		return
	}

	// Nesting machinery: a worker whose bridge task is some other orchestrator's
	// coordinator bridge task IS that child orchestrator's coordinator, so the
	// child nests under the worker row. `bridge` maps a coordinator's bridge task
	// to its orchestrator; `consumed` marks every orchestrator reachable as a
	// child so it does NOT also render as a top-level root; `placed` guards
	// single-placement + cycles across the whole build (an orchestrator is
	// rendered at most once, breaking any bridge cycle).
	bridge := r.bridgeIndex()
	consumed := r.consumedSet(bridge)
	placed := make(map[int64]bool)

	// 1. Pinned section. Pinned orchestrators are always top-level roots
	// (user intent), even if some worker bridges them.
	if len(r.model.Pinned) > 0 {
		r.rows = append(r.rows, railRow{kind: rrSectionHeader, label: "Pinned"})
		for i := range r.model.Pinned {
			r.appendOrch(&r.model.Pinned[i], 0, r.model.Pinned[i].Archived, bridge, placed)
		}
	}

	// 2. Active orchestrators (no section header). Render roots (not consumed as
	// a child) first; then a safety sweep places any active orchestrator left
	// unplaced by a pure bridge cycle so nothing ever vanishes from the rail.
	for i := range r.model.Active {
		if !consumed[r.model.Active[i].ID] {
			r.appendOrch(&r.model.Active[i], 0, false, bridge, placed)
		}
	}
	for i := range r.model.Active {
		if !placed[r.model.Active[i].ID] {
			r.appendOrch(&r.model.Active[i], 0, false, bridge, placed)
		}
	}

	// 3. Freelance section.
	if len(r.model.Freelance) > 0 {
		r.rows = append(r.rows, railRow{kind: rrRule})
		r.rows = append(r.rows, railRow{
			kind:          rrSectionHeader,
			label:         fmt.Sprintf("Freelance (%d)", len(r.model.Freelance)),
			collFreelance: true,
		})
		if !r.freelanceCollap {
			for i := range r.model.Freelance {
				r.rows = append(r.rows, railRow{kind: rrFreelanceRole, role: &r.model.Freelance[i], depth: 1})
			}
		}
	}

	// 4. Archive section (collapsed by default): archived orchestrators not
	// already nested under a live parent (placed). A consumed-but-unplaced
	// archived orphan (pure cycle) still surfaces here.
	var archivedRoots []*OrchView
	for i := range r.model.Archived {
		if !placed[r.model.Archived[i].ID] {
			archivedRoots = append(archivedRoots, &r.model.Archived[i])
		}
	}
	if len(archivedRoots) > 0 {
		r.rows = append(r.rows, railRow{kind: rrRule})
		r.rows = append(r.rows, railRow{
			kind:        rrArchiveExpando,
			label:       fmt.Sprintf("Archive (%d)", len(archivedRoots)),
			collArchive: true,
		})
		if !r.archiveCollapsed {
			for _, o := range archivedRoots {
				r.appendOrch(o, 1, true, bridge, placed)
			}
		}
	}
}

// bridgeIndex maps each orchestrator's coordinator bridge task to the
// orchestrator it coordinates (first wins — a coord task is unique to one
// orchestrator in practice). A worker whose bridge task matches a key IS that
// orchestrator's coordinator, so the keyed orchestrator nests under the worker.
func (r *Rail) bridgeIndex() map[string]*OrchView {
	idx := make(map[string]*OrchView)
	for _, sec := range [][]OrchView{r.model.Pinned, r.model.Active, r.model.Archived} {
		for i := range sec {
			if k := sec[i].CoordBridgeTaskID(); k != "" {
				if _, dup := idx[k]; !dup {
					idx[k] = &sec[i]
				}
			}
		}
	}
	return idx
}

// consumedSet marks every orchestrator that is bridged as a child by some OTHER
// orchestrator's (non-teardown) worker, so the top-level passes skip it (it
// renders nested instead).
func (r *Rail) consumedSet(bridge map[string]*OrchView) map[int64]bool {
	consumed := make(map[int64]bool)
	for _, sec := range [][]OrchView{r.model.Pinned, r.model.Active, r.model.Archived} {
		for i := range sec {
			p := &sec[i]
			for j := range p.Roles {
				w := &p.Roles[j]
				if w.Kind == db.HeraKindCoordinator || !roleBridges(w) {
					continue
				}
				if c := bridge[bridgeTaskID(w)]; c != nil && c.ID != p.ID {
					consumed[c.ID] = true
				}
			}
		}
	}
	return consumed
}

// appendOrch emits an orchestrator HEADER (the folded coordinator) and, when
// expanded, its non-coordinator roles nested via appendOrchWorkers. The header
// is rendered once per build (placed guard). dim propagates an archived
// placement down the whole subtree.
func (r *Rail) appendOrch(o *OrchView, depth int, dim bool, bridge map[string]*OrchView, placed map[int64]bool) {
	if placed[o.ID] {
		return
	}
	placed[o.ID] = true
	r.rows = append(r.rows, railRow{
		kind:       rrOrch,
		orch:       o,
		depth:      depth,
		dim:        dim,
		collOrchID: o.ID,
	})
	if r.collapsed[o.ID] {
		return
	}
	r.appendOrchWorkers(o, depth+1, dim, bridge, placed)
}

// appendOrchWorkers emits o's non-coordinator role rows at `depth`. A worker
// that bridges a not-yet-placed child orchestrator nests that child's workers
// one level deeper, immediately under the worker row (the worker row IS the
// child's coordinator — same multi-binding task — so no separate child header is
// drawn). The bridging row carries collOrchID = child.ID so Space folds the
// nested subtree, and a chevron marks it foldable. The placed guard breaks any
// bridge cycle and guarantees single placement.
//
// The bridging row keeps its PARENT worker selection context (a worker role
// under o): nesting is purely visual, so mutations (notably Ctrl+D) act on the
// worker role, never the child orchestrator — conservative multi-binding safety.
func (r *Rail) appendOrchWorkers(o *OrchView, depth int, dim bool, bridge map[string]*OrchView, placed map[int64]bool) {
	for i := range o.Roles {
		w := &o.Roles[i]
		if w.Kind == db.HeraKindCoordinator {
			continue // folded into the header / the bridging row above
		}
		childDim := dim || w.Archived

		var child *OrchView
		if roleBridges(w) {
			if c := bridge[bridgeTaskID(w)]; c != nil && c.ID != o.ID && !placed[c.ID] {
				child = c
			}
		}
		collID := int64(0)
		if child != nil {
			collID = child.ID
		}
		r.rows = append(r.rows, railRow{kind: rrRole, role: w, depth: depth, dim: childDim, collOrchID: collID})

		if child != nil {
			placed[child.ID] = true
			if !r.collapsed[child.ID] {
				r.appendOrchWorkers(child, depth+1, childDim || child.Archived, bridge, placed)
			}
		}
	}
}

// --- navigation ---

// CursorDown moves to the next selectable row.
func (r *Rail) CursorDown() { r.step(1) }

// CursorUp moves to the previous selectable row.
func (r *Rail) CursorUp() { r.step(-1) }

func (r *Rail) step(dir int) {
	if len(r.rows) == 0 {
		return
	}
	i := r.cursor
	for {
		i += dir
		if i < 0 || i >= len(r.rows) {
			return // no further selectable row; leave cursor put
		}
		if r.rows[i].selectable() {
			r.setCursor(i)
			return
		}
	}
}

func (r *Rail) setCursor(i int) {
	if i == r.cursor {
		return
	}
	r.cursor = i
	if r.onSelectionChanged != nil {
		r.onSelectionChanged()
	}
}

func (r *Rail) clampCursor() {
	if len(r.rows) == 0 {
		r.cursor = 0
		return
	}
	if r.cursor < 0 {
		r.cursor = 0
	}
	if r.cursor >= len(r.rows) {
		r.cursor = len(r.rows) - 1
	}
	// Land on a selectable row if the clamp parked on a rule/header.
	if !r.rows[r.cursor].selectable() {
		for i := r.cursor; i < len(r.rows); i++ {
			if r.rows[i].selectable() {
				r.cursor = i
				return
			}
		}
		for i := r.cursor; i >= 0; i-- {
			if r.rows[i].selectable() {
				r.cursor = i
				return
			}
		}
	}
}

// ToggleCollapse flips the fold state of the collapsible row under the cursor.
// Roles and rules are no-ops.
func (r *Rail) ToggleCollapse() {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return
	}
	row := r.rows[r.cursor]
	switch {
	case row.collOrchID > 0:
		r.collapsed[row.collOrchID] = !r.collapsed[row.collOrchID]
	case row.collFreelance:
		r.freelanceCollap = !r.freelanceCollap
	case row.collArchive:
		r.archiveCollapsed = !r.archiveCollapsed
	default:
		return
	}
	ref := r.currentRef()
	r.buildRows()
	r.restoreCursor(ref)
	r.clampCursor()
}

// Selected returns the RoleView under the cursor, or nil when the cursor is on
// a header/orchestrator. 6b uses this to bind the panes.
func (r *Rail) Selected() *RoleView {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return nil
	}
	return r.rows[r.cursor].role
}

// SelectedOrch returns the OrchView under the cursor, or nil.
func (r *Rail) SelectedOrch() *OrchView {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return nil
	}
	return r.rows[r.cursor].orch
}

// Selection returns the (role, orchestrator) context under the cursor. The
// orchestrator is resolved from the selected role's OrchID — correct even in
// the multi-binding case where the same task surfaces under two orchestrators
// (each via a distinct role with a distinct OrchID) — or taken directly when
// the cursor rests on an orchestrator header. 6b binds the panes off this; 6c
// reads it for mutations via the orchestrator disambiguator.
func (r *Rail) Selection() Selection {
	role := r.Selected()
	orch := r.SelectedOrch()
	if orch == nil && role != nil {
		orch = r.model.OrchByID(role.OrchID)
	}
	return Selection{Role: role, Orch: orch}
}

// Rows returns the flattened row count (test seam).
func (r *Rail) Rows() int { return len(r.rows) }

// CursorIndex returns the cursor's row index (test seam).
func (r *Rail) CursorIndex() int { return r.cursor }

// --- rendering ---

// Draw paints the rail inside a bordered panel, covering its full bounding
// rect (DrawBorderedPanel blanks the interior) so no stale cells survive — per
// the CLAUDE.md UX-rendering rules (no Sync; full-rect coverage instead).
func (r *Rail) Draw(screen tcell.Screen) {
	r.DrawForSubclass(screen, r)
	x, y, w, h := r.GetRect()
	if w <= 0 || h <= 0 {
		return
	}
	borderStyle := theme.StyleBorder
	if r.focused {
		borderStyle = theme.StyleFocusedBorder
	}
	inner := widget.DrawBorderedPanel(screen, x, y, w, h, " Hera ", borderStyle)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	r.adjustOffset(inner.H)
	for vis := 0; vis < inner.H; vis++ {
		idx := r.offset + vis
		if idx >= len(r.rows) {
			break
		}
		r.drawRow(screen, inner.X, inner.Y+vis, inner.W, r.rows[idx], idx == r.cursor)
	}
}

// adjustOffset keeps the cursor visible within the inner viewport height.
func (r *Rail) adjustOffset(viewH int) {
	if viewH <= 0 {
		return
	}
	if r.cursor < r.offset {
		r.offset = r.cursor
	}
	if r.cursor >= r.offset+viewH {
		r.offset = r.cursor - viewH + 1
	}
	if r.offset < 0 {
		r.offset = 0
	}
}

func (r *Rail) drawRow(screen tcell.Screen, x, y, w int, row railRow, selected bool) {
	const marker = '›'
	indent := row.depth * 2

	// Selection marker gutter.
	gutterStyle := theme.StyleDimmed
	if selected {
		screen.SetContent(x, y, marker, nil, theme.StyleSelected)
		gutterStyle = theme.StyleSelected
	}
	textX := x + 2 + indent
	textW := w - 2 - indent
	if textW <= 0 {
		return
	}

	switch row.kind {
	case rrRule:
		for c := x; c < x+w; c++ {
			screen.SetContent(c, y, '─', nil, theme.StyleBorder)
		}
	case rrEmpty:
		widget.DrawText(screen, textX, y, textW, row.label, theme.StyleDimmed)
	case rrSectionHeader:
		style := theme.StyleTitle
		if selected {
			style = theme.StyleSelected
		}
		label := row.label
		if row.collFreelance {
			label = chevron(r.freelanceCollap) + " " + label
		}
		widget.DrawText(screen, textX, y, textW, label, style)
	case rrArchiveExpando:
		style := theme.StyleDimmed
		if selected {
			style = theme.StyleSelected
		}
		widget.DrawText(screen, textX, y, textW, chevron(r.archiveCollapsed)+" "+row.label, style)
	case rrOrch:
		r.drawOrchRow(screen, textX, y, textW, row, selected)
	case rrRole, rrFreelanceRole:
		r.drawRoleRow(screen, textX, y, textW, row, selected, gutterStyle)
	}
}

func (r *Rail) drawOrchRow(screen tcell.Screen, x, y, w int, row railRow, selected bool) {
	o := row.orch
	nameStyle := theme.StyleProject
	if row.dim {
		nameStyle = theme.StyleDimmed
	}
	if selected {
		nameStyle = theme.StyleSelected
	}
	col := x
	// Coordinator status glyph first (the header IS the coordinator — folded
	// from a redundant child row). It reads with the same vocabulary as worker
	// rows; the glyph keeps its own status style even when the row is selected
	// (the glyph never lies). Worker-less / coordinator-less orchestrators skip it.
	if coord := o.CoordRole(); coord != nil {
		glyph, gstyle := statusIcon(coord, row.dim)
		screen.SetContent(col, y, glyph, nil, gstyle)
		col += 2
	}
	// chevron
	if col < x+w {
		screen.SetContent(col, y, []rune(chevron(r.collapsed[o.ID]))[0], nil, nameStyle)
		col += 2
	}
	// coordinator marker
	if col < x+w {
		screen.SetContent(col, y, heraIconCoord, nil, nameStyle)
		col += 2
	}
	remaining := w - (col - x)
	if remaining <= 0 {
		return
	}
	live := liveRoleCount(o)
	label := o.Name
	count := fmt.Sprintf(" (%d)", live)
	if len(label)+len(count) > remaining {
		widget.DrawText(screen, col, y, remaining, label, nameStyle)
		return
	}
	widget.DrawText(screen, col, y, remaining, label, nameStyle)
	widget.DrawText(screen, x+w-len(count), y, len(count), count, theme.StyleDimmed)
}

func (r *Rail) drawRoleRow(screen tcell.Screen, x, y, w int, row railRow, selected bool, _ tcell.Style) {
	role := row.role
	icon, iconStyle := statusIcon(role, row.dim)
	nameStyle := theme.StyleNormal
	if row.dim {
		nameStyle = theme.StyleDimmed
	}
	if selected {
		nameStyle = theme.StyleSelected
	}
	col := x
	screen.SetContent(col, y, icon, nil, iconStyle)
	col += 2
	// A bridging sub-coordinator row (collOrchID set) reads like a nested
	// orchestrator header: a fold chevron + the coordinator marker before the
	// name, so the operator can tell it carries (and folds) a child subtree.
	if row.collOrchID > 0 {
		if col < x+w {
			markerStyle := nameStyle
			if !selected {
				markerStyle = theme.StyleProject
				if row.dim {
					markerStyle = theme.StyleDimmed
				}
			}
			screen.SetContent(col, y, []rune(chevron(r.collapsed[row.collOrchID]))[0], nil, markerStyle)
			col += 2
			if col < x+w {
				screen.SetContent(col, y, heraIconCoord, nil, markerStyle)
				col += 2
			}
		}
	}
	remaining := w - (col - x)
	if remaining <= 0 {
		return
	}
	widget.DrawText(screen, col, y, remaining, role.Name, nameStyle)
}

// chevron returns the fold glyph for a collapsed/expanded state.
func chevron(collapsed bool) string {
	if collapsed {
		return "▸"
	}
	return "▾"
}

// liveRoleCount counts live, non-coordinator roles (the agents shown under the
// header). The coordinator is folded into the header itself, so it never inflates
// the (N) child count.
func liveRoleCount(o *OrchView) int {
	n := 0
	for i := range o.Roles {
		if o.Roles[i].Live && o.Roles[i].Kind != db.HeraKindCoordinator {
			n++
		}
	}
	return n
}

// statusIcon picks the glyph + style for a role row. ready_to_close (M4) wins
// over everything else with a distinct "ready to check off" mark; otherwise the
// hera role status (idle/working/blocked/done) drives the glyph, falling back
// to binding presence when no status row exists. dim forces the dimmed style
// for archived placement (the glyph never lies — only the style dims).
func statusIcon(role *RoleView, dim bool) (rune, tcell.Style) {
	if role.ReadyToClose {
		st := tcell.StyleDefault.Foreground(theme.ColorComplete).Bold(true)
		if dim {
			st = theme.StyleDimmed
		}
		return theme.IconReview, st
	}
	var glyph rune
	var style tcell.Style
	switch {
	case role.HasStatus && role.Status == db.HeraStatusWorking:
		glyph, style = theme.IconMoonStars, theme.StyleInProgress
	case role.HasStatus && role.Status == db.HeraStatusBlocked:
		glyph, style = theme.IconNeedsInput, theme.StyleNeedsInput
	case role.HasStatus && role.Status == db.HeraStatusDone:
		glyph, style = '✓', theme.StyleComplete
	case role.HasStatus && role.Status == db.HeraStatusIdle:
		glyph, style = theme.IconMoonOutline, theme.StyleInReview
	case role.Live:
		glyph, style = theme.IconMoonStars, theme.StyleInReview
	default:
		glyph, style = theme.IconMoonOutline, theme.StyleDimmed
	}
	if dim {
		style = theme.StyleDimmed
	}
	return glyph, style
}

// InputHandler routes navigation keys. Left/Right are currently unused here
// (Up/Down/j/k drive the cursor, Space toggles collapse); they are free for
// future horizontal navigation now that the global handler no longer consumes
// them for tab switching (tab nav is 1/2/3 only).
func (r *Rail) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return r.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyDown:
			r.CursorDown()
		case tcell.KeyUp:
			r.CursorUp()
		case tcell.KeyRune:
			switch event.Rune() {
			case 'j':
				r.CursorDown()
			case 'k':
				r.CursorUp()
			case ' ':
				r.ToggleCollapse()
			}
		}
	})
}
