package planview

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
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

// TestDraw_LiveNodeUsesStampedIcon (BUG-007): a node carrying a projection-
// stamped Icon renders that glyph (1:1 with the rail), NOT its State.Glyph().
func TestDraw_LiveNodeUsesStampedIcon(t *testing.T) {
	w := New()
	// State says working (⟳) but the stamped Icon is the rail's moon-outline (idle):
	// the render must use the Icon, proving Icon wins over State.Glyph().
	w.SetData([]Node{{
		ID: "1a", Name: "1a-role", State: StateWorking,
		Icon: &NodeIcon{Glyph: theme.IconMoonOutline, Style: theme.StyleInReview},
	}}, nil)
	w.SetFocused(true)
	out := drawToString(t, w, 50, 14)
	testutil.Contains(t, out, string(theme.IconMoonOutline))
	testutil.Equal(t, strings.ContainsRune(out, '⟳'), false) // State glyph NOT used
}

// TestDraw_AnimatedIconRendersSpinnerFrame (BUG-007): an Animated icon renders a
// live spinner frame (re-resolved at Draw), not the stored placeholder glyph.
func TestDraw_AnimatedIconRendersSpinnerFrame(t *testing.T) {
	w := New()
	w.SetData([]Node{{
		ID: "1a", Name: "1a-role", State: StateWorking,
		Icon: &NodeIcon{Glyph: 'X', Style: theme.StyleInProgress, Animated: true},
	}}, nil)
	w.SetFocused(true)
	out := drawToString(t, w, 50, 14)
	// The stored placeholder 'X' must NOT appear; a real spinner frame does.
	testutil.Equal(t, strings.ContainsRune(out, 'X'), false)
	testutil.Equal(t, strings.ContainsRune(out, widget.SpinnerFrame(planSpinnerFrame())), true)
}

func TestDraw_GroupBoxRendered(t *testing.T) {
	w := fanGroup("2a", "2b", "2c")
	// Boxes are 3 rows tall; give the region enough height for the header + two
	// stacked boxes + footer so the collapsed group's label row is visible.
	out := drawToString(t, w, 60, 18)
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

// TestDraw_FannedGroupShowsMemberBoxes is the core bug-2 fix in its boxed form:
// pressing Enter on a collapsed group must EXPAND the diagram into individual
// member node-boxes inside a SOLID rounded enclosure (BUG-005), not keep
// rendering the collapsed "[2a–2c]" range box.
func TestDraw_FannedGroupShowsMemberBoxes(t *testing.T) {
	w := fanGroup("2a", "2b", "2c")
	w.SetFocused(true)
	// Collapsed: the dashed range box shows the [2a–2c] label.
	collapsed := drawToString(t, w, 70, 18)
	testutil.Contains(t, collapsed, "[2a–2c]")

	// Move to the group (stage 1) and fan it out.
	w.MoveStage(1)
	_, isGroup := w.GroupAt(w.CursorPos().Stage, w.CursorPos().Slot)
	testutil.Equal(t, isGroup, true)
	w.ActivateCursor() // fan out

	fanned := drawToString(t, w, 70, 18)
	// All three member short-ids render on the SAME row (their boxes' middle row).
	memberRow := rowOf(fanned, "2b")
	testutil.Contains(t, memberRow, "2a")
	testutil.Contains(t, memberRow, "2c")
	// The expansion is wrapped in a solid rounded enclosure with a ▲ collapse
	// affordance; the vertical role label ("role" → first rune "r") rides the left
	// inner edge.
	testutil.Contains(t, fanned, "▲")
	testutil.Contains(t, fanned, "╭")
	// The collapsed range-box label is GONE once fanned.
	testutil.Equal(t, strings.Contains(fanned, "[2a–2c]"), false)
}

// TestDraw_CollapsedGroupStillShowsDashedRangeBox: a group the cursor has NOT
// fanned renders as the dashed range box (expansion scoped to the fanned slot).
func TestDraw_CollapsedGroupStillShowsDashedRangeBox(t *testing.T) {
	w := fanGroup("2a", "2b", "2c")
	w.SetFocused(true)
	// Cursor parked at stage 0 (lone 0a) — the group at stage 1 stays collapsed.
	out := drawToString(t, w, 70, 18)
	testutil.Contains(t, out, "[2a–2c]")
	testutil.Contains(t, out, "╌") // dashed border rune
}

// TestDraw_CollapsedGroupTwoLineFormat is the BUG-005 collapsed-box format: a
// FULL-feed group renders top line `[range] → <target>` and sub line
// `<role token> · <per-state counts>`. The bare planned-count must NOT trail the
// range label (that read as "blocks 3a").
func TestDraw_CollapsedGroupTwoLineFormat(t *testing.T) {
	w := New()
	// {2d,2e,2f}=drafting all feed 3a (full feed → "→ 3a"); 1a blocks them.
	w.SetData(
		[]Node{
			liveNode("1a", StateDone),
			{ID: "2d", Name: "2d-drafting", State: StatePlanned, Planned: true},
			{ID: "2e", Name: "2e-drafting", State: StatePlanned, Planned: true},
			{ID: "2f", Name: "2f-drafting", State: StatePlanned, Planned: true},
			{ID: "3a", Name: "3a-final", State: StatePlanned, Planned: true},
		},
		[]Edge{
			{From: "1a", To: "2d"}, {From: "1a", To: "2e"}, {From: "1a", To: "2f"},
			{From: "2d", To: "3a"}, {From: "2e", To: "3a"}, {From: "2f", To: "3a"},
		},
	)
	w.SetFocused(true)
	out := drawToString(t, w, 72, 18)
	// Top line: range + full-feed arrow to the single target.
	topRow := rowOf(out, "[2d–2f]")
	testutil.Contains(t, topRow, "→ 3a")
	// Sub line: role token + per-state counts.
	subRow := rowOf(out, "drafting")
	testutil.Contains(t, subRow, "3 ○")
	// The range label must NOT carry a bare trailing count (the old "[2d–2f] 3 ○").
	testutil.Equal(t, strings.Contains(topRow, "3 ○"), false)
}

// TestDraw_CollapsedGroupPartialFeedTopLine: a PARTIAL-feed group (only some
// members feed downstream) shows `↘` on the top line, not `→ <target>`.
func TestDraw_CollapsedGroupPartialFeedTopLine(t *testing.T) {
	w := New()
	// {2a,2b,2c}=research, only 2b feeds 3a → partial.
	w.SetData(
		[]Node{
			liveNode("1a", StateDone),
			{ID: "2a", Name: "2a-research", State: StateDone},
			{ID: "2b", Name: "2b-research", State: StatePlanned, Planned: true},
			{ID: "2c", Name: "2c-research", State: StatePlanned, Planned: true},
			{ID: "3a", Name: "3a-final", State: StatePlanned, Planned: true},
		},
		[]Edge{
			{From: "1a", To: "2a"}, {From: "1a", To: "2b"}, {From: "1a", To: "2c"},
			{From: "2b", To: "3a"},
		},
	)
	w.SetFocused(true)
	out := drawToString(t, w, 72, 18)
	topRow := rowOf(out, "[2a–2c]")
	testutil.Contains(t, topRow, "↘")
	testutil.Equal(t, strings.Contains(topRow, "→"), false) // partial, not a full-feed arrow
	subRow := rowOf(out, "research")
	testutil.Contains(t, subRow, "✓") // counts present on the sub line
}

// TestDraw_FannedMemberCursorBoxSelected: the cursor's member box inside a fanned
// group carries the subtle selection BACKGROUND fill (primary cue) + the selection
// border foreground (secondary); a different member's box has neither.
func TestDraw_FannedMemberCursorBoxSelected(t *testing.T) {
	w := fanGroup("2a", "2b", "2c")
	w.SetFocused(true)
	w.MoveStage(1)
	w.ActivateCursor() // fan out, cursor lands on member 0 (2a)
	w.MoveSlot(1)      // walk to member 1 (2b)
	testutil.Equal(t, w.CurrentNodeID(), "2b")

	sc := drawToSim(t, w, 70, 18)
	// Search the box content form "○ 2b" (glyph+id, only in a box — the header
	// shows the full role name "2b-role", never the box form), so the cell + corner
	// resolve the member box, not the panel/header.
	bx, by, ok := findStringCell(sc, "○ 2b")
	testutil.Equal(t, ok, true)
	// The 2b content cell carries the selection background (primary cue).
	cells, scw, _ := sc.GetContents()
	_, selBg, _ := cells[by*scw+bx].Style.Decompose()
	testutil.Equal(t, selBg, selectionBG)
	// And its box border keeps the bright selection foreground (secondary cue).
	bSel, ok := boxCornerStyleNear(sc, bx, by)
	testutil.Equal(t, ok, true)
	bfg, _, _ := bSel.Decompose()
	testutil.Equal(t, bfg, theme.ColorSelected)

	// 2a's box (non-cursor member): no selection background, border not selection fg.
	ax, ay, ok2 := findStringCell(sc, "○ 2a")
	testutil.Equal(t, ok2, true)
	_, aBg, _ := cells[ay*scw+ax].Style.Decompose()
	testutil.Equal(t, aBg == selectionBG, false)
	aSel, ok := boxCornerStyleNear(sc, ax, ay)
	testutil.Equal(t, ok, true)
	afg, _, _ := aSel.Decompose()
	testutil.Equal(t, afg == theme.ColorSelected, false)
}

// TestDraw_FannedPartialFeedMemberCarriesMarker: when a group partially feeds a
// downstream node, the fanned-out feeding member box carries the ↘ marker (D5).
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
	testutil.Equal(t, g.FeedingMembers["1b"], true)

	// Fan out the group; only the feeding member box's content row carries ↘.
	w.MoveStage(1)
	w.ActivateCursor()
	out := drawToString(t, w, 70, 18)
	// The ↘ rides 1b's row (its box content), and 1b's box only.
	feedRow := rowOf(out, "1b")
	testutil.Contains(t, feedRow, "↘")
}

// findStringCell returns the (x,y) of the first cell that begins a row-contiguous
// run matching s (rune by rune), scanning row-major. ok false when not found.
func findStringCell(sc tcell.SimulationScreen, s string) (int, int, bool) {
	want := []rune(s)
	cells, w, h := sc.GetContents()
	cellRune := func(x, y int) rune {
		c := cells[y*w+x]
		if len(c.Runes) > 0 {
			return c.Runes[0]
		}
		return ' '
	}
	for y := 0; y < h; y++ {
		for x := 0; x+len(want) <= w; x++ {
			ok := true
			for i, r := range want {
				if cellRune(x+i, y) != r {
					ok = false
					break
				}
			}
			if ok {
				return x, y, true
			}
		}
	}
	return 0, 0, false
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
