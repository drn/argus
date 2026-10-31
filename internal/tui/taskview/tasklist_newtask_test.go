package taskview

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
)

func TestDrawTaskRow_NewTaskIndicator(t *testing.T) {
	t.Run("new task draws marker before name", func(t *testing.T) {
		sim := newSim(t, 60, 3)
		tl := NewTaskListView()
		task := &model.Task{ID: "1", Name: "fix-bug", Project: "p", Status: model.StatusInProgress}
		tl.SetTasks([]*model.Task{task})
		tl.SetNewTasks(map[string]bool{"1": true})

		tl.drawTaskRow(sim, 0, 0, 60, task, false)

		got, style, _ := sim.Get(prCellCol, 0)
		testutil.Equal(t, firstRune(got), theme.IconNewTask)
		testutil.Equal(t, style, theme.StyleNewTask)
		testutil.Contains(t, readRow(sim, 0, 60), "fix-bug")
		first, _, _ := sim.Get(prNameCol, 0)
		testutil.Equal(t, firstRune(first), 'f')
	})

	t.Run("new marker precedes PR glyph", func(t *testing.T) {
		sim := newSim(t, 60, 3)
		tl := NewTaskListView()
		task := &model.Task{ID: "1", Name: "fix-bug", Project: "p", Status: model.StatusInReview}
		tl.SetTasks([]*model.Task{task})
		tl.SetNewTasks(map[string]bool{"1": true})
		tl.SetPRStates(map[string]model.PRState{"1": model.PRApproved})

		tl.drawTaskRow(sim, 0, 0, 60, task, false)

		got, _, _ := sim.Get(prCellCol, 0)
		testutil.Equal(t, firstRune(got), theme.IconNewTask)
		pr, _, _ := sim.Get(prNameCol, 0)
		testutil.Equal(t, firstRune(pr), theme.IconPRApproved)
	})

	t.Run("not new reclaims the cell", func(t *testing.T) {
		sim := newSim(t, 60, 3)
		tl := NewTaskListView()
		task := &model.Task{ID: "1", Name: "fix-bug", Project: "p", Status: model.StatusInProgress}
		tl.SetTasks([]*model.Task{task})
		tl.SetNewTasks(map[string]bool{"other": true})

		tl.drawTaskRow(sim, 0, 0, 60, task, false)

		got, _, _ := sim.Get(prCellCol, 0)
		testutil.Equal(t, firstRune(got), 'f')
	})
}

func TestDrawProjectRow_NewTaskMarker(t *testing.T) {
	cases := []struct {
		name    string
		newIDs  map[string]bool
		width   int
		wantNew bool
	}{
		{"project with new task", map[string]bool{"2": true}, 40, true},
		{"new task in other project", map[string]bool{"3": true}, 40, false},
		{"no new tasks", nil, 40, false},
		{"too narrow", map[string]bool{"2": true}, 12, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sim := newSim(t, 40, 3)
			tl := NewTaskListView()
			tl.SetTasks([]*model.Task{
				{ID: "1", Name: "a", Project: "forge", Status: model.StatusPending},
				{ID: "2", Name: "b", Project: "forge", Status: model.StatusInProgress},
				{ID: "3", Name: "c", Project: "argus", Status: model.StatusPending},
			})
			tl.SetNewTasks(tc.newIDs)

			tl.drawProjectRow(sim, 0, 0, tc.width, "forge", sectionActive)

			row := strings.TrimRight(readRow(sim, 0, 40), " ")
			testutil.Equal(t, strings.ContainsRune(row, theme.IconNewTask), tc.wantNew)
			if tc.wantNew {
				testutil.Equal(t, strings.HasSuffix(row, "forge (2) "+string(theme.IconNewTask)), true)
			}
		})
	}
}

func TestSetNewTasks_NilSafe(t *testing.T) {
	tl := NewTaskListView()
	tl.SetNewTasks(map[string]bool{"1": true})
	testutil.Equal(t, tl.IsNewTask("1"), true)
	tl.SetNewTasks(nil)
	testutil.Equal(t, tl.IsNewTask("1"), false)
}
