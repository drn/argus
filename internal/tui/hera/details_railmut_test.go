package hera

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// TestDetailsMode_RailMutationKeysRouted is the BUG-010 regression guard: with a
// coordinator selected (details mode) and focus parked in the Details/plan region
// (FocusAgent — where BUG-002's plan-Enter→jumpToLeaf lands it), the rail-mutation
// keys must still reach handleRailMutation and fire their callbacks against the
// selected coordinator. Before the fix the plan widget swallowed them, so pin (P)
// and friends silently no-op'd while the Details view was open.
func TestDetailsMode_RailMutationKeysRouted(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true)
	toAgentFocus(p)
	testutil.Equal(t, p.Machine().State(), FocusAgent)

	var got string
	var gotSel Selection
	record := func(name string) func(Selection) {
		return func(s Selection) { got = name; gotSel = s }
	}
	p.OnSpawnWorker = record("spawn")
	p.OnRename = record("rename")
	p.OnArchiveToggle = record("archive")
	p.OnPinToggle = record("pin")
	p.OnStatusAdvance = record("adv")
	p.OnStatusRevert = record("rev")
	p.OnDelete = record("delete")
	p.OnAdopt = record("adopt")
	p.OnClearArchive = record("clear")

	h := p.InputHandler()
	cases := []struct {
		ev   *tcell.EventKey
		want string
	}{
		{tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone), "pin"},
		{tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone), "spawn"},
		{tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone), "rename"},
		{tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), "archive"},
		{tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone), "adv"},
		{tcell.NewEventKey(tcell.KeyRune, 'S', tcell.ModNone), "rev"},
		{tcell.NewEventKey(tcell.KeyRune, 'J', tcell.ModNone), "adopt"},
		{tcell.NewEventKey(tcell.KeyRune, 'C', tcell.ModNone), "clear"},
		{tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModNone), "delete"},
	}
	for _, c := range cases {
		got = ""
		h(c.ev, noFocus)
		testutil.Equal(t, got, c.want)
		// Focus must stay in the Details region — a mutation key never moves it.
		testutil.Equal(t, p.Machine().State(), FocusAgent)
		// Every fire targets the selected coordinator's orchestrator.
		testutil.Equal(t, gotSel.Orch != nil, true)
	}
}

// TestDetailsMode_PinFiresWithRecordingHook narrows BUG-010 to the reported key:
// pressing P in details mode fires OnPinToggle exactly once.
func TestDetailsMode_PinFiresWithRecordingHook(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	toAgentFocus(p)

	pins := 0
	p.OnPinToggle = func(Selection) { pins++ }

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone), noFocus)
	testutil.Equal(t, pins, 1)
}

// TestDetailsMode_PlanNavKeysNotHijacked proves the fix is surgical: plan-NAV
// keys are NOT intercepted by the rail-mutation routing — j/k still move the
// embedded plan widget's stage cursor (reaching handleDetailsKey → the plan),
// and pressing them fires NO rail-mutation callback.
func TestDetailsMode_PlanNavKeysNotHijacked(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	toAgentFocus(p)

	mutated := false
	// Wire every mutation callback so any stray dispatch is caught.
	p.OnSpawnWorker = func(Selection) { mutated = true }
	p.OnRename = func(Selection) { mutated = true }
	p.OnArchiveToggle = func(Selection) { mutated = true }
	p.OnPinToggle = func(Selection) { mutated = true }
	p.OnStatusAdvance = func(Selection) { mutated = true }
	p.OnStatusRevert = func(Selection) { mutated = true }
	p.OnDelete = func(Selection) { mutated = true }
	p.OnAdopt = func(Selection) { mutated = true }
	p.OnClearArchive = func(Selection) { mutated = true }
	p.OnNewCoordinator = func(Selection) { mutated = true }

	testutil.Equal(t, p.Plan().CursorPos().Stage, 0)
	h := p.InputHandler()
	// j → plan widget moves down a stage (not a rail mutation).
	h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
	testutil.Equal(t, p.Plan().CursorPos().Stage, 1)
	testutil.Equal(t, mutated, false)
	// k → back up.
	h(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), noFocus)
	testutil.Equal(t, p.Plan().CursorPos().Stage, 0)
	testutil.Equal(t, mutated, false)
}

// TestDetailsMode_EnterNotHijackedByRailMutation guards the most dangerous
// overlap: Enter IS a rail-focus command (reattach), but in details mode it
// belongs to the plan widget (fan-out / drill / open-leaf via the page-owned
// OnEnter). isRailMutationKey excludes Enter, so it must reach the plan widget,
// NOT the rail's reattach path.
func TestDetailsMode_EnterNotHijackedByRailMutation(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	toAgentFocus(p)
	testutil.Equal(t, p.isRailMutationKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)), false)

	reattached := false
	p.OnReattach = func(Selection) { reattached = true }
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	// Enter went to the plan widget, not the rail's reattach handler.
	testutil.Equal(t, reattached, false)
}
