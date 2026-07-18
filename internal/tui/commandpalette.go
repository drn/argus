package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// paletteRow is one command-palette entry: a human label, its resolved key
// chord (display only), and the func that performs its effect when invoked.
// invoke is never nil — it is a thin wrapper around the same method the
// action's bound key already calls (the App's per-context action registry,
// or one of Hera's two enumerated non-keymap literal actions), so invoking a
// row never duplicates logic.
type paletteRow struct {
	Label  string
	Key    string
	invoke func()
}

// CommandPaletteModal is a searchable, filterable list of the keymap actions
// applicable to whatever context/focus region was active when it opened
// (ctrl+k) — type to filter by label substring, arrow keys move the cursor,
// Enter invokes the selected row's action immediately. Structurally mirrors
// TaskSwitcherModal's filter/cursor mechanics (flat mode); a new type because
// the row shape (label + resolved-hotkey column) differs from a task/role
// row, not because the interaction model differs.
type CommandPaletteModal struct {
	*tview.Box
	all      []paletteRow
	filtered []paletteRow
	query    []rune
	qCursor  int
	cursor   int
	selected bool
	canceled bool
}

// NewCommandPaletteModal creates a palette over the given rows. rows SHOULD
// already be in the caller's preferred display order (contextOrder-derived) —
// the modal does not re-sort, only filters.
func NewCommandPaletteModal(rows []paletteRow) *CommandPaletteModal {
	return &CommandPaletteModal{
		Box:      tview.NewBox(),
		all:      rows,
		filtered: rows,
	}
}

// Selected reports whether the user picked a row.
func (m *CommandPaletteModal) Selected() bool { return m.selected }

// Canceled reports whether the user dismissed the modal.
func (m *CommandPaletteModal) Canceled() bool { return m.canceled }

// Invoke calls the currently-selected row's action. No-op if the cursor isn't
// parked on a row (e.g. the filter matched nothing).
func (m *CommandPaletteModal) Invoke() {
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		m.filtered[m.cursor].invoke()
	}
}

// PasteHandler handles bracketed paste into the filter field.
func (m *CommandPaletteModal) PasteHandler() func(string, func(tview.Primitive)) {
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

// InputHandler handles key events for the command palette.
func (m *CommandPaletteModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.canceled = true
		case tcell.KeyEnter:
			if m.cursor >= 0 && m.cursor < len(m.filtered) {
				m.selected = true
			}
		case tcell.KeyUp:
			if m.cursor > 0 {
				m.cursor--
			}
		case tcell.KeyDown:
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
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

// refilter narrows the row list by case-insensitive substring match against
// the label, preserving the caller's order (contextOrder-derived, never
// re-sorted here).
func (m *CommandPaletteModal) refilter() {
	q := strings.ToLower(string(m.query))
	if q == "" {
		m.filtered = m.all
	} else {
		var matches []paletteRow
		for _, r := range m.all {
			if strings.Contains(strings.ToLower(r.Label), q) {
				matches = append(matches, r)
			}
		}
		m.filtered = matches
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(len(m.filtered)-1, 0)
	}
}

// Draw renders the palette: a bordered box with a filter input row and the
// (possibly scrolled) row list below, label left-aligned and the resolved key
// chord right-aligned. No screen.Sync() — full bounding-rect coverage via the
// blanking loop, matching every other modal in this package.
func (m *CommandPaletteModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	maxDisplayW := 30
	for _, r := range m.all {
		if w := utf8.RuneCountInString(r.Label) + utf8.RuneCountInString(r.Key) + 4; w > maxDisplayW {
			maxDisplayW = w
		}
	}
	modalW := max(maxDisplayW+6, 44)
	modalW = min(modalW, width-4)
	innerW := modalW - 4

	maxItems := max(min(len(m.all), height-8), 1)
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

	title := " Command palette "
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	titleStyle := tcell.StyleDefault.Foreground(theme.ColorTitle).Bold(true)
	for i, r := range []rune(title) {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2

	filterY := my + 2
	widget.DrawText(screen, innerX, filterY, 2, "› ", theme.StyleFilter)
	before := string(m.query[:m.qCursor])
	after := string(m.query[m.qCursor:])
	fieldW := innerW - 2
	fieldW = max(fieldW, 1)
	val := before + "█" + after
	if runes := []rune(val); len(runes) > fieldW {
		val = string(runes[len(runes)-fieldW:])
	}
	widget.DrawText(screen, innerX+2, filterY, fieldW, val, theme.StyleNormal)

	itemsY := my + 4
	m.drawItems(screen, innerX, itemsY, innerW, maxItems)

	help := "↑/↓ select  Enter run  Esc cancel"
	helpRow := my + modalH - 2
	widget.DrawText(screen, innerX, helpRow, innerW, help, theme.StyleDimmed)
}

func (m *CommandPaletteModal) drawItems(screen tcell.Screen, innerX, itemsY, innerW, maxItems int) {
	if len(m.all) == 0 {
		widget.DrawText(screen, innerX, itemsY, innerW, "No actions available here.", theme.StyleDimmed)
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
		r := m.filtered[idx]
		rowY := itemsY + i
		style := theme.StyleNormal
		if idx == m.cursor {
			style = theme.StyleSelected
		}
		keyW := utf8.RuneCountInString(r.Key)
		labelW := innerW - keyW - 1
		label := r.Label
		if utf8.RuneCountInString(label) > labelW && labelW > 3 {
			label = string([]rune(label)[:labelW-1]) + "…"
		}
		widget.DrawText(screen, innerX, rowY, labelW, label, style)
		widget.DrawText(screen, innerX+innerW-keyW, rowY, keyW, r.Key, theme.StyleDimmed)
	}
}
