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
	workerRows := len(d.workers())
	if workerRows == 0 {
		workerRows = 1 // the "(none)" line
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

	// Worker roster — every non-coordinator role under the orchestrator, with
	// its status glyph, ready_to_close / PR marks.
	workers := d.workers()
	draw(inner.X, fmt.Sprintf("Agents (%d):", len(workers)), theme.StyleDimmed)
	if len(workers) == 0 {
		draw(inner.X+2, "(none)", theme.StyleDimmed)
	}
	for i := range workers {
		w := &workers[i]
		if row >= maxRow {
			break
		}
		glyph, gstyle := statusIcon(w, d.orch.Archived, frame)
		screen.SetContent(inner.X+2, row, glyph, nil, gstyle)
		label := w.Name
		if mark := d.roleMark(w); mark != "" {
			label += "  " + mark
		}
		widget.DrawText(screen, inner.X+4, row, inner.W-4, label, theme.StyleNormal)
		row++
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

// roleMark composes the trailing indicators for a roster row: a "ready" mark
// for a finished worker awaiting close-out (M4 meta:hera.ready_to_close) and a
// "PR" mark when the bound task carries a "pr" meta url (best-effort, like the
// task list — never fetched here).
func (d *DetailsView) roleMark(r *RoleView) string {
	mark := ""
	if r.ReadyToClose {
		mark = "ready"
	}
	if r.TaskID != "" && d.prMeta != nil {
		if kv := d.prMeta[r.TaskID]; kv != nil && kv["url"] != "" {
			if mark != "" {
				mark += " "
			}
			mark += "PR"
		}
	}
	return mark
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
