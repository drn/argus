package tui

import (
	"strconv"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestSwitcherPopupBoardAttention(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			feed := switcherFeed()
			feed.CoordinatorBoard = enabled
			feed.HeraModel.Active[0].Roles[0].BoardUnread = 1
			feed.HeraModel.Active[0].KanbanStatus = db.HeraKanbanBacklog
			p := newPopup(t, feed, SwitcherSeed{CurrentTaskID: "a1"})
			if enabled {
				testutil.Equal(t, p.active, SwitcherColumnProjects)
				testutil.Equal(t, p.rail.Selection().FocusTaskID(), "hc")
			} else {
				testutil.Equal(t, p.active, SwitcherColumnTasks)
				testutil.Equal(t, selTask(p), "a1")
			}
			// The active setting must also flow through the per-tick refeed.
			feed.CoordinatorBoard = !enabled
			p.Refeed(feed)
			press(p, tcell.KeyCtrlG, 0)
			if enabled {
				testutil.Equal(t, p.rail.NeedsInputCount(), 0)
			} else {
				testutil.Equal(t, p.active, SwitcherColumnProjects)
				testutil.Equal(t, p.rail.Selection().FocusTaskID(), "hc")
			}
		})
	}
}

func TestJumpSwitcherFeedsBoardSetting(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			d := testDB(t)
			testutil.NoError(t, d.SetConfigValue("ui.coordinator_board", strconv.FormatBool(enabled)))
			app := New(d, agent.NewRunner(nil), false)
			testutil.Equal(t, app.jumpSwitcherFeed(switcherReads{}).CoordinatorBoard, enabled)
		})
	}
}
