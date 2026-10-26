package tui

import (
	"strconv"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
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
			_, err := d.PostHeraBoardItem(c.ID, "decision", "Release approval needed", "Choose the release")
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
				// Assert rendered board content: selection alone could pass with
				// the details region still showing its plan instead of the board.
				w, h := sim.Size()
				testutil.Contains(t, screenText(sim, w, h), "Release approval needed")
			})
		})
	}
}
