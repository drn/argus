package modal

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// HelpModal shows a centered overlay listing keybindings grouped by context.
// Esc, Ctrl+Q, or `?` closes it. j/k, ↑/↓, PgUp/PgDn, g/G scroll. Mouse wheel
// scrolls too.
type HelpModal struct {
	*tview.Box
	closed    bool
	scroll    int
	maxScroll int
	pageStep  int
}

// HelpBinding is a single key/action pair.
type HelpBinding struct {
	Key    string
	Action string
}

// HelpSection groups bindings under a heading.
type HelpSection struct {
	Title    string
	Bindings []HelpBinding
}

// HelpSections is the canonical inventory rendered by the modal. Source of
// truth for the help overlay — mirror updates here when bindings change.
var HelpSections = []HelpSection{
	{
		Title: "Task List",
		Bindings: []HelpBinding{
			{"n", "new task"},
			{"Enter", "open agent view"},
			{"j / k", "navigate up/down"},
			{"s / S", "advance / revert status"},
			{"a", "toggle archive"},
			{"P", "toggle pin"},
			{"r", "rename"},
			{"c", "copy prompt"},
			{"/", "filter"},
			{"ctrl+f", "fork task"},
			{"ctrl+d", "destroy task"},
			{"ctrl+r", "prune completed"},
			{"ctrl+p", "open PR"},
			{"ctrl+o", "open repo"},
			{"ctrl+l", "refresh screen"},
			{"1 / 2 / 3", "switch tab (tasks / DAG / settings)"},
			{"q", "quit"},
		},
	},
	{
		Title: "Agent View",
		Bindings: []HelpBinding{
			{"ctrl+q / Esc", "back (diff → files → list)"},
			{"Cmd+← / Cmd+→", "switch panels"},
			{"Cmd+↑ / Cmd+↓", "navigate tasks"},
			{"Shift+↑ / Shift+↓", "scroll terminal"},
			{"ctrl+z", "toggle single-pane (zoom)"},
			{"ctrl+l", "link picker"},
			{"ctrl+p", "open PR"},
			{"ctrl+y", "copy staged text"},
		},
	},
	{
		Title: "File Panel",
		Bindings: []HelpBinding{
			{"Enter", "open diff"},
			{"s", "toggle split/unified"},
			{"o", "reveal in Finder"},
			{"e", "open in editor"},
			{"t", "open terminal in worktree"},
		},
	},
	{
		Title: "Settings",
		Bindings: []HelpBinding{
			{"j / k", "navigate rows"},
			{"n", "new project / backend / schedule"},
			{"e", "edit"},
			{"d", "delete / set default"},
			{"t", "toggle schedule enabled"},
			{"r", "run schedule now"},
			{"i", "quick add projects"},
			{"Enter / ◀ / ▶", "toggle / cycle settings"},
		},
	},
	{
		Title: "Modals & Forms",
		Bindings: []HelpBinding{
			{"Esc / ctrl+q", "close / cancel"},
			{"Enter", "confirm / submit"},
			{"Tab / Shift+Tab", "navigate fields"},
		},
	},
}

// helpRow is one rendered line of help content.
type helpRow struct {
	key     string
	action  string
	heading bool
	spacer  bool
}

// helpRows flattens HelpSections into a list of rendered rows. Spacers go
// between sections (not after the last one).
func helpRows() []helpRow {
	var rows []helpRow
	for i, sec := range HelpSections {
		if i > 0 {
			rows = append(rows, helpRow{spacer: true})
		}
		rows = append(rows, helpRow{action: sec.Title, heading: true})
		for _, b := range sec.Bindings {
			rows = append(rows, helpRow{key: b.Key, action: b.Action})
		}
	}
	return rows
}

// NewHelpModal creates a help overlay.
func NewHelpModal() *HelpModal {
	return &HelpModal{Box: tview.NewBox()}
}

// Closed reports whether the user dismissed the modal.
func (m *HelpModal) Closed() bool { return m.closed }

// InputHandler closes on Esc/Ctrl+Q/?, scrolls on j/k, ↑/↓, PgUp/PgDn, g/G.
// Out-of-range scroll values are clamped on the next Draw.
func (m *HelpModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.closed = true
			return
		case tcell.KeyUp:
			m.scroll--
			return
		case tcell.KeyDown:
			m.scroll++
			return
		case tcell.KeyPgUp, tcell.KeyCtrlU:
			m.scroll -= m.pageStep
			return
		case tcell.KeyPgDn, tcell.KeyCtrlD:
			m.scroll += m.pageStep
			return
		case tcell.KeyHome:
			m.scroll = 0
			return
		case tcell.KeyEnd:
			m.scroll = 1 << 30
			return
		}
		if event.Key() == tcell.KeyRune {
			switch event.Rune() {
			case '?':
				m.closed = true
			case 'j':
				m.scroll++
			case 'k':
				m.scroll--
			case 'g':
				m.scroll = 0
			case 'G':
				m.scroll = 1 << 30
			}
		}
	})
}

// MouseHandler scrolls on wheel up/down. Click captures focus so the modal
// keeps receiving keyboard events when something behind it would otherwise
// steal focus.
func (m *HelpModal) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
	return m.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
		switch action {
		case tview.MouseScrollUp:
			m.scroll -= 3
			return true, nil
		case tview.MouseScrollDown:
			m.scroll += 3
			return true, nil
		case tview.MouseLeftDown, tview.MouseLeftClick:
			setFocus(m)
			return true, nil
		}
		return false, nil
	})
}

// helpModalSize returns the modal's width and total drawn height for the
// available rect. Height is the natural full-content height clamped to the
// available space; scrolling handles the overflow.
func helpModalSize(width, height int) (w, h int) {
	w = min(72, width-4)
	if w < 24 {
		w = width
	}
	// border (2) + content + hint (1)
	h = 3 + len(helpRows())
	if h > height {
		h = height
	}
	return w, h
}

// Draw renders the help modal centered in the available rect.
func (m *HelpModal) Draw(screen tcell.Screen) {
	m.Box.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW, formH := helpModalSize(width, height)
	if formW < 8 || formH < 4 {
		return
	}
	formX := x + (width-formW)/2
	formY := y + (height-formH)/2
	if formY < y {
		formY = y
	}

	widget.FillArea(screen, formX, formY, formW, formH, ' ', tcell.StyleDefault)
	inner := widget.DrawBorderedPanel(screen, formX, formY, formW, formH, "Keybindings", theme.StyleFocusedBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	keyCol := 18
	if keyCol > inner.W/2 {
		keyCol = inner.W / 2
	}

	rows := helpRows()
	visible := inner.H - 1 // reserve last row for hint
	if visible < 1 {
		visible = 1
	}
	if visible > len(rows) {
		visible = len(rows)
	}
	maxScroll := len(rows) - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	m.maxScroll = maxScroll
	m.pageStep = visible

	for i := 0; i < visible; i++ {
		idx := m.scroll + i
		if idx >= len(rows) {
			break
		}
		r := rows[idx]
		rowY := inner.Y + i
		switch {
		case r.spacer:
			// blank
		case r.heading:
			widget.DrawText(screen, inner.X, rowY, inner.W, r.action, theme.StyleTitle)
		default:
			widget.DrawText(screen, inner.X+2, rowY, keyCol, r.key, theme.StyleNormal)
			widget.DrawText(screen, inner.X+2+keyCol, rowY, inner.W-keyCol-2, r.action, theme.StyleDimmed)
		}
	}

	hint := "[esc / ?] close"
	if maxScroll > 0 {
		hint = fmt.Sprintf("[esc / ?] close   [↑↓ / jk] scroll   %d-%d / %d", m.scroll+1, m.scroll+visible, len(rows))
	}
	widget.DrawText(screen, inner.X, inner.Y+inner.H-1, inner.W, hint, theme.StyleDimmed)
}
