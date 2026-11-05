package tui

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestSmoke_RepaintVisibleTasksAndHera(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	orch, err := d.CreateHeraOrchestrator("output-test", "")
	testutil.NoError(t, err)
	sessions := map[string]*repaintSession{}
	for _, entry := range []struct {
		id   string
		kind db.HeraRoleKind
	}{{"coord", db.HeraKindCoordinator}, {"worker", db.HeraKindWorker}} {
		testutil.NoError(t, d.Add(&model.Task{ID: entry.id, Name: entry.id, Status: model.StatusInProgress, CreatedAt: time.Now()}))
		role, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: entry.id, Kind: entry.kind})
		testutil.NoError(t, err)
		_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: role.ID, ArgusTaskID: entry.id, WorktreePath: t.TempDir()})
		testutil.NoError(t, err)
		sessions[entry.id] = newRepaintSession()
	}
	a := New(d, agent.NewRunner(nil), false)
	a.heraPage.SetRerenderKicker(nil)
	a.heraPage.OnReattach = nil // fixture sessions are already live; never launch an agent
	a.heraPage.SetSessionResolver(func(id string) agentview.TerminalAdapter {
		if s := sessions[id]; s != nil {
			return s
		}
		return nil
	})
	a.heraPage.Refresh()
	testutil.Equal(t, a.heraPage.JumpToTask("worker"), true)
	task, hidden := newRepaintSession(), newRepaintSession()
	a.agentPane.SetTaskID("tasks-output")
	a.agentPane.SetSession(task)
	tapp, sim, _ := simApp(t)
	sim.SetSize(160, 30)
	a.tapp = tapp
	pages := tview.NewPages().AddPage("tasks", a.agentPane, true, true).AddPage("hera", a.heraPage, true, false)
	tapp.SetRoot(pages, true)
	tapp.SetAfterDrawFunc(a.afterDraw)
	frames := make(chan struct{}, 20)
	a.startOutputRepaint()
	// No frame or wake exists until runApp starts below. Configure the
	// completion observation before that first wake, retaining production wiring.
	draw := a.repaint.draw
	a.repaint.draw = func() { draw(); frames <- struct{}{} }
	stop := runApp(t, tapp)
	var once sync.Once
	shutdown := func() {
		once.Do(func() {
			close(a.tickDone)
			select {
			case <-a.repaint.stopped:
			case <-time.After(uiTimeout):
				t.Error("repaint pump did not stop while UI alive")
			}
			readUI(t, tapp, a.repaint.close)
			stop()
		})
	}
	t.Cleanup(shutdown)
	defer shutdown()
	waitFrame := func() {
		t.Helper()
		select {
		case <-frames:
		case <-time.After(uiTimeout):
			t.Fatal("output did not repaint")
		}
	}
	text := func() string {
		var b strings.Builder
		readUI(t, tapp, func() {
			cells, w, _ := sim.GetContents()
			for i, c := range cells {
				if len(c.Runes) > 0 {
					b.WriteRune(c.Runes[0])
				}
				if (i+1)%w == 0 {
					b.WriteByte('\n')
				}
			}
		})
		return b.String()
	}
	waitFrame() // the one subscription wake
	testutil.Equal(t, task.writerCount(), 1)
	testutil.Equal(t, len(frames), 0) // quiet pane does not cause pump draws
	task.emit([]byte("TASK-ECHO"))
	waitFrame()
	testutil.Contains(t, text(), "TASK-ECHO")
	hidden.emit([]byte("HIDDEN"))
	testutil.Equal(t, hidden.writerCount(), 0)
	testutil.Equal(t, len(a.repaint.wakes), 0)
	testutil.Equal(t, len(frames), 0)
	tapp.QueueUpdateDraw(func() { pages.SwitchToPage("hera"); tapp.SetFocus(a.heraPage) })
	waitFrame()
	testutil.Equal(t, task.writerCount(), 0)
	testutil.Equal(t, sessions["coord"].writerCount(), 1)
	testutil.Equal(t, sessions["worker"].writerCount(), 1)
	sessions["coord"].emit([]byte("COORD-ECHO"))
	waitFrame()
	testutil.Contains(t, text(), "COORD-ECHO")
	sessions["worker"].emit([]byte("WORKER-ECHO"))
	waitFrame()
	testutil.Contains(t, text(), "WORKER-ECHO")
	// Fullscreen draws only the focused worker pane. The coordinator stays
	// bound but must lose its subscription because it was not painted.
	tapp.QueueUpdateDraw(func() {
		a.heraPage.Machine().SetRegion(hera.FocusAgent)
		a.heraPage.InputHandler()(tcell.NewEventKey(tcell.KeyCtrlZ, 0, 0), func(tview.Primitive) {})
	})
	testutil.Equal(t, sessions["coord"].writerCount(), 0)
	sessions["coord"].emit([]byte("HIDDEN-COORD"))
	testutil.Equal(t, len(a.repaint.wakes), 0)
	testutil.Equal(t, len(frames), 0)
	sessions["worker"].emit([]byte("-FULLSCREEN"))
	waitFrame()
	testutil.Contains(t, text(), "WORKER-ECHO-FULLSCREEN")
	close(sessions["worker"].ended)
	waitFrame()
}

func TestSmoke_RepaintQuitDrainsQueuedDraw(t *testing.T) {
	tapp, _, _ := simApp(t)
	tapp.SetRoot(tview.NewBox(), true)
	a := &App{tapp: tapp, tickDone: make(chan struct{})}
	entered := make(chan struct{})
	var drawn atomic.Bool
	a.repaint = newRepaintPump(a.tickDone, func() {
		close(entered)
		tapp.QueueUpdateDraw(func() { drawn.Store(true) })
	}, false)
	go a.repaint.run()
	runDone := make(chan error, 1)
	go func() { runDone <- tapp.Run() }()
	syncUI(t, tapp)
	t.Cleanup(func() {
		a.stopTUI()
		select {
		case err := <-runDone:
			testutil.NoError(t, err)
		case <-time.After(uiTimeout):
			t.Error("event loop did not finish quitting")
		}
	})
	tapp.QueueUpdate(func() {
		a.repaint.wake()
		<-entered // draw is dispatched while this UI callback is still running
		a.stopTUI()
		a.stopTUI() // repeated quit remains idempotent
	})
	select {
	case <-a.repaint.stopped:
	case <-time.After(uiTimeout):
		t.Fatal("quit stranded the queued pump draw")
	}
	testutil.Equal(t, drawn.Load(), true)
}
