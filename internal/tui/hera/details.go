package hera

import (
	"fmt"

	"github.com/drn/argus/internal/db"
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
}

// NewDetailsView builds an empty details view.
func NewDetailsView() *DetailsView { return &DetailsView{} }

// SetOrch sets the orchestrator whose coordinator summary is rendered. prMeta
// is the daemon-populated "pr" namespace cache (taskID -> {state,url}); pass
// nil when unavailable — the PR mark just won't render (best-effort, no fetch).
func (d *DetailsView) SetOrch(o *OrchView, prMeta map[string]map[string]string) {
	d.orch = o
	d.prMeta = prMeta
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
	// title + spacer + spacer + "Agents (N):" header = 4 content rows; the
	// coordinator status line is drawn only when a coordinator role exists, so
	// count it conditionally to match Draw (which skips it when coordRole==nil).
	content := 4
	if d.coordRole() != nil {
		content++
	}
	workerRows := len(d.workers())
	if workerRows == 0 {
		workerRows = 1 // the "(none)" line
	}
	return border + content + workerRows
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

	// Title — orchestrator name.
	draw(inner.X, d.orch.Name, theme.StyleTitle)
	row++ // blank spacer

	// Coordinator status line — reuse the rail's coordinator glyph so the
	// Details status never disagrees with the rail header.
	if coord := d.coordRole(); coord != nil {
		glyph, gstyle := statusIcon(coord, d.orch.Archived)
		if row < maxRow {
			screen.SetContent(inner.X, row, glyph, nil, gstyle)
			widget.DrawText(screen, inner.X+2, row, inner.W-2, "coordinator: "+coordStatusLabel(coord), theme.StyleDimmed)
			row++
		}
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
		glyph, gstyle := statusIcon(w, d.orch.Archived)
		screen.SetContent(inner.X+2, row, glyph, nil, gstyle)
		label := w.Name
		if mark := d.roleMark(w); mark != "" {
			label += "  " + mark
		}
		widget.DrawText(screen, inner.X+4, row, inner.W-4, label, theme.StyleNormal)
		row++
	}
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

// coordStatusLabel renders a human label for a coordinator role's status.
func coordStatusLabel(r *RoleView) string {
	switch {
	case r.HasStatus && r.Status != "":
		return string(r.Status)
	case r.Live:
		return "live"
	default:
		return "—"
	}
}
