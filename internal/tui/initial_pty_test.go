package tui

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/widget"
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
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	screen, stop := wireApp(t, app)
	defer stop()
	readUI(t, app.tapp, func() { screen.SetSize(238, 40); app.switchTab(widget.TabHera) })
	app.tapp.QueueUpdateDraw(func() {})
	var coord, worker db.PTYSize
	waitRerender(t, func() bool {
		var err error
		coord, err = d.InitialPTYSize(db.HeraKindCoordinator, time.Now())
		testutil.NoError(t, err)
		worker, err = d.InitialPTYSize(db.HeraKindWorker, time.Now())
		testutil.NoError(t, err)
		return coord.Valid() && worker.Valid() && coord.Cols > 80 && coord.Cols < 150 && coord.Rows > 21
	})
	var coordW, coordH, w, h int
	readUI(t, app.tapp, func() {
		_, _, coordW, coordH = app.heraPage.CoordPane().GetInnerRect()
		_, _, w, h = app.heraPage.AgentPane().GetInnerRect()
	})
	testutil.Equal(t, int(coord.Rows), coordH-2)
	testutil.Equal(t, int(coord.Cols), coordW-2)
	// The worker region may show Details, so LaunchPaneSizes derives its
	// geometry even if the terminal itself has never been drawn.
	if w > 30 && h > 10 {
		testutil.Equal(t, int(worker.Rows), h-2)
		testutil.Equal(t, int(worker.Cols), w-2)
	}
	testutil.True(t, worker.Valid())
	testutil.Equal(t, agent.ShouldKickRerender(true, int(coord.Cols), 0, coordW, true, false, false), agent.RerenderSkip)
}
