package hera

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/testutil"
)

func TestBoardAttentionJump(t *testing.T) {
	for _, tc := range []struct {
		name                                             string
		nested, shared, folded, disabled, needs, unbound bool
	}{
		{name: "top-level header"},
		{name: "nested bridge", nested: true},
		{name: "folded nested bridge", nested: true, folded: true},
		{name: "shared task exact header", shared: true, folded: true},
		{name: "both signals", needs: true},
		{name: "unbound coordinator", unbound: true},
		{name: "disabled", disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := coordOf(1, "parent", 10, "parent-task")
			task := "board-task"
			if tc.shared {
				task = "parent-task"
			}
			if tc.unbound {
				task = ""
			}
			if tc.nested {
				parent.Roles = append(parent.Roles, heramodel.RoleView{RoleID: 11, OrchID: 1, Name: "bridge", Kind: db.HeraKindWorker, TaskID: task, BridgeTaskID: task, Live: true})
			}
			board := coordOf(2, "board", 20, task)
			if tc.unbound {
				board.Roles[0].Live = false
			}
			board.Roles[0].BoardUnread = 2
			board.Roles[0].BoardItems = []*db.HeraBoardItem{
				{ID: 1, Subject: "Already read", Read: true},
				{ID: 2, Subject: "Choose a release"},
				{ID: 3, Subject: "Another unread item"},
			}
			board.Roles[0].NeedsInput = tc.needs
			m := heramodel.Model{Active: []heramodel.OrchView{parent, board}}
			m.RollupNeedsInput()
			p := NewHeraPage(memDB(t))
			reads := 0
			p.OnBoardRead = func(int64, int64, time.Time) { reads++ }
			var reattached []string
			p.OnReattach = func(sel heramodel.Selection) {
				reattached = append(reattached, sel.FocusTaskID())
			}
			p.SetBoardAuto(!tc.disabled)
			r := p.Rail()
			r.collapsed[1] = tc.folded
			r.collapsed[2] = tc.folded
			r.SetModel(m)
			testutil.Equal(t, r.NeedsInputCount(), map[bool]int{true: 0, false: 1}[tc.disabled])
			id, ok := r.NextNeedsInputTaskID()
			testutil.Equal(t, ok, !tc.disabled)
			if tc.disabled {
				return
			}
			testutil.Equal(t, id, task)
			testutil.True(t, p.JumpToNextNeedsInput())
			testutil.Equal(t, p.detailsOrch().ID, int64(2))
			testutil.True(t, p.detailsMode)
			testutil.True(t, p.showBoard)
			testutil.Equal(t, p.focus.State(), FocusAgent)
			testutil.Equal(t, p.board.cursor, 1)
			testutil.True(t, p.board.followCursor)
			testutil.Equal(t, p.board.expandedID, int64(0))
			testutil.Equal(t, reads, 0)
			testutil.False(t, p.board.items[1].Read)
			testutil.False(t, p.board.items[2].Read)
			if tc.unbound {
				testutil.Equal(t, len(reattached), 0)
			} else {
				testutil.DeepEqual(t, reattached, []string{task})
			}
			testutil.False(t, r.OrchCollapsed(2))
			if tc.nested || tc.shared {
				testutil.False(t, r.OrchCollapsed(1))
			}
			// Even the sole candidate already under the cursor must auto-land.
			p.showBoard = false
			p.board.cursor = 2
			p.board.followCursor = false
			// Ordinary refresh preserves the selected plan and cursor.
			r.SetModel(m)
			testutil.False(t, p.showBoard)
			testutil.Equal(t, p.board.cursor, 2)
			testutil.True(t, p.JumpToNextNeedsInput())
			testutil.True(t, p.showBoard)
			testutil.Equal(t, p.focus.State(), FocusAgent)
			testutil.Equal(t, p.board.cursor, 1)
			testutil.True(t, p.board.followCursor)
			testutil.Equal(t, p.board.expandedID, int64(0))
			testutil.Equal(t, reads, 0)
		})
	}
}

func TestBoardAttentionMixedCycleAndPopup(t *testing.T) {
	parent := coordOf(1, "parent", 10, "tc",
		heramodel.RoleView{RoleID: 11, OrchID: 1, Name: "worker", Kind: db.HeraKindWorker, Live: true, TaskID: "tw", NeedsInput: true})
	parent.Roles[0].BoardUnread = 2
	parent.Roles[0].NeedsInput = true
	other := coordOf(2, "other", 20, "to")
	other.Roles[0].BoardUnread = 1
	m := heramodel.Model{Active: []heramodel.OrchView{parent, other}}
	m.RollupNeedsInput()
	p := NewHeraPage(memDB(t))
	p.Rail().SetModel(m)
	r := p.Rail()
	testutil.Equal(t, r.NeedsInputCount(), 3)
	for _, want := range []string{"tw", "to", "tc", "tw", "to"} {
		testutil.True(t, p.JumpToNextNeedsInput())
		testutil.Equal(t, p.SelectionContext().FocusTaskID(), want)
		testutil.Equal(t, p.focus.State(), FocusAgent)
		testutil.Equal(t, p.detailsMode, want != "tw")
		if want != "tw" {
			testutil.True(t, p.showBoard)
		}
	}
	for i, want := range []string{"tc", "tw", "to"} {
		testutil.True(t, r.CursorToNextNeedsInput(i == 0))
		testutil.Equal(t, r.Selection().FocusTaskID(), want)
	}
	testutil.False(t, r.CursorToNextNeedsInput(false))
}

func TestBoardAttentionExcursionIdentity(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{true: "disabled", false: "enabled"}[disabled], func(t *testing.T) {
			model := func(first, second int) heramodel.Model {
				a, b := coordOf(1, "a", 10, "ta"), coordOf(2, "b", 20, "tb")
				a.Roles[0].BoardUnread, b.Roles[0].BoardUnread = first, second
				return heramodel.Model{Active: []heramodel.OrchView{a, b}}
			}
			p := NewHeraPage(memDB(t))
			p.SetBoardAuto(!disabled)
			r := p.Rail()
			r.SetModel(model(1, 0)) // cold-start seeds only
			testutil.False(t, r.HasExcursionSnapshot())
			r.SetModel(model(0, 0))
			r.collapsed[2] = true
			r.SetModel(model(1, 0))
			testutil.Equal(t, r.HasExcursionSnapshot(), !disabled)
			if disabled {
				return
			}
			testutil.True(t, r.RestoreExcursion())
			r.SetModel(model(2, 0)) // new item, same coordinator identity
			testutil.False(t, r.HasExcursionSnapshot())
			r.collapsed[2] = false
			r.SetModel(model(0, 1)) // same count, distinct identity
			testutil.True(t, r.HasExcursionSnapshot())
			testutil.True(t, r.RestoreExcursion())
			testutil.False(t, r.OrchCollapsed(2))
			r.SetModel(model(0, 1))
			testutil.False(t, r.HasExcursionSnapshot())
		})
	}
}
