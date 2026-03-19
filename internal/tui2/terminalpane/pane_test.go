package terminalpane

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/hinshun/vt10x"
)

// mockSession implements TerminalSession for testing.
type mockSession struct {
	output       []byte
	totalWritten uint64
	alive        bool
	cols, rows   int
}

func (m *mockSession) RecentOutput() []byte              { return m.output }
func (m *mockSession) TotalWritten() uint64               { return m.totalWritten }
func (m *mockSession) Alive() bool                        { return m.alive }
func (m *mockSession) PTYSize() (cols, rows int)          { return m.cols, m.rows }

func TestVtColorToTcell(t *testing.T) {
	tests := []struct {
		name string
		c    vt10x.Color
		want tcell.Color
	}{
		{"default_fg", vt10x.DefaultFG, tcell.ColorDefault},
		{"default_bg", vt10x.DefaultBG, tcell.ColorDefault},
		{"black", 0, tcell.PaletteColor(0)},
		{"red", 1, tcell.PaletteColor(1)},
		{"bright_white", 15, tcell.PaletteColor(15)},
		{"xterm_87", 87, tcell.PaletteColor(87)},
		{"xterm_255", 255, tcell.PaletteColor(255)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vtColorToTcell(tt.c)
			if got != tt.want {
				t.Errorf("vtColorToTcell(%v) = %v, want %v", tt.c, got, tt.want)
			}
		})
	}
}

func TestCellStyle(t *testing.T) {
	// Bold + red foreground
	cell := vt10x.Glyph{
		Char: 'A',
		FG:   1, // red
		BG:   vt10x.DefaultBG,
		Mode: vtAttrBold,
	}
	style := cellStyle(cell)
	fg, bg, attr := style.Decompose()
	if fg != tcell.PaletteColor(1) {
		t.Errorf("fg = %v, want PaletteColor(1)", fg)
	}
	if bg != tcell.ColorDefault {
		t.Errorf("bg = %v, want ColorDefault", bg)
	}
	if attr&tcell.AttrBold == 0 {
		t.Error("expected bold attribute")
	}
}

func TestRowHasContent(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(10, 5))
	vt.Write([]byte("hello\n"))

	vt.Lock()
	defer vt.Unlock()

	if !rowHasContent(vt, 0, 10) {
		t.Error("row 0 should have content")
	}
	if rowHasContent(vt, 3, 10) {
		t.Error("row 3 should be empty")
	}
}

func TestEstimateVTRows(t *testing.T) {
	raw := []byte("line1\nline2\nline3\nline4\nline5\n")
	rows := estimateVTRows(raw, 80, 10)
	if rows < 10 {
		t.Errorf("estimateVTRows = %d, want >= 10", rows)
	}
}

func TestPaneNoSession(t *testing.T) {
	p := New()
	if p.HasContent() {
		t.Error("empty pane should not have content")
	}
}

func TestPaneWithReplayData(t *testing.T) {
	p := New()
	p.SetReplayData([]byte("Hello World\r\n"))
	if !p.HasContent() {
		t.Error("pane with replay data should have content")
	}
}

func TestPaneScrollback(t *testing.T) {
	p := New()
	p.ScrollUp(5)
	if p.ScrollOffset() != 5 {
		t.Errorf("ScrollOffset() = %d, want 5", p.ScrollOffset())
	}
	p.ScrollDown(3)
	if p.ScrollOffset() != 2 {
		t.Errorf("ScrollOffset() = %d, want 2", p.ScrollOffset())
	}
	p.ScrollToBottom()
	if p.ScrollOffset() != 0 {
		t.Errorf("ScrollOffset() = %d, want 0", p.ScrollOffset())
	}
	// ScrollDown past 0 should clamp
	p.ScrollDown(10)
	if p.ScrollOffset() != 0 {
		t.Errorf("ScrollOffset() = %d after over-scroll-down, want 0", p.ScrollOffset())
	}
}

func TestPaneRenderWithMockSession(t *testing.T) {
	sess := &mockSession{
		output:       []byte("Hello\r\nWorld\r\n"),
		totalWritten: 14,
		alive:        true,
		cols:         80,
		rows:         24,
	}
	p := New()
	p.SetSession(sess)

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(80, 24)

	// Should not panic
	p.Render(screen, 0, 0, 80, 24)
}

func TestPaneRenderReplay(t *testing.T) {
	p := New()
	p.SetReplayData([]byte("Line 1\r\nLine 2\r\nLine 3\r\n"))

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(40, 10)

	// Should not panic
	p.Render(screen, 0, 0, 40, 10)

	// Verify some content was rendered
	ch, _, _, _ := screen.GetContent(0, 0)
	if ch != 'L' {
		t.Errorf("expected 'L' at (0,0), got %q", ch)
	}
}

func TestPaneRenderScrollback(t *testing.T) {
	p := New()
	// Create enough content to need scrollback
	var data []byte
	for i := 0; i < 50; i++ {
		data = append(data, []byte("line content here\r\n")...)
	}
	p.SetReplayData(data)
	p.ScrollUp(10)

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(40, 10)

	// Should not panic
	p.Render(screen, 0, 0, 40, 10)

	if p.ScrollOffset() > 50 {
		t.Errorf("scroll offset %d should be clamped", p.ScrollOffset())
	}
}

func TestPaneZeroDimensions(t *testing.T) {
	p := New()
	p.SetReplayData([]byte("Hello"))

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(80, 24)

	// Should not panic with zero dimensions
	p.Render(screen, 0, 0, 0, 0)
	p.Render(screen, 0, 0, -1, -1)
}

func TestPaneLiveIncrementalFeed(t *testing.T) {
	sess := &mockSession{
		output:       []byte("first output"),
		totalWritten: 12,
		alive:        true,
		cols:         40,
		rows:         10,
	}
	p := New()
	p.SetSession(sess)

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(40, 10)

	// First render
	p.Render(screen, 0, 0, 40, 10)

	// Simulate more output
	sess.output = []byte("first outputmore data")
	sess.totalWritten = 21

	// Second render — should only feed new bytes
	p.Render(screen, 0, 0, 40, 10)

	if p.fedTotal != 21 {
		t.Errorf("fedTotal = %d, want 21", p.fedTotal)
	}
}

func TestPaneRingBufferWrap(t *testing.T) {
	sess := &mockSession{
		output:       []byte("new data after wrap"),
		totalWritten: 500000, // much larger than buffer
		alive:        true,
		cols:         40,
		rows:         10,
	}
	p := New()
	p.SetSession(sess)
	p.fedTotal = 100 // pretend we've only seen 100 bytes

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(40, 10)

	// Should detect wrap and full reset
	p.Render(screen, 0, 0, 40, 10)
	if p.fedTotal != 500000 {
		t.Errorf("fedTotal = %d, want 500000 after wrap reset", p.fedTotal)
	}
}

func TestFindContentRows(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 10))
	vt.Write([]byte("\n\nhello\nworld\n"))

	vt.Lock()
	defer vt.Unlock()

	last := findLastContentRow(vt, 20, 10)
	if last < 2 {
		t.Errorf("findLastContentRow = %d, want >= 2", last)
	}

	first := findFirstContentRow(vt, 20, last)
	if first > 3 {
		t.Errorf("findFirstContentRow = %d, want <= 3", first)
	}
}
