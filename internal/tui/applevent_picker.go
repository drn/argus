package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/macapps"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// aeRow is one displayed picker entry. Custom-entry synthetic rows have
// IsCustom=true and an empty App (only BundleID is meaningful).
type aeRow struct {
	App      macapps.App
	IsCustom bool // true for the synthetic "Add custom: <id>" row
	BundleID string
}

// AppleEventsPickerModal is a multi-select picker over a list of
// scriptable macOS apps. Used to populate a project's
// AllowAppleEvents SBPL allowlist from the Settings TUI.
//
// Features:
//   - Filter input at top; live filtering of the list as the user types.
//   - Space toggles selection on the cursor row; selections persist across
//     filter changes (you can search, toggle, clear filter, search again).
//   - When the filter text matches no scanned app but is itself a valid
//     CFBundleIdentifier (e.g. "com.apple.iChat" — the Messages.app legacy
//     AppleEvent target that no .app bundle has on disk), the picker
//     surfaces it as a synthetic "Add custom" row that can be toggled too.
//   - Pre-populates selections from the project's existing AllowAppleEvents
//     so re-opening the modal shows current state.
type AppleEventsPickerModal struct {
	*tview.Box

	projectName string

	// All scriptable apps known to the system, sorted by name. Set once
	// on construction; rebuilt only on Refresh().
	apps []macapps.App

	// filter is the current filter text. Drives which rows render.
	filter []rune

	// selected is the set of currently-selected bundle IDs (across both
	// scanned apps and custom entries the user has added during this
	// session). Lookup by BundleID — survives filter changes.
	selected map[string]struct{}

	// rows is the materialized list shown on screen for the current filter.
	// Rebuilt by rebuildRows() whenever the filter or apps change.
	rows []aeRow

	cursor int

	// scrollOff is the index of the first row drawn (offset into rows[]).
	// Updated in Draw to keep cursor visible.
	scrollOff int

	done     bool
	canceled bool
}

// NewAppleEventsPickerModal builds the picker. projectName is shown in the
// title. apps is the list of scriptable apps the system reports (typically
// from macapps.ScanScriptable). preselected is the bundle IDs that should
// start in the selected set — usually the project's existing
// AllowAppleEvents, so re-opening the modal shows current state. The order
// of preselected is preserved on Result() so a stable round-trip is
// possible even when no edits are made.
func NewAppleEventsPickerModal(projectName string, apps []macapps.App, preselected []string) *AppleEventsPickerModal {
	m := &AppleEventsPickerModal{
		Box:         tview.NewBox(),
		projectName: projectName,
		apps:        apps,
		selected:    make(map[string]struct{}, len(preselected)),
	}
	for _, id := range preselected {
		id = strings.TrimSpace(id)
		if id != "" {
			m.selected[id] = struct{}{}
		}
	}
	m.rebuildRows()
	return m
}

// Done reports whether the user confirmed the modal (Enter).
func (m *AppleEventsPickerModal) Done() bool { return m.done }

// Canceled reports whether the user dismissed the modal (Esc / Ctrl+Q).
func (m *AppleEventsPickerModal) Canceled() bool { return m.canceled }

// Result returns the selected bundle IDs in a stable order. Sorted
// alphabetically so the persisted CSV is deterministic across saves —
// callers that compare-before-write to avoid a no-op DB hit can rely on
// equal slices for equal selections.
func (m *AppleEventsPickerModal) Result() []string {
	out := make([]string, 0, len(m.selected))
	for id := range m.selected {
		out = append(out, id)
	}
	// Sort for stable output. Avoids gratuitous DB writes when nothing
	// changed but the user re-opened and confirmed.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// SetApps replaces the scriptable-app list (used when the caller refreshes
// the macapps cache). Preserves selections and current filter; resets cursor
// to top of the new filtered list.
func (m *AppleEventsPickerModal) SetApps(apps []macapps.App) {
	m.apps = apps
	m.cursor = 0
	m.scrollOff = 0
	m.rebuildRows()
}

// PasteHandler accepts pasted text into the filter field. tview's bracketed
// paste bypasses InputHandler, so any focused widget that takes text must
// implement PasteHandler explicitly.
func (m *AppleEventsPickerModal) PasteHandler() func(string, func(tview.Primitive)) {
	return m.WrapPasteHandler(func(pasted string, _ func(tview.Primitive)) {
		// Keep filter input single-line — drop control chars and embedded
		// newlines so a multi-line paste doesn't corrupt the row layout.
		for _, r := range pasted {
			if r == '\n' || r == '\r' {
				continue
			}
			m.filter = append(m.filter, r)
		}
		m.cursor = 0
		m.scrollOff = 0
		m.rebuildRows()
	})
}

// InputHandler processes key events for the picker.
func (m *AppleEventsPickerModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.canceled = true
			return
		case tcell.KeyEnter:
			m.done = true
			return
		case tcell.KeyUp:
			m.moveCursor(-1)
			return
		case tcell.KeyDown:
			m.moveCursor(1)
			return
		case tcell.KeyPgUp:
			m.moveCursor(-pageStep)
			return
		case tcell.KeyPgDn:
			m.moveCursor(pageStep)
			return
		case tcell.KeyHome:
			m.cursor = 0
			m.scrollOff = 0
			return
		case tcell.KeyEnd:
			if len(m.rows) > 0 {
				m.cursor = len(m.rows) - 1
			}
			return
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
				m.cursor = 0
				m.scrollOff = 0
				m.rebuildRows()
			}
			return
		case tcell.KeyRune:
			r := event.Rune()
			if r == ' ' {
				m.toggleCursor()
				return
			}
			// Printable characters extend the filter. Use IsValidBundleID's
			// charset PLUS letters/digits/dot/hyphen + space PLUS underscore
			// so users searching for app names can type freely — the filter
			// is a substring match, not a bundle-ID parse.
			if r >= ' ' && r < 0x7f {
				m.filter = append(m.filter, r)
				m.cursor = 0
				m.scrollOff = 0
				m.rebuildRows()
			}
			return
		}
	})
}

// pageStep is the row delta for PgUp/PgDn. Conservative — half a typical
// modal height — so the cursor doesn't jump past the visible window.
const pageStep = 8

// moveCursor adjusts m.cursor by delta, clamping to [0, len(rows)-1].
func (m *AppleEventsPickerModal) moveCursor(delta int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(m.rows)-1 {
		m.cursor = len(m.rows) - 1
	}
}

// toggleCursor flips the selected state of whichever row the cursor is on.
// Custom-entry rows behave the same — adding/removing the typed bundle ID
// from the selected set.
func (m *AppleEventsPickerModal) toggleCursor() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	id := m.rows[m.cursor].BundleID
	if id == "" {
		return
	}
	if _, ok := m.selected[id]; ok {
		delete(m.selected, id)
	} else {
		m.selected[id] = struct{}{}
	}
}

// rebuildRows produces the displayed list for the current filter. Rules:
//   - Empty filter → every scanned app, in name order.
//   - Non-empty filter → apps whose name or bundle ID contains the filter
//     (case-insensitive).
//   - Plus: any currently-selected bundle ID not present in the filtered
//     scan results (so the user can still see + toggle their selections
//     even when the filter would otherwise hide them).
//   - Plus: if the trimmed filter is a syntactically valid bundle ID AND
//     matches no scanned app exactly AND is not already in selected, append
//     a synthetic "Add custom" row at the bottom so the user can add the
//     legacy alias (com.apple.iChat is the canonical example).
func (m *AppleEventsPickerModal) rebuildRows() {
	query := strings.TrimSpace(string(m.filter))
	filtered := macapps.FilterByText(m.apps, query)

	rows := make([]aeRow, 0, len(filtered)+len(m.selected))
	seen := make(map[string]struct{}, len(filtered))
	for _, a := range filtered {
		rows = append(rows, aeRow{App: a, BundleID: a.BundleID})
		seen[a.BundleID] = struct{}{}
	}

	// Surface selected-but-filtered-out entries so the user can deselect
	// them without first clearing the filter. They render as custom-style
	// rows when the scanner doesn't know about them (e.g., com.apple.iChat).
	for id := range m.selected {
		if _, ok := seen[id]; ok {
			continue
		}
		// Look up an App entry from the master list for nicer display when
		// the filter merely excluded a known app rather than the app being
		// unknown.
		var app macapps.App
		isCustom := true
		for _, a := range m.apps {
			if a.BundleID == id {
				app = a
				isCustom = false
				break
			}
		}
		rows = append(rows, aeRow{App: app, IsCustom: isCustom, BundleID: id})
		seen[id] = struct{}{}
	}

	// Custom-entry suggestion. Only when the filter LOOKS like a bundle ID:
	// valid charset AND contains at least one dot. Apple's bundle IDs are
	// always dotted (com.<vendor>.<app>), so requiring a dot blocks "mess"
	// and "music" from triggering a useless "Add custom: mess" row while
	// still surfacing real legacy aliases like "com.apple.iChat".
	if query != "" && strings.Contains(query, ".") && macapps.IsValidBundleID(query) {
		if _, already := seen[query]; !already {
			rows = append(rows, aeRow{IsCustom: true, BundleID: query})
		}
	}

	m.rows = rows
	if m.cursor >= len(rows) {
		m.cursor = max(len(rows)-1, 0)
	}
}

// Draw renders the picker as a centered modal. Layout:
//
//	┌─ AppleEvents Allowlist — <project> ─────────┐
//	│ Filter: <typed text>▎                       │
//	│                                              │
//	│ [x] Messages           com.apple.MobileSMS  │
//	│ [ ] Add custom         com.apple.iChat      │
//	│ ...                                          │
//	│                                              │
//	│ ↑/↓ space toggle  enter save  esc cancel    │
//	└──────────────────────────────────────────────┘
func (m *AppleEventsPickerModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Modal sizing.
	modalW := min(max(width-4, 40), 100)
	modalH := min(height-2, 24)
	if modalH < 8 {
		// Too small — bail rather than draw a degenerate frame.
		return
	}
	mx := x + (width-modalW)/2
	my := y + (height-modalH)/2

	// Clear modal area so cells from underneath don't bleed through.
	clearStyle := tcell.StyleDefault.Background(tcell.ColorDefault)
	for row := my; row < my+modalH; row++ {
		for col := mx; col < mx+modalW; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}

	widget.DrawBorder(screen, mx, my, modalW, modalH, theme.StyleFocusedBorder)

	// Title.
	title := " AppleEvents Allowlist — " + m.projectName + " "
	if utf8.RuneCountInString(title) > modalW-4 {
		title = " AppleEvents Allowlist "
	}
	titleStyle := tcell.StyleDefault.Foreground(theme.ColorTitle).Bold(true)
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	for i, r := range title {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2
	innerW := modalW - 4

	// Filter input row.
	filterLabel := "Filter: "
	filterRow := my + 1
	widget.DrawText(screen, innerX, filterRow, len(filterLabel), filterLabel, theme.StyleDimmed)
	filterText := string(m.filter) + "▎"
	widget.DrawText(screen, innerX+len(filterLabel), filterRow, innerW-len(filterLabel), filterText, theme.StyleNormal)

	// List area. Top of list is one row below filter (with a blank separator).
	listTop := my + 3
	helpRow := my + modalH - 2
	listH := helpRow - listTop - 1
	if listH < 1 {
		listH = 1
	}

	// Adjust scroll offset to keep cursor visible.
	if m.cursor < m.scrollOff {
		m.scrollOff = m.cursor
	}
	if m.cursor >= m.scrollOff+listH {
		m.scrollOff = m.cursor - listH + 1
	}
	if m.scrollOff < 0 {
		m.scrollOff = 0
	}

	// Bundle-ID column starts at a fixed offset so names + IDs align.
	// Reserve ~half the inner width for the name column.
	nameColW := max(innerW/2, 16)
	for i := 0; i < listH; i++ {
		idx := m.scrollOff + i
		if idx >= len(m.rows) {
			break
		}
		row := m.rows[idx]
		y := listTop + i

		checkbox := "[ ] "
		if _, ok := m.selected[row.BundleID]; ok {
			checkbox = "[x] "
		}

		name := row.App.Name
		if row.IsCustom {
			name = "Add custom"
		}
		if name == "" {
			name = "(unknown)"
		}

		style := theme.StyleNormal
		if idx == m.cursor {
			style = theme.StyleSelected
		}
		if row.IsCustom && idx != m.cursor {
			style = theme.StyleDimmed
		}

		// Truncate name to nameColW.
		nameDisplay := truncRunes(name, nameColW-len(checkbox)-1)

		widget.DrawText(screen, innerX, y, nameColW, checkbox+nameDisplay, style)
		// Bundle ID column.
		widget.DrawText(screen, innerX+nameColW+1, y, innerW-nameColW-1, row.BundleID, style)
	}

	// Empty-state hint.
	if len(m.rows) == 0 {
		hint := "no matching apps — type a valid bundle ID to add it"
		widget.DrawText(screen, innerX, listTop, innerW, hint, theme.StyleDimmed)
	}

	// Footer help.
	help := "↑/↓ navigate  space toggle  enter save  esc cancel"
	widget.DrawText(screen, innerX, helpRow, innerW, help, theme.StyleDimmed)
}
