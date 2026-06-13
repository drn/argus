package tui

import (
	"unicode/utf8"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// taskSwitcherEntry is one row in the task switcher: a task the user can jump
// to from the agent view. NeedsInput flags tasks blocked on a user prompt so
// they can be surfaced (and visually marked) at the top of the list.
type taskSwitcherEntry struct {
	ID         string
	Name       string
	Project    string
	Status     model.Status
	NeedsInput bool
}

// TaskSwitcherModal presents a fuzzy-filterable list of tasks for jumping
// directly to another task's agent view (Ctrl+K). Entries are supplied
// pre-sorted by the caller — needs-input tasks first — and the fuzzy filter
// preserves that order so blocked tasks stay pinned to the top.
type TaskSwitcherModal struct {
	*tview.Box
	all      []taskSwitcherEntry // full set, pre-sorted (needs-input first)
	filtered []taskSwitcherEntry // matches current query
	query    []rune
	qCursor  int
	cursor   int // position within filtered
	selected bool
	canceled bool
	title    string // centered title bar text (default " Switch task ")
	help     string // footer hint (default switch wording)
}

// NewTaskSwitcherModal creates a task switcher over the given entries.
func NewTaskSwitcherModal(entries []taskSwitcherEntry) *TaskSwitcherModal {
	return &TaskSwitcherModal{
		Box:      tview.NewBox(),
		all:      entries,
		filtered: entries,
		title:    " Switch task ",
		help:     "↑/↓ select  Enter switch  Esc cancel",
	}
}

// SetTitles overrides the modal title bar and footer hint so the same widget can
// drive the Hera DAG link/unlink parent picker (M7) without reading "Switch
// task". Empty strings keep the current value.
func (m *TaskSwitcherModal) SetTitles(title, help string) {
	if title != "" {
		m.title = title
	}
	if help != "" {
		m.help = help
	}
}

// Selected reports whether the user picked a task.
func (m *TaskSwitcherModal) Selected() bool { return m.selected }

// Canceled reports whether the user dismissed the modal.
func (m *TaskSwitcherModal) Canceled() bool { return m.canceled }

// SelectedTask returns the chosen task's ID (empty if none).
func (m *TaskSwitcherModal) SelectedTask() string {
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		return m.filtered[m.cursor].ID
	}
	return ""
}

// PasteHandler handles bracketed paste into the filter field.
func (m *TaskSwitcherModal) PasteHandler() func(string, func(tview.Primitive)) {
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

// InputHandler handles key events for the task switcher.
func (m *TaskSwitcherModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
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

// refilter updates the filtered list from the current query. Matching is fuzzy
// across task name and project; the input order (needs-input first) is
// preserved so blocked tasks stay at the top of the matches.
func (m *TaskSwitcherModal) refilter() {
	q := string(m.query)
	if q == "" {
		m.filtered = m.all
	} else {
		var matches []taskSwitcherEntry
		for _, e := range m.all {
			if fuzzyMatch(q, e.Name) || fuzzyMatch(q, e.Project) {
				matches = append(matches, e)
			}
		}
		m.filtered = matches
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(len(m.filtered)-1, 0)
	}
}

// taskSwitcherRowText renders the non-marker portion of a row: the task name
// followed by a dimmed metadata tail (project · status).
func taskSwitcherRowText(e taskSwitcherEntry) string {
	meta := e.Status.DisplayName()
	if e.Project != "" {
		meta = e.Project + " · " + meta
	}
	return e.Name + "  ·  " + meta
}

// Draw renders the task switcher as a centered modal.
func (m *TaskSwitcherModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Compute modal width from the widest row (+2 for the marker column).
	maxDisplayW := 30
	for _, e := range m.all {
		if w := utf8.RuneCountInString(taskSwitcherRowText(e)) + 2; w > maxDisplayW {
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

	title := m.title
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	titleStyle := tcell.StyleDefault.Foreground(theme.ColorTitle).Bold(true)
	// Iterate the []rune (not the byte-indexed range over the string) so a
	// multi-byte title rune — e.g. an arrow in the link-picker title — doesn't
	// leave gap cells (the rune-vs-byte placement bug; see gotchas/dag-rendering.md).
	for i, r := range []rune(title) {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2

	// Filter input row.
	filterY := my + 2
	widget.DrawText(screen, innerX, filterY, 2, "› ", theme.StyleFilter)
	before := string(m.query[:m.qCursor])
	after := string(m.query[m.qCursor:])
	fieldW := innerW - 2
	// On a very narrow terminal innerW can be ≤ 2, making fieldW ≤ 0. A
	// negative fieldW underflows the scroll-truncation slice below
	// (runes[len-fieldW:]) into an out-of-range panic, so clamp to ≥ 1.
	fieldW = max(fieldW, 1)
	val := before + "█" + after
	if runes := []rune(val); len(runes) > fieldW {
		val = string(runes[len(runes)-fieldW:])
	}
	widget.DrawText(screen, innerX+2, filterY, fieldW, val, theme.StyleNormal)

	// Items.
	itemsY := my + 4
	if len(m.all) == 0 {
		widget.DrawText(screen, innerX, itemsY, innerW, "No other tasks to switch to.", theme.StyleDimmed)
	} else if len(m.filtered) == 0 {
		widget.DrawText(screen, innerX, itemsY, innerW, "No matches", theme.StyleDimmed)
	} else {
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
			e := m.filtered[idx]
			rowY := itemsY + i
			selected := idx == m.cursor
			style := theme.StyleNormal
			if selected {
				style = theme.StyleSelected
			}
			// Marker column: needs-input tasks get the attention icon. When the
			// row is selected the marker adopts the selection style so the cue
			// doesn't fight the highlight.
			if e.NeedsInput {
				markerStyle := theme.StyleNeedsInput
				if selected {
					markerStyle = theme.StyleSelected
				}
				screen.SetContent(innerX, rowY, theme.IconNeedsInput, nil, markerStyle)
			}
			display := taskSwitcherRowText(e)
			textW := innerW - 2
			if utf8.RuneCountInString(display) > textW && textW > 3 {
				display = string([]rune(display)[:textW-1]) + "…"
			}
			widget.DrawText(screen, innerX+2, rowY, textW, display, style)
		}
	}

	helpRow := my + modalH - 2
	widget.DrawText(screen, innerX, helpRow, innerW, m.help, theme.StyleDimmed)
}
