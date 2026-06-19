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

// rowOf returns the diagram row (from drawToString output) that contains substr,
// or "" when none does. Used to assert chip text lands on the expanded stage row.
func rowOf(out, substr string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	return ""
}

// TestDraw_FannedGroupShowsMemberChips is the core bug-2 fix: pressing Enter on a
// collapsed group must EXPAND the diagram into individual member chips (glyph +
// short-id), not keep rendering the collapsed "[2a–2c]" range box. This is the
// user's "[2a–2c] never expands" complaint.
func TestDraw_FannedGroupShowsMemberChips(t *testing.T) {
	w := fanGroup("2a", "2b", "2c")
	w.SetFocused(true)
	// Collapsed: the range box is shown.
	collapsed := drawToString(t, w, 60, 14)
	testutil.Contains(t, collapsed, "[2a–2c]")

	// Move to the group (stage 1) and fan it out.
	w.MoveStage(1)
	_, isGroup := w.GroupAt(w.CursorPos().Stage, w.CursorPos().Slot)
	testutil.Equal(t, isGroup, true)
	w.ActivateCursor() // fan out

	fanned := drawToString(t, w, 60, 14)
	// The expanded stage row carries all three member short-ids as separate chips.
	// (Find it by a row that holds 2b AND 2c — only the diagram member row does;
	// the header shows a single member's full role name, never all three.)
	memberRow := rowOf(fanned, "2b")
	testutil.Contains(t, memberRow, "2a")
	testutil.Contains(t, memberRow, "2c")
	// The collapsed range-box label is GONE for that stage once fanned.
	testutil.Equal(t, strings.Contains(fanned, "[2a–2c]"), false)
}

// TestDraw_CollapsedGroupStillShowsRangeBox: a group the cursor has NOT fanned
// renders as the range box (the expansion is scoped to the fanned slot only).
func TestDraw_CollapsedGroupStillShowsRangeBox(t *testing.T) {
	w := fanGroup("2a", "2b", "2c")
	w.SetFocused(true)
	// Cursor parked at stage 0 (lone 0a) — the group at stage 1 stays collapsed.
	out := drawToString(t, w, 60, 14)
	testutil.Contains(t, out, "[2a–2c]")
}

// TestDraw_FannedMemberCursorHighlighted: the cursor's member chip inside a
// fanned group carries the reverse-video highlight; a different member does not.
// This realizes the member-level highlight (bug-3) on the expanded diagram.
func TestDraw_FannedMemberCursorHighlighted(t *testing.T) {
	w := fanGroup("2a", "2b", "2c")
	w.SetFocused(true)
	w.MoveStage(1)
	w.ActivateCursor() // fan out, cursor lands on member 0 (2a)
	w.MoveSlot(1)      // walk to member 1 (2b)
	testutil.Equal(t, w.CurrentNodeID(), "2b")

	chips := w.stageRowChips(1)
	// Three member chips, one per member.
	testutil.Equal(t, len(chips), 3)
	// The cursor member (index 1, "2b") is reversed; index 0 ("2a") is not.
	_, _, cur := chips[1].style.Decompose()
	_, _, other := chips[0].style.Decompose()
	testutil.Equal(t, cur&tcell.AttrReverse != 0, true)
	testutil.Equal(t, other&tcell.AttrReverse != 0, false)
}

// TestDraw_FannedPartialFeedMemberCarriesMarker: when a group partially feeds a
// downstream node, the fanned-out feeding member chip carries the ↘ marker (D5).
func TestDraw_FannedPartialFeedMemberCarriesMarker(t *testing.T) {
	w := New()
	// stage0 [0a] -> group {1a,1b,1c} at stage1; only 1b feeds a stage-2 node.
	w.SetData(
		[]Node{node("0a"), node("1a"), node("1b"), node("1c"), node("2a")},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"}, {From: "0a", To: "1c"},
			{From: "1b", To: "2a"}, // only 1b feeds downstream → partial feed
		},
	)
	w.SetFocused(true)
	g, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	testutil.Equal(t, g.PartialFeed, true)
	testutil.Equal(t, g.FeedingMember, "1b")

	// Fan out the group and assert only the feeding member's chip carries ↘.
	w.MoveStage(1)
	w.ActivateCursor()
	chips := w.stageRowChips(1)
	var feederHasMarker, otherHasMarker bool
	for _, c := range chips {
		if strings.Contains(c.label, "1b") && strings.Contains(c.label, "↘") {
			feederHasMarker = true
		}
		if (strings.Contains(c.label, "1a") || strings.Contains(c.label, "1c")) && strings.Contains(c.label, "↘") {
			otherHasMarker = true
		}
	}
	testutil.Equal(t, feederHasMarker, true)
	testutil.Equal(t, otherHasMarker, false)
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
