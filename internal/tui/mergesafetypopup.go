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
type mergeSafetyCandidate struct {
	TaskID string
	Name   string
	Safe   bool
	Reason string // shown for NOT-SAFE rows only
}

// mergeSafetyRowKind distinguishes a section header from a candidate row in
// MergeSafetyPopup's flattened render list (mirrors switcherRowKind).
type mergeSafetyRowKind uint8

const (
	mergeSafetyRowHeader mergeSafetyRowKind = iota
	mergeSafetyRowItem
)

type mergeSafetyRow struct {
	kind mergeSafetyRowKind
	text string // header text
	cand mergeSafetyCandidate
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
	title      string
	candidates []mergeSafetyCandidate
	rows       []mergeSafetyRow
	scrollOff  int
	actionIdx  int // 0 Clean safe (default), 1 Clean all, 2 Cancel
	confirmed  bool
	canceled   bool
	scanning   bool
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

// SetCandidates (re)builds the popup's candidate list and its rendered rows,
// NOT-SAFE section first, then SAFE — the spec's required section order.
// Called both at construction and, for the global Cleanup action, on every
// classification poll tick as fresh results arrive.
func (m *MergeSafetyPopup) SetCandidates(candidates []mergeSafetyCandidate) {
	m.candidates = candidates
	var notSafe, safe []mergeSafetyCandidate
	for _, c := range candidates {
		if c.Safe {
			safe = append(safe, c)
		} else {
			notSafe = append(notSafe, c)
		}
	}
	rows := make([]mergeSafetyRow, 0, len(candidates)+2)
	if len(notSafe) > 0 {
		rows = append(rows, mergeSafetyRow{kind: mergeSafetyRowHeader, text: fmt.Sprintf("NOT-SAFE (%d)", len(notSafe))})
		for _, c := range notSafe {
			rows = append(rows, mergeSafetyRow{kind: mergeSafetyRowItem, cand: c})
		}
	}
	if len(safe) > 0 {
		rows = append(rows, mergeSafetyRow{kind: mergeSafetyRowHeader, text: fmt.Sprintf("SAFE (%d)", len(safe))})
		for _, c := range safe {
			rows = append(rows, mergeSafetyRow{kind: mergeSafetyRowItem, cand: c})
		}
	}
	m.rows = rows
	if maxOff := max(len(rows)-1, 0); m.scrollOff > maxOff {
		m.scrollOff = maxOff
	}
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
		footer = "Scanning… (results update live)   " + footer
	}
	widget.DrawText(screen, innerX, modalY+modalH-2, innerW, footer, theme.StyleDimmed)
}

// drawRows renders the visible window of the flattened NOT-SAFE/SAFE row list
// starting at m.scrollOff, clipped to h rows.
func (m *MergeSafetyPopup) drawRows(screen tcell.Screen, x, y, w, h int) {
	off := min(m.scrollOff, max(len(m.rows)-1, 0))
	visible := min(h, len(m.rows)-off)
	for i := 0; i < visible; i++ {
		r := m.rows[off+i]
		rowY := y + i
		if r.kind == mergeSafetyRowHeader {
			style := theme.StyleComplete.Bold(true)
			if strings.HasPrefix(r.text, "NOT-SAFE") {
				style = theme.StyleError.Bold(true)
			}
			widget.DrawText(screen, x, rowY, w, r.text, style)
			continue
		}
		text := r.cand.Name
		if !r.cand.Safe && r.cand.Reason != "" {
			text = text + "  —  " + r.cand.Reason
		}
		textW := max(w-2, 1)
		if rw := utf8.RuneCountInString(text); rw > textW && textW > 1 {
			text = string([]rune(text)[:textW-1]) + "…"
		}
		widget.DrawText(screen, x+2, rowY, textW, text, theme.StyleNormal)
	}
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
