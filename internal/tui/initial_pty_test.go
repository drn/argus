package tui

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

func waitPTYPublication(t *testing.T, p *initialPTYPublisher) {
	t.Helper()
	p.mu.Lock()
	done := p.done
	p.mu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("pane publication did not finish")
	}
}

func TestInitialPTYPublisher_OrderedAndNonblocking(t *testing.T) {
	var p initialPTYPublisher
	first := db.InitialPTYLayout{ReportedAt: time.Now(), Active: db.PTYSize{Rows: 30, Cols: 98}}
	second := first
	second.Active.Cols = 120
	entered, release := make(chan struct{}), make(chan struct{})
	secondEntered := make(chan struct{})
	var mu sync.Mutex
	var writes []db.InitialPTYLayout
	save := func(layout db.InitialPTYLayout) error {
		mu.Lock()
		writes = append(writes, layout)
		count := len(writes)
		mu.Unlock()
		if count == 1 {
			close(entered)
			<-release
		} else {
			close(secondEntered)
		}
		return nil
	}
	returned := make(chan struct{})
	go func() { p.queue(first, save); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("publication blocked on SQL")
	}
	<-entered
	p.queue(second, save)
	p.queue(second, save)
	select {
	case <-secondEntered:
		t.Error("newer SQL write ran before older write completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	waitPTYPublication(t, &p)
	mu.Lock()
	testutil.DeepEqual(t, writes, []db.InitialPTYLayout{first, second})
	mu.Unlock()
	second.ReportedAt = second.ReportedAt.Add(initialPTYHeartbeat)
	p.queue(second, func(db.InitialPTYLayout) error { return errors.New("db unavailable") })
	waitPTYPublication(t, &p)
}

func TestInitialPTYLayout_ProjectsUsesDrawnPane(t *testing.T) {
	for _, mode := range []string{"projects", "tasks", "agent", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			d := testDB(t)
			app := New(d, agent.NewRunner(nil), false)
			screen, stop := wireApp(t, app)
			defer stop()
			readUI(t, app.tapp, func() {
				screen.SetSize(239, 40)
				app.switchTab(widget.TabHera)
				switch mode {
				case "tasks":
					app.switchTab(widget.TabTasks)
				case "agent":
					app.mu.Lock()
					app.mode = modeAgent
					app.mu.Unlock()
					app.pages.SwitchToPage("agent")
				case "fullscreen":
					app.heraPage.Machine().SetRegion(hera.FocusCoord)
					app.heraPage.Machine().ToggleFullscreen()
				}
			})
			app.tapp.QueueUpdateDraw(func() {})
			var coord, worker db.PTYSize
			waitRerender(t, func() bool {
				var err error
				coord, err = d.InitialPTYSize(db.HeraKindCoordinator, time.Now())
				testutil.NoError(t, err)
				worker, err = d.InitialPTYSize(db.HeraKindWorker, time.Now())
				testutil.NoError(t, err)
				return coord.Cols == 100 && worker.Cols == 101 && coord.Rows == 36
			})
			var wantRows, wantCols uint16
			readUI(t, app.tapp, func() {
				wantRows, wantCols = ptySizeFromHostTerm(239, 40, nil)
				if mode == "agent" {
					_, _, w, h := app.agentPane.GetInnerRect()
					wantRows, wantCols = ptySizeFromPaneRect(w, h)
				} else if mode != "tasks" {
					wantRows, wantCols = 36, 101
				}
			})
			testutil.Equal(t, worker, db.PTYSize{Rows: 36, Cols: 101})
			active, err := d.InitialPTYSize("", time.Now())
			testutil.NoError(t, err)
			testutil.Equal(t, active, db.PTYSize{Rows: wantRows, Cols: wantCols})
		})
	}
}

func TestInitialPTYPublisher_Heartbeat(t *testing.T) {
	var p initialPTYPublisher
	layout := db.InitialPTYLayout{ReportedAt: time.Now(), Active: db.PTYSize{Rows: 30, Cols: 150}}
	writes := 0
	save := func(db.InitialPTYLayout) error { writes++; return nil }
	p.queue(layout, save)
	waitPTYPublication(t, &p)
	testutil.Equal(t, writes, 1)
	layout.ReportedAt = layout.ReportedAt.Add(time.Second)
	p.queue(layout, save)
	waitPTYPublication(t, &p)
	testutil.Equal(t, writes, 1)
	layout.ReportedAt = layout.ReportedAt.Add(initialPTYHeartbeat - time.Second)
	p.queue(layout, save)
	waitPTYPublication(t, &p)
	testutil.Equal(t, writes, 2)
}

func TestInitialPTYLayout_SecondTasksViewer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	projects := New(d, agent.NewRunner(nil), false)
	tasks := New(d, agent.NewRunner(nil), false)
	screen := tcell.NewSimulationScreen("")
	testutil.NoError(t, screen.Init())
	defer screen.Fini()
	screen.SetSize(239, 40)
	now := time.Now().Add(-time.Minute)
	projects.nowFn = func() time.Time { return now }
	projects.header.SetTab(widget.TabHera)
	projects.publishInitialPTYLayout(screen)
	waitPTYPublication(t, &projects.initialPTYPublisher)
	for _, tick := range []time.Duration{initialPTYHeartbeat, 2 * initialPTYHeartbeat} {
		t.Run(tick.String(), func(t *testing.T) {
			tasks.nowFn = func() time.Time { return now.Add(tick) }
			tasks.publishInitialPTYLayout(screen)
			waitPTYPublication(t, &tasks.initialPTYPublisher)
			coord, err := d.InitialPTYSize(db.HeraKindCoordinator, now.Add(tick))
			testutil.NoError(t, err)
			worker, err := d.InitialPTYSize(db.HeraKindWorker, now.Add(tick))
			testutil.NoError(t, err)
			active, err := d.InitialPTYSize("", now.Add(tick))
			testutil.NoError(t, err)
			testutil.Equal(t, coord, db.PTYSize{Rows: 36, Cols: 100})
			testutil.Equal(t, worker, db.PTYSize{Rows: 36, Cols: 101})
			testutil.True(t, active.Cols > 200)
		})
	}
}
