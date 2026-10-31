package tui

import (
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func markNew(t *testing.T, d *db.DB, id string) {
	t.Helper()
	testutil.NoError(t, d.SetMetaBatch(id, db.UIMetaNamespace, map[string]string{
		db.UIMetaKeyNew:    "true",
		db.UIMetaKeySource: "voice",
	}))
}

func addTask(t *testing.T, d *db.DB, id, name, project string) *model.Task {
	t.Helper()
	task := &model.Task{ID: id, Name: name, Project: project, Status: model.StatusInProgress, Branch: "argus/" + id}
	testutil.NoError(t, d.Add(task))
	return task
}

func TestReadNewTasks(t *testing.T) {
	t.Run("returns only true flags in ui namespace", func(t *testing.T) {
		d := testDB(t)
		app := New(d, agent.NewRunner(nil), false)
		markNew(t, d, "t1")
		testutil.NoError(t, d.SetMeta("t2", db.UIMetaNamespace, db.UIMetaKeyNew, "false"))
		testutil.NoError(t, d.SetMeta("t3", db.UIMetaNamespace, db.UIMetaKeySource, "voice"))
		testutil.NoError(t, d.SetMeta("t4", "pr", db.UIMetaKeyNew, "true"))

		got := app.readNewTasks()
		testutil.DeepEqual(t, got, map[string]bool{"t1": true})
	})

	t.Run("empty returns nil", func(t *testing.T) {
		d := testDB(t)
		app := New(d, agent.NewRunner(nil), false)
		testutil.Nil(t, app.readNewTasks())
	})

	t.Run("query error returns nil", func(t *testing.T) {
		d := testDB(t)
		app := New(d, agent.NewRunner(nil), false)
		testutil.NoError(t, d.Close())
		testutil.Nil(t, app.readNewTasks())
	})
}

func TestSyncNewTasks_SeedsSilentlyThenAnnounces(t *testing.T) {
	d := testDB(t)
	addTask(t, d, "old", "old-task", "forge")
	markNew(t, d, "old")

	app := New(d, agent.NewRunner(nil), false)
	app.refreshTasks()

	testutil.Equal(t, app.tasklist.IsNewTask("old"), true)
	testutil.Equal(t, app.statusbar.Info(), "")

	addTask(t, d, "fresh", "fix-login", "argus")
	markNew(t, d, "fresh")
	app.refreshTasks()

	testutil.Equal(t, app.tasklist.IsNewTask("fresh"), true)
	testutil.Equal(t, app.statusbar.Info(), "✦ New task: fix-login (argus)")

	app.statusbar.ClearInfo()
	app.refreshTasks()
	testutil.Equal(t, app.statusbar.Info(), "")
}

func TestSyncNewTasks_DefersUnknownTask(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.refreshTasks()

	markNew(t, d, "late")
	app.refreshTasks()
	testutil.Equal(t, app.statusbar.Info(), "")

	addTask(t, d, "late", "late-task", "forge")
	app.refreshTasks()
	testutil.Equal(t, app.statusbar.Info(), "✦ New task: late-task (forge)")
}

func TestSyncNewTasks_AnnouncesMultiple(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.refreshTasks()

	addTask(t, d, "a", "first", "forge")
	addTask(t, d, "b", "second", "argus")
	markNew(t, d, "a")
	markNew(t, d, "b")
	app.refreshTasks()
	testutil.Equal(t, app.statusbar.Info(), "✦ New task: first (forge), second (argus)")
}

func TestOnTaskSelect_ClearsNewFlag(t *testing.T) {
	d := testDB(t)
	task := addTask(t, d, "t1", "voice-task", "forge")
	markNew(t, d, "t1")

	app := New(d, agent.NewRunner(nil), false)
	app.refreshTasks()
	testutil.Equal(t, app.tasklist.IsNewTask("t1"), true)

	app.onTaskSelect(task, false)

	testutil.Equal(t, app.tasklist.IsNewTask("t1"), false)
	meta, err := d.ListMetaByNamespace(db.UIMetaNamespace)
	testutil.NoError(t, err)
	testutil.Equal(t, meta["t1"][db.UIMetaKeyNew], "")
	testutil.Equal(t, meta["t1"][db.UIMetaKeySource], "voice")

	app.refreshTasks()
	testutil.Equal(t, app.tasklist.IsNewTask("t1"), false)
}

type nonLocalStore struct{ *db.DB }

func TestClearNewTask_RemoteNoOp(t *testing.T) {
	d := testDB(t)
	addTask(t, d, "t1", "voice-task", "forge")
	markNew(t, d, "t1")

	app := New(nonLocalStore{d}, agent.NewRunner(nil), false)
	app.refreshTasks()
	testutil.Equal(t, app.tasklist.IsNewTask("t1"), true)

	app.clearNewTask("t1")

	testutil.Equal(t, app.tasklist.IsNewTask("t1"), true)
	meta, err := d.ListMetaByNamespace(db.UIMetaNamespace)
	testutil.NoError(t, err)
	testutil.Equal(t, meta["t1"][db.UIMetaKeyNew], "true")
}

func TestClearNewTask_DeleteErrorKeepsFlag(t *testing.T) {
	d := testDB(t)
	addTask(t, d, "t1", "voice-task", "forge")
	markNew(t, d, "t1")

	app := New(d, agent.NewRunner(nil), false)
	app.refreshTasks()
	testutil.NoError(t, d.Close())

	app.clearNewTask("t1")
	testutil.Equal(t, app.tasklist.IsNewTask("t1"), true)
}
