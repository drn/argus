package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// OrchPickerModal presents a filterable list of Hera orchestrators for the `J`
// adopt/reparent flow. Selecting one adopts the freelancer (or re-parents the
// coordinator) under that orchestrator. It mirrors SessionPickerModal: a typed
// query narrows the list by case-insensitive substring on the orchestrator
// name, Enter selects the highlighted row, Esc cancels.
type OrchPickerModal struct {
	*tview.Box
	title    string
	all      []*db.HeraOrchestrator // full set, listing order
	filtered []*db.HeraOrchestrator // matches current query
	query    []rune
	qCursor  int
	cursor   int // position within filtered
	selected bool
	canceled bool
}

// NewOrchPickerModal creates a picker over the given orchestrators. title names
// the row being adopted (e.g. `Adopt "foo" into…`).
func NewOrchPickerModal(title string, orchs []*db.HeraOrchestrator) *OrchPickerModal {
	return &OrchPickerModal{
		Box:      tview.NewBox(),
		title:    title,
		all:      orchs,
		filtered: orchs,
	}
}

// Selected reports whether the user picked an orchestrator.
func (m *OrchPickerModal) Selected() bool { return m.selected }

// Canceled reports whether the user dismissed the modal.
func (m *OrchPickerModal) Canceled() bool { return m.canceled }

// SelectedOrch returns the chosen orchestrator (nil if none).
func (m *OrchPickerModal) SelectedOrch() *db.HeraOrchestrator {
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		return m.filtered[m.cursor]
	}
	return nil
}

// PasteHandler handles bracketed paste into the filter field.
func (m *OrchPickerModal) PasteHandler() func(string, func(tview.Primitive)) {
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

// InputHandler handles key events for the orchestrator picker.
func (m *OrchPickerModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.canceled = true
		case tcell.KeyEnter:
			if len(m.filtered) > 0 {
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
			if m.qCursor > 0 {
				m.qCursor--
			}
		case tcell.KeyRight:
			if m.qCursor < len(m.query) {
				m.qCursor++
			}
		case tcell.KeyHome, tcell.KeyCtrlA:
			m.qCursor = 0
		case tcell.KeyEnd, tcell.KeyCtrlE:
			m.qCursor = len(m.query)
		case tcell.KeyRune:
			r := event.Rune()
			m.query = append(m.query[:m.qCursor], append([]rune{r}, m.query[m.qCursor:]...)...)
			m.qCursor++
			m.refilter()
		}
	})
}

// refilter updates the filtered list from the current query (case-insensitive
// substring on the orchestrator name).
func (m *OrchPickerModal) refilter() {
	q := strings.ToLower(strings.TrimSpace(string(m.query)))
	if q == "" {
		m.filtered = m.all
	} else {
		var matches []*db.HeraOrchestrator
		for _, o := range m.all {
			if strings.Contains(strings.ToLower(o.Name), q) {
				matches = append(matches, o)
			}
		}
		m.filtered = matches
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(len(m.filtered)-1, 0)
	}
}

// Draw renders the picker as a centered modal (mirrors SessionPickerModal).
func (m *OrchPickerModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	maxDisplayW := utf8.RuneCountInString(m.title)
	for _, o := range m.all {
		if w := utf8.RuneCountInString(o.Name); w > maxDisplayW {
			maxDisplayW = w
		}
	}
	modalW := max(maxDisplayW+6, 40)
	modalW = min(modalW, width-4)
	// A very narrow terminal can drive innerW negative; clamp the inner width to
	// ≥ 1 so the truncation slices below never underflow into a panic (shared
	// guard with the other narrow-terminal pickers).
	innerW := max(modalW-4, 1)

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

	title := " " + m.title + " "
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	if titleX < mx+1 {
		titleX = mx + 1
	}
	titleStyle := tcell.StyleDefault.Foreground(theme.ColorTitle).Bold(true)
	widget.DrawText(screen, titleX, my, modalW-2, title, titleStyle)

	innerX := mx + 2

	// Filter input row.
	filterY := my + 2
	widget.DrawText(screen, innerX, filterY, 2, "› ", theme.StyleFilter)
	before := string(m.query[:m.qCursor])
	after := string(m.query[m.qCursor:])
	fieldW := max(innerW-2, 1)
	val := before + "█" + after
	if runes := []rune(val); len(runes) > fieldW {
		val = string(runes[len(runes)-fieldW:])
	}
	widget.DrawText(screen, innerX+2, filterY, fieldW, val, theme.StyleNormal)

	// Items.
	itemsY := my + 4
	if len(m.all) == 0 {
		widget.DrawText(screen, innerX, itemsY, innerW, "No active coordinators.", theme.StyleDimmed)
	} else if len(m.filtered) == 0 {
		widget.DrawText(screen, innerX, itemsY, innerW, "No matches", theme.StyleDimmed)
	} else {
		offset := 0
		if m.cursor >= maxItems {
			offset = m.cursor - maxItems + 1
		}
		maxVisible := min(maxItems, len(m.filtered))
		for i := 0; i < maxVisible; i++ {
			idx := offset + i
			if idx >= len(m.filtered) {
				break
			}
			display := m.filtered[idx].Name
			if utf8.RuneCountInString(display) > innerW && innerW > 3 {
				display = string([]rune(display)[:innerW-1]) + "…"
			}
			style := theme.StyleNormal
			if idx == m.cursor {
				style = theme.StyleSelected
			}
			widget.DrawText(screen, innerX, itemsY+i, innerW, display, style)
		}
	}

	helpRow := my + modalH - 2
	widget.DrawText(screen, innerX, helpRow, innerW, "↑/↓ select  Enter choose  Esc cancel", theme.StyleDimmed)
}
