package widget

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
)

func TestAttentionBar_DesiredHeight(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want int
	}{
		{"empty", 0, 0},
		{"one", 1, 3},
		{"two", 2, 4},
		{"at cap", AttentionMaxRows, AttentionMaxRows + 2},
		{"over cap collapses to cap", AttentionMaxRows + 5, AttentionMaxRows + 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewAttentionBar()
			entries := make([]AttentionEntry, tc.n)
			for i := range entries {
				entries[i] = AttentionEntry{TaskName: "t"}
			}
			b.SetEntries(entries)
			testutil.Equal(t, b.DesiredHeight(), tc.want)
		})
	}
}

func TestAttentionBar_OnHeightChangeFiresOnTransition(t *testing.T) {
	b := NewAttentionBar()
	calls := 0
	b.OnHeightChange = func() { calls++ }

	b.SetEntries([]AttentionEntry{{TaskName: "a"}})
	testutil.Equal(t, calls, 1)

	// Same height — no extra fire.
	b.SetEntries([]AttentionEntry{{TaskName: "b"}})
	testutil.Equal(t, calls, 1)

	// Height changes from 3 to 4.
	b.SetEntries([]AttentionEntry{{TaskName: "a"}, {TaskName: "b"}})
	testutil.Equal(t, calls, 2)

	// Back to empty.
	b.SetEntries(nil)
	testutil.Equal(t, calls, 3)
}

func TestAttentionBar_DrawEmpty_NoBorder(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(20, 6)

	b := NewAttentionBar()
	b.Box.SetRect(0, 0, 20, 0) // zero-height box reflects desiredHeight=0
	b.Draw(screen)
	screen.Show()

	// With no entries, we expect no border characters anywhere.
	for x := 0; x < 20; x++ {
		for y := 0; y < 6; y++ {
			r, _, _, _ := screen.GetContent(x, y)
			if r == '╭' || r == '╮' || r == '─' {
				t.Fatalf("found border rune %q at %d,%d when bar should be hidden", r, x, y)
			}
		}
	}
}

func TestAttentionBar_DrawShowsTaskNames(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(30, 6)

	b := NewAttentionBar()
	b.SetEntries([]AttentionEntry{
		{TaskName: "alpha"},
		{TaskName: "beta"},
	})
	b.Box.SetRect(0, 0, 30, b.DesiredHeight())
	b.Draw(screen)
	screen.Show()

	got := dumpScreen(screen, 30, b.DesiredHeight())
	if !strings.Contains(got, "alpha") {
		t.Errorf("expected 'alpha' in rendered output, got:\n%s", got)
	}
	if !strings.Contains(got, "beta") {
		t.Errorf("expected 'beta' in rendered output, got:\n%s", got)
	}
	// Top border should be drawn.
	if !strings.ContainsRune(got, '╭') {
		t.Errorf("expected border top-left corner in output, got:\n%s", got)
	}
	// Icon rune should be present.
	if !strings.ContainsRune(got, theme.IconNeedsInput) {
		t.Errorf("expected IconNeedsInput in output, got:\n%s", got)
	}
	// The icon cell at the first inner column of the first entry row
	// must render in the needs-input (orange) style so it matches the
	// task-list badge. Inner col 1 / row 1 accounts for the border.
	r, _, style, _ := screen.GetContent(1, 1)
	if r != theme.IconNeedsInput {
		t.Errorf("icon cell rune = %q, want IconNeedsInput", r)
	}
	if style != theme.StyleNeedsInput {
		t.Errorf("icon cell style = %v, want StyleNeedsInput", style)
	}
}

func TestAttentionBar_OverflowSummary(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(30, 20)

	b := NewAttentionBar()
	entries := make([]AttentionEntry, AttentionMaxRows+3)
	for i := range entries {
		entries[i] = AttentionEntry{TaskName: "task-" + string(rune('a'+i))}
	}
	b.SetEntries(entries)
	b.Box.SetRect(0, 0, 30, b.DesiredHeight())
	b.Draw(screen)
	screen.Show()

	got := dumpScreen(screen, 30, b.DesiredHeight())
	// Overflow line takes the last interior row.
	if !strings.Contains(got, "+ 4 more") {
		t.Errorf("expected '+ 4 more' summary in output, got:\n%s", got)
	}
}

func TestAttentionBar_TruncatesLongNames(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(12, 4)

	b := NewAttentionBar()
	b.SetEntries([]AttentionEntry{{TaskName: "a-very-long-task-name"}})
	b.Box.SetRect(0, 0, 12, b.DesiredHeight())
	b.Draw(screen)
	screen.Show()

	got := dumpScreen(screen, 12, b.DesiredHeight())
	if !strings.ContainsRune(got, '…') {
		t.Errorf("expected ellipsis in truncated output, got:\n%s", got)
	}
}

func dumpScreen(s tcell.Screen, w, h int) string {
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, _, _, _ := s.GetContent(x, y)
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
