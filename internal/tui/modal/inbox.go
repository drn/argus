package modal

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// InboxEntry is one message rendered by InboxModal. Source is "task" for a
// task_messages row or "hera" for a hera_messages row.
type InboxEntry struct {
	At       time.Time
	Source   string
	From     string
	To       string
	Summary  string
	Unread   bool
	ReadAt   time.Time
	Delivery string
	Body     string
}

type inboxLine struct {
	text  string
	style tcell.Style
}

// InboxModal is a read-only, scrollable viewer of a task's received messages.
// It never mutates message state; the App owns loading and passes entries in.
type InboxModal struct {
	*tview.Box
	title       string
	entries     []InboxEntry
	loading     bool
	errMsg      string
	unavailable string

	closed bool
	reload bool

	scroll   int
	pageStep int
}

// NewInboxModal creates an inbox viewer in the loading state.
func NewInboxModal(title string) *InboxModal {
	return &InboxModal{Box: tview.NewBox(), title: singleLine(title), loading: true}
}

// SetEntries replaces the rendered messages and scrolls to the newest.
func (m *InboxModal) SetEntries(entries []InboxEntry) {
	m.entries = entries
	m.loading = false
	m.errMsg = ""
	m.scroll = 1 << 30
}

// SetError shows a load failure in place of the message list.
func (m *InboxModal) SetError(msg string) {
	m.loading = false
	m.errMsg = msg
}

// SetUnavailable shows a permanent notice (e.g. remote mode) instead of loading.
func (m *InboxModal) SetUnavailable(msg string) {
	m.loading = false
	m.unavailable = msg
}

// SetLoading marks a reload in flight.
func (m *InboxModal) SetLoading() {
	m.loading = true
	m.errMsg = ""
}

// Entries returns the rendered messages and whether a load has completed
// (false while loading or after an error).
func (m *InboxModal) Entries() ([]InboxEntry, bool) {
	return m.entries, !m.loading && m.errMsg == "" && m.unavailable == ""
}

// Unavailable returns the permanent notice, if any.
func (m *InboxModal) Unavailable() string { return m.unavailable }

// Closed reports whether the user dismissed the modal.
func (m *InboxModal) Closed() bool { return m.closed }

// TakeReload reports and clears a pending reload request.
func (m *InboxModal) TakeReload() bool {
	r := m.reload
	m.reload = false
	return r
}

// InputHandler closes on Esc/Ctrl+Q/q/i, reloads on r, scrolls on j/k,
// ↑/↓, PgUp/PgDn, g/G, Home/End. Out-of-range scroll is clamped on Draw.
func (m *InboxModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.closed = true
		case tcell.KeyUp:
			m.scroll--
		case tcell.KeyDown:
			m.scroll++
		case tcell.KeyPgUp, tcell.KeyCtrlU:
			m.scroll -= m.pageStep
		case tcell.KeyPgDn, tcell.KeyCtrlD:
			m.scroll += m.pageStep
		case tcell.KeyHome:
			m.scroll = 0
		case tcell.KeyEnd:
			m.scroll = 1 << 30
		case tcell.KeyRune:
			switch event.Rune() {
			case 'q', 'i':
				m.closed = true
			case 'r':
				if m.unavailable == "" {
					m.reload = true
				}
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

// MouseHandler scrolls on wheel and captures focus on click.
func (m *InboxModal) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
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

func (m *InboxModal) lines(width int) []inboxLine {
	switch {
	case m.unavailable != "":
		return []inboxLine{{singleLine(m.unavailable), theme.StyleDimmed}}
	case m.loading:
		return []inboxLine{{"Loading…", theme.StyleDimmed}}
	case m.errMsg != "":
		return []inboxLine{{"Error: " + singleLine(m.errMsg), theme.StyleError}}
	case len(m.entries) == 0:
		return []inboxLine{{"No messages.", theme.StyleDimmed}}
	}
	var out []inboxLine
	for i, e := range m.entries {
		if i > 0 {
			out = append(out, inboxLine{})
		}
		out = append(out, inboxLine{inboxHeader(e), theme.StyleTitle})
		state, style := "read "+e.ReadAt.Local().Format("01-02 15:04:05"), theme.StyleDimmed
		if e.Unread {
			state, style = "unread", theme.StyleNeedsInput
		}
		if e.Delivery != "" {
			state += "  ·  delivery: " + singleLine(e.Delivery)
		}
		out = append(out, inboxLine{"  " + state, style})
		if e.Summary != "" {
			for _, l := range wrapPreservingLines(e.Summary, width-2) {
				out = append(out, inboxLine{"  " + l, theme.StyleSelected})
			}
		}
		for _, l := range wrapPreservingLines(e.Body, width-2) {
			out = append(out, inboxLine{"  " + l, theme.StyleNormal})
		}
	}
	return out
}

func inboxHeader(e InboxEntry) string {
	h := fmt.Sprintf("%s  [%s]  %s", e.At.Local().Format("2006-01-02 15:04:05"), singleLine(e.Source), singleLine(e.From))
	if e.To != "" {
		h += " → " + singleLine(e.To)
	}
	return h
}

// stripControls drops control characters (ESC and friends) so message
// content, sender names, and task names can never emit terminal escape
// sequences. Tabs become spaces; newlines are kept only when keepNewlines.
func stripControls(s string, keepNewlines bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && keepNewlines:
			return r
		case r == '\t', r == '\n':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

func singleLine(s string) string { return stripControls(s, false) }

// wrapPreservingLines word-wraps each line of text to width display columns,
// keeping blank lines and hard-breaking overlong tokens. Control characters
// are stripped first.
func wrapPreservingLines(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	text = stripControls(text, true)
	var out []string
	for _, para := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(para) == "" {
			out = append(out, "")
			continue
		}
		var cur strings.Builder
		curW := 0
		flush := func() {
			if curW > 0 {
				out = append(out, cur.String())
				cur.Reset()
				curW = 0
			}
		}
		for _, word := range strings.Fields(para) {
			for runewidth.StringWidth(word) > width {
				flush()
				head := runewidth.Truncate(word, width, "")
				if head == "" {
					// A single rune wider than the whole line; emit it alone.
					_, size := utf8.DecodeRuneInString(word)
					head = word[:size]
				}
				out = append(out, head)
				word = word[len(head):]
			}
			ww := runewidth.StringWidth(word)
			if ww == 0 {
				continue
			}
			if curW > 0 && curW+1+ww > width {
				flush()
			}
			if curW > 0 {
				cur.WriteByte(' ')
				curW++
			}
			cur.WriteString(word)
			curW += ww
		}
		flush()
	}
	return out
}

// drawCells paints text at (x, y), advancing by each rune's display width and
// clipping at maxWidth columns so a wide rune never straddles the border.
func drawCells(screen tcell.Screen, x, y, maxWidth int, text string, style tcell.Style) {
	col := 0
	for _, r := range text {
		w := runewidth.RuneWidth(r)
		if w == 0 {
			continue
		}
		if col+w > maxWidth {
			return
		}
		screen.SetContent(x+col, y, r, nil, style)
		col += w
	}
}

// Draw renders the inbox as a large centered bordered panel.
func (m *InboxModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}
	formW := min(110, width-4)
	if formW < 24 {
		formW = width
	}
	formH := height - 2
	if formH < 6 {
		formH = height
	}
	if formW < 8 || formH < 4 {
		return
	}
	formX := x + (width-formW)/2
	formY := y + (height-formH)/2

	widget.FillArea(screen, formX, formY, formW, formH, ' ', tcell.StyleDefault)
	inner := widget.DrawBorderedPanel(screen, formX, formY, formW, formH, m.title, theme.StyleFocusedBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	lines := m.lines(inner.W)
	visible := max(inner.H-1, 1)
	maxScroll := max(len(lines)-visible, 0)
	m.scroll = min(max(m.scroll, 0), maxScroll)
	m.pageStep = visible

	for i := 0; i < visible && m.scroll+i < len(lines); i++ {
		l := lines[m.scroll+i]
		drawCells(screen, inner.X, inner.Y+i, inner.W, l.text, l.style)
	}

	hint := "[esc/q] close  [r] reload  read-only"
	if !m.loading && m.errMsg == "" && m.unavailable == "" {
		hint += fmt.Sprintf("  %d message(s)", len(m.entries))
	}
	if maxScroll > 0 {
		hint += fmt.Sprintf("   %d-%d / %d", m.scroll+1, min(m.scroll+visible, len(lines)), len(lines))
	}
	widget.DrawText(screen, inner.X, inner.Y+inner.H-1, inner.W, hint, theme.StyleDimmed)
}
