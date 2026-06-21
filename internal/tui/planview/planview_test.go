package planview

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// node is a fixture builder for a planned plan node named by its short-id.
func node(name string) Node {
	return Node{ID: name, Name: name + "-role", State: StatePlanned, Planned: true}
}

// liveNode is a fixture builder for a live node with a bound-task state.
func liveNode(name string, st State) Node {
	return Node{ID: name, Name: name + "-role", State: st}
}

// --- Short-id parse + fallback (Requirement: Short-id node labels) ---

func TestParseShortID(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantStage int
		wantMem   string
		wantLabel string
		wantOK    bool
	}{
		{"digit+letter", "2c-fact-checker", 2, "c", "2c", true},
		{"single digit single letter", "1a-research", 1, "a", "1a", true},
		{"multi-letter member", "10ab-thing", 10, "ab", "10ab", true},
		{"no prefix falls back to name", "researcher", 0, "", "researcher", false},
		{"no dash but parseable", "3b", 3, "b", "3b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseShortID(tt.in)
			testutil.Equal(t, got.OK, tt.wantOK)
			testutil.Equal(t, got.Label, tt.wantLabel)
			if tt.wantOK {
				testutil.Equal(t, got.Stage, tt.wantStage)
				testutil.Equal(t, got.Member, tt.wantMem)
			}
		})
	}
}

// TestLabelOf_ShortIDFromName mirrors "Short-id parsed from the name prefix":
// a role named 2c-fact-checker labels its node 2c.
func TestLabelOf_ShortIDFromName(t *testing.T) {
	w := New()
	w.SetData([]Node{{ID: "n1", Name: "2c-fact-checker", State: StatePlanned, Planned: true}}, nil)
	testutil.Equal(t, w.LabelOf("n1"), "2c")
}

// TestLabelOf_FallsBackToTruncatedName mirrors "Unparseable name falls back to
// the truncated name".
func TestLabelOf_FallsBackToTruncatedName(t *testing.T) {
	w := New()
	w.SetData([]Node{{ID: "n1", Name: "researcher", State: StatePlanned, Planned: true}}, nil)
	got := w.LabelOf("n1")
	// Falls back to the (truncated) name, NOT empty and NOT a short-id.
	testutil.Equal(t, len(got) > 0, true)
	testutil.Contains(t, "researcher", got) // got is a prefix/substring of the name
}

// --- Stage = computed longest-path (Requirement: plan projects ... stage placement) ---

// TestStage_ComputedFromEdgesNotShortID mirrors "Stage is computed from edges,
// not the short-id number": a node whose short-id number says stage 1 but which
// is blocked by a stage-2 node lands at the computed longest-path layer.
func TestStage_ComputedFromEdgesNotShortID(t *testing.T) {
	w := New()
	// Names claim 1x stages, but the edges force a chain a→b→c (3 layers).
	// "1a" blocks "1b" blocks "1c" — short-id number is 1 for all three, but the
	// computed stages must be 0,1,2.
	w.SetData(
		[]Node{node("1a"), node("1b"), node("1c")},
		[]Edge{{From: "1a", To: "1b"}, {From: "1b", To: "1c"}},
	)
	sa, _ := w.StageOf("1a")
	sb, _ := w.StageOf("1b")
	sc, _ := w.StageOf("1c")
	testutil.Equal(t, sa, 0)
	testutil.Equal(t, sb, 1)
	testutil.Equal(t, sc, 2)
	testutil.Equal(t, w.Stages(), 3)
	// The label still reflects the short-id, not the computed stage.
	testutil.Equal(t, w.LabelOf("1c"), "1c")
}

// --- Parallel-group collapse (Requirement: Parallel groups auto-collapse) ---

// fanGroup builds a plan with one blocker (stage 0) feeding `members` siblings
// (stage 1) that share the blocker set and have no internal edges.
func fanGroup(members ...string) *Widget {
	w := New()
	nodes := []Node{node("0a")}
	var edges []Edge
	for _, m := range members {
		nodes = append(nodes, node(m))
		edges = append(edges, Edge{From: "0a", To: m})
	}
	w.SetData(nodes, edges)
	return w
}

// TestGroup_ContiguousCollapsesToRangeBox mirrors "Same-stage siblings collapse
// into a range box": 2a,2b,2c → [2a–2c].
func TestGroup_ContiguousCollapsesToRangeBox(t *testing.T) {
	w := fanGroup("2a", "2b", "2c")
	// Stage 1 has exactly one slot: the collapsed group.
	testutil.Equal(t, w.SlotCount(1), 1)
	g, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	testutil.Equal(t, g.Label, "[2a–2c]")
	testutil.Equal(t, len(g.Members), 3)
}

// TestGroup_NonContiguousShowsSpanAndCount mirrors "Non-contiguous group shows
// the span and a count": 2a,2b,2f → [2a–2f +1].
func TestGroup_NonContiguousShowsSpanAndCount(t *testing.T) {
	w := fanGroup("2a", "2b", "2f")
	g, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	testutil.Equal(t, g.Label, "[2a–2f +1]")
}

// TestGroup_AggregateStateCounts mirrors "Aggregate state counts on a collapsed
// group box": a group with mixed states reports per-state counts.
func TestGroup_AggregateStateCounts(t *testing.T) {
	w := New()
	w.SetData(
		[]Node{
			node("0a"),
			liveNode("1a", StateDone),
			liveNode("1b", StateDone),
			liveNode("1c", StateWorking),
			node("1d"), // planned
		},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"},
			{From: "0a", To: "1c"}, {From: "0a", To: "1d"},
		},
	)
	g, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	testutil.Equal(t, g.Counts[StateDone], 2)
	testutil.Equal(t, g.Counts[StateWorking], 1)
	testutil.Equal(t, g.Counts[StatePlanned], 1)
}

// TestGroup_SingleNodeIsNotAGroup: a stage with one node renders as an
// individual chip, not a group.
func TestGroup_SingleNodeIsNotAGroup(t *testing.T) {
	w := fanGroup("2a")
	_, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, false)
	testutil.Equal(t, w.SlotCount(1), 1) // one lone-node slot
}

// --- Partial dependency, option B (Requirement: Partial-dependency marker) ---

// TestPartialDep_MarksGroupAndFeedingMember mirrors "Partially-feeding group is
// marked": only 2b of [2a–2c] blocks downstream 3a → box shows [2a–2c ↘] and
// the feeding member is 2b.
func TestPartialDep_MarksGroupAndFeedingMember(t *testing.T) {
	w := New()
	w.SetData(
		[]Node{node("0a"), node("2a"), node("2b"), node("2c"), node("3a")},
		[]Edge{
			{From: "0a", To: "2a"}, {From: "0a", To: "2b"}, {From: "0a", To: "2c"},
			// Only 2b feeds 3a.
			{From: "2b", To: "3a"},
		},
	)
	g, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	// Only some members feed → PartialFeed, and 2b is in the feeding set; the bare
	// range Label carries NO ↘ (the indicator is rendered on the top line, BUG-005).
	testutil.Equal(t, g.PartialFeed, true)
	testutil.Equal(t, g.FeedingMembers["2b"], true)
	testutil.Equal(t, g.FeedingMembers["2a"], false)
	testutil.Equal(t, g.FeedTarget, "") // partial, not a single-target full feed
	testutil.Equal(t, strings.Contains(g.Label, "↘"), false)
}

// TestFullFeed_AllMembersToOneTargetSetsFeedTarget: when every out-of-group edge
// points to ONE node, the group is a full feed → FeedTarget is that node's
// short-id (renders "→ 3a"), not PartialFeed, and all feeders are marked.
func TestFullFeed_AllMembersToOneTargetSetsFeedTarget(t *testing.T) {
	w := New()
	w.SetData(
		[]Node{node("0a"), node("2d"), node("2e"), node("2f"), node("3a")},
		[]Edge{
			{From: "0a", To: "2d"}, {From: "0a", To: "2e"}, {From: "0a", To: "2f"},
			{From: "2d", To: "3a"}, {From: "2e", To: "3a"}, {From: "2f", To: "3a"},
		},
	)
	g, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	testutil.Equal(t, g.FeedTarget, "3a")
	testutil.Equal(t, g.PartialFeed, false)
	testutil.Equal(t, g.FeedingMembers["2d"] && g.FeedingMembers["2e"] && g.FeedingMembers["2f"], true)
}

// --- Degenerate no-plan (Requirement: no plan authored renders live roles flat) ---

func TestNoPlan_FlatSingleStage(t *testing.T) {
	w := New()
	w.SetData([]Node{liveNode("w1", StateWorking), liveNode("w2", StateWorking)}, nil)
	testutil.Equal(t, w.NoPlan(), true)
	testutil.Equal(t, w.Stages(), 1)
	testutil.Equal(t, w.SlotCount(0), 2) // both live workers flat, no edges
}

// --- Navigation (Requirement: Four-way plan navigation with group fan-out) ---

// navGraph builds: stage0 [0a], stage1 group [1a–1c], stage2 [2a].
func navGraph() *Widget {
	w := New()
	w.SetData(
		[]Node{node("0a"), node("1a"), node("1b"), node("1c"), node("2a")},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"}, {From: "0a", To: "1c"},
			// 2a is blocked by the whole group so it sits at stage 2.
			{From: "1a", To: "2a"}, {From: "1b", To: "2a"}, {From: "1c", To: "2a"},
		},
	)
	w.SetFocused(true)
	return w
}

// TestNav_UpDownChangesStageAndCollapses mirrors "Up/down changes stage and
// collapses": fanning a group then pressing ↓ moves to the next stage and
// collapses the group.
func TestNav_UpDownChangesStageAndCollapses(t *testing.T) {
	w := navGraph()
	// Move to stage 1 (the group) and fan it out.
	w.MoveStage(1)
	testutil.Equal(t, w.CursorPos().Stage, 1)
	w.ActivateCursor() // fan out
	testutil.Equal(t, w.Fanned(1, 0), true)

	// ↓ moves to stage 2 AND collapses the fanned group.
	w.MoveStage(1)
	testutil.Equal(t, w.CursorPos().Stage, 2)
	testutil.Equal(t, w.Fanned(1, 0), false)
}

// TestNav_LeftRightMovesSlot: within a multi-slot stage ←/→ moves between slots.
func TestNav_LeftRightMovesSlot(t *testing.T) {
	// Two same-stage roots that share the EMPTY blocker set collapse into a
	// single group per D4 — that is spec-compliant, so we can't use two planned
	// roots here. The no-plan path (live roles, no planned nodes, no edges)
	// renders each live worker as its own lone-node slot, never grouped, giving a
	// genuine two-slot stage 0 that preserves this test's intent (←/→ between
	// slots).
	w := New()
	w.SetData([]Node{liveNode("w1", StateWorking), liveNode("w2", StateWorking)}, nil)
	w.SetFocused(true)
	testutil.Equal(t, w.CursorPos().Slot, 0)
	w.MoveSlot(1)
	testutil.Equal(t, w.CursorPos().Slot, 1)
	w.MoveSlot(-1)
	testutil.Equal(t, w.CursorPos().Slot, 0)
	// Clamp: stepping off the left edge is a no-op.
	w.MoveSlot(-1)
	testutil.Equal(t, w.CursorPos().Slot, 0)
}

// TestNav_EnterFansOutGroupLandsOnFirstMember mirrors "Enter fans out a group".
func TestNav_EnterFansOutGroupLandsOnFirstMember(t *testing.T) {
	w := navGraph()
	w.MoveStage(1) // cursor on the group slot
	testutil.Equal(t, w.Fanned(1, 0), false)
	w.ActivateCursor()
	testutil.Equal(t, w.Fanned(1, 0), true)
	// Cursor lands on the first member (member index 0 → node "1a").
	testutil.Equal(t, w.CursorPos().Member, 0)
	testutil.Equal(t, w.CurrentNodeID(), "1a")
}

// TestNav_EnterOnMemberNavigatesNotCollapse pins BUG-013: with a group fanned
// out and the cursor on an interior MEMBER, Enter fires OnEnter for THAT member's
// node id and leaves the group expanded — it must NOT collapse (collapse is Esc's
// job, asserted by the companion case). Drill-in is unaffected: a Drillable member
// fires OnDrillIn, not OnEnter.
func TestNav_EnterOnMemberNavigatesNotCollapse(t *testing.T) {
	noFocus := func(tview.Primitive) {}
	enter := func(w *Widget) {
		w.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	}

	t.Run("Enter on a fanned member fires OnEnter and keeps the group fanned", func(t *testing.T) {
		w := navGraph()
		var entered string
		w.OnEnter = func(id string) { entered = id }
		w.MoveStage(1)     // onto the [1a–1c] group
		w.ActivateCursor() // fan out → cursor on member 0 (1a)
		w.MoveSlot(1)      // walk to member 1 (1b) — an interior member
		testutil.Equal(t, w.CurrentNodeID(), "1b")

		enter(w)
		// OnEnter fired for the member's id, and the group is STILL fanned.
		testutil.Equal(t, entered, "1b")
		testutil.Equal(t, w.Fanned(1, 0), true)
		testutil.Equal(t, w.CursorPos().Member, 1)
	})

	t.Run("Esc on a member still collapses the group (no BUG-001 regression)", func(t *testing.T) {
		w := navGraph()
		w.MoveStage(1)
		w.ActivateCursor() // fan out → cursor on member 0
		testutil.Equal(t, w.Fanned(1, 0), true)
		w.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.Fanned(1, 0), false)
		testutil.Equal(t, w.CursorPos().Member, -1)
	})

	t.Run("Enter on a Drillable member drills in, not OnEnter", func(t *testing.T) {
		w := New()
		var entered, drilled string
		w.OnEnter = func(id string) { entered = id }
		w.OnDrillIn = func(id string) { drilled = id }
		// Two same-stage, same-blocker members form a group; both Drillable.
		w.SetData(
			[]Node{
				node("0a"),
				{ID: "1a", Name: "1a-sub", State: StateWorking, Drillable: true},
				{ID: "1b", Name: "1b-sub", State: StateWorking, Drillable: true},
			},
			[]Edge{{From: "0a", To: "1a"}, {From: "0a", To: "1b"}},
		)
		w.SetFocused(true)
		w.MoveStage(1)     // onto the [1a–1b] group
		w.ActivateCursor() // fan out → cursor on member 0 (1a)
		testutil.Equal(t, w.Fanned(1, 0), true)
		enter(w)
		testutil.Equal(t, drilled, "1a")
		testutil.Equal(t, entered, "")
		testutil.Equal(t, w.Fanned(1, 0), true) // not collapsed
	})
}

// TestNav_SpaceTogglesNeverNavigates pins the BUG-013 follow-up: Space is a PURE
// fan-out/collapse toggle and NEVER navigates. On a fanned member it collapses
// the group (does not fire OnEnter); on a lone leaf it is a no-op (Enter opens
// leaves, not Space). A collapsed group still fans out.
func TestNav_SpaceTogglesNeverNavigates(t *testing.T) {
	noFocus := func(tview.Primitive) {}
	space := func(w *Widget) {
		w.InputHandler()(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone), noFocus)
	}

	t.Run("Space on a fanned member collapses, does not fire OnEnter", func(t *testing.T) {
		w := navGraph()
		var entered string
		w.OnEnter = func(id string) { entered = id }
		w.MoveStage(1) // onto the [1a–1c] group
		space(w)       // fan out → cursor on member 0
		testutil.Equal(t, w.Fanned(1, 0), true)
		w.MoveSlot(1) // walk to member 1 (1b)
		testutil.Equal(t, w.CurrentNodeID(), "1b")
		space(w) // Space collapses — never navigates
		testutil.Equal(t, w.Fanned(1, 0), false)
		testutil.Equal(t, w.CursorPos().Member, -1)
		testutil.Equal(t, entered, "") // OnEnter must NOT have fired
	})

	t.Run("Space on a lone leaf is a no-op (does not fire OnEnter)", func(t *testing.T) {
		w := New()
		var entered string
		w.OnEnter = func(id string) { entered = id }
		w.SetData([]Node{liveNode("w1", StateWorking)}, nil)
		w.SetFocused(true)
		testutil.Equal(t, w.CurrentNodeID(), "w1")
		space(w)
		testutil.Equal(t, entered, "") // Space never opens a leaf — that's Enter
	})
}

// TestNav_MemberWalkInsideGroup mirrors "walk members on ←/→ inside a fanned-out
// group".
func TestNav_MemberWalkInsideGroup(t *testing.T) {
	w := navGraph()
	w.MoveStage(1)
	w.ActivateCursor() // fan out, member 0 = 1a
	w.MoveSlot(1)
	testutil.Equal(t, w.CurrentNodeID(), "1b")
	w.MoveSlot(1)
	testutil.Equal(t, w.CurrentNodeID(), "1c")
}

// TestNav_StepOffGroupEdgeExitsAndCollapses mirrors "Stepping off a group edge
// exits and collapses": from the last member, → collapses the group and moves
// to the next slot (or clamps at the stage edge).
func TestNav_StepOffGroupEdgeExitsAndCollapses(t *testing.T) {
	w := navGraph()
	w.MoveStage(1)
	w.ActivateCursor() // fan out
	w.MoveSlot(1)      // 1b
	w.MoveSlot(1)      // 1c (last member)
	testutil.Equal(t, w.CurrentNodeID(), "1c")
	// Step off the right edge: group collapses, cursor exits the group.
	w.MoveSlot(1)
	testutil.Equal(t, w.Fanned(1, 0), false)
	testutil.Equal(t, w.CursorPos().Member, -1) // no longer inside a group
}

// TestNav_StepOffGroupLeftEdgeExitsAndCollapses: from the first member, ←
// collapses the group and clamps at the stage's left edge (no slot to the left).
func TestNav_StepOffGroupLeftEdgeExitsAndCollapses(t *testing.T) {
	w := navGraph()
	w.MoveStage(1)
	w.ActivateCursor() // fan out, member 0 = 1a (first member)
	testutil.Equal(t, w.CurrentNodeID(), "1a")
	w.MoveSlot(-1) // step off the left edge
	testutil.Equal(t, w.Fanned(1, 0), false)
	testutil.Equal(t, w.CursorPos().Member, -1)
	testutil.Equal(t, w.CursorPos().Slot, 0) // clamped: stage 1 has one slot
}

// TestNav_MoveStageClampsAtEdges: ↑ at stage 0 and ↓ past the last stage clamp.
func TestNav_MoveStageClampsAtEdges(t *testing.T) {
	w := navGraph() // stages 0,1,2
	w.MoveStage(-1) // already at 0
	testutil.Equal(t, w.CursorPos().Stage, 0)
	w.MoveStage(5) // past the last stage (2)
	testutil.Equal(t, w.CursorPos().Stage, 2)
}

// TestNav_EmptyWidgetNavIsNoop: navigation on an empty widget never panics and
// leaves the cursor at the origin.
func TestNav_EmptyWidgetNavIsNoop(t *testing.T) {
	w := New()
	w.MoveStage(1)
	w.MoveSlot(1)
	w.ActivateCursor()
	testutil.Equal(t, w.CursorPos().Stage, 0)
	testutil.Equal(t, w.CursorPos().Slot, 0)
	testutil.Equal(t, w.CurrentNodeID(), "")
}

// TestNav_CollapsedGroupCursorHasNoNode: the cursor on a collapsed group names
// no node (a group is not itself a node).
func TestNav_CollapsedGroupCursorHasNoNode(t *testing.T) {
	w := navGraph()
	w.MoveStage(1) // on the collapsed group at stage 1
	_, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	testutil.Equal(t, w.CurrentNodeID(), "") // collapsed group → no node
}

// TestInputHandler_RoutesKeys exercises the InputHandler key routing: arrows and
// the j/k/h/l/Space/Enter aliases drive the same cursor moves as the methods.
func TestInputHandler_RoutesKeys(t *testing.T) {
	noFocus := func(tview.Primitive) {}
	t.Run("arrows move stage and slot", func(t *testing.T) {
		w := navGraph()
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.CursorPos().Stage, 1)
		h(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.CursorPos().Stage, 0)
	})
	t.Run("vim aliases move", func(t *testing.T) {
		w := navGraph()
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
		testutil.Equal(t, w.CursorPos().Stage, 1)
		h(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), noFocus)
		testutil.Equal(t, w.CursorPos().Stage, 0)
	})
	t.Run("Enter fans out a group", func(t *testing.T) {
		w := navGraph()
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus) // to the group
		h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.Fanned(1, 0), true)
	})
	t.Run("Space toggles a group and h/l walk members", func(t *testing.T) {
		w := navGraph()
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus) // to the group
		h(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone), noFocus) // fan out
		testutil.Equal(t, w.Fanned(1, 0), true)
		h(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone), noFocus) // member 1b
		testutil.Equal(t, w.CurrentNodeID(), "1b")
		h(tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone), noFocus) // back to 1a
		testutil.Equal(t, w.CurrentNodeID(), "1a")
	})
}

// --- Master-detail header (Requirement: Plan master-detail header) ---

// TestHeader_NodeView mirrors "Header shows the selected node": for a node the
// header shows its name, description, and feeds.
func TestHeader_NodeView(t *testing.T) {
	w := New()
	w.SetData(
		[]Node{
			{ID: "1a", Name: "1a-research", State: StatePlanned, Planned: true, Description: "research the topic"},
			{ID: "2a", Name: "2a-write", State: StatePlanned, Planned: true, Description: "write it up"},
		},
		[]Edge{{From: "1a", To: "2a"}},
	)
	w.SetFocused(true)
	// Cursor starts at stage 0 slot 0 (node 1a).
	testutil.Equal(t, w.CurrentNodeID(), "1a")
	lines := w.HeaderLines()
	joined := joinLines(lines)
	testutil.Contains(t, joined, "1a-research")        // name
	testutil.Contains(t, joined, "research the topic") // description (prompt first line)
	testutil.Contains(t, joined, "2a")                 // feeds → 2a
}

// TestHeader_NodeShowsStatusLine is BUG-006: the single-node header carries a
// Status line reflecting the node's state — "planned" for a never-bound role,
// the live worker's state otherwise — with the state glyph.
func TestHeader_NodeShowsStatusLine(t *testing.T) {
	t.Run("planned node", func(t *testing.T) {
		w := New()
		w.SetData([]Node{{ID: "1a", Name: "1a-research", State: StatePlanned, Planned: true}}, nil)
		w.SetFocused(true)
		testutil.Equal(t, w.CurrentNodeID(), "1a")
		joined := joinLines(w.HeaderLines())
		testutil.Contains(t, joined, "Status:")
		testutil.Contains(t, joined, "planned")
		testutil.Contains(t, joined, string(StatePlanned.Glyph()))
	})
	t.Run("live working node", func(t *testing.T) {
		w := New()
		w.SetData([]Node{liveNode("1a", StateWorking)}, nil)
		w.SetFocused(true)
		joined := joinLines(w.HeaderLines())
		testutil.Contains(t, joined, "Status:")
		testutil.Contains(t, joined, "working")
	})
	t.Run("done node reflects state", func(t *testing.T) {
		w := New()
		w.SetData([]Node{liveNode("1a", StateDone)}, nil)
		w.SetFocused(true)
		joined := joinLines(w.HeaderLines())
		testutil.Contains(t, joined, "done")
		// The Status line is its own line, distinct from the name line.
		lines := w.HeaderLines()
		testutil.Equal(t, lines[0], "1a-role")    // name
		testutil.Contains(t, lines[1], "Status:") // status on the second line
	})
}

// TestState_Label pins the state→word mapping the Status line renders.
func TestState_Label(t *testing.T) {
	cases := map[State]string{
		StatePlanned:  "planned",
		StateWorking:  "working",
		StateInReview: "in review",
		StateDone:     "done",
		StateFailed:   "failed",
		StatePending:  "pending",
	}
	for st, want := range cases {
		t.Run(want, func(t *testing.T) {
			testutil.Equal(t, st.Label(), want)
		})
	}
}

// TestHeader_GroupView mirrors "Header shows the selected group": for a
// collapsed group the header shows the range/title, members, and downstream.
func TestHeader_GroupView(t *testing.T) {
	w := New()
	w.SetData(
		[]Node{node("0a"), node("1a"), node("1b"), node("1c"), node("2a")},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"}, {From: "0a", To: "1c"},
			{From: "1a", To: "2a"}, {From: "1b", To: "2a"}, {From: "1c", To: "2a"},
		},
	)
	w.SetFocused(true)
	w.MoveStage(1) // cursor on the collapsed group at stage 1
	_, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	lines := w.HeaderLines()
	joined := joinLines(lines)
	testutil.Contains(t, joined, "[1a–1c]") // group range
	testutil.Contains(t, joined, "2a")      // downstream target
}

// TestHeader_FixedHeightBudget mirrors "header height is budgeted exactly":
// HeaderHeight is a fixed positive value and HeaderLines never exceeds it.
func TestHeader_FixedHeightBudget(t *testing.T) {
	w := New()
	w.SetData([]Node{node("1a")}, nil)
	w.SetFocused(true)
	h := w.HeaderHeight()
	testutil.Equal(t, h > 0, true)
	testutil.Equal(t, len(w.HeaderLines()) <= h, true)
}

// --- Sub-coordinator drill-in (Requirement: Sub-coordinator drill-in) ---

// TestDrillIn_EnterPushesChildPlan mirrors "Enter drills into a sub-coordinator":
// Enter on a drillable node pushes a child plan and increments the nav depth.
func TestDrillIn_EnterPushesChildPlan(t *testing.T) {
	w := New()
	var drilled string
	w.OnDrillIn = func(id string) {
		drilled = id
		// The page wires OnDrillIn to project + push the child plan.
		w.PushOrch("child", []Node{node("c1"), node("c2")}, []Edge{{From: "c1", To: "c2"}})
	}
	w.SetData([]Node{{ID: "sub", Name: "1a-subcoord", State: StateWorking, Drillable: true}}, nil)
	w.SetFocused(true)
	testutil.Equal(t, w.CurrentNodeID(), "sub")
	w.ActivateCursor() // drill in
	testutil.Equal(t, drilled, "sub")
	testutil.Equal(t, w.DrillDepth(), 1)
	// The child plan is now displayed: c2 is blocked by c1 → 2 stages.
	testutil.Equal(t, w.Stages(), 2)
}

// TestDrillIn_EscPopsToParent mirrors "Esc pops back to the parent": after
// drilling in, PopOrch returns to the parent plan.
func TestDrillIn_EscPopsToParent(t *testing.T) {
	w := New()
	w.OnDrillIn = func(string) {
		w.PushOrch("child", []Node{node("c1")}, nil)
	}
	w.SetData([]Node{{ID: "sub", Name: "1a-subcoord", State: StateWorking, Drillable: true}, node("2a")}, []Edge{{From: "sub", To: "2a"}})
	w.SetFocused(true)
	w.ActivateCursor() // drill into "sub"
	testutil.Equal(t, w.DrillDepth(), 1)
	w.PopOrch()
	testutil.Equal(t, w.DrillDepth(), 0)
	// Back at the parent: the parent's nodes are visible again.
	_, ok := w.StageOf("sub")
	testutil.Equal(t, ok, true)
}

// TestDrillIn_PlainLeafEnterFiresOnEnter mirrors "Enter on a plain leaf node
// opens its agent view (unchanged behavior)": a non-drillable, non-group node
// fires OnEnter, NOT OnDrillIn.
func TestDrillIn_PlainLeafEnterFiresOnEnter(t *testing.T) {
	w := New()
	var entered, drilled string
	w.OnEnter = func(id string) { entered = id }
	w.OnDrillIn = func(id string) { drilled = id }
	w.SetData([]Node{liveNode("1a", StateWorking)}, nil)
	w.SetFocused(true)
	testutil.Equal(t, w.CurrentNodeID(), "1a")
	w.ActivateCursor()
	testutil.Equal(t, entered, "1a")
	testutil.Equal(t, drilled, "") // NOT a drill-in
}

// TestDrillIn_DrillableMarkerPresent mirrors "A sub-coordinator node SHALL carry
// a visible drillable marker": the drillable node's chip label carries a marker.
func TestDrillIn_DrillableMarkerPresent(t *testing.T) {
	w := New()
	w.SetData([]Node{{ID: "sub", Name: "1a-subcoord", State: StateWorking, Drillable: true}}, nil)
	label := w.LabelOf("sub")
	// The label (or its rendered chip) carries a drillable affordance (▸ / ⊕).
	hasMarker := containsAny(label, "▸", "⊕")
	testutil.Equal(t, hasMarker, true)
}

// TestInputHandler_EscRouting pins the Esc "back out one level" contract: Esc on
// a FANNED group collapses it (highest priority); else at DrillDepth>0 it pops
// the nav stack and fires OnDrillOut; else at the root it is a consumed no-op
// (the widget swallows Esc — it never reaches the page/rail). Priority order:
// un-fan → drill-out → root no-op.
func TestInputHandler_EscRouting(t *testing.T) {
	noFocus := func(tview.Primitive) {}
	t.Run("Esc on a fanned group collapses it (cursor back on the slot)", func(t *testing.T) {
		w := navGraph()
		w.MoveStage(1)     // onto the [1a–1c] group
		w.ActivateCursor() // fan out → cursor lands on member 0
		testutil.Equal(t, w.Fanned(1, 0), true)
		testutil.Equal(t, w.CursorPos().Member, 0)
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
		// Un-fanned, cursor back on the collapsed slot (Member -1), still on the slot.
		testutil.Equal(t, w.Fanned(1, 0), false)
		testutil.Equal(t, w.CursorPos().Member, -1)
		testutil.Equal(t, w.CursorPos().Slot, 0)
	})
	t.Run("Esc on a fanned group does NOT drill out even when drilled in", func(t *testing.T) {
		// Un-fan wins over drill-out: a fanned group inside a drilled-in child plan
		// collapses first; DrillDepth is untouched until the next Esc.
		w := New()
		var popped int
		w.OnDrillOut = func() { popped++ }
		w.OnDrillIn = func(string) {
			w.PushOrch("child",
				[]Node{node("0a"), node("1a"), node("1b"), node("1c")},
				[]Edge{{From: "0a", To: "1a"}, {From: "0a", To: "1b"}, {From: "0a", To: "1c"}},
			)
		}
		w.SetData([]Node{{ID: "sub", Name: "1a-subcoord", State: StateWorking, Drillable: true}}, nil)
		w.SetFocused(true)
		w.ActivateCursor() // drill into child → depth 1
		testutil.Equal(t, w.DrillDepth(), 1)
		w.MoveStage(1)     // onto the child's [1a–1c] group
		w.ActivateCursor() // fan it out
		testutil.Equal(t, w.Fanned(1, 0), true)
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
		// First Esc un-fanned; still drilled in, OnDrillOut not fired.
		testutil.Equal(t, w.Fanned(1, 0), false)
		testutil.Equal(t, w.DrillDepth(), 1)
		testutil.Equal(t, popped, 0)
		// Second Esc now drills out.
		h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.DrillDepth(), 0)
		testutil.Equal(t, popped, 1)
	})
	t.Run("Esc at root no-ops and does not fire OnDrillOut", func(t *testing.T) {
		w := New()
		var popped bool
		w.OnDrillOut = func() { popped = true }
		w.SetData([]Node{liveNode("1a", StateWorking)}, nil)
		w.SetFocused(true)
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.DrillDepth(), 0)
		testutil.Equal(t, popped, false)
	})
	t.Run("Esc at drill-depth>0 (nothing fanned) pops and fires OnDrillOut", func(t *testing.T) {
		w := New()
		var popped int
		w.OnDrillOut = func() { popped++ }
		w.OnDrillIn = func(string) { w.PushOrch("child", []Node{node("c1")}, nil) }
		w.SetData([]Node{{ID: "sub", Name: "1a-subcoord", State: StateWorking, Drillable: true}}, nil)
		w.SetFocused(true)
		w.ActivateCursor() // drill in → depth 1
		testutil.Equal(t, w.DrillDepth(), 1)
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.DrillDepth(), 0)
		testutil.Equal(t, popped, 1)
	})
}

// TestDrillIn_MultiLevelPushPop exercises a depth≥2 drill stack: pushing two
// child orchestrators then popping back through both restores each parent in
// LIFO order.
func TestDrillIn_MultiLevelPushPop(t *testing.T) {
	w := New()
	w.SetData([]Node{node("root1"), node("root2")}, []Edge{{From: "root1", To: "root2"}})
	w.SetFocused(true)
	testutil.Equal(t, w.DrillDepth(), 0)

	// Drill two levels deep.
	w.PushOrch("L1", []Node{node("l1a"), node("l1b")}, []Edge{{From: "l1a", To: "l1b"}})
	testutil.Equal(t, w.DrillDepth(), 1)
	testutil.Equal(t, w.Title(), "L1")
	w.PushOrch("L2", []Node{node("l2a")}, nil)
	testutil.Equal(t, w.DrillDepth(), 2)
	testutil.Equal(t, w.Title(), "L2")
	_, ok := w.StageOf("l2a")
	testutil.Equal(t, ok, true)

	// Pop back through both levels (LIFO): L2 → L1 → root.
	w.PopOrch()
	testutil.Equal(t, w.DrillDepth(), 1)
	testutil.Equal(t, w.Title(), "L1")
	_, ok = w.StageOf("l1a")
	testutil.Equal(t, ok, true)
	w.PopOrch()
	testutil.Equal(t, w.DrillDepth(), 0)
	_, ok = w.StageOf("root1")
	testutil.Equal(t, ok, true)
	// Past the root, PopOrch is a no-op.
	w.PopOrch()
	testutil.Equal(t, w.DrillDepth(), 0)
}

// TestHeader_EmptyDescriptionFallback: a node with no Description renders the
// "(no description)" placeholder in the header.
func TestHeader_EmptyDescriptionFallback(t *testing.T) {
	w := New()
	w.SetData([]Node{{ID: "1a", Name: "1a-research", State: StatePlanned, Planned: true}}, nil)
	w.SetFocused(true)
	testutil.Equal(t, w.CurrentNodeID(), "1a")
	joined := joinLines(w.HeaderLines())
	testutil.Contains(t, joined, "(no description)")
}

// TestHeader_LongNameStaysWithinBudget: a node with a very long name still
// yields exactly headerContentRows lines (the fixed-height budget never grows).
func TestHeader_LongNameStaysWithinBudget(t *testing.T) {
	w := New()
	long := "1a-" + strings.Repeat("verylongsegment-", 12)
	w.SetData([]Node{{ID: "1a", Name: long, State: StatePlanned, Planned: true, Description: "d"}}, nil)
	w.SetFocused(true)
	lines := w.HeaderLines()
	testutil.Equal(t, len(lines), headerContentRows)
	testutil.Equal(t, len(lines) <= w.HeaderHeight(), true)
	// The full (untruncated) name is the header's first line — the strip clips it
	// at Draw, but HeaderLines reports the content faithfully.
	testutil.Equal(t, lines[0], long)
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}

// --- Refresh-safe re-projection (Requirement: cursor + fan-out survive refresh) ---

// TestUpdateData_UnchangedPreservesCursorAndFanned: re-projecting the identical
// plan on a refresh tick must be a no-op for the cursor and the fanned set —
// this is the BUG-1/2 root cause (applySelection's unconditional SetData reset
// the cursor to stage0/slot0 ~1s after every user move).
func TestUpdateData_UnchangedPreservesCursorAndFanned(t *testing.T) {
	w := navGraph()
	// Walk the cursor down to the group at stage 1 and fan it out, landing on a
	// member, then move to the second member.
	w.MoveStage(1)
	w.ActivateCursor() // fan out group [1a–1c]
	w.MoveSlot(1)      // member 0 -> 1
	before := w.CursorPos()
	testutil.Equal(t, w.Fanned(1, 0), true)
	testutil.Equal(t, before.Member, 1)

	// Re-project the SAME nodes + edges (a refresh tick with no structural change).
	w.UpdateData(
		[]Node{node("0a"), node("1a"), node("1b"), node("1c"), node("2a")},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"}, {From: "0a", To: "1c"},
			{From: "1a", To: "2a"}, {From: "1b", To: "2a"}, {From: "1c", To: "2a"},
		},
	)
	testutil.DeepEqual(t, w.CursorPos(), before)
	testutil.Equal(t, w.Fanned(1, 0), true)
}

// TestUpdateData_StateChangeReanchorsByNodeID: when a node's STATE flips (a
// cascade step materialized/progressed) but the node IDs are stable, the cursor
// must re-anchor to the same node ID, not snap back to stage 0.
func TestUpdateData_StateChangeReanchorsByNodeID(t *testing.T) {
	w := navGraph()
	w.MoveStage(2) // land on the lone node 2a at stage 2
	testutil.Equal(t, w.CurrentNodeID(), "2a")

	// Same structure, but 2a flipped planned -> working (a state-only change).
	w.UpdateData(
		[]Node{node("0a"), node("1a"), node("1b"), node("1c"), liveNode("2a", StateWorking)},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"}, {From: "0a", To: "1c"},
			{From: "1a", To: "2a"}, {From: "1b", To: "2a"}, {From: "1c", To: "2a"},
		},
	)
	// Cursor still names 2a (re-anchored by ID), not reset to stage 0.
	testutil.Equal(t, w.CurrentNodeID(), "2a")
	testutil.Equal(t, w.CursorPos().Stage, 2)
}

// TestUpdateData_ReanchorsFannedGroupAcrossNewNode: a newly-materialized node
// elsewhere in the plan changes the structure, but the fanned group the operator
// was walking must stay fanned and the cursor stay on its member.
func TestUpdateData_ReanchorsFannedGroupAcrossNewNode(t *testing.T) {
	w := navGraph()
	w.MoveStage(1)
	w.ActivateCursor() // fan out [1a–1c]
	w.MoveSlot(1)      // member 1 (1b)
	testutil.Equal(t, w.CurrentNodeID(), "1b")

	// A new planned node 0b appears at stage 0 (structure changed); the 1a–1c group
	// membership is unchanged.
	w.UpdateData(
		[]Node{node("0a"), node("0b"), node("1a"), node("1b"), node("1c"), node("2a")},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"}, {From: "0a", To: "1c"},
			{From: "1a", To: "2a"}, {From: "1b", To: "2a"}, {From: "1c", To: "2a"},
		},
	)
	// The group still exists (same member set) → still fanned, cursor still on 1b.
	testutil.Equal(t, w.Fanned(w.CursorPos().Stage, w.CursorPos().Slot), true)
	testutil.Equal(t, w.CurrentNodeID(), "1b")
}

// TestUpdateData_ClampsWhenCursorNodeVanishes: when the node under the cursor is
// gone from the new projection, the cursor clamps into the new layout rather than
// dangling at an out-of-range position.
func TestUpdateData_ClampsWhenCursorNodeVanishes(t *testing.T) {
	w := navGraph()
	w.MoveStage(2)
	testutil.Equal(t, w.CurrentNodeID(), "2a")

	// 2a (and its blocking edges) removed: the plan now ends at the stage-1 group.
	w.UpdateData(
		[]Node{node("0a"), node("1a"), node("1b"), node("1c")},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"}, {From: "0a", To: "1c"},
		},
	)
	// Cursor clamped to a valid stage within the smaller layout (no panic, in range).
	testutil.Equal(t, w.CursorPos().Stage < w.Stages(), true)
	testutil.Equal(t, w.CursorPos().Stage >= 0, true)
}

// TestUpdateData_CollapsedGroupCursorReanchors: a cursor resting on a COLLAPSED
// group re-anchors to that same group (by member-id set) after a structural
// change, not to stage 0.
func TestUpdateData_CollapsedGroupCursorReanchors(t *testing.T) {
	w := navGraph()
	w.MoveStage(1) // collapsed group [1a–1c] at stage 1, slot 0
	_, isGroup := w.GroupAt(w.CursorPos().Stage, w.CursorPos().Slot)
	testutil.Equal(t, isGroup, true)

	w.UpdateData(
		[]Node{node("0a"), node("0b"), node("1a"), node("1b"), node("1c"), node("2a")},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"}, {From: "0a", To: "1c"},
			{From: "1a", To: "2a"}, {From: "1b", To: "2a"}, {From: "1c", To: "2a"},
		},
	)
	_, stillGroup := w.GroupAt(w.CursorPos().Stage, w.CursorPos().Slot)
	testutil.Equal(t, stillGroup, true)
	testutil.Equal(t, w.CursorPos().Stage, 1)
}

// --- Selection highlight + centering (SimulationScreen Draw tests) ---

// drawToSim renders w into a fresh SimulationScreen sized (cols, rows) and
// returns the screen for cell inspection.
func drawToSim(t *testing.T, w *Widget, cols, rows int) tcell.SimulationScreen {
	t.Helper()
	sc := tcell.NewSimulationScreen("")
	if err := sc.Init(); err != nil {
		t.Fatalf("sim screen init: %v", err)
	}
	t.Cleanup(sc.Fini)
	sc.SetSize(cols, rows)
	w.SetRect(0, 0, cols, rows)
	w.Draw(sc)
	sc.Show()
	return sc
}

// boxCornerStyleNear returns the style of the rounded box top-left corner (`╭`)
// that sits at-or-left-of the glyph at column gx on the same row's box top (gy-1),
// i.e. the border of the box whose middle row holds that glyph. ok is false when
// no corner is found on that row.
func boxCornerStyleNear(sc tcell.SimulationScreen, gx, gy int) (tcell.Style, bool) {
	cells, w, _ := sc.GetContents()
	// The box top border is the row above the glyph row.
	row := gy - 1
	if row < 0 {
		return tcell.StyleDefault, false
	}
	for x := gx; x >= 0; x-- {
		c := cells[row*w+x]
		if len(c.Runes) > 0 && c.Runes[0] == '╭' {
			return c.Style, true
		}
	}
	return tcell.StyleDefault, false
}

// TestDraw_NodeRendersAsRoundedBox: every node renders as a 3-row rounded box
// (the artifact's boxed treatment), so the box corner/edge runes are present.
func TestDraw_NodeRendersAsRoundedBox(t *testing.T) {
	w := New()
	w.SetData([]Node{liveNode("0a", StateWorking)}, nil)
	w.SetFocused(true)
	out := drawToString(t, w, 60, 16)
	testutil.Contains(t, out, "╭")
	testutil.Contains(t, out, "╮")
	testutil.Contains(t, out, "╰")
	testutil.Contains(t, out, "╯")
	// The glyph + short-id ride the middle row.
	testutil.Contains(t, out, "⟳ 0a")
}

// boxGlyphCell finds the GLYPH cell of a node box by its content form
// "<glyph> <shortid>" (e.g. "⟳ 0a"), returning the glyph's (x,y) + style. This
// skips the header's Status-line glyph (BUG-006 added a second ⟳ to the header),
// which a bare findGlyphCell would match first. ok false when not found.
func boxGlyphCell(sc tcell.SimulationScreen, content string) (int, int, tcell.Style, bool) {
	x, y, ok := findStringCell(sc, content)
	if !ok {
		return 0, 0, tcell.StyleDefault, false
	}
	cells, w, _ := sc.GetContents()
	return x, y, cells[y*w+x].Style, true
}

// doubleBoxCornerStyleNear returns the style of the DOUBLE-LINE box top-left
// corner (`╔`) at-or-left-of the glyph at column gx, on the box top row (gy-1) —
// the double-border analogue of boxCornerStyleNear. ok is false when none found.
func doubleBoxCornerStyleNear(sc tcell.SimulationScreen, gx, gy int) (tcell.Style, bool) {
	cells, w, _ := sc.GetContents()
	row := gy - 1
	if row < 0 {
		return tcell.StyleDefault, false
	}
	for x := gx; x >= 0; x-- {
		c := cells[row*w+x]
		if len(c.Runes) > 0 && c.Runes[0] == '╔' {
			return c.Style, true
		}
	}
	return tcell.StyleDefault, false
}

// TestDraw_CursorBoxDoubleBorderSelection is the BUG-008 cue: the cursor's node
// box renders with a DOUBLE-LINE border (╔ ═ ║ …) in the node's OWN state colour
// (bold when focused) and state-coloured content — NO green selection colour, NO
// background fill anywhere. A non-cursor box keeps the single rounded border.
func TestDraw_CursorBoxDoubleBorderSelection(t *testing.T) {
	w := New()
	// stage0 [0a working] -> stage1 [1a working]. Cursor on 0a. Both working
	// (amber), so the selected box border is amber too — proving selection is the
	// glyph weight, not a hue.
	w.SetData([]Node{liveNode("0a", StateWorking), liveNode("1a", StateWorking)}, []Edge{{From: "0a", To: "1a"}})
	w.SetFocused(true)
	testutil.Equal(t, w.CurrentNodeID(), "0a")

	sc := drawToSim(t, w, 60, 20)
	cells, _, _ := sc.GetContents()
	// Selection is neither a colour nor a fill: no cell carries the old
	// ColorHighlight selection background, and no cell is the old green-selection.
	for i := range cells {
		_, bg, _ := cells[i].Style.Decompose()
		testutil.Equal(t, bg == theme.ColorHighlight, false)
	}
	// The double-line runes appear (the selected box drew them).
	out := drawToString(t, w, 60, 20)
	testutil.Contains(t, out, "╔")
	testutil.Contains(t, out, "╚")

	// 0a's box glyph cell content keeps its STATE colour (amber), not green.
	gx, gy, curGlyphStyle, ok := boxGlyphCell(sc, "⟳ 0a")
	testutil.Equal(t, ok, true)
	cfg, curBg, _ := curGlyphStyle.Decompose()
	testutil.Equal(t, cfg, theme.ColorInProgress)           // content keeps state colour, NOT green
	testutil.Equal(t, curBg == theme.ColorHighlight, false) // no background fill
	// Its corner is a DOUBLE-LINE corner in the state colour, bold (focused).
	curBorder, ok := doubleBoxCornerStyleNear(sc, gx, gy)
	testutil.Equal(t, ok, true)
	bfg, _, battr := curBorder.Decompose()
	testutil.Equal(t, bfg, theme.ColorInProgress)      // border = state colour (amber), not green
	testutil.Equal(t, battr&tcell.AttrBold != 0, true) // focused → bold

	// 1a (⟳, working) is NOT the cursor → single rounded box in the state colour,
	// non-bold; no double-line corner sits left of its glyph.
	ox, oy, otherGlyphStyle, ok2 := boxGlyphCell(sc, "⟳ 1a")
	testutil.Equal(t, ok2, true)
	ofg, _, _ := otherGlyphStyle.Decompose()
	testutil.Equal(t, ofg, theme.ColorInProgress)
	_, isDouble := doubleBoxCornerStyleNear(sc, ox, oy)
	testutil.Equal(t, isDouble, false) // unselected → no double border
	roundBorder, ok := boxCornerStyleNear(sc, ox, oy)
	testutil.Equal(t, ok, true)
	rfg, _, rattr := roundBorder.Decompose()
	testutil.Equal(t, rfg, theme.ColorInProgress)       // rounded border in state colour
	testutil.Equal(t, rattr&tcell.AttrBold != 0, false) // unselected → not bold
}

// TestDraw_SelectedDoneDistinctFromUnselectedDone proves a selected DONE (green)
// node is distinguishable from an unselected done node: the selected one draws a
// DOUBLE border, the unselected one a single rounded border — both green. This is
// the exact collision the BUG-008 rework removed (green selection vs green done).
func TestDraw_SelectedDoneDistinctFromUnselectedDone(t *testing.T) {
	w := New()
	// stage0 [0a done] -> stage1 [1a done]. Cursor on 0a (selected, done/green).
	w.SetData([]Node{liveNode("0a", StateDone), liveNode("1a", StateDone)}, []Edge{{From: "0a", To: "1a"}})
	w.SetFocused(true)
	testutil.Equal(t, w.CurrentNodeID(), "0a")

	sc := drawToSim(t, w, 60, 20)

	// Selected done node: DOUBLE border, green, bold.
	sx, sy, _, ok := boxGlyphCell(sc, "✓ 0a")
	testutil.Equal(t, ok, true)
	selBorder, ok := doubleBoxCornerStyleNear(sc, sx, sy)
	testutil.Equal(t, ok, true)
	sfg, _, sattr := selBorder.Decompose()
	testutil.Equal(t, sfg, theme.ColorComplete) // green (done)
	testutil.Equal(t, sattr&tcell.AttrBold != 0, true)

	// Unselected done node: single rounded border, green, NOT a double border.
	ux, uy, _, ok2 := boxGlyphCell(sc, "✓ 1a")
	testutil.Equal(t, ok2, true)
	_, isDouble := doubleBoxCornerStyleNear(sc, ux, uy)
	testutil.Equal(t, isDouble, false)
	unselBorder, ok := boxCornerStyleNear(sc, ux, uy)
	testutil.Equal(t, ok, true)
	ufg, _, uattr := unselBorder.Decompose()
	testutil.Equal(t, ufg, theme.ColorComplete) // also green (done)
	testutil.Equal(t, uattr&tcell.AttrBold != 0, false)
}

// TestDraw_StageBoxCentered: a single node box is centered horizontally — its
// box glyph sits well right of the inner left edge.
func TestDraw_StageBoxCentered(t *testing.T) {
	w := New()
	w.SetData([]Node{liveNode("0a", StateWorking)}, nil)
	w.SetFocused(true)
	sc := drawToSim(t, w, 60, 18)
	x, _, _, ok := boxGlyphCell(sc, "⟳ 0a")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, x > 4, true)
}

// TestDraw_WiderRegionPushesBoxFurtherRight: doubling the region width moves the
// centered box further right — centering tracks (W - boxWidth)/2.
func TestDraw_WiderRegionPushesBoxFurtherRight(t *testing.T) {
	w := New()
	w.SetData([]Node{liveNode("0a", StateWorking)}, nil)
	w.SetFocused(true)
	narrow := drawToSim(t, w, 40, 18)
	xNarrow, _, _, ok1 := boxGlyphCell(narrow, "⟳ 0a")
	testutil.Equal(t, ok1, true)

	w2 := New()
	w2.SetData([]Node{liveNode("0a", StateWorking)}, nil)
	w2.SetFocused(true)
	wide := drawToSim(t, w2, 80, 18)
	xWide, _, _, ok2 := boxGlyphCell(wide, "⟳ 0a")
	testutil.Equal(t, ok2, true)
	testutil.Equal(t, xWide > xNarrow, true)
}

// TestDraw_FooterHintPresent: the dim nav legend renders on a bottom row.
func TestDraw_FooterHintPresent(t *testing.T) {
	w := New()
	w.SetData([]Node{liveNode("0a", StateWorking)}, nil)
	out := drawToString(t, w, 60, 16)
	testutil.Contains(t, out, "stage")
	testutil.Contains(t, out, "Enter fan")
}

// TestDraw_ScrollKeepsCursorBoxVisible: with many stages in a short region the
// block overflows; the cursor's stage box must be painted within the region
// (the view scrolls to follow the cursor). Moving the cursor to the last stage
// makes its short-id visible even though it sits far past the region height.
func TestDraw_ScrollKeepsCursorBoxVisible(t *testing.T) {
	w := New()
	// A linear chain of 8 stages — boxes are 3 rows each, so the block (≈31 rows)
	// far exceeds a 12-row screen.
	var nodes []Node
	var edges []Edge
	ids := []string{"0a", "1a", "2a", "3a", "4a", "5a", "6a", "7a"}
	prev := ""
	for _, id := range ids {
		nodes = append(nodes, node(id))
		if prev != "" {
			edges = append(edges, Edge{From: prev, To: id})
		}
		prev = id
	}
	w.SetData(nodes, edges)
	w.SetFocused(true)
	// Walk to the last stage.
	for i := 0; i < len(ids); i++ {
		w.MoveStage(1)
	}
	testutil.Equal(t, w.CursorPos().Stage, len(ids)-1)

	out := drawToString(t, w, 50, 12)
	// The last node's short-id "7a" must be visible (scrolled into view), and the
	// first "0a" scrolled off.
	testutil.Contains(t, out, "7a")
	testutil.Equal(t, strings.Contains(out, "0a"), false)
}
