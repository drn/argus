package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/gdamore/tcell/v2"
)

func addTaskRow(t *testing.T, d *db.DB, id string, deps ...string) {
	t.Helper()
	testutil.NoError(t, d.Add(&model.Task{
		ID: id, Name: id, Status: model.StatusInProgress, Project: "p",
		DependsOn: deps, CreatedAt: time.Now(),
	}))
}

// --- pure helpers ----------------------------------------------------------

func TestScopeTasksToOrch(t *testing.T) {
	tasks := []*model.Task{
		{ID: "t-coord"}, {ID: "t-wkr"}, {ID: "t-other"}, {ID: "t-dead"},
	}
	o := &hera.OrchView{
		ID:   1,
		Name: "orch",
		Roles: []hera.RoleView{
			{Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t-coord"},
			{Name: "wkr", Kind: db.HeraKindWorker, Live: true, TaskID: "t-wkr"},
			{Name: "dead", Kind: db.HeraKindWorker, Live: false, TaskID: "t-dead"}, // not live → excluded
			{Name: "unbound", Kind: db.HeraKindWorker, Live: true, TaskID: ""},     // no task → excluded
		},
	}
	got := scopeTasksToOrch(tasks, o)
	ids := make([]string, 0, len(got))
	for _, tk := range got {
		ids = append(ids, tk.ID)
	}
	testutil.DeepEqual(t, ids, []string{"t-coord", "t-wkr"})

	// Nil orch → nil.
	testutil.Nil(t, scopeTasksToOrch(tasks, nil))
}

func TestFindTaskAndSort(t *testing.T) {
	tasks := []*model.Task{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}}
	testutil.Equal(t, findTask(tasks, "b").Name, "b")
	testutil.Nil(t, findTask(tasks, "missing"))

	entries := []taskSwitcherEntry{{ID: "2", Name: "Zeta"}, {ID: "1", Name: "alpha"}, {ID: "3", Name: "alpha"}}
	sortTaskPickerEntries(entries)
	testutil.Equal(t, entries[0].Name, "alpha")
	testutil.Equal(t, entries[0].ID, "1") // tie broken on ID
	testutil.Equal(t, entries[2].Name, "Zeta")
}

// --- doLink / doUnlink ------------------------------------------------------

func TestDoLink_Success(t *testing.T) {
	d := testDB(t)
	addTaskRow(t, d, "parent")
	addTaskRow(t, d, "child")
	app := New(d, agent.NewRunner(nil), false)

	app.doLink("child", "parent")

	got, err := d.Get("child")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got.DependsOn, []string{"parent"})
	testutil.Equal(t, app.header.Notice(), "linked")
}

func TestDoLink_CycleRejected(t *testing.T) {
	d := testDB(t)
	addTaskRow(t, d, "parent", "child") // parent already depends on child
	addTaskRow(t, d, "child")
	app := New(d, agent.NewRunner(nil), false)

	// Linking child → parent would close child→parent→child.
	app.doLink("child", "parent")

	got, err := d.Get("child")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got.DependsOn), 0) // link NOT created
	testutil.Contains(t, app.header.Notice(), "cycle")
}

func TestDoUnlink_RemovesEdge(t *testing.T) {
	d := testDB(t)
	addTaskRow(t, d, "parent")
	addTaskRow(t, d, "child", "parent")
	app := New(d, agent.NewRunner(nil), false)

	app.doUnlink("child", "parent")

	got, err := d.Get("child")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got.DependsOn), 0)
	testutil.Equal(t, app.header.Notice(), "unlinked")
}

func TestDoLinkUnlink_EmptyArgsNoop(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.doLink("", "x")   // no panic, no-op
	app.doUnlink("x", "") // no panic, no-op
}

// --- link / unlink pickers --------------------------------------------------

func TestOpenLinkPicker_FiltersCandidates(t *testing.T) {
	d := testDB(t)
	addTaskRow(t, d, "child", "existing") // child already depends on "existing"
	addTaskRow(t, d, "existing")
	addTaskRow(t, d, "free")
	// archived candidate must be excluded
	testutil.NoError(t, d.Add(&model.Task{ID: "old", Name: "old", Status: model.StatusComplete, Archived: true, Project: "p", CreatedAt: time.Now()}))
	app := New(d, agent.NewRunner(nil), false)

	app.openLinkPickerForTask("child")
	testutil.Equal(t, app.mode, modeHeraPicker)
	if app.heraPickerModal == nil {
		t.Fatal("expected link picker modal to open")
	}

	ids := pickerIDs(app.heraPickerModal)
	// Only "free" is a valid parent: child (self), existing (already a parent),
	// old (archived) are all excluded.
	testutil.DeepEqual(t, ids, []string{"free"})
}

func TestOpenLinkPicker_NoCandidatesNotice(t *testing.T) {
	d := testDB(t)
	addTaskRow(t, d, "lonely")
	app := New(d, agent.NewRunner(nil), false)

	app.openLinkPickerForTask("lonely")
	testutil.Equal(t, app.mode, modeTaskList) // never opened the picker
	testutil.Contains(t, app.header.Notice(), "no available parent")
}

func TestOpenUnlinkPicker_NoParentsNotice(t *testing.T) {
	d := testDB(t)
	addTaskRow(t, d, "child")
	app := New(d, agent.NewRunner(nil), false)

	app.openUnlinkPickerForTask("child")
	testutil.Equal(t, app.mode, modeTaskList)
	testutil.Contains(t, app.header.Notice(), "no parent links")
}

func TestOpenUnlinkPicker_OffersParents(t *testing.T) {
	d := testDB(t)
	addTaskRow(t, d, "p1")
	addTaskRow(t, d, "p2")
	addTaskRow(t, d, "child", "p1", "p2")
	app := New(d, agent.NewRunner(nil), false)

	app.openUnlinkPickerForTask("child")
	testutil.Equal(t, app.mode, modeHeraPicker)
	testutil.DeepEqual(t, pickerIDs(app.heraPickerModal), []string{"p1", "p2"})
}

// TestHeraPicker_SelectRunsSubmit drives the picker to a selection and asserts
// the submit callback fires with the chosen task; cancel closes without firing.
func TestHeraPicker_SelectRunsSubmit(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	var chosen string
	app.openHeraPicker(" Pick ", "help", []taskSwitcherEntry{{ID: "x", Name: "x"}, {ID: "y", Name: "y"}}, func(id string) { chosen = id })
	testutil.Equal(t, app.mode, modeHeraPicker)

	// Enter selects the first entry → submit fires, picker closes.
	app.handleHeraPickerKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	testutil.Equal(t, chosen, "x")
	testutil.Equal(t, app.mode, modeTaskList)
	testutil.Nil(t, app.heraPickerModal)
}

func TestHeraPicker_CancelClosesWithoutSubmit(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	fired := false
	app.openHeraPicker(" Pick ", "help", []taskSwitcherEntry{{ID: "x", Name: "x"}}, func(string) { fired = true })
	app.handleHeraPickerKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	testutil.Equal(t, fired, false)
	testutil.Equal(t, app.mode, modeTaskList)
}

// TestTaskSwitcher_SetTitles proves the generalized title/help render.
func TestTaskSwitcher_SetTitles(t *testing.T) {
	m := NewTaskSwitcherModal([]taskSwitcherEntry{{ID: "a", Name: "alpha"}})
	m.SetTitles(" Link → parent ", "Enter link")
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(80, 24)
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim)
	sim.Show()
	testutil.Equal(t, simContainsRow(sim, 80, "Link → parent"), true)
	testutil.Equal(t, simContainsRow(sim, 80, "Enter link"), true)
	// Empty args keep the prior values.
	m.SetTitles("", "")
	testutil.Equal(t, m.title, " Link → parent ")
}

// --- tiny test helpers ------------------------------------------------------

// pickerIDs returns the candidate task IDs in a picker modal (package-internal
// field access; the test lives in package tui).
func pickerIDs(m *TaskSwitcherModal) []string {
	out := make([]string, 0, len(m.all))
	for _, e := range m.all {
		out = append(out, e.ID)
	}
	return out
}

// simContainsRow reports whether any row of the sim screen contains sub.
func simContainsRow(sim tcell.SimulationScreen, w int, sub string) bool {
	cells, _, _ := sim.GetContents()
	_, h := sim.Size()
	for y := 0; y < h; y++ {
		runes := make([]rune, 0, w)
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				runes = append(runes, c.Runes[0])
			} else {
				runes = append(runes, ' ')
			}
		}
		if substrIn(string(runes), sub) {
			return true
		}
	}
	return false
}

func substrIn(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
