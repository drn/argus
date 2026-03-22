package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

func TestLazyScreen_ClearDelegatesByDefault(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	sim.Init()
	defer sim.Fini()
	sim.SetSize(10, 5)

	ls := &lazyScreen{Screen: sim}

	// Write a character, then Clear — should erase it.
	sim.SetContent(0, 0, 'X', nil, tcell.StyleDefault)
	ls.Clear()
	str, _, _ := sim.Get(0, 0)
	testutil.Equal(t, str, " ")
}

func TestLazyScreen_ClearSkippedWhenFlagged(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	sim.Init()
	defer sim.Fini()
	sim.SetSize(10, 5)

	ls := &lazyScreen{Screen: sim}

	// Write a character, set skipClear, then Clear — should preserve the character.
	sim.SetContent(0, 0, 'X', nil, tcell.StyleDefault)
	ls.skipClear = true
	ls.Clear()
	str, _, _ := sim.Get(0, 0)
	testutil.Equal(t, str, "X")

	// Flag should be consumed — next Clear should work normally.
	testutil.Equal(t, ls.skipClear, false)
	sim.SetContent(0, 0, 'Y', nil, tcell.StyleDefault)
	ls.Clear()
	str, _, _ = sim.Get(0, 0)
	testutil.Equal(t, str, " ")
}
