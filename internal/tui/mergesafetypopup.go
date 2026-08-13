package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// mergeSafetyScope selects which section of a MergeSafetyPopup a Clean
// action applies to — also doubles as the wire value for the global
// Cleanup action's REST `scope` parameter ("safe"/"all"), since the two are
// definitionally the same choice (add-merge-safety-review).
type mergeSafetyScope string

const (
	mergeSafetyScopeSafe mergeSafetyScope = "safe"
	mergeSafetyScopeAll  mergeSafetyScope = "all"
)

// mergeSafetyCandidate is one task row in a MergeSafetyPopup: Safe/Reason
// mirror mergesafety.Verdict (this package deliberately doesn't import
// internal/mergesafety here — the popup is a pure display/choice widget, so
// callers convert their own Verdict values into this shape).
//
// Pending is true when this candidate has not actually been classified yet
// (fix-hera-reclaim-status) — it MUST NOT be confused with a confirmed
// NOT-SAFE verdict, even though Safe is also false for a pending candidate.
// Zero value is false, so every existing caller that always classifies
// before constructing a candidate (the single-role nuke, cascade nuke)
// unambiguously means "classified" without needing to set this explicitly —
// only the global Cleanup action's live poll (which can observe a task with
// no cached verdict yet) ever sets it true.
//
// Coordinator (5a-cleanup-tree-view) is the name of the Hera orchestrator
// this candidate's task most recently belonged to — empty when the task
// never held a Hera role at all. Empty is the common case for the
// single-role nuke site (its sole candidate is always the task being nuked
// right now, not a backlog reconstruction) and is also valid for the global
// Cleanup backlog's plain non-Hera candidates; either way it means "render
// this row flat, not nested under a group header" (see SetCandidates).
//
// Tier (add-coordinator-inferred-safety) mirrors mergesafety.Verdict.Tier
// verbatim — a plain string, not an import of internal/mergesafety (see this
// file's own doc comment on staying a pure display/choice widget). Only one
// value is ever compared against directly: mergeSafetyTierCoordinatorInferred,
// which drives a SAFE row's extra annotation (see drawRows). Empty for a
// Tier-A-only site (the single-role nuke) that never sets it.
type mergeSafetyCandidate struct {
	TaskID      string
	Name        string
	Safe        bool
	Reason      string // shown for NOT-SAFE and PENDING rows
	Pending     bool
	Coordinator string
	Tier        string
}

// mergeSafetyTierCoordinatorInferred mirrors mergesafety.TierCoordinatorInferred
// verbatim (add-coordinator-inferred-safety) — kept as a local string constant
// rather than an internal/mergesafety import, matching how Safe/Reason are
// already handled in this file.
const mergeSafetyTierCoordinatorInferred = "coordinator-inferred"

// mergeSafetyRowKind distinguishes a section header, a coordinator group
// header, and a candidate row in MergeSafetyPopup's flattened render list
// (mirrors switcherRowKind).
type mergeSafetyRowKind uint8

const (
	mergeSafetyRowHeader mergeSafetyRowKind = iota
	mergeSafetyRowItem
	mergeSafetyRowGroup
)

type mergeSafetyRow struct {
	kind    mergeSafetyRowKind
	text    string // header/group text
	cand    mergeSafetyCandidate
	grouped bool // item nested under a coordinator group header (deeper indent)
}

// MergeSafetyPopup is the "merge-safety review popup" (add-merge-safety-review):
// two sections — NOT-SAFE listed first, then SAFE — each candidate row showing
// its task name (NOT-SAFE rows also show the classification reason), and three
// actions: Clean safe (default-selected), Clean all, Cancel. It is used by
// exactly two entry points — the single-role nuke (candidate set of one) and
// the global Cleanup action (the full cross-project stuck-task backlog) — so it
// supports an optional "scanning" state (SetScanning) for the latter, whose
// classification runs on-demand after the popup is already open.
//
// The popup is a pure display/choice widget: it does not itself clean
// anything. Callers read Confirmed()+Scope() (or Canceled()) after the key
// that resolves the dialog and perform the actual action themselves — Clean
// safe acting only on the SAFE section is the CALLER's responsibility, since
// only the caller knows how to "clean" its own candidate set (a nuke call for
// the single-role site, a REST scope for the global Cleanup site).
type MergeSafetyPopup struct {
	*tview.Box
	title        string
	candidates   []mergeSafetyCandidate
	rows         []mergeSafetyRow
	pendingCount int // how many of candidates are still unclassified — drives the "X of Y classified" progress footer
	scrollOff    int
	actionIdx    int // 0 Clean safe (default), 1 Clean all, 2 Cancel
	confirmed    bool
	canceled     bool
	scanning     bool
}

// mergeSafetyActionLabels is the fixed 3-action set, in display order — index
// position IS the action's meaning (see actionIdx doc above).
var mergeSafetyActionLabels = []string{"Clean safe", "Clean all", "Cancel"}

// NewMergeSafetyPopup builds a review popup over the given candidates,
// defaulting to the "Clean safe" action selected (per spec).
func NewMergeSafetyPopup(title string, candidates []mergeSafetyCandidate) *MergeSafetyPopup {
	m := &MergeSafetyPopup{Box: tview.NewBox(), title: title}
	m.SetCandidates(candidates)
	return m
}

// SetCandidates (re)builds the popup's candidate list and its rendered rows:
// PENDING first (fix-hera-reclaim-status — candidates with no verdict yet,
// so the operator never mistakes "not yet checked" for a confirmed result),
// then NOT-SAFE, then SAFE — the spec's required order for the latter two.
// Within each of those three sections, candidates are further grouped by
// Coordinator (5a-cleanup-tree-view) via appendSectionRows — grouping is a
// sub-structure of the safety split, not a replacement for it. Called both
// at construction and, for the global Cleanup action, on every
// classification poll tick as fresh results arrive.
func (m *MergeSafetyPopup) SetCandidates(candidates []mergeSafetyCandidate) {
	m.candidates = candidates
	var pending, notSafe, safe []mergeSafetyCandidate
	for _, c := range candidates {
		switch {
		case c.Pending:
			pending = append(pending, c)
		case c.Safe:
			safe = append(safe, c)
		default:
			notSafe = append(notSafe, c)
		}
	}
	rows := make([]mergeSafetyRow, 0, len(candidates)+3)
	if len(pending) > 0 {
		rows = append(rows, mergeSafetyRow{kind: mergeSafetyRowHeader, text: fmt.Sprintf("PENDING (%d)", len(pending))})
		rows = appendSectionRows(rows, pending)
	}
	if len(notSafe) > 0 {
		rows = append(rows, mergeSafetyRow{kind: mergeSafetyRowHeader, text: fmt.Sprintf("NOT-SAFE (%d)", len(notSafe))})
		rows = appendSectionRows(rows, notSafe)
	}
	if len(safe) > 0 {
		rows = append(rows, mergeSafetyRow{kind: mergeSafetyRowHeader, text: fmt.Sprintf("SAFE (%d)", len(safe))})
		rows = appendSectionRows(rows, safe)
	}
	m.rows = rows
	m.pendingCount = len(pending)
	if maxOff := max(len(rows)-1, 0); m.scrollOff > maxOff {
		m.scrollOff = maxOff
	}
}

// appendSectionRows appends one safety section's candidate rows to rows:
// candidates with no Coordinator render flat (in their original relative
// order — never nested under a fabricated header), followed by one group
// header per distinct Coordinator (in order of first appearance within this
// section), each followed by every one of that coordinator's candidates in
// this section (in their original relative order, even if they weren't
// contiguous in the input — a tree groups ALL of a coordinator's children
// under its one header, not one header per contiguous run).
func appendSectionRows(rows []mergeSafetyRow, cands []mergeSafetyCandidate) []mergeSafetyRow {
	var order []string
	groups := make(map[string][]mergeSafetyCandidate)
	for _, c := range cands {
		if c.Coordinator == "" {
			rows = append(rows, mergeSafetyRow{kind: mergeSafetyRowItem, cand: c})
			continue
		}
		if _, seen := groups[c.Coordinator]; !seen {
			order = append(order, c.Coordinator)
		}
		groups[c.Coordinator] = append(groups[c.Coordinator], c)
	}
	for _, name := range order {
		rows = append(rows, mergeSafetyRow{kind: mergeSafetyRowGroup, text: name})
		for _, c := range groups[name] {
			rows = append(rows, mergeSafetyRow{kind: mergeSafetyRowItem, cand: c, grouped: true})
		}
	}
	return rows
}

// SetScanning toggles the "scanning…" wait state (global Cleanup only — the
// single-role site always computes before opening the popup at all, so it
// never needs this). See design.md's "compute-first" vs "on-demand poll"
// split between the two entry points.
func (m *MergeSafetyPopup) SetScanning(v bool) { m.scanning = v }

// Scanning reports the current wait-state flag (test seam: lets a poll-driven
// caller's test wait for a background classification pass to fully finish,
// rather than merely for the first partial candidate batch to arrive).
func (m *MergeSafetyPopup) Scanning() bool { return m.scanning }

// Candidates returns the full candidate list currently shown (both sections).
func (m *MergeSafetyPopup) Candidates() []mergeSafetyCandidate { return m.candidates }

// Confirmed reports whether the operator picked Clean safe/Clean all.
func (m *MergeSafetyPopup) Confirmed() bool { return m.confirmed }

// Canceled reports whether the operator dismissed the popup with no action.
func (m *MergeSafetyPopup) Canceled() bool { return m.canceled }

// Scope returns which section the confirmed Clean action applies to. Only
// meaningful when Confirmed() is true.
func (m *MergeSafetyPopup) Scope() mergeSafetyScope {
	if m.actionIdx == 1 {
		return mergeSafetyScopeAll
	}
	return mergeSafetyScopeSafe
}

// SelectedLabel returns the currently-highlighted action's label (test seam +
// potential future status-bar hint).
func (m *MergeSafetyPopup) SelectedLabel() string { return mergeSafetyActionLabels[m.actionIdx] }

// PasteHandler is a no-op — the popup carries no text input.
func (m *MergeSafetyPopup) PasteHandler() func(string, func(tview.Primitive)) {
	return m.WrapPasteHandler(func(string, func(tview.Primitive)) {})
}

// InputHandler handles key events: Left/Right (or Tab/Backtab) cycle the
// highlighted action, Up/Down scroll the candidate list, Enter resolves the
// dialog (Cancel action or Esc/Ctrl+Q cancels; the other two confirm).
func (m *MergeSafetyPopup) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.canceled = true
		case tcell.KeyEnter:
			if m.actionIdx == 2 {
				m.canceled = true
			} else {
				m.confirmed = true
			}
		case tcell.KeyLeft, tcell.KeyBacktab:
			m.actionIdx = (m.actionIdx + len(mergeSafetyActionLabels) - 1) % len(mergeSafetyActionLabels)
		case tcell.KeyRight, tcell.KeyTab:
			m.actionIdx = (m.actionIdx + 1) % len(mergeSafetyActionLabels)
		case tcell.KeyUp:
			if m.scrollOff > 0 {
				m.scrollOff--
			}
		case tcell.KeyDown:
			if maxOff := max(len(m.rows)-1, 0); m.scrollOff < maxOff {
				m.scrollOff++
			}
		case tcell.KeyPgUp:
			m.scrollOff = max(m.scrollOff-10, 0)
		case tcell.KeyPgDn:
			m.scrollOff = min(m.scrollOff+10, max(len(m.rows)-1, 0))
		}
	})
}

// Draw renders the popup centered, covering its full panel rect: title,
// scanning/empty/section-list body, the 3-action row, and a key hint footer.
func (m *MergeSafetyPopup) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	modalW := min(84, width-4)
	if modalW < 20 {
		modalW = width
	}
	// Chrome: top border, title, blank, [body], blank, actions, footer, bottom
	// border — 7 fixed rows plus whatever's left for the body.
	modalH := min(height, max(12, height-2))
	modalX := x + (width-modalW)/2
	modalY := max(y+(height-modalH)/2, y)

	for row := modalY; row < modalY+modalH && row < y+height; row++ {
		for col := modalX; col < modalX+modalW && col < x+width; col++ {
			screen.SetContent(col, row, ' ', nil, tcell.StyleDefault)
		}
	}
	widget.DrawBorder(screen, modalX, modalY, modalW, modalH, theme.StyleFocusedBorder)
	widget.DrawText(screen, modalX+2, modalY+1, modalW-4, m.title, theme.StyleTitle)

	innerX := modalX + 2
	innerW := max(modalW-4, 1)
	bodyY := modalY + 3
	bodyH := max(modalH-7, 1)

	switch {
	case len(m.rows) == 0 && m.scanning:
		widget.DrawText(screen, innerX, bodyY, innerW, "Scanning for stuck tasks…", theme.StyleDimmed)
	case len(m.rows) == 0:
		widget.DrawText(screen, innerX, bodyY, innerW, "No candidates.", theme.StyleDimmed)
	default:
		m.drawRows(screen, innerX, bodyY, innerW, bodyH)
	}

	actionsY := modalY + modalH - 3
	m.drawActions(screen, innerX, actionsY, innerW)

	footer := "←/→ choose action   ↑/↓ scroll   Enter confirm   Esc cancel"
	if m.scanning {
		prefix := "Scanning… (results update live)   "
		if total := len(m.candidates); total > 0 {
			prefix = fmt.Sprintf("Scanning… %d of %d classified (results update live)   ", total-m.pendingCount, total)
		}
		footer = prefix + footer
	}
	widget.DrawText(screen, innerX, modalY+modalH-2, innerW, footer, theme.StyleDimmed)
}

// drawRows renders the visible window of the flattened PENDING/NOT-SAFE/SAFE
// row list starting at m.scrollOff, clipped to h rows.
//
// Clamps (and self-heals) m.scrollOff to the largest offset that still lets
// the final screenful fill the window — mirroring SettingsView's own
// pane/log scroll clamps (settings.go), which key off the visible capacity
// rather than the row count. The clamp used to be len(m.rows)-1 (the last
// ROW index, not "rows-that-fit"), so scrolling to the bottom of a list
// taller than the window left only the single last row visible with the
// rest of the body blank, and Up needed several "dead" presses before the
// view visibly moved again — self-healing the field here (not just a local
// copy) means the very next Draw already shows the corrected position, so
// there's no dead zone in the running app, where a redraw follows every key.
func (m *MergeSafetyPopup) drawRows(screen tcell.Screen, x, y, w, h int) {
	if maxOff := max(len(m.rows)-h, 0); m.scrollOff > maxOff {
		m.scrollOff = maxOff
	}
	off := m.scrollOff
	visible := min(h, len(m.rows)-off)
	for i := 0; i < visible; i++ {
		r := m.rows[off+i]
		rowY := y + i
		switch r.kind {
		case mergeSafetyRowHeader:
			style := theme.StyleComplete.Bold(true)
			switch {
			case strings.HasPrefix(r.text, "NOT-SAFE"):
				style = theme.StyleError.Bold(true)
			case strings.HasPrefix(r.text, "PENDING"):
				style = theme.StyleDimmed.Bold(true)
			}
			widget.DrawText(screen, x, rowY, w, r.text, style)
			continue
		case mergeSafetyRowGroup:
			// Coordinator group header (5a-cleanup-tree-view): one indent level
			// in from the section header, matching where a flat item would sit —
			// the coordinator icon is what marks it as a group, mirroring the
			// native Hera rail's own orchestrator-header convention
			// (internal/tui/hera/rail.go's drawOrchRow: icon + name, no fixed
			// "coordinator"/"orchestrator" label word).
			text := string(theme.IconCoordinator) + " " + r.text
			textW := max(w-2, 1)
			if rw := utf8.RuneCountInString(text); rw > textW && textW > 1 {
				text = string([]rune(text)[:textW-1]) + "…"
			}
			widget.DrawText(screen, x+2, rowY, textW, text, theme.StyleCoordinator)
			continue
		}
		text := r.cand.Name
		switch {
		case !r.cand.Safe && r.cand.Reason != "":
			text = text + "  —  " + r.cand.Reason
		case r.cand.Safe && r.cand.Tier == mergeSafetyTierCoordinatorInferred:
			// add-coordinator-inferred-safety: the visibility condition this
			// tier's whole existence was contingent on — a coordinator-
			// inferred SAFE row must never look indistinguishable from a
			// directly-confirmed one. Every other SAFE row's rendering is
			// unaffected (Reason is deliberately never shown for those).
			text = text + "  (safe via coordinator)"
		}
		indent := 2
		if r.grouped {
			indent = 4 // nested one level deeper than a flat item, under its group header
		}
		textW := max(w-indent, 1)
		if rw := utf8.RuneCountInString(text); rw > textW && textW > 1 {
			text = string([]rune(text)[:textW-1]) + "…"
		}
		style := theme.StyleNormal
		if r.cand.Pending {
			style = theme.StyleDimmed
		}
		widget.DrawText(screen, x+indent, rowY, textW, text, style)
	}
}

// MouseHandler handles wheel-scroll over the popup body, advancing
// m.scrollOff the same one-row-per-notch amount the keyboard Up/Down
// bindings do. This widget's scrollOff is a content viewport pan, not a
// cursor position, so the scroll direction maps directly (mirrors
// SettingsView.HandleMouse's log-panel scroll — NOT Rail.MouseHandler's
// inverted cursor-drag convention, since there is no cursor here to drag).
// Previously absent entirely, which made the wheel a complete no-op over
// this popup (fix-hera-reclaim-status BUG 2).
func (m *MergeSafetyPopup) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return m.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
		if !m.InRect(event.Position()) {
			return false, nil
		}
		switch action {
		case tview.MouseScrollUp:
			if m.scrollOff > 0 {
				m.scrollOff--
			}
			consumed = true
		case tview.MouseScrollDown:
			if maxOff := max(len(m.rows)-1, 0); m.scrollOff < maxOff {
				m.scrollOff++
			}
			consumed = true
		}
		return
	})
}

// drawActions renders the 3-action row, highlighting the currently selected one.
func (m *MergeSafetyPopup) drawActions(screen tcell.Screen, x, y, w int) {
	col := x
	for i, label := range mergeSafetyActionLabels {
		style := theme.StyleNormal
		text := " " + label + " "
		if i == m.actionIdx {
			style = theme.StyleSelected
			text = "[" + label + "]"
		}
		remaining := max(w-(col-x), 0)
		widget.DrawText(screen, col, y, remaining, text, style)
		col += utf8.RuneCountInString(text) + 2
	}
}
