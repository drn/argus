// Package streampane provides a read-mostly tview widget that renders bytes
// arriving on a channel. It is used by layouts that include a `streampane`
// node (PR 6) and by plugin-registered settings sections of type `stream`
// (PR 7).
//
// The widget owns one consumer goroutine that drains the source channel into
// a bounded internal byte buffer. Draw strips ANSI escape sequences and
// renders the trailing lines that fit inside a bordered panel. Damage
// tracking via [StreamPane.Touched] lets the surrounding tick loop know when
// a redraw would surface new content.
//
// When an input-back channel is wired via [StreamPane.SetInputBack], the
// widget routes typed runes and a small handful of mapped keys back to the
// plugin, plus pasted text. Without an input-back channel the widget is
// fully read-only.
package streampane

import (
	"sync"
	"sync/atomic"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/tui/keyenc"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// DefaultMaxBytes is the default cap on the internal byte buffer.
const DefaultMaxBytes = 256 * 1024

// Option configures a StreamPane at construction time.
type Option func(*StreamPane)

// WithMaxBytes overrides [DefaultMaxBytes].
func WithMaxBytes(n int) Option {
	return func(sp *StreamPane) {
		if n > 0 {
			sp.maxBytes = n
		}
	}
}

// StreamPane renders ANSI text streamed from a channel.
type StreamPane struct {
	*tview.Box

	mu       sync.Mutex
	buf      []byte
	maxBytes int
	title    string

	touched uint64 // accessed via sync/atomic

	source    <-chan []byte
	inputBack chan<- []byte

	closeOnce sync.Once
	closeCh   chan struct{}
	done      chan struct{}

	// OnNeedRedraw, when set, is invoked once per new byte chunk so the
	// surrounding app can queue a redraw. Safe to leave nil.
	OnNeedRedraw func()
}

// New constructs a StreamPane that consumes ANSI bytes from source.
//
// Source is consumed by an internal goroutine. The caller may close source
// to signal end-of-stream; the goroutine exits cleanly and the widget keeps
// displaying whatever bytes were already buffered.
func New(source <-chan []byte, opts ...Option) *StreamPane {
	sp := &StreamPane{
		Box:      tview.NewBox(),
		maxBytes: DefaultMaxBytes,
		source:   source,
		closeCh:  make(chan struct{}),
		done:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(sp)
	}
	go sp.consume()
	return sp
}

// SetTitle sets the title rendered in the top border.
func (sp *StreamPane) SetTitle(t string) {
	sp.mu.Lock()
	sp.title = t
	sp.mu.Unlock()
}

// SetInputBack wires the channel that receives keystrokes and pasted text
// when the pane is focused. Pass a nil channel to disable input forwarding.
func (sp *StreamPane) SetInputBack(ch chan<- []byte) {
	sp.mu.Lock()
	sp.inputBack = ch
	sp.mu.Unlock()
}

// Touched returns a monotonic counter that increments every time a new
// non-empty chunk arrives from the source. Callers compare against a
// previous value to detect undrawn content.
func (sp *StreamPane) Touched() uint64 {
	return atomic.LoadUint64(&sp.touched)
}

// Close stops the consumer goroutine. Safe to call multiple times.
func (sp *StreamPane) Close() {
	sp.closeOnce.Do(func() { close(sp.closeCh) })
}

func (sp *StreamPane) consume() {
	defer close(sp.done)
	for {
		select {
		case <-sp.closeCh:
			return
		case chunk, ok := <-sp.source:
			if !ok {
				return
			}
			if len(chunk) == 0 {
				continue
			}
			sp.appendBytes(chunk)
			atomic.AddUint64(&sp.touched, 1)
			if sp.OnNeedRedraw != nil {
				sp.OnNeedRedraw()
			}
		}
	}
}

func (sp *StreamPane) appendBytes(b []byte) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.buf = append(sp.buf, b...)
	if len(sp.buf) > sp.maxBytes {
		// Drop the oldest bytes — keep the trailing maxBytes window.
		excess := len(sp.buf) - sp.maxBytes
		sp.buf = sp.buf[excess:]
	}
}

// Draw paints the pane onto screen.
func (sp *StreamPane) Draw(screen tcell.Screen) {
	sp.DrawForSubclass(screen, sp)
	x, y, w, h := sp.GetRect()
	if w <= 0 || h <= 0 {
		return
	}

	sp.mu.Lock()
	title := sp.title
	buf := append([]byte(nil), sp.buf...)
	sp.mu.Unlock()

	style := theme.StyleDimmed
	if sp.HasFocus() {
		style = tcell.StyleDefault
	}
	inner := widget.DrawBorderedPanel(screen, x, y, w, h, title, style)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	lines := wrapStripped(buf, inner.W)
	start := 0
	if len(lines) > inner.H {
		start = len(lines) - inner.H
	}
	for i := start; i < len(lines); i++ {
		widget.DrawText(screen, inner.X, inner.Y+(i-start), inner.W, lines[i], tcell.StyleDefault)
	}
}

// wrapStripped strips ANSI sequences from b and breaks the result into
// display lines no wider than width. Newlines force a line break; runes
// past width wrap to a new line. Returns lines in chronological order.
func wrapStripped(b []byte, width int) []string {
	if width <= 0 {
		return nil
	}
	clean := widget.AnsiRe.ReplaceAll(b, nil)

	var (
		lines   []string
		current []rune
	)
	// Iterate runes, not bytes — `for _, by := range clean` over a []byte
	// emits individual bytes, which decomposes every UTF-8 multi-byte
	// sequence (box drawing, arrows, anything outside ASCII) into its raw
	// bytes and renders them as garbage. Casting `string(clean)` makes the
	// range emit code points instead.
	for _, r := range string(clean) {
		switch r {
		case '\n':
			lines = append(lines, string(current))
			current = current[:0]
		case '\r':
			// drop
		default:
			if r < 0x20 {
				continue
			}
			current = append(current, r)
			if len(current) >= width {
				lines = append(lines, string(current))
				current = current[:0]
			}
		}
	}
	if len(current) > 0 {
		lines = append(lines, string(current))
	}
	return lines
}

// PasteHandler forwards pasted text to the configured InputBack channel.
// Without an input-back channel the handler is a non-blocking no-op.
func (sp *StreamPane) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return sp.WrapPasteHandler(func(pastedText string, _ func(p tview.Primitive)) {
		sp.send([]byte(pastedText))
	})
}

// InputHandler routes runes / mapped keys to the InputBack channel. Returns
// nil when no input-back channel is configured, which leaves the widget
// effectively read-only.
func (sp *StreamPane) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	sp.mu.Lock()
	hasBack := sp.inputBack != nil
	sp.mu.Unlock()
	if !hasBack {
		return nil
	}
	return sp.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		sp.send(eventBytes(event))
	})
}

// send writes b to the input-back channel without blocking. If the channel
// is full (downstream slow) the bytes are dropped — matching how PTY
// writers handle backpressure elsewhere in argus.
func (sp *StreamPane) send(b []byte) {
	if len(b) == 0 {
		return
	}
	sp.mu.Lock()
	ch := sp.inputBack
	sp.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- b:
	default:
	}
}

// eventBytes maps a tcell key event to the bytes a remote plugin would
// expect. It delegates to the shared keyenc.Encode — the single source of
// truth for key encoding across the agent PTY and both plugin panes. The
// prior local allowlist here silently dropped arrows and every modifier
// combo; keyenc forwards them so a plugin can bind Ctrl/Alt/Shift+arrow.
func eventBytes(ev *tcell.EventKey) []byte {
	return keyenc.Encode(ev)
}
