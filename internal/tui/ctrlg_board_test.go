package tui

import (
	"strconv"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

func TestSmoke_CtrlGUnreadBoard(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			d := testDB(t)
			oid := seedHeraOrch(t, d, "board-jump")
			c := seedHeraBoundRole(t, d, oid, "coord", db.HeraKindCoordinator, "tc")
			read, err := d.PostHeraBoardItem(c.ID, "fyi", "Already read", "Previous update")
			testutil.NoError(t, err)
			testutil.NoError(t, d.ReadHeraBoardItem(c.ID, read.ID))
			first, err := d.PostHeraBoardItem(c.ID, "decision", "Release approval needed", "Choose the release")
			testutil.NoError(t, err)
			second, err := d.PostHeraBoardItem(c.ID, "decision", "Second decision", "Another choice")
			testutil.NoError(t, err)
			testutil.NoError(t, d.SetConfigValue("ui.coordinator_board", strconv.FormatBool(enabled)))
			app := New(d, agent.NewRunner(nil), false)
			sim, stop := wireApp(t, app)
			defer stop()
			sim.SetSize(160, 50)
			readUI(t, app.tapp, func() { app.heraPage.Refresh() })
			sim.InjectKey(tcell.KeyCtrlG, 0, 0)
			syncUI(t, app.tapp)
			readUI(t, app.tapp, func() {
				if !enabled {
					testutil.Equal(t, app.header.ActiveTab(), widget.TabTasks)
					return
				}
				testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
				testutil.Equal(t, app.heraPage.SelectionContext().FocusTaskID(), "tc")
				testutil.Nil(t, app.heraPage.SelectionContext().Role)
				testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusAgent)
				// Assert rendered board content: selection alone could pass with
				// the details region still showing its plan instead of the board.
				w, h := sim.Size()
				testutil.Contains(t, screenText(sim, w, h), "Release approval needed")
			})
			items, err := d.ListHeraBoardItems(c.ID, false)
			testutil.NoError(t, err)
			testutil.True(t, items[0].Read)
			testutil.False(t, items[1].Read)
			testutil.False(t, items[2].Read)
			if !enabled {
				return
			}
			// Observe the existing acknowledgment seam synchronously: landing
			// and j/k must not acknowledge; Enter must expand the selected item.
			var expanded []int64
			readUI(t, app.tapp, func() {
				app.heraPage.OnBoardRead = func(_ int64, id int64, _ time.Time) {
					expanded = append(expanded, id)
				}
			})
			sim.InjectKey(tcell.KeyRune, 'j', 0)
			sim.InjectKey(tcell.KeyEnter, 0, 0)
			syncUI(t, app.tapp)
			readUI(t, app.tapp, func() { testutil.DeepEqual(t, expanded, []int64{second.ID}) })
			sim.InjectKey(tcell.KeyRune, 'k', 0)
			sim.InjectKey(tcell.KeyEnter, 0, 0)
			syncUI(t, app.tapp)
			readUI(t, app.tapp, func() { testutil.DeepEqual(t, expanded, []int64{second.ID, first.ID}) })
		})
	}
}

// Re-entering the Hera tab (here via Cmd+→) onto a coordinator that received
// board mail while the tab was away must open its board, not keep the plan the
// earlier visit chose.
func TestSmoke_TabEntryLandsUnreadBoard(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("ui.cross_tab_arrows", "true"))
	oid := seedHeraOrch(t, d, "board-entry")
	c := seedHeraBoundRole(t, d, oid, "coord", db.HeraKindCoordinator, "tc")
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	sim.SetSize(160, 50)
	cmd := func(k tcell.Key) {
		sim.InjectKey(k, 0, tcell.ModCtrl|tcell.ModAlt)
		syncUI(t, app.tapp)
	}

	cmd(tcell.KeyRight)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
		testutil.Equal(t, app.heraPage.SelectionContext().FocusTaskID(), "tc")
	})
	cmd(tcell.KeyLeft)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.header.ActiveTab(), widget.TabTasks) })

	_, err := d.PostHeraBoardItem(c.ID, "decision", "Release approval needed", "Choose the release")
	testutil.NoError(t, err)
	cmd(tcell.KeyRight)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
		testutil.Equal(t, app.heraPage.SelectionContext().FocusTaskID(), "tc")
		testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusRail)
		w, h := sim.Size()
		testutil.Contains(t, screenText(sim, w, h), "Release approval needed")
	})
}
