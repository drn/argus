package hera

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestFocusMachine_AdvanceRetreat(t *testing.T) {
	f := NewFocusMachine() // both panes present, starts on rail
	testutil.Equal(t, f.State(), FocusRail)

	f.Advance()
	testutil.Equal(t, f.State(), FocusCoord)
	f.Advance()
	testutil.Equal(t, f.State(), FocusAgent)
	f.Advance() // already right-most
	testutil.Equal(t, f.State(), FocusAgent)

	f.Retreat()
	testutil.Equal(t, f.State(), FocusCoord)
	f.Retreat()
	testutil.Equal(t, f.State(), FocusRail)
	f.Retreat() // already left-most
	testutil.Equal(t, f.State(), FocusRail)
}

func TestFocusMachine_AdvanceSkipsAbsentCoord(t *testing.T) {
	f := NewFocusMachine()
	f.SetCoordPresent(false) // freelance mode: rail ↔ agent
	testutil.Equal(t, f.State(), FocusRail)

	f.Advance()
	testutil.Equal(t, f.State(), FocusAgent) // skipped absent coord

	f.Retreat()
	testutil.Equal(t, f.State(), FocusRail)
}

func TestFocusMachine_AdvanceWithOnlyCoord(t *testing.T) {
	f := NewFocusMachine()
	changed := f.SetAgentPresent(false) // coordinator mode: rail ↔ coord
	testutil.Equal(t, changed, false)   // was on rail, no bump needed

	f.Advance()
	testutil.Equal(t, f.State(), FocusCoord)
	f.Advance() // right-most present is coord
	testutil.Equal(t, f.State(), FocusCoord)
}

func TestFocusMachine_RebalanceBumpsOffAbsentPane(t *testing.T) {
	f := NewFocusMachine()
	f.Advance()
	f.Advance()
	testutil.Equal(t, f.State(), FocusAgent)

	// Agent disappears → focus bumps to coord.
	changed := f.SetAgentPresent(false)
	testutil.Equal(t, changed, true)
	testutil.Equal(t, f.State(), FocusCoord)

	// Coord disappears too → focus bumps to rail.
	changed = f.SetCoordPresent(false)
	testutil.Equal(t, changed, true)
	testutil.Equal(t, f.State(), FocusRail)
}

func TestFocusMachine_ToRail(t *testing.T) {
	f := NewFocusMachine()
	f.Advance()
	testutil.Equal(t, f.State(), FocusCoord)
	f.ToRail()
	testutil.Equal(t, f.State(), FocusRail)
}

func TestFocusMachine_SetPresentNoChangeReturnsFalse(t *testing.T) {
	f := NewFocusMachine() // on rail
	// Removing the agent while focused on the rail does not move focus.
	testutil.Equal(t, f.SetAgentPresent(false), false)
}
