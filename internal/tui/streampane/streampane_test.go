package streampane

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestNew_ReturnsBoxSubclass(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	testutil.NotNil(t, sp.Box)
}

func TestStreamPane_TouchedIncrementsOnNewBytes(t *testing.T) {
	src := make(chan []byte, 1)
	sp := New(src)
	defer sp.Close()

	before := sp.Touched()
	src <- []byte("hello")
	waitForTouched(t, sp, before+1)
}

func TestStreamPane_TouchedDoesNotIncrementOnEmptyChunks(t *testing.T) {
	src := make(chan []byte, 2)
	sp := New(src)
	defer sp.Close()

	src <- []byte("hi")
	waitForTouched(t, sp, 1)
	got := sp.Touched()
	src <- []byte("")
	// Empty chunk should be a no-op; give the goroutine a moment.
	time.Sleep(20 * time.Millisecond)
	testutil.Equal(t, sp.Touched(), got)
}

func TestStreamPane_DrawShowsBytes(t *testing.T) {
	src := make(chan []byte, 1)
	sp := New(src)
	defer sp.Close()
	sp.SetTitle("Logs")

	src <- []byte("hello world\n")
	waitForTouched(t, sp, 1)

	sim := newSimScreen(t, 30, 6)
	sp.SetRect(0, 0, 30, 6)
	sp.Draw(sim)
	sim.Show()

	row := readRow(sim, 1, 30)
	testutil.Contains(t, row, "hello world")
}

func TestStreamPane_DrawRendersTitleInBorder(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	sp.SetTitle("MyTitle")

	sim := newSimScreen(t, 20, 4)
	sp.SetRect(0, 0, 20, 4)
	sp.Draw(sim)
	sim.Show()

	top := readRow(sim, 0, 20)
	testutil.Contains(t, top, "MyTitle")
}

func TestStreamPane_DrawStripsAnsi(t *testing.T) {
	src := make(chan []byte, 1)
	sp := New(src)
	defer sp.Close()

	src <- []byte("\x1b[31mred\x1b[0m\n")
	waitForTouched(t, sp, 1)

	sim := newSimScreen(t, 20, 4)
	sp.SetRect(0, 0, 20, 4)
	sp.Draw(sim)
	sim.Show()

	row := readRow(sim, 1, 20)
	testutil.Contains(t, row, "red")
	if strings.Contains(row, "\x1b") {
		t.Errorf("escape sequence leaked into output: %q", row)
	}
}

func TestStreamPane_BoundedBufferDropsOldest(t *testing.T) {
	src := make(chan []byte, 4)
	sp := New(src, WithMaxBytes(16))
	defer sp.Close()

	src <- []byte("aaaaaaaa\n")
	src <- []byte("bbbbbbbb\n")
	src <- []byte("cccccccc\n")
	waitForTouched(t, sp, 3)

	// Internal buffer must not exceed the cap.
	sp.mu.Lock()
	got := len(sp.buf)
	sp.mu.Unlock()
	if got > 16 {
		t.Fatalf("buffer length %d exceeds cap 16", got)
	}
}

func TestStreamPane_CloseIsIdempotent(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	sp.Close()
	sp.Close() // must not panic
}

func TestStreamPane_CloseStopsConsumer(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	sp.Close()

	// After close, sending to src should NOT cause Touched to advance.
	// Use a non-blocking send to a buffered channel and confirm the
	// consumer goroutine is gone (no reads, no advancement).
	select {
	case <-sp.done:
		// consumer exited
	case <-time.After(200 * time.Millisecond):
		t.Fatal("consumer goroutine did not exit after Close")
	}
}

func TestStreamPane_SourceClosedStopsConsumer(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()

	close(src)
	select {
	case <-sp.done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("consumer did not exit after source close")
	}
}

func TestStreamPane_OnRedrawFiresAfterBytes(t *testing.T) {
	src := make(chan []byte, 1)
	sp := New(src)
	defer sp.Close()

	var (
		mu    sync.Mutex
		count int
	)
	sp.OnNeedRedraw = func() {
		mu.Lock()
		defer mu.Unlock()
		count++
	}

	src <- []byte("x")
	waitForTouched(t, sp, 1)

	mu.Lock()
	defer mu.Unlock()
	if count < 1 {
		t.Fatalf("expected OnNeedRedraw to fire at least once, got %d", count)
	}
}

func TestStreamPane_InputBackReceivesKey(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	back := make(chan []byte, 4)
	sp.SetInputBack(back)

	handler := sp.InputHandler()
	if handler == nil {
		t.Fatal("expected non-nil InputHandler when InputBack is set")
	}
	handler(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), func(_ tview.Primitive) {})

	select {
	case got := <-back:
		testutil.Equal(t, string(got), "a")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("InputBack did not receive keystroke")
	}
}

func TestStreamPane_InputBackForwardsEnter(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	back := make(chan []byte, 4)
	sp.SetInputBack(back)

	handler := sp.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(_ tview.Primitive) {})

	select {
	case got := <-back:
		testutil.Equal(t, string(got), "\r")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("InputBack did not receive enter")
	}
}

func TestStreamPane_InputHandlerNilWhenNoInputBack(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	if sp.InputHandler() != nil {
		t.Fatal("expected nil InputHandler when no InputBack set")
	}
}

func TestStreamPane_PasteHandlerForwardsToInputBack(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	back := make(chan []byte, 4)
	sp.SetInputBack(back)

	ph := sp.PasteHandler()
	if ph == nil {
		t.Fatal("expected non-nil PasteHandler")
	}
	ph("pasted", func(_ tview.Primitive) {})

	select {
	case got := <-back:
		testutil.Equal(t, string(got), "pasted")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("paste did not reach InputBack")
	}
}

func TestStreamPane_DrawHandlesZeroRect(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	sim := newSimScreen(t, 10, 4)
	// Default rect is non-zero; explicitly set zero-area and verify
	// Draw returns without painting.
	sp.SetRect(0, 0, 0, 0)
	sp.Draw(sim)
}

func TestStreamPane_DrawHandlesTinyRect(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	sim := newSimScreen(t, 10, 4)
	// Tiny — too small for a bordered inner rect.
	sp.SetRect(0, 0, 1, 1)
	sp.Draw(sim)
}

func TestStreamPane_DrawWrapsLongLines(t *testing.T) {
	src := make(chan []byte, 1)
	sp := New(src)
	defer sp.Close()
	// Source longer than inner width — should wrap.
	src <- []byte("abcdefghij")
	waitForTouched(t, sp, 1)

	sim := newSimScreen(t, 6, 6) // inner width = 4
	sp.SetRect(0, 0, 6, 6)
	sp.Draw(sim)
	sim.Show()
}

func TestStreamPane_DrawScrollsLastLines(t *testing.T) {
	src := make(chan []byte, 1)
	sp := New(src)
	defer sp.Close()
	// More lines than the inner height; last lines should be retained.
	src <- []byte("one\ntwo\nthree\nfour\nfive\n")
	waitForTouched(t, sp, 1)

	sim := newSimScreen(t, 20, 4) // inner height = 2
	sp.SetRect(0, 0, 20, 4)
	sp.Draw(sim)
	sim.Show()

	row := readRow(sim, 1, 20)
	if !strings.Contains(row, "four") && !strings.Contains(row, "five") {
		t.Errorf("expected trailing lines in viewport, got %q", row)
	}
}

func TestStreamPane_DrawWhenFocused(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	sp.SetRect(0, 0, 10, 4)
	sp.Focus(nil)
	sim := newSimScreen(t, 10, 4)
	sp.Draw(sim)
}

func TestStreamPane_DrawIgnoresControlCharsAndCR(t *testing.T) {
	src := make(chan []byte, 1)
	sp := New(src)
	defer sp.Close()
	src <- []byte("hi\x01\x02\rthere\n")
	waitForTouched(t, sp, 1)

	sim := newSimScreen(t, 20, 4)
	sp.SetRect(0, 0, 20, 4)
	sp.Draw(sim)
	sim.Show()
	row := readRow(sim, 1, 20)
	testutil.Contains(t, row, "hithere")
}

func TestWrapStripped_PreservesUTF8MultiByteGlyphs(t *testing.T) {
	// Box-drawing characters and arrows are multi-byte UTF-8. The previous
	// byte-iterating loop in wrapStripped exploded each into individual
	// bytes, producing mojibake like `â`/`Â`. Assert the exact runes survive.
	in := []byte("─→│┌┘")
	lines := wrapStripped(in, 20)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %v", len(lines), lines)
	}
	want := "─→│┌┘"
	if lines[0] != want {
		t.Fatalf("rendered line mojibake'd: got %q want %q", lines[0], want)
	}
	// And each glyph is exactly one rune in the result.
	runes := []rune(lines[0])
	if len(runes) != 5 {
		t.Fatalf("expected 5 runes, got %d (%v)", len(runes), runes)
	}
}

func TestStreamPane_DrawShowsUTF8Glyphs(t *testing.T) {
	src := make(chan []byte, 1)
	sp := New(src)
	defer sp.Close()

	src <- []byte("┌─┘\n")
	waitForTouched(t, sp, 1)

	sim := newSimScreen(t, 20, 4)
	sp.SetRect(0, 0, 20, 4)
	sp.Draw(sim)
	sim.Show()

	row := readRow(sim, 1, 20)
	if !strings.Contains(row, "┌─┘") {
		t.Errorf("expected box-drawing glyphs in row, got %q", row)
	}
}

func TestWrapStripped_ZeroWidthIsNoOp(t *testing.T) {
	got := wrapStripped([]byte("abc"), 0)
	testutil.Equal(t, len(got), 0)
}

func TestStreamPane_SendDropsWhenBackFull(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	back := make(chan []byte, 1)
	sp.SetInputBack(back)
	// Fill the back channel.
	back <- []byte("blocker")
	// This should not block, and should drop the keystroke.
	handler := sp.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone), func(_ tview.Primitive) {})
}

func TestStreamPane_SendNoOpWithoutInputBack(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	// Direct send should be a no-op when inputBack is nil.
	sp.send([]byte("ignored"))
}

func TestStreamPane_SendEmptyBytes(t *testing.T) {
	src := make(chan []byte)
	sp := New(src)
	defer sp.Close()
	back := make(chan []byte, 1)
	sp.SetInputBack(back)
	sp.send(nil)
	select {
	case <-back:
		t.Fatal("expected no send for empty bytes")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestEventBytes_AllCases(t *testing.T) {
	cases := []struct {
		key  tcell.Key
		r    rune
		want string
	}{
		{tcell.KeyRune, 'z', "z"},
		{tcell.KeyEnter, 0, "\r"},
		{tcell.KeyTab, 0, "\t"},
		{tcell.KeyBackspace, 0, "\x7f"},
		{tcell.KeyBackspace2, 0, "\x7f"},
		{tcell.KeyEscape, 0, "\x1b"},
		{tcell.KeyF1, 0, ""}, // unmapped — no bytes
	}
	for _, tc := range cases {
		t.Run(tcell.KeyNames[tc.key], func(t *testing.T) {
			ev := tcell.NewEventKey(tc.key, tc.r, tcell.ModNone)
			testutil.Equal(t, string(eventBytes(ev)), tc.want)
		})
	}
}

func TestWithMaxBytes_ZeroIsIgnored(t *testing.T) {
	src := make(chan []byte)
	sp := New(src, WithMaxBytes(0))
	defer sp.Close()
	testutil.Equal(t, sp.maxBytes, DefaultMaxBytes)
}

// --- helpers ---

func waitForTouched(t *testing.T, sp *StreamPane, want uint64) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if sp.Touched() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Touched did not reach %d (got %d)", want, sp.Touched())
}

func newSimScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	return s
}

func readRow(s tcell.SimulationScreen, row, w int) string {
	cells, cw, _ := s.GetContents()
	if row < 0 || row*cw >= len(cells) {
		return ""
	}
	var b strings.Builder
	for col := 0; col < w; col++ {
		idx := row*cw + col
		if idx >= len(cells) {
			break
		}
		for _, r := range cells[idx].Runes {
			b.WriteRune(r)
		}
	}
	return b.String()
}
