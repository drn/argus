package planview

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// drawToString renders the widget at (0,0,w,h) onto a SimulationScreen and
// returns the full screen contents as a newline-joined string (trailing
// spaces trimmed per row), so render assertions can scan for chip text.
func drawToString(t *testing.T, w *Widget, width, height int) string {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(width, height)
	w.SetRect(0, 0, width, height)
	w.Draw(sim)
	sim.Show()
	cells, _, _ := sim.GetContents()
	var b strings.Builder
	for r := 0; r < height; r++ {
		row := make([]rune, 0, width)
		for c := 0; c < width; c++ {
			cell := cells[r*width+c]
			if len(cell.Runes) > 0 {
				row = append(row, cell.Runes[0])
			} else {
				row = append(row, ' ')
			}
		}
		b.WriteString(strings.TrimRight(string(row), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestState_Glyph(t *testing.T) {
	tests := []struct {
		st   State
		want rune
	}{
		{StatePlanned, '○'},
		{StateWorking, '⟳'},
		{StateInReview, '◔'},
		{StateDone, '✓'},
		{StateFailed, '✕'},
		{StatePending, '·'},
	}
	for _, tt := range tests {
		t.Run(string(tt.want), func(t *testing.T) {
			testutil.Equal(t, tt.st.Glyph(), tt.want)
		})
	}
}

func TestDraw_EmptyNoPanic(t *testing.T) {
	w := New()
	// No SetData: stages empty. Must render the placeholder without panic.
	out := drawToString(t, w, 50, 12)
	testutil.Contains(t, out, "No plan")
}

func TestDraw_DefaultTitleAndOverride(t *testing.T) {
	w := New()
	w.SetData([]Node{node("1a")}, nil)
	out := drawToString(t, w, 50, 12)
	testutil.Contains(t, out, "Plan")

	w.SetTitle(" Details ▸ child · Plan ")
	out = drawToString(t, w, 50, 12)
	testutil.Contains(t, out, "child")
}

func TestDraw_PlannedChipGlyphRendered(t *testing.T) {
	w := New()
	w.SetData([]Node{node("1a"), node("2a")}, []Edge{{From: "1a", To: "2a"}})
	out := drawToString(t, w, 50, 12)
	// Both planned chips carry the violet ○ glyph and their short-id labels.
	testutil.Contains(t, out, "○")
	testutil.Contains(t, out, "1a")
	testutil.Contains(t, out, "2a")
}

func TestDraw_GroupBoxRendered(t *testing.T) {
	w := fanGroup("2a", "2b", "2c")
	out := drawToString(t, w, 60, 12)
	// The collapsed group box and its aggregate counts render.
	testutil.Contains(t, out, "[2a–2c]")
}

func TestDraw_NoPlanHint(t *testing.T) {
	w := New()
	w.SetData([]Node{liveNode("w1", StateWorking), liveNode("w2", StateWorking)}, nil)
	out := drawToString(t, w, 60, 12)
	testutil.Contains(t, out, "no plan authored")
}

func TestGroupCounts_OmitsZeroStates(t *testing.T) {
	g := &Group{Counts: map[State]int{StateDone: 3, StateWorking: 2, StatePlanned: 1}}
	got := groupCounts(g)
	// Order follows the enum-stable list: done, working, planned.
	testutil.Equal(t, got, "3 ✓ · 2 ⟳ · 1 ○")
}

func TestBranchChange_FiresOnStructuralChange(t *testing.T) {
	w := New()
	var fired int
	w.OnBranchChange = func() { fired++ }
	w.SetData([]Node{node("1a")}, nil)
	testutil.Equal(t, fired >= 1, true)
	prev := fired
	// Adding a node + edge is a structural change → fires again.
	w.SetData([]Node{node("1a"), node("2a")}, []Edge{{From: "1a", To: "2a"}})
	testutil.Equal(t, fired > prev, true)
}

func TestTruncateLabel_RuneAware(t *testing.T) {
	// A long name truncates to fallbackLabelRunes with an ellipsis, rune-counted.
	long := "verylongrolenamehere"
	got := truncateLabel(long)
	testutil.Equal(t, len([]rune(got)) <= fallbackLabelRunes, true)
	testutil.Equal(t, strings.HasSuffix(got, "…"), true)
}
