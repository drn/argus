// Package terminalpane provides a tview widget that maintains a real
// terminal-emulator surface for ANSI bytes arriving on a channel.
//
// Unlike streampane (a log viewer that strips ANSI sequences and renders the
// trailing lines as plain text), TerminalPane feeds inbound bytes into a
// VT100-compatible emulator (charmbracelet/x/vt) and paints the resulting
// cell grid directly to a tcell.Screen. Cursor positioning, screen clears,
// SGR colors, UTF-8 multi-byte glyphs, and the rest of the standard VT
// repertoire are handled natively — full-screen-refresh-style emitters
// (tview, ncurses-likes) render correctly without confetti.
//
// Plugin views (PR 8) mount this widget. The public API is intentionally
// shaped to mirror streampane so the swap in plugin_views.go is mechanical.
package terminalpane

import (
	"fmt"
	"image/color"
	"io"
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/tui/keyenc"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// Default emulator dimensions until Draw or Resize provides a real size.
const (
	defaultCols = 80
	defaultRows = 24
	minCols     = 2
	minRows     = 2
)

// TerminalPane renders an ANSI byte stream through a VT emulator.
type TerminalPane struct {
	*tview.Box

	mu    sync.Mutex
	title string

	emu  *xvt.SafeEmulator
	cols int
	rows int

	touched uint64 // accessed via sync/atomic

	source    <-chan []byte
	inputBack chan<- []byte

	closeOnce sync.Once
	closeCh   chan struct{}
	done      chan struct{}

	// OnNeedRedraw, when set, fires once per non-empty inbound chunk so the
	// surrounding app can queue a redraw. Safe to leave nil.
	OnNeedRedraw func()
}

// New constructs a TerminalPane that consumes ANSI bytes from source.
//
// The emulator starts at 80x24; Draw and Resize adopt the real dimensions
// once they become known. The consumer goroutine exits when source is
// closed or Close is called.
func New(source <-chan []byte) *TerminalPane {
	tp := &TerminalPane{
		Box:     tview.NewBox(),
		cols:    defaultCols,
		rows:    defaultRows,
		source:  source,
		closeCh: make(chan struct{}),
		done:    make(chan struct{}),
	}
	tp.emu = newDrainedEmulator(tp.cols, tp.rows)
	go tp.consume()
	return tp
}

// newDrainedEmulator creates an x/vt SafeEmulator with a goroutine draining
// the response pipe. x/vt uses io.Pipe internally — when the emulator parses
// terminal query sequences (DA1, DA2, DSR, etc.) it writes responses to its
// internal pipe, which blocks Write indefinitely without a reader.
func newDrainedEmulator(cols, rows int) *xvt.SafeEmulator {
	emu := xvt.NewSafeEmulator(cols, rows)
	go io.Copy(io.Discard, emu) //nolint:errcheck
	return emu
}

// SetTitle sets the title rendered in the top border.
func (tp *TerminalPane) SetTitle(t string) {
	tp.mu.Lock()
	tp.title = t
	tp.mu.Unlock()
}

// SetInputBack wires the channel that receives keystrokes and pasted text
// when the pane is focused. Pass nil to disable input forwarding.
func (tp *TerminalPane) SetInputBack(ch chan<- []byte) {
	tp.mu.Lock()
	tp.inputBack = ch
	tp.mu.Unlock()
}

// Touched returns a monotonic counter that increments every time a new
// non-empty chunk arrives from the source. Callers compare against a
// previous value to detect undrawn content.
func (tp *TerminalPane) Touched() uint64 {
	return atomic.LoadUint64(&tp.touched)
}

// Close stops the consumer goroutine. Safe to call multiple times.
func (tp *TerminalPane) Close() {
	tp.closeOnce.Do(func() { close(tp.closeCh) })
}

// Resize sets the emulator surface dimensions explicitly. Draw also auto-
// resizes when the inner rect changes, so callers don't need to invoke this
// for every layout shuffle — it's exposed so plugin_views can pre-size the
// emulator on activation before the first frame arrives.
func (tp *TerminalPane) Resize(cols, rows int) {
	if cols < minCols {
		cols = minCols
	}
	if rows < minRows {
		rows = minRows
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.cols == cols && tp.rows == rows {
		return
	}
	tp.cols = cols
	tp.rows = rows
	tp.emu.Resize(cols, rows)
}

// PTYSize returns the emulator's current cols/rows. Useful in tests.
func (tp *TerminalPane) PTYSize() (int, int) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.cols, tp.rows
}

func (tp *TerminalPane) consume() {
	defer close(tp.done)
	for {
		select {
		case <-tp.closeCh:
			return
		case chunk, ok := <-tp.source:
			if !ok {
				return
			}
			if len(chunk) == 0 {
				continue
			}
			tp.feed(chunk)
			atomic.AddUint64(&tp.touched, 1)
			if tp.OnNeedRedraw != nil {
				tp.OnNeedRedraw()
			}
		}
	}
}

func (tp *TerminalPane) feed(b []byte) {
	tp.mu.Lock()
	emu := tp.emu
	tp.mu.Unlock()
	if emu == nil {
		return
	}
	_, _ = emu.Write(b)
}

// Draw paints the emulator surface onto screen inside a bordered panel.
func (tp *TerminalPane) Draw(screen tcell.Screen) {
	tp.DrawForSubclass(screen, tp)
	x, y, w, h := tp.GetRect()
	if w <= 0 || h <= 0 {
		return
	}

	tp.mu.Lock()
	title := tp.title
	tp.mu.Unlock()

	style := theme.StyleDimmed
	if tp.HasFocus() {
		style = tcell.StyleDefault
	}
	inner := widget.DrawBorderedPanel(screen, x, y, w, h, title, style)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	// Adopt the inner rect as the emulator surface size. We do this on every
	// Draw so a Flex layout shuffle just-works without a separate resize RPC.
	if inner.W >= minCols && inner.H >= minRows {
		tp.Resize(inner.W, inner.H)
	}

	tp.paint(screen, inner.X, inner.Y, inner.W, inner.H)
}

// paint walks the emulator's main screen and writes each cell to tcell.
// No scrollback rendering — plugin views ship discrete full-screen frames;
// the host terminal already owns the scrollback for the surrounding TUI.
func (tp *TerminalPane) paint(screen tcell.Screen, x, y, w, h int) {
	tp.mu.Lock()
	emu := tp.emu
	cols := tp.cols
	rows := tp.rows
	tp.mu.Unlock()
	if emu == nil {
		return
	}

	renderCols := min(cols, w)
	renderRows := min(rows, h)

	for row := 0; row < renderRows; row++ {
		for col := 0; col < renderCols; col++ {
			cell := emu.CellAt(col, row)
			ch := ' '
			st := tcell.StyleDefault
			if cell != nil {
				if cell.Content != "" {
					rs := []rune(cell.Content)
					if len(rs) > 0 {
						ch = rs[0]
					}
				}
				st = uvCellToTcellStyle(cell)
			}
			screen.SetContent(x+col, y+row, ch, nil, st)
		}
	}
}

// Bracketed-paste markers. tview's bracket-paste support consumes the
// ESC[200~ / ESC[201~ framing on argus's *real* terminal and hands
// WrapPasteHandler the already-unwrapped content. If we forward only that
// content, the downstream consumer of the input-back stream (a plugin's tcell
// parser over the WebSocket, or a real PTY with bracketed paste enabled) sees
// raw bytes with no framing and treats every rune as an individual keystroke —
// which is why pasting into a hera modal was ingested rune-by-rune. Re-wrapping
// once here restores the framing so the consumer coalesces the whole paste into
// a single paste event.
const (
	bracketPasteStart = "\x1b[200~"
	bracketPasteEnd   = "\x1b[201~"
)

// PasteHandler forwards pasted text to the configured InputBack channel,
// re-wrapped in bracketed-paste markers. Without an input-back channel the
// handler is a non-blocking no-op. The markers bracket the entire paste in a
// single send (send does not chunk), so multi-line and large pastes arrive
// framed as one paste event with embedded newlines intact.
func (tp *TerminalPane) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return tp.WrapPasteHandler(func(pastedText string, _ func(p tview.Primitive)) {
		tp.send([]byte(bracketPasteStart + pastedText + bracketPasteEnd))
	})
}

// InputHandler routes runes / mapped keys to the InputBack channel. Returns
// nil when no input-back channel is configured, leaving the widget read-only.
func (tp *TerminalPane) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	tp.mu.Lock()
	hasBack := tp.inputBack != nil
	tp.mu.Unlock()
	if !hasBack {
		return nil
	}
	return tp.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		tp.send(eventBytes(event))
	})
}

// MouseHandler forwards mouse-wheel events to the plugin as SGR mouse
// sequences (ESC [ < Cb ; Cx ; Cy M — Cb 64 = wheel-up, 65 = wheel-down) so
// plugins can scroll their own surfaces. Coordinates are 1-based relative to
// the pane's inner rect (the same rect Draw paints: outer minus the 1-cell
// border), clamped so a tick on the border still lands in range. Every other
// action is left unconsumed — focus is already surrendered to the plugin
// page. Without an input-back channel, wheel events are consumed but dropped
// (same read-only posture as InputHandler).
func (tp *TerminalPane) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return tp.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, _ func(p tview.Primitive)) (bool, tview.Primitive) {
		var cb int
		switch action {
		case tview.MouseScrollUp:
			cb = 64
		case tview.MouseScrollDown:
			cb = 65
		default:
			return false, nil
		}
		// Inner rect mirrors Draw: GetRect minus the border. With innerX =
		// x+1, the 1-based inner-relative column is ex - (x+1) + 1 = ex - x.
		x, y, w, h := tp.GetRect()
		ex, ey := event.Position()
		cx := min(max(ex-x, 1), max(w-2, 1))
		cy := min(max(ey-y, 1), max(h-2, 1))
		tp.send([]byte(fmt.Sprintf("\x1b[<%d;%d;%dM", cb, cx, cy)))
		return true, nil
	})
}

// send writes b to the input-back channel without blocking. If the channel is
// full, the bytes are dropped — matches streampane / PTY writer behavior.
func (tp *TerminalPane) send(b []byte) {
	if len(b) == 0 {
		return
	}
	tp.mu.Lock()
	ch := tp.inputBack
	tp.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- b:
	default:
	}
}

// eventBytes maps a tcell key event to the bytes a remote plugin expects.
// It delegates to the shared keyenc.Encode — the single source of truth for
// key encoding across the agent PTY and both plugin panes. The prior local
// allowlist here silently dropped arrows and every modifier combo, so a
// plugin could never bind Ctrl/Alt/Shift+arrow; keyenc forwards them.
func eventBytes(ev *tcell.EventKey) []byte {
	return keyenc.Encode(ev)
}

// uvCellToTcellStyle converts an ultraviolet cell's style to a tcell.Style.
// Covers fg/bg, the common SGR attributes, underline styles, and OSC-8
// hyperlinks — mirrors internal/tui/terminal/UvCellToTcellStyle.
func uvCellToTcellStyle(cell *uv.Cell) tcell.Style {
	if cell == nil {
		return tcell.StyleDefault
	}
	st := tcell.StyleDefault.
		Foreground(uvColorToTcell(cell.Style.Fg)).
		Background(uvColorToTcell(cell.Style.Bg))

	a := cell.Style.Attrs
	if a&uv.AttrBold != 0 {
		st = st.Bold(true)
	}
	if a&uv.AttrFaint != 0 {
		st = st.Dim(true)
	}
	if a&uv.AttrItalic != 0 {
		st = st.Italic(true)
	}
	if a&uv.AttrBlink != 0 {
		st = st.Blink(true)
	}
	if a&uv.AttrReverse != 0 {
		st = st.Reverse(true)
	}
	if a&uv.AttrStrikethrough != 0 {
		st = st.StrikeThrough(true)
	}
	if ul := cell.Style.Underline; ul != 0 {
		var ulStyle tcell.UnderlineStyle
		switch ul {
		case ansi.UnderlineSingle:
			ulStyle = tcell.UnderlineStyleSolid
		case ansi.UnderlineDouble:
			ulStyle = tcell.UnderlineStyleDouble
		case ansi.UnderlineCurly:
			ulStyle = tcell.UnderlineStyleCurly
		case ansi.UnderlineDotted:
			ulStyle = tcell.UnderlineStyleDotted
		case ansi.UnderlineDashed:
			ulStyle = tcell.UnderlineStyleDashed
		default:
			ulStyle = tcell.UnderlineStyleSolid
		}
		if cell.Style.UnderlineColor != nil {
			st = st.Underline(ulStyle, uvColorToTcell(cell.Style.UnderlineColor))
		} else {
			st = st.Underline(ulStyle)
		}
	}
	// Hyperlinks (OSC 8).
	if cell.Link.URL != "" {
		st = st.Url(cell.Link.URL)
	}
	return st
}

func uvColorToTcell(c color.Color) tcell.Color {
	if c == nil {
		return tcell.ColorDefault
	}
	switch v := c.(type) {
	case ansi.BasicColor:
		return tcell.PaletteColor(int(v))
	case ansi.IndexedColor:
		return tcell.PaletteColor(int(v))
	default:
		return tcell.FromImageColor(c)
	}
}
