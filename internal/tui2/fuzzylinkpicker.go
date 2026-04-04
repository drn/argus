package tui2

import (
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// FuzzyLinkPickerModal presents a filterable list of links with fuzzy matching.
type FuzzyLinkPickerModal struct {
	*tview.Box
	allLinks []Link // full set
	filtered []Link // matches current query
	query    []rune
	qCursor  int
	cursor   int // position within filtered
	selected bool
	canceled bool
}

// NewFuzzyLinkPickerModal creates a fuzzy link picker dialog.
func NewFuzzyLinkPickerModal(links []Link) *FuzzyLinkPickerModal {
	m := &FuzzyLinkPickerModal{
		Box:      tview.NewBox(),
		allLinks: links,
		filtered: links,
	}
	return m
}

// Selected returns true if the user picked a link.
func (m *FuzzyLinkPickerModal) Selected() bool { return m.selected }

// Canceled returns true if the user dismissed the modal.
func (m *FuzzyLinkPickerModal) Canceled() bool { return m.canceled }

// SelectedLink returns the chosen link.
func (m *FuzzyLinkPickerModal) SelectedLink() Link {
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		return m.filtered[m.cursor]
	}
	return Link{}
}

// PasteHandler handles bracketed paste events.
func (m *FuzzyLinkPickerModal) PasteHandler() func(string, func(tview.Primitive)) {
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

// InputHandler handles key events for the fuzzy link picker.
func (m *FuzzyLinkPickerModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
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
				m.query, m.qCursor = deleteWordLeft(m.query, m.qCursor)
			} else if m.qCursor > 0 {
				m.query = append(m.query[:m.qCursor-1], m.query[m.qCursor:]...)
				m.qCursor--
			}
			m.refilter()
		case tcell.KeyCtrlW:
			m.query, m.qCursor = deleteWordLeft(m.query, m.qCursor)
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

// refilter updates the filtered list based on the current query.
func (m *FuzzyLinkPickerModal) refilter() {
	q := string(m.query)
	if q == "" {
		m.filtered = m.allLinks
	} else {
		var matches []Link
		for _, l := range m.allLinks {
			if fuzzyMatch(q, l.URL) || fuzzyMatch(q, l.Label) {
				matches = append(matches, l)
			}
		}
		m.filtered = matches
	}
	// Clamp cursor. When filtered is empty, cursor stays at 0;
	// SelectedLink() bounds-checks before returning.
	if m.cursor >= len(m.filtered) {
		m.cursor = max(len(m.filtered)-1, 0)
	}
}

// fuzzyMatch returns true if all characters in pattern appear in str in order
// (case-insensitive).
func fuzzyMatch(pattern, str string) bool {
	pattern = strings.ToLower(pattern)
	str = strings.ToLower(str)
	pRunes := []rune(pattern)
	pi := 0
	for _, r := range str {
		if pi < len(pRunes) && r == pRunes[pi] {
			pi++
		}
	}
	return pi == len(pRunes)
}

// Draw renders the fuzzy link picker as a centered modal.
func (m *FuzzyLinkPickerModal) Draw(screen tcell.Screen) {
	m.Box.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Compute modal dimensions from the full display string.
	maxDisplayW := 20
	for _, l := range m.allLinks {
		w := utf8.RuneCountInString(l.URL)
		if l.Label != "" && l.Label != l.URL {
			w += utf8.RuneCountInString(l.Label) + 2 // "label  url"
		}
		if w > maxDisplayW {
			maxDisplayW = w
		}
	}

	// modal width: label + padding + border
	modalW := max(maxDisplayW+6, 40)
	modalW = min(modalW, width-4)
	innerW := modalW - 4

	// Height: border(1) + title(1) + filter(1) + gap(1) + items + gap(1) + help(1) + border(1)
	maxItems := max(min(len(m.allLinks), height-8), 1)
	modalH := maxItems + 7
	if modalH > height {
		modalH = height
		maxItems = modalH - 7
		if maxItems < 1 {
			maxItems = 1
		}
	}

	mx := x + (width-modalW)/2
	my := y + (height-modalH)/2

	// Clear modal area
	clearStyle := tcell.StyleDefault.Background(tcell.ColorDefault)
	for row := my; row < my+modalH; row++ {
		for col := mx; col < mx+modalW; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}

	drawBorder(screen, mx, my, modalW, modalH, StyleFocusedBorder)

	// Title
	title := " Open Link "
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	titleStyle := tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
	for i, r := range title {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2

	// Filter input row
	filterY := my + 2
	filterLabel := "› "
	drawText(screen, innerX, filterY, 2, filterLabel, StyleFilter)
	// Query text with cursor
	before := string(m.query[:m.qCursor])
	after := string(m.query[m.qCursor:])
	fieldW := innerW - 2
	val := before + "█" + after
	if utf8.RuneCountInString(val) > fieldW {
		// Scroll to keep cursor visible
		runes := []rune(val)
		if len(runes) > fieldW {
			val = string(runes[len(runes)-fieldW:])
		}
	}
	drawText(screen, innerX+2, filterY, fieldW, val, StyleNormal)

	// Items
	itemsY := my + 4
	maxVisible := min(maxItems, len(m.filtered))

	if len(m.filtered) == 0 && len(m.query) > 0 {
		drawText(screen, innerX, itemsY, innerW, "No matches", StyleDimmed)
	} else {
		// Scrolling offset
		offset := 0
		if m.cursor >= maxItems {
			offset = m.cursor - maxItems + 1
		}

		for i := 0; i < maxVisible; i++ {
			idx := offset + i
			if idx >= len(m.filtered) {
				break
			}
			link := m.filtered[idx]
			isCursor := idx == m.cursor

			// Show URL, with label prefix if different
			display := link.URL
			if link.Label != "" && link.Label != link.URL {
				display = link.Label + "  " + link.URL
			}

			if utf8.RuneCountInString(display) > innerW {
				runes := []rune(display)
				if innerW > 3 {
					display = string(runes[:innerW-1]) + "…"
				}
			}

			style := StyleNormal
			if isCursor {
				style = StyleSelected
			}
			drawText(screen, innerX, itemsY+i, innerW, display, style)
		}
	}

	// Help text
	helpRow := my + modalH - 2
	help := "↑/↓ select  Enter open  Esc cancel"
	drawText(screen, innerX, helpRow, innerW, help, StyleDimmed)
}
