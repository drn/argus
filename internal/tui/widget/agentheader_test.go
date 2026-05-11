package widget

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestAgentHeader_SetTaskName(t *testing.T) {
	h := NewAgentHeader()

	if h.taskName != "" {
		t.Errorf("initial taskName = %q, want empty", h.taskName)
	}

	h.SetTaskName("fix-login-bug")
	if h.taskName != "fix-login-bug" {
		t.Errorf("taskName = %q, want %q", h.taskName, "fix-login-bug")
	}

	h.SetTaskName("")
	if h.taskName != "" {
		t.Errorf("taskName = %q, want empty", h.taskName)
	}
}

func TestAgentHeader_ClipboardHint(t *testing.T) {
	h := NewAgentHeader()
	if h.ClipboardHint() {
		t.Error("default clipboard hint should be off")
	}
	h.SetClipboardHint(true)
	if !h.ClipboardHint() {
		t.Error("SetClipboardHint(true) should turn it on")
	}
	h.SetClipboardHint(false)
	if h.ClipboardHint() {
		t.Error("SetClipboardHint(false) should turn it off")
	}
}

func parsedDiffEmpty() gitutil.ParsedDiff {
	return gitutil.ParsedDiff{}
}

// newSim returns a SimulationScreen of the given size, registering Fini cleanup.
func newSim(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	sim.SetSize(w, h)
	t.Cleanup(sim.Fini)
	return sim
}

func TestAgentHeader_Draw_Basic(t *testing.T) {
	sim := newSim(t, 80, 1)

	h := NewAgentHeader()
	h.SetRect(0, 0, 80, 1)
	h.SetTaskName("my-task")
	h.Draw(sim)

	row := readAllScreenText(sim, 80, 1)
	testutil.Contains(t, row, "my-task")
}

func TestAgentHeader_Draw_EmptyTaskName(t *testing.T) {
	sim := newSim(t, 80, 1)

	h := NewAgentHeader()
	h.SetRect(0, 0, 80, 1)
	// No task name — should still draw (just a blank row).
	h.Draw(sim)
	// Verify the row exists (no panic) and is filled with spaces.
	row := readAllScreenText(sim, 80, 1)
	testutil.Equal(t, strings.TrimSpace(row), "")
}

func TestAgentHeader_Draw_ZeroWidth(t *testing.T) {
	sim := newSim(t, 80, 1)
	h := NewAgentHeader()
	h.SetRect(0, 0, 0, 1) // zero width — early return
	h.SetTaskName("anything")
	h.Draw(sim) // must not panic
}

func TestAgentHeader_Draw_WithClipboardHint(t *testing.T) {
	sim := newSim(t, 80, 1)
	h := NewAgentHeader()
	h.SetRect(0, 0, 80, 1)
	h.SetTaskName("clipboard-task")
	h.SetClipboardHint(true)
	h.Draw(sim)

	row := readAllScreenText(sim, 80, 1)
	testutil.Contains(t, row, "ctrl+y to copy")
	testutil.Contains(t, row, "clipboard-task")
}

func TestAgentHeader_Draw_ClipboardHintNarrowWidth(t *testing.T) {
	// Width too narrow to fit the hint — function returns early before drawing it.
	sim := newSim(t, 8, 1)
	h := NewAgentHeader()
	h.SetRect(0, 0, 8, 1)
	h.SetTaskName("t")
	h.SetClipboardHint(true)
	h.Draw(sim) // hintStart < x — early return path
}

func TestRuneWidth(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"hello", 5},
		{" ctrl+y to copy ", 16},
	} {
		t.Run(tc.in, func(t *testing.T) {
			testutil.Equal(t, runeWidth(tc.in), tc.want)
		})
	}
}

func TestDrawStyledLine(t *testing.T) {
	sim := newSim(t, 20, 2)

	style := tcell.StyleDefault.Foreground(tcell.ColorRed)
	cells := []StyledChar{
		{Ch: 'h', Style: style},
		{Ch: 'i', Style: style},
		{Ch: '!', Style: style},
	}
	DrawStyledLine(sim, 2, 0, 10, cells)

	r0, _, _, _ := sim.GetContent(2, 0)
	r1, _, _, _ := sim.GetContent(3, 0)
	r2, _, _, _ := sim.GetContent(4, 0)
	testutil.Equal(t, r0, 'h')
	testutil.Equal(t, r1, 'i')
	testutil.Equal(t, r2, '!')
}

func TestDrawStyledLine_TruncatesAtMaxW(t *testing.T) {
	sim := newSim(t, 20, 2)
	cells := []StyledChar{
		{Ch: 'a', Style: tcell.StyleDefault},
		{Ch: 'b', Style: tcell.StyleDefault},
		{Ch: 'c', Style: tcell.StyleDefault},
	}
	// maxW=2 — only first two cells written.
	DrawStyledLine(sim, 0, 0, 2, cells)
	r0, _, _, _ := sim.GetContent(0, 0)
	r1, _, _, _ := sim.GetContent(1, 0)
	r2, _, _, _ := sim.GetContent(2, 0)
	testutil.Equal(t, r0, 'a')
	testutil.Equal(t, r1, 'b')
	// Cell at col 2 should NOT have been written (default to space).
	testutil.Equal(t, r2, ' ')
}

func TestPlainLine(t *testing.T) {
	hl := plainLine("hi")
	testutil.Equal(t, len(hl.Cells), 2)
	testutil.Equal(t, hl.Cells[0].Ch, 'h')
	testutil.Equal(t, hl.Cells[1].Ch, 'i')
	// All cells should have default style.
	for _, c := range hl.Cells {
		testutil.Equal(t, c.Style, tcell.StyleDefault)
	}
}

func TestPlainLine_Empty(t *testing.T) {
	hl := plainLine("")
	testutil.Equal(t, len(hl.Cells), 0)
}

func TestHighlightLines_FallbackUsesPlainLine(t *testing.T) {
	// Unknown extension forces the no-lexer fallback through plainLine.
	hl := HighlightLines([]string{"hello", ""}, "weird-file.unknownext")
	testutil.Equal(t, len(hl), 2)
	testutil.Equal(t, len(hl[0].Cells), 5)
	testutil.Equal(t, len(hl[1].Cells), 0)
}

func TestSpinnerTickInterval(t *testing.T) {
	t.Cleanup(func() { SetActiveSpinner("progress") })

	SetActiveSpinner("progress")
	d := SpinnerTickInterval()
	if d <= 0 {
		t.Errorf("expected positive tick interval for progress, got %v", d)
	}

	SetActiveSpinner("classic")
	classic := SpinnerTickInterval()
	if classic <= 0 {
		t.Errorf("expected positive tick interval for classic, got %v", classic)
	}
}

func TestStatusBar_SetRunning(t *testing.T) {
	sb := NewStatusBar()
	sb.SetTasks([]*model.Task{
		{ID: "a", Status: model.StatusInProgress},
		{ID: "b", Status: model.StatusInProgress},
		{ID: "c", Status: model.StatusInProgress},
	})

	// No running ids — none counted active.
	sim := newSim(t, 100, 1)
	sb.SetRect(0, 0, 100, 1)
	sb.Draw(sim)
	row := readAllScreenText(sim, 100, 1)
	testutil.Contains(t, row, "0 active")

	// Mark two as running.
	sb.SetRunning([]string{"a", "b"})
	sb.Draw(sim)
	row = readAllScreenText(sim, 100, 1)
	testutil.Contains(t, row, "2 active")
}

// ---------- Hit tiny edge-case branches ----------

func TestDrawText_ZeroWidth(t *testing.T) {
	sim := newSim(t, 10, 1)
	DrawText(sim, 0, 0, 0, "hello", tcell.StyleDefault)
	r, _, _, _ := sim.GetContent(0, 0)
	if r != ' ' {
		t.Errorf("DrawText with maxWidth=0 should not write anything, got %c", r)
	}
}

func TestDrawBorder_TooSmall(t *testing.T) {
	sim := newSim(t, 5, 5)
	DrawBorder(sim, 0, 0, 1, 5, tcell.StyleDefault) // w<2 → return
	DrawBorder(sim, 0, 0, 5, 1, tcell.StyleDefault) // h<2 → return
}

func TestSplitLines_ZeroMaxWidth(t *testing.T) {
	// maxWidth <= 0 → defaults to 80.
	got := splitLines([]byte("hello"), 0)
	testutil.Equal(t, len(got), 1)
}

func TestTruncStr_NoTruncation(t *testing.T) {
	got := truncStr("hi", 10)
	testutil.Equal(t, got, "hi")
}

func TestTruncStr_Truncates(t *testing.T) {
	got := truncStr("hello world", 5)
	testutil.Equal(t, got, "hello")
}

func TestBuildUnifiedDiffLines_EmptyParsed(t *testing.T) {
	// Empty parsed diff hits the early-return path.
	lines := BuildUnifiedDiffLines(parsedDiffEmpty(), "x.go")
	if lines != nil {
		t.Errorf("empty parsed diff should return nil, got %d lines", len(lines))
	}
}

func TestBuildSideBySideDiffLines_EmptyParsed(t *testing.T) {
	lines := BuildSideBySideDiffLines(parsedDiffEmpty(), "x.go", 80)
	if lines != nil {
		t.Errorf("empty parsed diff should return nil, got %d lines", len(lines))
	}
}

func TestBanner_NarrowWidth(t *testing.T) {
	// padLeft would go negative — exercises the clamp branches.
	sim := newSim(t, 5, 30)
	h := DrawBanner(sim, 0, 0, 5)
	if h <= 0 {
		t.Errorf("DrawBanner returned %d, expected positive", h)
	}
}

func TestBanner_ZeroWidth(t *testing.T) {
	sim := newSim(t, 1, 1)
	h := DrawBanner(sim, 0, 0, 0)
	testutil.Equal(t, h, 0)
}

func TestFadeDashes_ZeroLength(t *testing.T) {
	got := FadeDashes(0, false)
	testutil.Equal(t, got, "")
}

func TestDrawGradientChars_Empty(t *testing.T) {
	sim := newSim(t, 5, 1)
	// Empty pattern — early return.
	DrawGradientChars(sim, 0, 0, "", rgbVal{0, 0, 0}, rgbVal{255, 255, 255})
}

func TestDrawGradientUnderline_NarrowWidth(t *testing.T) {
	sim := newSim(t, 5, 1)
	// Width smaller than textWidth → padLeft<0 branch.
	DrawGradientUnderline(sim, 0, 0, 5, 100, []tcell.Color{tcell.ColorRed, tcell.ColorBlue})
}

func TestHighlight_Tokenize_StyleNotFound(t *testing.T) {
	// Even with normal lexer, exercise different token paths via plain text input.
	hl := HighlightLines([]string{""}, "test.go")
	testutil.Equal(t, len(hl), 1)
}

func TestAgentHeader_Draw_VeryNarrow(t *testing.T) {
	// Very small width — exercises col<x branch.
	sim := newSim(t, 2, 1)
	h := NewAgentHeader()
	h.SetRect(0, 0, 2, 1)
	h.SetTaskName("very-long-name-that-wont-fit-in-2-cols")
	h.Draw(sim)
}

func TestStatusBar_SetTab_ChangesHints(t *testing.T) {
	sb := NewStatusBar()
	sim := newSim(t, 200, 1)
	sb.SetRect(0, 0, 200, 1)

	for _, tc := range []struct {
		name      string
		tab       Tab
		needs     []string
		notExpect []string
	}{
		{
			name:      "tasks",
			tab:       TabTasks,
			needs:     []string{"new", "attach"},
			notExpect: []string{"new project"},
		},
		{
			name:      "settings",
			tab:       TabSettings,
			needs:     []string{"new project"},
			notExpect: []string{"attach"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sb.SetTab(tc.tab)
			sb.Draw(sim)
			row := readAllScreenText(sim, 200, 1)
			for _, want := range tc.needs {
				testutil.Contains(t, row, want)
			}
			for _, no := range tc.notExpect {
				if strings.Contains(row, no) {
					t.Errorf("tab %s: row should NOT contain %q\n%s", tc.name, no, row)
				}
			}
		})
	}
}
