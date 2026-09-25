package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/tui/widget"
)

type inboxFixture struct {
	d        *db.DB
	recv     *model.Task
	workerID int64
}

// seedInbox gives task "recv" two task messages (one acked), one system
// message, and one hera message addressed to the worker role it is bound to.
func seedInbox(t *testing.T) inboxFixture {
	t.Helper()
	d := testDB(t)
	recv := &model.Task{Name: "recv", Status: model.StatusInProgress, Project: "p"}
	sender := &model.Task{Name: "sender", Status: model.StatusInProgress, Project: "p"}
	testutil.NoError(t, d.Add(recv))
	testutil.NoError(t, d.Add(sender))

	acked, err := d.InsertMessage(&model.TaskMessage{From: sender.ID, To: recv.ID, Kind: model.KindNote, Body: "already read"})
	testutil.NoError(t, err)
	_, err = d.AckMessages(recv.ID, []string{acked.ID})
	testutil.NoError(t, err)
	_, err = d.InsertMessage(&model.TaskMessage{From: sender.ID, To: recv.ID, Kind: model.KindQuestion, Body: "still unread"})
	testutil.NoError(t, err)
	_, err = d.InsertSystemMessage(&model.TaskMessage{From: systemSenderID, To: recv.ID, Kind: model.KindNote, Body: "ARGUS_BOUNCED"})
	testutil.NoError(t, err)

	orch, err := d.CreateHeraOrchestrator("o", "")
	testutil.NoError(t, err)
	coord, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	worker, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "worker-1", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: worker.ID, ArgusTaskID: recv.ID, WorktreePath: "/wt/recv"})
	testutil.NoError(t, err)
	_, err = d.SendHeraMessage(coord.ID, worker.ID, "hera body", "hera tldr", nil)
	testutil.NoError(t, err)

	return inboxFixture{d: d, recv: recv, workerID: worker.ID}
}

func TestLoadInboxEntries(t *testing.T) {
	f := seedInbox(t)

	entries, err := loadInboxEntries(f.d, f.recv.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 4)
	for i := 1; i < len(entries); i++ {
		if entries[i].At.Before(entries[i-1].At) {
			t.Fatalf("entries not oldest-first at %d", i)
		}
	}

	byBody := map[string]modal.InboxEntry{}
	for _, e := range entries {
		byBody[e.Body] = e
	}
	read := byBody["already read"]
	testutil.Equal(t, read.Source, "task")
	testutil.Equal(t, read.From, "sender")
	testutil.False(t, read.Unread)
	testutil.Equal(t, byBody["still unread"].Unread, true)
	testutil.Equal(t, byBody["still unread"].Summary, "question")
	testutil.Equal(t, byBody["ARGUS_BOUNCED"].From, "system")

	h := byBody["hera body"]
	testutil.Equal(t, h.Source, "hera")
	testutil.Equal(t, h.From, "coord")
	testutil.Equal(t, h.To, "worker-1")
	testutil.Equal(t, h.Summary, "hera tldr")
	testutil.Equal(t, h.Delivery, db.HeraDeliveryPending)
	testutil.True(t, h.Unread)

	// Loading must never ack/mark read in either store.
	unread, err := f.d.UnreadCount(f.recv.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, unread, 2)
	heraUnread, err := f.d.HeraInbox(f.workerID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(heraUnread), 1)
}

func TestLoadInboxEntries_Empty(t *testing.T) {
	d := testDB(t)
	entries, err := loadInboxEntries(d, "nope")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 0)
}

func TestInboxEntryConversions(t *testing.T) {
	now := time.Now()
	e := taskInboxEntry(&model.TaskMessage{Kind: model.KindAnswer, InReplyTo: "q1", CreatedAt: now, ReadAt: now}, "s")
	testutil.Equal(t, e.Summary, "answer (reply to q1)")
	testutil.False(t, e.Unread)

	delivered := now
	h := heraInboxEntry(&db.HeraMessage{SentAt: now, ReadAt: &now, DeliveryMode: db.HeraDeliveryIdleSubmit, DeliveredAt: &delivered}, "a", "b")
	testutil.False(t, h.Unread)
	testutil.Equal(t, h.ReadAt, now)
	testutil.Contains(t, h.Delivery, "idle_submit at ")
}

func waitInboxLoaded(t *testing.T, app *App) []modal.InboxEntry {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		var got []modal.InboxEntry
		var loaded bool
		readUI(t, app.tapp, func() {
			if app.inboxModal != nil {
				got, loaded = app.inboxModal.Entries()
			}
		})
		if loaded {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("inbox never finished loading")
	return nil
}

func TestSmoke_InboxModal(t *testing.T) {
	f := seedInbox(t)
	app := New(f.d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	readUI(t, app.tapp, func() { app.refreshTasks() })
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		sel := app.tasklist.SelectedTask()
		if sel == nil || sel.ID != f.recv.ID {
			app.tasklist.SelectByID(f.recv.ID)
		}
	})

	sim.InjectKey(tcell.KeyRune, 'i', tcell.ModNone)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.mode, modeInbox)
		testutil.Equal(t, app.inboxTaskID, f.recv.ID)
		testutil.Equal(t, app.tapp.GetFocus() == app.inboxModal, true)
	})
	testutil.Equal(t, len(waitInboxLoaded(t, app)), 4)

	// A new message shows up after `r`, and still nothing gets acked.
	_, err := f.d.InsertSystemMessage(&model.TaskMessage{From: systemSenderID, To: f.recv.ID, Kind: model.KindNote, Body: "late"})
	testutil.NoError(t, err)
	sim.InjectKey(tcell.KeyRune, 'r', tcell.ModNone)
	syncUI(t, app.tapp)
	testutil.Equal(t, len(waitInboxLoaded(t, app)), 5)
	unread, err := f.d.UnreadCount(f.recv.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, unread, 3)

	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.mode, modeTaskList)
		testutil.Nil(t, app.inboxModal)
		testutil.Equal(t, app.tapp.GetFocus() == app.tasklist, true)
		name, _ := app.pages.GetFrontPage()
		testutil.Equal(t, name, "tasks")
	})
}

func TestSmoke_InboxModal_StaleLoadDropped(t *testing.T) {
	f := seedInbox(t)
	app := New(f.d, agent.NewRunner(nil), false)
	_, stop := wireApp(t, app)
	defer stop()

	readUI(t, app.tapp, func() {
		app.openInbox(f.recv.ID, "recv")
		app.closeInbox()
		app.openInboxForTaskID(f.recv.ID)
	})
	testutil.Equal(t, len(waitInboxLoaded(t, app)), 4)
	readUI(t, app.tapp, func() { app.closeInbox() })
}

func TestSmoke_InboxModal_RemoteMode(t *testing.T) {
	app := New(stubStore{}, agent.NewRunner(nil), false)
	_, stop := wireApp(t, app)
	defer stop()

	readUI(t, app.tapp, func() {
		app.openInboxForTaskID("t1")
		testutil.Equal(t, app.mode, modeInbox)
		testutil.Contains(t, app.inboxModal.Unavailable(), "remote mode")
		app.handleInboxKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
		testutil.Equal(t, app.mode, modeTaskList)
	})
}

func TestOpenInbox_IgnoresEmptyAndDoubleOpen(t *testing.T) {
	f := seedInbox(t)
	app := New(f.d, agent.NewRunner(nil), false)
	_, stop := wireApp(t, app)
	defer stop()

	readUI(t, app.tapp, func() {
		app.openInbox("", "x")
		testutil.Nil(t, app.inboxModal)
		app.openInbox(f.recv.ID, "recv")
		first := app.inboxModal
		app.openInbox(f.recv.ID, "recv")
		testutil.Equal(t, app.inboxModal == first, true)
	})
	waitInboxLoaded(t, app)
}

func TestSmoke_InboxModal_ClosesBackToHeraTab(t *testing.T) {
	f := seedInbox(t)
	app := New(f.d, agent.NewRunner(nil), false)
	_, stop := wireApp(t, app)
	defer stop()

	var heraPage string
	readUI(t, app.tapp, func() {
		app.switchTab(widget.TabHera)
		heraPage, _ = app.pages.GetFrontPage()
		app.heraPage.OnInbox(f.recv.ID)
		testutil.Equal(t, app.mode, modeInbox)
	})
	waitInboxLoaded(t, app)
	readUI(t, app.tapp, func() {
		app.handleInboxKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
		name, _ := app.pages.GetFrontPage()
		testutil.Equal(t, name, heraPage)
		testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
	})
}
