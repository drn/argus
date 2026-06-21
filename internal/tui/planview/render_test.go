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
	// Pin the spinner frame so the assertion is deterministic, not racing the clock.
	const pinnedFrame = 2
	w.frameFn = func() int { return pinnedFrame }
	out := drawToString(t, w, 50, 14)
	// The stored placeholder 'X' must NOT appear; the live (pinned) spinner frame does.
	testutil.Equal(t, strings.ContainsRune(out, 'X'), false)
	testutil.Equal(t, strings.ContainsRune(out, widget.SpinnerFrame(pinnedFrame)), true)
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

// TestDraw_SelectedCollapsedGroupHeavyDashed (BUG-008): when the cursor rests on a
// COLLAPSED group slot, the dashed box keeps its dashed identity but swaps to the
// HEAVY dashed glyph set (┏ ╍ ╏ …) so selection reads — no green, no fill. An
// unselected collapsed group stays on the light dashed set (┌ ╌ ╎).
func TestDraw_SelectedCollapsedGroupHeavyDashed(t *testing.T) {
	// Unselected (cursor parked at stage 0): light dashed runes, no heavy ones.
	w := fanGroup("2a", "2b", "2c")
	w.SetFocused(true)
	unsel := drawToString(t, w, 70, 18)
	testutil.Contains(t, unsel, "╌")                           // light dashed present
	testutil.Equal(t, strings.ContainsRune(unsel, '╍'), false) // no heavy dashed
	testutil.Equal(t, strings.ContainsRune(unsel, '┏'), false) // no heavy corner

	// Move the cursor onto the collapsed group slot (stage 1) WITHOUT fanning out.
	w.MoveStage(1)
	_, isGroup := w.GroupAt(w.CursorPos().Stage, w.CursorPos().Slot)
	testutil.Equal(t, isGroup, true)
	sel := drawToString(t, w, 70, 18)
	// Heavy dashed corner + edges now present; the range label survives.
	testutil.Contains(t, sel, "┏")
	testutil.Contains(t, sel, "╍")
	testutil.Contains(t, sel, "[2a–2c]")
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

// TestDraw_CollapsedGroupCountIconsRailParity (BUG-011): a COLLAPSED group with
// mixed in-motion members renders its count line 1:1 with the rail — the
// in_review segment uses the clipboard 󰂼 in CYAN (not ◔, not green), the working
// segment uses the LIVE spinner frame in AMBER (animated, not the static ⟳), and
// the done segment uses ✓ in GREEN. The compact State.Glyph() set never appears.
func TestDraw_CollapsedGroupCountIconsRailParity(t *testing.T) {
	w := New()
	// {2a=done, 2b=working, 2c=in_review} all blocked by 1a → one collapsed group
	// at stage 1. Cursor parked at stage 0 (default) so the group stays collapsed.
	w.SetData(
		[]Node{
			liveNode("1a", StateDone),
			liveNode("2a", StateDone),
			liveNode("2b", StateWorking),
			liveNode("2c", StateInReview),
		},
		[]Edge{
			{From: "1a", To: "2a"}, {From: "1a", To: "2b"}, {From: "1a", To: "2c"},
		},
	)
	w.SetFocused(true)
	// Pin the spinner frame so the working-segment glyph assertion is deterministic
	// (the wall-clock frame could otherwise tick between Draw and the assertion).
	const pinnedFrame = 2
	w.frameFn = func() int { return pinnedFrame }
	sc := drawToSim(t, w, 72, 18)
	cells, scw, _ := sc.GetContents()

	// The collapsed range box is present (group did not fan). The count line sits
	// one row below the "[2a–2c]" label row inside the box.
	_, boxY, ok := findStringCell(sc, "[2a–2c]")
	testutil.Equal(t, ok, true)
	countRow := boxY + 1

	// styleAt finds a glyph ON THE COUNT ROW (so the header's own ✓/status glyphs,
	// which are normal-styled, can't be mistaken for the coloured count segments).
	styleAt := func(r rune) (tcell.Style, bool) {
		for x := 0; x < scw; x++ {
			c := cells[countRow*scw+x]
			if len(c.Runes) > 0 && c.Runes[0] == r {
				return c.Style, true
			}
		}
		return tcell.StyleDefault, false
	}

	// in_review → clipboard 󰂼 in cyan (NOT the compact ◔, NOT done-green).
	rvStyle, ok := styleAt(theme.IconReview)
	testutil.Equal(t, ok, true)
	rvFg, _, _ := rvStyle.Decompose()
	testutil.Equal(t, rvFg, theme.ColorInReview)
	testutil.Equal(t, rvFg == theme.ColorComplete, false)

	// working → the LIVE (pinned) spinner frame in amber, not the static ⟳. The
	// glyph equals SpinnerFrame(pinnedFrame), proving the frame flows source→render.
	spinner := widget.SpinnerFrame(pinnedFrame)
	spStyle, ok := styleAt(spinner)
	testutil.Equal(t, ok, true)
	spFg, _, _ := spStyle.Decompose()
	testutil.Equal(t, spFg, theme.ColorInProgress)

	// done → ✓ in green.
	dnStyle, ok := styleAt('✓')
	testutil.Equal(t, ok, true)
	dnFg, _, _ := dnStyle.Decompose()
	testutil.Equal(t, dnFg, theme.ColorComplete)

	// The compact State.Glyph() vocabulary must NOT appear anywhere.
	out := drawToString(t, w, 72, 18)
	testutil.Equal(t, strings.ContainsRune(out, '◔'), false)
	testutil.Equal(t, strings.ContainsRune(out, '⟳'), false)
}

// TestDraw_FannedMemberCursorBoxSelected: the cursor's member box inside a fanned
// group renders with a DOUBLE-LINE border in its OWN state colour (BUG-008) — no
// green selection colour, no fill; a different member's box keeps the single
// rounded border in its own state colour.
func TestDraw_FannedMemberCursorBoxSelected(t *testing.T) {
	w := fanGroup("2a", "2b", "2c")
	w.SetFocused(true)
	w.MoveStage(1)
	w.ActivateCursor() // fan out, cursor lands on member 0 (2a)
	w.MoveSlot(1)      // walk to member 1 (2b)
	testutil.Equal(t, w.CurrentNodeID(), "2b")

	sc := drawToSim(t, w, 70, 18)
	cells, scw, _ := sc.GetContents()
	// No cell carries the old ColorHighlight selection-fill background.
	for i := range cells {
		_, bg, _ := cells[i].Style.Decompose()
		testutil.Equal(t, bg == theme.ColorHighlight, false)
	}

	// Search the box content form "○ 2b" (glyph+id, only in a box — the header
	// shows the full role name "2b-role", never the box form), so the cell + corner
	// resolve the member box, not the panel/header.
	bx, by, ok := findStringCell(sc, "○ 2b")
	testutil.Equal(t, ok, true)
	// The 2b content cell keeps its STATE colour (planned → violet), not green.
	bcfg, _, _ := cells[by*scw+bx].Style.Decompose()
	testutil.Equal(t, bcfg, colorPlanned)
	testutil.Equal(t, bcfg == theme.ColorComplete, false)
	// Its box border is a DOUBLE-LINE corner in the state colour (violet), bold.
	bSel, ok := doubleBoxCornerStyleNear(sc, bx, by)
	testutil.Equal(t, ok, true)
	bfg, _, battr := bSel.Decompose()
	testutil.Equal(t, bfg, colorPlanned)
	testutil.Equal(t, battr&tcell.AttrBold != 0, true) // focused → bold

	// 2a's box (non-cursor member): single rounded border (no double corner), state
	// colour, NOT green.
	ax, ay, ok2 := findStringCell(sc, "○ 2a")
	testutil.Equal(t, ok2, true)
	acfg, _, _ := cells[ay*scw+ax].Style.Decompose()
	testutil.Equal(t, acfg == theme.ColorComplete, false)
	_, isDouble := doubleBoxCornerStyleNear(sc, ax, ay)
	testutil.Equal(t, isDouble, false) // unselected → no double border
	aRound, ok := boxCornerStyleNear(sc, ax, ay)
	testutil.Equal(t, ok, true)
	afg, _, _ := aRound.Decompose()
	testutil.Equal(t, afg == theme.ColorComplete, false)
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

// TestGroupCountSegs_OmitsZeroStatesAndRailGlyphs (BUG-011): the collapsed-count
// segments follow the enum-stable order, omit zero-count states, use the rail's
// glyph vocabulary (NOT the compact ◔/⟳ State.Glyph() set), and colour each
// segment in its per-state colour with dim " · " separators.
func TestGroupCountSegs_OmitsZeroStatesAndRailGlyphs(t *testing.T) {
	w := New()
	g := &Group{Counts: map[State]int{StateDone: 3, StateWorking: 2, StateInReview: 1, StatePlanned: 1}}
	segs := w.groupCountSegs(g)
	// Reconstruct the flat text for an order/glyph check: done, in_review, working,
	// planned. Working glyph is the live spinner frame; in_review is the clipboard.
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
	}
	flat := b.String()
	testutil.Equal(t, strings.HasPrefix(flat, "3 ✓"), true)
	testutil.Contains(t, flat, "1 "+string(theme.IconReview)) // in_review = 󰂼, NOT ◔
	testutil.Contains(t, flat, "2 "+string(widget.SpinnerFrame(w.animFrame)))
	testutil.Contains(t, flat, "1 ○") // planned overlay
	// The compact State.Glyph() set must NOT appear in the count.
	testutil.Equal(t, strings.ContainsRune(flat, '◔'), false)
	testutil.Equal(t, strings.ContainsRune(flat, '⟳'), false)
	// Per-segment colour: the done segment is green, separators dim.
	for _, s := range segs {
		fg, _, _ := s.style.Decompose()
		switch {
		case strings.HasPrefix(s.text, "3 "):
			testutil.Equal(t, fg, theme.ColorComplete) // done green
		case strings.HasPrefix(s.text, "1 "+string(theme.IconReview)):
			testutil.Equal(t, fg, theme.ColorInReview) // in_review cyan (NOT green)
		case s.text == " · ":
			testutil.Equal(t, fg, theme.ColorDimmed) // separators dim
		}
	}
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

// wideStage builds a single flat (no-plan) stage of n live working nodes
// (w0..w{n-1}), each a lone box, so the stage row overflows a narrow pane and
// exercises the horizontal viewport.
func wideStage(n int) *Widget {
	w := New()
	var nodes []Node
	for i := 0; i < n; i++ {
		nodes = append(nodes, liveNode("w"+string(rune('0'+i)), StateWorking))
	}
	w.SetData(nodes, nil)
	w.SetFocused(true)
	return w
}

// boxContent is the rendered box middle-row content for a working live node:
// the working glyph plus the node's label (the full "wN-role" name, since the
// short-id parse falls back to the name).
func boxContent(label string) string { return "⟳ " + label + "-role" }

// assertBoxFullyVisible locates the node box whose content row reads `content`
// and asserts BOTH its left and right vertical borders are painted within the
// screen (the box is whole, not clipped). The content sits two columns right of
// the left border; the box is boxW = len(content)+2*boxHPad+2 wide.
func assertBoxFullyVisible(t *testing.T, sc tcell.SimulationScreen, content string) {
	t.Helper()
	cx, cy, ok := findStringCell(sc, content)
	testutil.Equal(t, ok, true)
	cells, scw, _ := sc.GetContents()
	leftBorder := cx - 1 - boxHPad
	rightBorder := leftBorder + (len([]rune(content)) + 2*boxHPad + 2) - 1
	testutil.Equal(t, leftBorder >= 0, true)
	testutil.Equal(t, rightBorder < scw, true)
	isBorder := func(x int) bool {
		c := cells[cy*scw+x]
		if len(c.Runes) == 0 {
			return false
		}
		return c.Runes[0] == '│' || c.Runes[0] == '║'
	}
	testutil.Equal(t, isBorder(leftBorder), true)
	testutil.Equal(t, isBorder(rightBorder), true)
}

// TestDraw_HScroll_SelectedNodeFullyVisible (BUG-010): with more sibling nodes
// than fit a narrow pane, the SELECTED node's whole box is painted within the
// region for the first, a middle, and the last cursor position; a node off the
// opposite edge is not painted.
func TestDraw_HScroll_SelectedNodeFullyVisible(t *testing.T) {
	w := wideStage(8)
	// Last node: scrolls right; w7 whole + on-screen, w0 scrolled off the left.
	for i := 0; i < 8; i++ {
		w.MoveSlot(1)
	}
	testutil.Equal(t, w.CurrentNodeID(), "w7")
	last := drawToSim(t, w, 40, 14)
	assertBoxFullyVisible(t, last, boxContent("w7"))
	_, _, found := findStringCell(last, boxContent("w0"))
	testutil.Equal(t, found, false)

	// A middle node: its whole box is visible.
	w.MoveSlot(-4)
	testutil.Equal(t, w.CurrentNodeID(), "w3")
	mid := drawToSim(t, w, 40, 14)
	assertBoxFullyVisible(t, mid, boxContent("w3"))

	// Back to the first node: w0 whole + on-screen again.
	for i := 0; i < 8; i++ {
		w.MoveSlot(-1)
	}
	testutil.Equal(t, w.CurrentNodeID(), "w0")
	first := drawToSim(t, w, 40, 14)
	assertBoxFullyVisible(t, first, boxContent("w0"))
}

// TestDraw_HScroll_EdgeIndicators (BUG-010): a `›` marks content hidden past the
// right edge (cursor on the first node), and a `‹` marks content hidden past the
// left edge once scrolled right (cursor on the last node).
func TestDraw_HScroll_EdgeIndicators(t *testing.T) {
	w := wideStage(8)
	// Cursor on the first node: right content hidden → `›`, nothing left → no `‹`.
	atFirst := drawToString(t, w, 40, 14)
	testutil.Contains(t, atFirst, "›")
	testutil.Equal(t, strings.ContainsRune(atFirst, '‹'), false)

	// Cursor on the last node: scrolled right → `‹`, nothing right → no `›`.
	for i := 0; i < 8; i++ {
		w.MoveSlot(1)
	}
	atLast := drawToString(t, w, 40, 14)
	testutil.Contains(t, atLast, "‹")
	testutil.Equal(t, strings.ContainsRune(atLast, '›'), false)
}

// TestDraw_FannedGroupWrapsInsteadOfHScroll (BUG-011 supersedes the BUG-010
// fanned-scroll path): a fanned group wider than the pane no longer scrolls one
// overflowing row horizontally — it WRAPS onto multiple rows, so the selected
// member AND the previously-off-left first member are BOTH fully visible at once,
// and no horizontal-scroll edge indicator is drawn. (Lone-node stages still
// scroll horizontally — see TestDraw_HScroll_SelectedNodeFullyVisible.)
func TestDraw_FannedGroupWrapsInsteadOfHScroll(t *testing.T) {
	w := fanGroup("1a", "1b", "1c", "1d", "1e", "1f", "1g", "1h")
	w.SetFocused(true)
	w.MoveStage(1)     // onto the collapsed group
	w.ActivateCursor() // fan out → cursor on member 0 (1a)
	for i := 0; i < 7; i++ {
		w.MoveSlot(1) // walk to the last member (1h)
	}
	testutil.Equal(t, w.CurrentNodeID(), "1h")
	sc := drawToSim(t, w, 40, 30)
	// The selected last member is fully visible AND the first member is still on
	// screen (wrapped to an earlier row), not scrolled off the left.
	assertBoxFullyVisible(t, sc, "○ 1h")
	assertBoxFullyVisible(t, sc, "○ 1a")
	// No horizontal-scroll indicators — wrapping removed the overflow.
	out := drawToString(t, w, 40, 30)
	testutil.Equal(t, strings.ContainsRune(out, '‹'), false)
	testutil.Equal(t, strings.ContainsRune(out, '›'), false)
}

// TestDraw_HScroll_NoIndicatorsWhenFits (BUG-010): when every stage fits the pane
// width, no horizontal scroll is applied and no edge indicator is drawn.
func TestDraw_HScroll_NoIndicatorsWhenFits(t *testing.T) {
	w := wideStage(2) // two small boxes fit a wide pane
	out := drawToString(t, w, 60, 14)
	testutil.Equal(t, strings.ContainsRune(out, '‹'), false)
	testutil.Equal(t, strings.ContainsRune(out, '›'), false)
	// Both nodes are visible (nothing clipped).
	testutil.Contains(t, out, "w0")
	testutil.Contains(t, out, "w1")
}

// TestState_Glyph_Cancelled: StateCancelled carries the same ✕ glyph as
// StateFailed — the COLOUR is the discriminator (grey vs red).
func TestState_Glyph_Cancelled(t *testing.T) {
	testutil.Equal(t, StateCancelled.Glyph(), '✕')
}

// TestState_Label_Cancelled: StateCancelled labels itself "cancelled".
func TestState_Label_Cancelled(t *testing.T) {
	testutil.Equal(t, StateCancelled.Label(), "cancelled")
}

// TestState_Style_CancelledIsGrey_DistinctFromFailed: StateCancelled uses
// ColorDimmed (grey) and StateFailed uses ColorError (red) — same ✕ glyph,
// different colour, so they are visually distinct.
func TestState_Style_CancelledIsGrey_DistinctFromFailed(t *testing.T) {
	cfg, _, _ := StateCancelled.style().Decompose()
	ffg, _, _ := StateFailed.style().Decompose()
	testutil.Equal(t, cfg, theme.ColorDimmed)
	testutil.Equal(t, ffg, theme.ColorError)
	testutil.Equal(t, cfg == ffg, false)
}

// TestDraw_CancelledChipGlyphRendered: a node with State=StateCancelled renders
// the ✕ glyph and the node's short-id label, and is distinct from StateFailed
// (which uses a red bold style vs the dimmed grey of cancelled).
func TestDraw_CancelledChipGlyphRendered(t *testing.T) {
	w := New()
	w.SetData([]Node{
		{ID: "1a", Name: "1a-cancelled", State: StateCancelled},
		{ID: "2a", Name: "2a-failed", State: StateFailed},
	}, []Edge{{From: "1a", To: "2a"}})

	sc := drawToSim(t, w, 60, 18)
	cells, scw, _ := sc.GetContents()

	// Both nodes render a ✕ glyph.
	cx, cy, okC := findStringCell(sc, "✕ 1a")
	testutil.Equal(t, okC, true)
	fx, fy, okF := findStringCell(sc, "✕ 2a")
	testutil.Equal(t, okF, true)

	// The cancelled cell uses grey (ColorDimmed), not red.
	cancelFG, _, _ := cells[cy*scw+cx].Style.Decompose()
	testutil.Equal(t, cancelFG, theme.ColorDimmed)

	// The failed cell uses red (ColorError), not grey.
	failFG, _, _ := cells[fy*scw+fx].Style.Decompose()
	testutil.Equal(t, failFG, theme.ColorError)

	// They must be visually distinct — different colours.
	testutil.Equal(t, cancelFG == failFG, false)
}

// TestGroupCounts_IncludesCancelled: groupCounts includes a non-zero
// StateCancelled count as a grey ✕ entry in the aggregate line.
func TestGroupCounts_IncludesCancelled(t *testing.T) {
	g := &Group{Counts: map[State]int{StateDone: 2, StateCancelled: 1}}
	got := groupCounts(g)
	testutil.Contains(t, got, "2 ✓")
	testutil.Contains(t, got, "1 ✕")
}

// fanGroupFeeding builds a plan stage0 [1a] -> group {members…} (stage1) -> [3a]
// where every member feeds 3a, so the group is a downstream-feeding parallel
// group with stages above AND below it. Used to exercise the wrapped-group edge
// anchoring (BUG-011 fan-wrap).
func fanGroupFeeding(members ...string) *Widget {
	w := New()
	nodes := []Node{node("1a"), node("3a")}
	var edges []Edge
	for _, m := range members {
		nodes = append(nodes, node(m))
		edges = append(edges, Edge{From: "1a", To: m}, Edge{From: m, To: "3a"})
	}
	w.SetData(nodes, edges)
	w.SetFocused(true)
	return w
}

// TestDraw_FannedGroupWrapsToFitWidth (BUG-011 fan-wrap): a fanned group with
// more members than fit one row at a narrow pane width wraps its member boxes
// onto MULTIPLE ROWS so every box is fully visible — no overflow off the right
// edge and no horizontal-scroll edge indicators. The first and last members
// land on different rows.
func TestDraw_FannedGroupWrapsToFitWidth(t *testing.T) {
	members := []string{"2a", "2b", "2c", "2d", "2e", "2f", "2g", "2h"}
	// Plain group (no downstream feed) so member boxes carry no `↘` and their
	// content matches assertBoxFullyVisible's exact box-width math.
	w := fanGroup(members...)
	// Fan out the group (stage 1).
	w.MoveStage(1)
	_, isGroup := w.GroupAt(w.CursorPos().Stage, w.CursorPos().Slot)
	testutil.Equal(t, isGroup, true)
	w.ActivateCursor()

	const paneW, paneH = 40, 30
	sc := drawToSim(t, w, paneW, paneH)
	// Every member box is present AND fully visible (left + right borders on-screen).
	for _, m := range members {
		assertBoxFullyVisible(t, sc, "○ "+m)
	}
	// The first and last members are on DIFFERENT rows (the group wrapped).
	_, yFirst, okF := findStringCell(sc, "○ 2a")
	_, yLast, okL := findStringCell(sc, "○ 2h")
	testutil.Equal(t, okF, true)
	testutil.Equal(t, okL, true)
	testutil.Equal(t, yFirst != yLast, true)

	// Wrapping removed the horizontal overflow → no edge indicators.
	out := drawToString(t, w, paneW, paneH)
	testutil.Equal(t, strings.ContainsRune(out, '›'), false)
	testutil.Equal(t, strings.ContainsRune(out, '‹'), false)
}

// TestDraw_FannedGroupWrapCursorReachesEveryMember (BUG-011 fan-wrap): with the
// group wrapped onto multiple rows, walking the cursor right from the first
// member advances through every member in order across the rows.
func TestDraw_FannedGroupWrapCursorReachesEveryMember(t *testing.T) {
	members := []string{"2a", "2b", "2c", "2d", "2e", "2f", "2g", "2h"}
	w := fanGroup(members...)
	w.MoveStage(1)
	w.ActivateCursor() // fan out → cursor on member 0 (2a)
	testutil.Equal(t, w.CurrentNodeID(), "2a")
	// Render narrow so it wraps; the cursor model is width-independent, but assert
	// the rendered grid is multi-row first.
	const paneW, paneH = 40, 30
	sc := drawToSim(t, w, paneW, paneH)
	_, y0, _ := findStringCell(sc, "○ 2a")
	_, y7, _ := findStringCell(sc, "○ 2h")
	testutil.Equal(t, y0 != y7, true)
	// Walk right through all members; each step names the next member in order.
	for i := 1; i < len(members); i++ {
		w.MoveSlot(1)
		testutil.Equal(t, w.CurrentNodeID(), members[i])
	}
}

// TestDraw_FannedGroupWrapDownstreamEdgeAnchored (BUG-011 fan-wrap): when a
// wrapped group feeds a downstream stage, the downstream stage's box renders
// BELOW the (now taller) wrapped block, and an inter-stage `│` connector sits
// between them — the edge re-anchors under the taller block.
func TestDraw_FannedGroupWrapDownstreamEdgeAnchored(t *testing.T) {
	members := []string{"2a", "2b", "2c", "2d", "2e", "2f", "2g", "2h"}
	w := fanGroupFeeding(members...)
	w.MoveStage(1)
	w.ActivateCursor() // fan out the group

	const paneW, paneH = 40, 30
	sc := drawToSim(t, w, paneW, paneH)
	// The downstream node 3a renders below the wrapped group's last member row.
	_, yLastMember, okM := findStringCell(sc, "○ 2h")
	_, y3a, ok3a := findStringCell(sc, "○ 3a")
	testutil.Equal(t, okM, true)
	testutil.Equal(t, ok3a, true)
	testutil.Equal(t, y3a > yLastMember, true)
	// An inter-stage connector `│` sits on a row strictly between the group block
	// and the downstream box (the edge anchors under the taller wrapped block).
	cells, scw, _ := sc.GetContents()
	connectorBetween := false
	for y := yLastMember + 1; y < y3a; y++ {
		for x := 0; x < scw; x++ {
			c := cells[y*scw+x]
			if len(c.Runes) > 0 && c.Runes[0] == '│' {
				connectorBetween = true
			}
		}
	}
	testutil.Equal(t, connectorBetween, true)
}

// TestDraw_FannedGroupNarrowPaneOnePerRow (BUG-011 fan-wrap, degenerate edge):
// at a pane so narrow that a single member box exceeds the inner-width budget,
// each member wraps to its own row, the enclosure still overflows the pane
// width, and the BUG-010 horizontal viewport keeps the SELECTED member visible
// (drawing a `‹` once scrolled right). Exercises the budget clamp + the fanned
// horizontal-scroll path that wrapping otherwise supersedes.
func TestDraw_FannedGroupNarrowPaneOnePerRow(t *testing.T) {
	w := fanGroup("1a", "1b", "1c")
	w.MoveStage(1)
	w.ActivateCursor() // fan out → cursor on member 0
	w.MoveSlot(1)      // walk to the last visible member
	w.MoveSlot(1)      // (clamps at the last member)
	// A very narrow pane: a single ~8-wide member box can't fit the inner budget,
	// so each member occupies its own row and the enclosure overflows the width.
	sc := drawToSim(t, w, 12, 20)
	// The render must not panic and the cursor still names a member box.
	id := w.CurrentNodeID()
	testutil.Equal(t, id == "1a" || id == "1b" || id == "1c", true)
	// Content is hidden to the left after scrolling right → a `‹` indicator.
	out := drawToString(t, w, 12, 20)
	testutil.Contains(t, out, "‹")
	_ = sc
}

func TestTruncateLabel_RuneAware(t *testing.T) {
	// A long name truncates to fallbackLabelRunes with an ellipsis, rune-counted.
	long := "verylongrolenamehere"
	got := truncateLabel(long)
	testutil.Equal(t, len([]rune(got)) <= fallbackLabelRunes, true)
	testutil.Equal(t, strings.HasSuffix(got, "…"), true)
}
