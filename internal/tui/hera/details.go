package hera

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

// DetailsView renders the read-only coordinator summary shown in the right
// region when a COORDINATOR role is selected: the orchestrator name, its
// coordinator status glyph, and the worker roster with per-role status,
// ready_to_close (M4) and PR marks. It is the Argus-native port of Hera's
// coord_details.go — trimmed to what 6b needs (the DAG render mode of this
// pane is M7). Every field is read from the already-built Model projection
// (the rail's source of truth, so Details never disagrees with the rail) plus
// the task-addressed "pr" meta namespace for the PR indicator.
//
// It holds no live session and issues no I/O at Draw time — it is a pure
// projection renderer, safe on the tview main thread.
type DetailsView struct {
	orch   *OrchView
	prMeta map[string]map[string]string // taskID -> pr meta (namespace "pr")
	meta   coordMeta                    // derived once in SetOrch (pure over orch)
}

// coordMeta is the rich metadata block the Details pane renders for a selected
// coordinator, derived purely from the rail's OrchView projection (no DB read).
// It is the native port of the plugin's coordDetails metadata fields (created /
// last-activity / repos-in-scope / agent / worktree).
type coordMeta struct {
	Created      time.Time // orchestrator creation time
	LastActivity time.Time // max over orch+role creation, binding start, status update
	AgentName    string    // coordinator role's bound argus task name ("" when unbound)
	Worktree     string    // coordinator role's live-binding worktree ("" when absent)
	Repos        []string  // distinct argus projects across roster roles, sorted
}

// deriveCoordMeta computes the coordinator Details metadata from an OrchView.
// It is a pure projection (no I/O), so it is unit-testable from a constructed
// OrchView and safe to call on the tview main thread. Last-activity is the max
// over the orchestrator's creation time and every role's creation time, live-
// binding start, and status-update time. Repos-in-scope are the distinct argus
// projects across the orchestrator's roster roles (the same role set the roster
// shows — hoisted freelance roles are not included), sorted. Agent + Worktree
// come from the coordinator role.
func deriveCoordMeta(o *OrchView) coordMeta {
	m := coordMeta{Created: o.CreatedAt, LastActivity: o.CreatedAt}
	bump := func(t time.Time) {
		if t.After(m.LastActivity) {
			m.LastActivity = t
		}
	}
	repoSet := map[string]struct{}{}
	for i := range o.Roles {
		r := &o.Roles[i]
		if r.ArgusProject != "" {
			repoSet[r.ArgusProject] = struct{}{}
		}
		bump(r.CreatedAt)
		bump(r.BindingStartedAt)
		bump(r.StatusUpdatedAt)
		if r.Kind == db.HeraKindCoordinator {
			m.AgentName = r.TaskName
			m.Worktree = r.WorktreePath
		}
	}
	repos := make([]string, 0, len(repoSet))
	for p := range repoSet {
		repos = append(repos, p)
	}
	sort.Strings(repos)
	m.Repos = repos
	return m
}

// NewDetailsView builds an empty details view.
func NewDetailsView() *DetailsView { return &DetailsView{} }

// SetOrch sets the orchestrator whose coordinator summary is rendered. prMeta
// is the daemon-populated "pr" namespace cache (taskID -> {state,url}); pass
// nil when unavailable — the PR mark just won't render (best-effort, no fetch).
// The rich metadata block is derived once here so Draw + ContentHeight share
// the same (immutable) inputs and never drift.
func (d *DetailsView) SetOrch(o *OrchView, prMeta map[string]map[string]string) {
	d.orch = o
	d.prMeta = prMeta
	if o != nil {
		d.meta = deriveCoordMeta(o)
	} else {
		d.meta = coordMeta{}
	}
}

// ContentHeight returns the natural height (including the 2-row border) the
// coordinator summary needs to render without truncation. It mirrors Draw's row
// budget EXACTLY so the stacked Details region can size the roster before
// handing the remainder to the DAG. With no orchestrator selected it is just the
// border plus the "(no coordinator selected)" line.
func (d *DetailsView) ContentHeight() int {
	const border = 2
	if d.orch == nil {
		return border + 1
	}
	// Always-present content rows (see Draw): title, blank, Created, Last
	// activity, blank, "Repos in scope:" header, blank, "Agents (N):" header,
	// blank, "Summary:" header, summary placeholder = 11.
	content := 11
	// The coordinator status line is drawn only when a coordinator role exists.
	if d.coordRole() != nil {
		content++
	}
	// Agent / Worktree are conditional on the coordinator being bound.
	if d.meta.AgentName != "" {
		content++
	}
	if d.meta.Worktree != "" {
		content++
	}
	reposRows := len(d.meta.Repos)
	if reposRows == 0 {
		reposRows = 1 // the "(none)" line
	}
	var workerRows int
	if n := len(d.workers()); n == 0 {
		workerRows = 1 // the "(none)" line
	} else {
		workerRows = 1 + n // column header + one row per agent
	}
	return border + content + reposRows + workerRows
}

// Draw paints the coordinator summary inside a bordered panel covering the full
// bounding rect (DrawBorderedPanel blanks the interior) so no stale cells
// survive — per the CLAUDE.md UX-rendering rules (no Sync; full-rect coverage).
func (d *DetailsView) Draw(screen tcell.Screen, x, y, w, h int, focused bool) {
	if w < 2 || h < 2 {
		return
	}
	style := theme.StyleBorder
	if focused {
		style = theme.StyleFocusedBorder
	}
	inner := widget.DrawBorderedPanel(screen, x, y, w, h, " Details ", style)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	if d.orch == nil {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "(no coordinator selected)", theme.StyleDimmed)
		return
	}

	row := inner.Y
	maxRow := inner.Y + inner.H
	draw := func(col int, text string, st tcell.Style) {
		if row >= maxRow {
			return
		}
		widget.DrawText(screen, col, row, inner.X+inner.W-col, text, st)
		row++
	}
	// field draws a "Label: value" row — the label dimmed, the value normal.
	field := func(label, value string) {
		if row >= maxRow {
			return
		}
		lbl := label + ": "
		widget.DrawText(screen, inner.X, row, inner.W, lbl, theme.StyleDimmed)
		if n := utf8.RuneCountInString(lbl); n < inner.W {
			widget.DrawText(screen, inner.X+n, row, inner.W-n, value, theme.StyleNormal)
		}
		row++
	}

	// Title — orchestrator name.
	draw(inner.X, d.orch.Name, theme.StyleTitle)
	row++ // blank spacer

	frame := spinnerFrame()
	// Coordinator status line — reuse the rail's coordinator glyph so the
	// Details status never disagrees with the rail header.
	if coord := d.coordRole(); coord != nil {
		glyph, gstyle := statusIcon(coord, d.orch.Archived, frame)
		if row < maxRow {
			screen.SetContent(inner.X, row, glyph, nil, gstyle)
			widget.DrawText(screen, inner.X+2, row, inner.W-2, "coordinator: "+coordStatusLabel(coord), theme.StyleDimmed)
			row++
		}
	}

	// Rich metadata block (native port of the plugin's coord_details fields).
	meta := d.meta
	field("Created", fmtDetailTime(meta.Created))
	field("Last activity", fmtDetailTime(meta.LastActivity))
	if meta.AgentName != "" {
		field("Agent", meta.AgentName)
	}
	if meta.Worktree != "" {
		field("Worktree", worktreeDisplay(meta.Worktree, inner.W))
	}
	row++ // blank spacer

	// Repos in scope — the distinct argus projects across the roster roles.
	draw(inner.X, "Repos in scope:", theme.StyleDimmed)
	if len(meta.Repos) == 0 {
		draw(inner.X+2, "(none)", theme.StyleDimmed)
	}
	for _, r := range meta.Repos {
		draw(inner.X+2, "• "+r, theme.StyleNormal)
	}
	row++ // blank spacer

	// Worker roster — every non-coordinator role under the orchestrator,
	// rendered as an aligned table: status (icon + label), name, diligence
	// archetype, resolved model.
	workers := d.workers()
	draw(inner.X, fmt.Sprintf("Agents (%d):", len(workers)), theme.StyleDimmed)
	if len(workers) == 0 {
		draw(inner.X+2, "(none)", theme.StyleDimmed)
	} else {
		cols := computeRosterColumns(workers, inner.W-2)
		if row < maxRow {
			drawRosterHeader(screen, inner.X+2, row, cols)
			row++
		}
		for i := range workers {
			if row >= maxRow {
				break
			}
			d.drawRosterRow(screen, inner.X+2, row, cols, &workers[i], frame)
			row++
		}
	}
	row++ // blank spacer

	// Reserved Summary placeholder (the inferred living-summary is not yet
	// implemented — mirrors the plugin's reserved field).
	draw(inner.X, "Summary:", theme.StyleDimmed)
	draw(inner.X+2, "(auto-generated overview coming soon)", theme.StyleDimmed)
}

// fmtDetailTime formats a timestamp for the Details pane in local time, or an
// en-dash placeholder when zero.
func fmtDetailTime(t time.Time) string {
	if t.IsZero() {
		return "–"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// worktreeDisplay formats a worktree path for the Details pane. When the full
// path fits in availWidth it is returned verbatim; otherwise the last two path
// components are shown (e.g. "Hera/the-hera-foo") so the meaningful project/task
// portion stays visible, falling back to the base name when even that overflows.
func worktreeDisplay(path string, availWidth int) string {
	if availWidth <= 0 || utf8.RuneCountInString(path) <= availWidth {
		return path
	}
	short := filepath.Base(filepath.Dir(path)) + "/" + filepath.Base(path)
	if utf8.RuneCountInString(short) <= availWidth {
		return short
	}
	return filepath.Base(path)
}

// coordRole returns the orchestrator's coordinator role, or nil.
func (d *DetailsView) coordRole() *RoleView {
	for i := range d.orch.Roles {
		if d.orch.Roles[i].Kind == db.HeraKindCoordinator {
			return &d.orch.Roles[i]
		}
	}
	return nil
}

// workers returns the non-coordinator roles under the orchestrator.
func (d *DetailsView) workers() []RoleView {
	out := make([]RoleView, 0, len(d.orch.Roles))
	for i := range d.orch.Roles {
		if d.orch.Roles[i].Kind != db.HeraKindCoordinator {
			out = append(out, d.orch.Roles[i])
		}
	}
	return out
}

// hasPR reports whether the role's bound task carries an open "pr" meta url
// (best-effort, like the task list — never fetched here).
func (d *DetailsView) hasPR(r *RoleView) bool {
	if r.TaskID == "" || d.prMeta == nil {
		return false
	}
	kv := d.prMeta[r.TaskID]
	return kv != nil && kv["url"] != ""
}

// --- Roster table ------------------------------------------------------
//
// The Agents roster renders as a compact, left-aligned table: status (icon +
// label), name, diligence archetype, resolved model. Column widths size to
// the widest cell (capped) and shrink — model, then archetype, then name,
// then status, in that priority order — when the pane is narrower than the
// ideal total width. Every value is truncated RUNE-safely (never a byte slice
// mid-codepoint), and a zero-width column simply stops rendering rather than
// corrupting the layout.

const (
	rosterColGap      = 2  // spaces between columns
	rosterIconGutter  = 2  // icon rune + 1 space, before the status column starts
	rosterStatusWidth = 14 // fits the longest label, "needs-input PR"
	rosterNameMin     = 6
	rosterNameMax     = 18
	rosterArchMin     = 9 // fits the "ARCHETYPE" header
	rosterArchMax     = 12
	rosterModelMin    = 6
	rosterModelMax    = 20
)

// rosterCols holds the resolved content width (not counting gaps/gutter) for
// each roster column.
type rosterCols struct {
	status, name, archetype, model int
}

// rosterTotalWidth is the full row width the given columns need: the icon
// gutter, each column's content width, and a gap between every column.
func rosterTotalWidth(c rosterCols) int {
	return rosterIconGutter + c.status + c.name + c.archetype + c.model + 3*rosterColGap
}

// rosterColStarts returns the x-offset (relative to a row's icon column)
// where each column's TEXT begins, so the header and data rows always agree.
func rosterColStarts(c rosterCols) (statusX, nameX, archX, modelX int) {
	statusX = rosterIconGutter
	nameX = statusX + c.status + rosterColGap
	archX = nameX + c.name + rosterColGap
	modelX = archX + c.archetype + rosterColGap
	return
}

// computeRosterColumns sizes the roster's four columns from the widest cell
// value in each (capped at a sane maximum), then shrinks them — model first,
// then archetype, then name, then status as the last resort — down to zero
// when avail is narrower than the ideal total width.
func computeRosterColumns(workers []RoleView, avail int) rosterCols {
	cols := rosterCols{status: rosterStatusWidth, name: rosterNameMin, archetype: rosterArchMin, model: rosterModelMin}
	for i := range workers {
		if n := utf8.RuneCountInString(workers[i].Name); n > cols.name {
			cols.name = n
		}
		if n := utf8.RuneCountInString(archetypeDisplay(workers[i].Archetype)); n > cols.archetype {
			cols.archetype = n
		}
		if n := utf8.RuneCountInString(modelDisplay(workers[i].AppliedModel)); n > cols.model {
			cols.model = n
		}
	}
	if cols.name > rosterNameMax {
		cols.name = rosterNameMax
	}
	if cols.archetype > rosterArchMax {
		cols.archetype = rosterArchMax
	}
	if cols.model > rosterModelMax {
		cols.model = rosterModelMax
	}
	if avail <= 0 {
		return rosterCols{}
	}
	overflow := rosterTotalWidth(cols) - avail
	shrink := func(w *int) {
		if overflow <= 0 {
			return
		}
		cut := *w
		if cut > overflow {
			cut = overflow
		}
		*w -= cut
		overflow -= cut
	}
	shrink(&cols.model)
	shrink(&cols.archetype)
	shrink(&cols.name)
	shrink(&cols.status)
	return cols
}

// rosterTruncate clamps s to w runes, appending a trailing "…" when clipped.
// Rune-aware so multibyte values (names, model ids) never split mid-codepoint.
func rosterTruncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return string(r[:1])
	}
	return string(r[:w-1]) + "…"
}

// archetypeDisplay / modelDisplay render the roster's ARCHETYPE / MODEL cells:
// "—" when the role carries no archetype (no profile consulted) or no
// resolved model (CLI/backend default applied) — never blank, so the column
// never reads as misaligned or broken.
func archetypeDisplay(a string) string {
	if a == "" {
		return "—"
	}
	return a
}

func modelDisplay(m string) string {
	if m == "" {
		return "—"
	}
	return m
}

// rosterStatusText renders the roster status cell's text, mirroring
// statusIcon's precedence exactly (widget.RoleStatusIcon) so the glyph and
// the label never disagree. A "PR" suffix is appended whenever the role's
// bound task carries an open PR, independent of the underlying status.
func rosterStatusText(r *RoleView, hasPR bool) string {
	in := roleStatusInputs(r)
	var text string
	switch {
	case in.NeedsInput:
		text = "needs-input"
	case in.Active:
		text = "working"
	case in.ReadyToClose:
		text = "ready"
	case in.Failed:
		text = "failed"
	case in.Done:
		text = "done"
	case in.Idle:
		text = "idle"
	case in.Live:
		text = "live"
	default:
		text = "—"
	}
	if hasPR {
		text += " PR"
	}
	return text
}

// drawRosterHeader paints the roster's column header row (STATUS / NAME /
// ARCHETYPE / MODEL), aligned to the same columns the data rows use.
func drawRosterHeader(screen tcell.Screen, x, y int, cols rosterCols) {
	statusX, nameX, archX, modelX := rosterColStarts(cols)
	widget.DrawText(screen, x+statusX, y, cols.status, rosterTruncate("STATUS", cols.status), theme.StyleDimmed)
	widget.DrawText(screen, x+nameX, y, cols.name, rosterTruncate("NAME", cols.name), theme.StyleDimmed)
	widget.DrawText(screen, x+archX, y, cols.archetype, rosterTruncate("ARCHETYPE", cols.archetype), theme.StyleDimmed)
	widget.DrawText(screen, x+modelX, y, cols.model, rosterTruncate("MODEL", cols.model), theme.StyleDimmed)
}

// drawRosterRow paints one agent's roster row: status icon + label, name,
// archetype, model — aligned to cols. The icon glyph reuses statusIcon (the
// SAME precedence the rail uses) so this table never disagrees with the rail.
func (d *DetailsView) drawRosterRow(screen tcell.Screen, x, y int, cols rosterCols, r *RoleView, frame int) {
	glyph, gstyle := statusIcon(r, d.orch.Archived, frame)
	screen.SetContent(x, y, glyph, nil, gstyle)
	statusX, nameX, archX, modelX := rosterColStarts(cols)
	text := rosterStatusText(r, d.hasPR(r))
	widget.DrawText(screen, x+statusX, y, cols.status, rosterTruncate(text, cols.status), gstyle)
	widget.DrawText(screen, x+nameX, y, cols.name, rosterTruncate(r.Name, cols.name), theme.StyleNormal)
	widget.DrawText(screen, x+archX, y, cols.archetype, rosterTruncate(archetypeDisplay(r.Archetype), cols.archetype), theme.StyleDimmed)
	widget.DrawText(screen, x+modelX, y, cols.model, rosterTruncate(modelDisplay(r.AppliedModel), cols.model), theme.StyleDimmed)
}

// coordStatusLabel renders the coordinator status line for the Details pane. It
// combines the coordinator's hera ROLE status with any TERMINAL bound-task
// signal: "<role> · task <state>" (e.g. "live · task complete") when the task
// adds a signal, else just the role-status label. The argus workflow status is
// owned by the session lifecycle, not the manual hera ladder, so a coordinator
// whose task has finished (in_review / complete / failed) otherwise gives no
// hint in the Details pane (BUG-015). One row — ContentHeight is unaffected.
func coordStatusLabel(r *RoleView) string {
	label := coordRoleStatusLabel(r)
	if task := coordTaskStatusLabel(r); task != "" {
		label += " · task " + task
	}
	return label
}

// coordRoleStatusLabel renders a human label for a coordinator role's hera
// status. A STALE "working" role-status (the manual/MCP-set ladder value that
// never reconciles down after a session idles/stops/dies) is not reported as
// "working" unless it is backed by real activity (role.IsActive) — the same
// honesty the rail spinner now enforces (BUG-003 / BUG-036). Post-BUG-C IsActive
// is gated on liveness + content-idle (NOT bound-task status), so a stale-working
// role reads "live" when its binding is alive but its session is idle
// (SessionIdle) and "stopped" when the binding is gone; a live, content-active
// role reads "working" honestly regardless of task status. Every other
// role-status passes through verbatim.
func coordRoleStatusLabel(r *RoleView) string {
	switch {
	case r.HasStatus && r.Status == db.HeraStatusWorking && !r.IsActive():
		if r.Live {
			return "live"
		}
		return "stopped"
	case r.HasStatus && r.Status != "":
		return string(r.Status)
	case r.Live:
		return "live"
	default:
		return "—"
	}
}

// coordTaskStatusLabel surfaces ONLY terminal bound-task states (in_review,
// complete, or failed) — the signals the manual hera role status can't convey.
// "failed" is derived from the task's opaque result blob {"failed":true},
// mirroring dagview.parseFailed and winning over the workflow status. Ongoing
// (pending / in_progress) or unbound tasks add no signal (returns "").
func coordTaskStatusLabel(r *RoleView) string {
	if coordTaskFailed(r.TaskResult) {
		return "failed"
	}
	switch r.TaskStatus {
	case model.StatusComplete.String():
		return "complete"
	case model.StatusInReview.String():
		return "in_review"
	default:
		return ""
	}
}

// coordTaskFailed reports whether the agent-supplied result blob set
// `failed: true`. A malformed/empty blob is tolerated as not-failed. Mirrors
// dagview.parseFailed (unexported there, so re-stated rather than imported).
func coordTaskFailed(raw string) bool {
	if raw == "" {
		return false
	}
	var v struct {
		Failed bool `json:"failed"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return false
	}
	return v.Failed
}
