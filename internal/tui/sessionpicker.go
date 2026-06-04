package tui

import (
	"unicode/utf8"

	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// SessionPickerModal presents a filterable list of Claude conversation
// sessions for the current task, mirroring the `claude --resume` listing.
// Selecting one rebinds the agent to that session (see App.switchSession).
type SessionPickerModal struct {
	*tview.Box
	all       []claudesession.Session // full set, newest first
	filtered  []claudesession.Session // matches current query
	currentID string                  // the session the task is bound to now
	query     []rune
	qCursor   int
	cursor    int // position within filtered
	selected  bool
	canceled  bool
}

// NewSessionPickerModal creates a session picker over the given sessions.
// currentID marks the session the task is currently bound to so it can be
// flagged in the list (selecting it is a no-op handled by the caller).
func NewSessionPickerModal(sessions []claudesession.Session, currentID string) *SessionPickerModal {
	return &SessionPickerModal{
		Box:       tview.NewBox(),
		all:       sessions,
		filtered:  sessions,
		currentID: currentID,
	}
}

// Selected reports whether the user picked a session.
func (m *SessionPickerModal) Selected() bool { return m.selected }

// Canceled reports whether the user dismissed the modal.
func (m *SessionPickerModal) Canceled() bool { return m.canceled }

// SelectedSession returns the chosen session (zero value if none).
func (m *SessionPickerModal) SelectedSession() claudesession.Session {
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		return m.filtered[m.cursor]
	}
	return claudesession.Session{}
}

// PasteHandler handles bracketed paste into the filter field.
func (m *SessionPickerModal) PasteHandler() func(string, func(tview.Primitive)) {
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

// InputHandler handles key events for the session picker.
func (m *SessionPickerModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
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

// refilter updates the filtered list from the current query. Matching is
// fuzzy across title, branch, and session ID.
func (m *SessionPickerModal) refilter() {
	q := string(m.query)
	if q == "" {
		m.filtered = m.all
	} else {
		var matches []claudesession.Session
		for _, s := range m.all {
			if fuzzyMatch(q, s.Title) || fuzzyMatch(q, s.Branch) || fuzzyMatch(q, s.ID) {
				matches = append(matches, s)
			}
		}
		m.filtered = matches
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(len(m.filtered)-1, 0)
	}
}

// rowText renders one session as a single line: an optional current-marker,
// the title, then a dimmed metadata tail (time · branch · size · pr).
func rowText(s claudesession.Session, current bool) string {
	marker := "  "
	if current {
		marker = "● "
	}
	meta := claudesession.RelativeTime(s.ModTime)
	if s.Branch != "" {
		meta += " · " + s.Branch
	}
	meta += " · " + claudesession.HumanSize(s.SizeBytes)
	if s.PRRef != "" {
		meta += " · " + s.PRRef
	}
	return marker + s.Title + "  ·  " + meta
}

// Draw renders the session picker as a centered modal.
func (m *SessionPickerModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Compute modal width from the widest row.
	maxDisplayW := 30
	for _, s := range m.all {
		if w := utf8.RuneCountInString(rowText(s, s.ID == m.currentID)); w > maxDisplayW {
			maxDisplayW = w
		}
	}
	modalW := max(maxDisplayW+6, 44)
	modalW = min(modalW, width-4)
	innerW := modalW - 4

	// Height: border + title + filter + gap + items + gap + help + border.
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

	title := " Switch Claude session "
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	titleStyle := tcell.StyleDefault.Foreground(theme.ColorTitle).Bold(true)
	for i, r := range title {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2

	// Filter input row.
	filterY := my + 2
	widget.DrawText(screen, innerX, filterY, 2, "› ", theme.StyleFilter)
	before := string(m.query[:m.qCursor])
	after := string(m.query[m.qCursor:])
	fieldW := innerW - 2
	val := before + "█" + after
	if runes := []rune(val); len(runes) > fieldW {
		val = string(runes[len(runes)-fieldW:])
	}
	widget.DrawText(screen, innerX+2, filterY, fieldW, val, theme.StyleNormal)

	// Items.
	itemsY := my + 4
	if len(m.all) == 0 {
		widget.DrawText(screen, innerX, itemsY, innerW, "No Claude sessions found for this task.", theme.StyleDimmed)
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
			s := m.filtered[idx]
			display := rowText(s, s.ID == m.currentID)
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
	help := "↑/↓ select  Enter switch  Esc cancel"
	widget.DrawText(screen, innerX, helpRow, innerW, help, theme.StyleDimmed)
}
