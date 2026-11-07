package tui

import (
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	hera "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// The tick path builds off the tview goroutine and publishes through the REAL
// QueueUpdateDraw: a store change made after the Hera tab is open reaches the
// rail with no forced refresh.
func TestSmoke_HeraAsyncRefreshPublishesThroughRealEventLoop(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch-one")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc1")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	readUI(t, app.tapp, app.enableAsyncHeraRefresh)

	sim.InjectKey(tcell.KeyRune, '2', 0) // Hera tab (forced, synchronous refresh)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, len(app.heraPage.Rail().Model().Active), 1) })

	orch2 := seedHeraOrch(t, d, "orch-two")
	seedHeraBoundRole(t, d, orch2, "coord", db.HeraKindCoordinator, "tc2")
	time.Sleep(150 * time.Millisecond) // clear the refresher's 100ms debounce window
	readUI(t, app.tapp, func() {
		app.heraPage.InvalidateChangeGate() // same-connection write: data_version cannot see it
		app.heraPage.ScheduleRefresh()      // tick path → background build
	})

	deadline := time.Now().Add(3 * time.Second)
	var got []hera.OrchView
	for time.Now().Before(deadline) {
		readUI(t, app.tapp, func() { got = app.heraPage.Rail().Model().Active })
		if len(got) == 2 {
			// It must have gone through the BACKGROUND path, not the inline one:
			// without this the test passes identically with async switched off.
			var builds, published int
			readUI(t, app.tapp, func() { builds, published = app.heraPage.AsyncStats() })
			testutil.Equal(t, builds >= 1, true)
			testutil.Equal(t, published >= 1, true)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("async publish never reached the rail; Active=%d", len(got))
}

// resolveHeraTier now runs on the build goroutine. It must touch only the
// mutex-guarded store and disk, never App state the tview goroutine owns.
func TestResolveHeraTier_SafeOffTheUIGoroutine(t *testing.T) {
	d := testDB(t)
	seedHeraBoundRole(t, d, seedHeraOrch(t, d, "o"), "w", db.HeraKindWorker, "tw")
	app := New(d, agent.NewRunner(nil), false)

	// Reference stamp, computed single-threaded.
	want := &hera.RoleView{TaskID: "tw", ArgusProject: "p", Archetype: "code_slice"}
	app.resolveHeraTier(want)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var mismatched int
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				rv := &hera.RoleView{TaskID: "tw", ArgusProject: "p", Archetype: "code_slice"}
				app.resolveHeraTier(rv)
				if rv.AppliedModel != want.AppliedModel || rv.AppliedEffort != want.AppliedEffort ||
					rv.ProfileWarning != want.ProfileWarning || rv.ContextPercent != want.ContextPercent {
					mu.Lock()
					mismatched++
					mu.Unlock()
				}
			}
		}()
	}
	// Concurrent store writes through the same handle, like the real app.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = d.SetMeta("tw", "x", "k", "v")
		}
	}()
	wg.Wait()
	testutil.Equal(t, mismatched, 0) // concurrent callers stamp the same readout
}

// prepareRun is what Run() calls right before the event loop starts. If it ever
// stops enabling the off-thread rebuild, the whole fix is silently off.
func TestPrepareRun_EnablesAsyncHeraRefresh(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), false)
	testutil.Equal(t, app.heraPage.AsyncEnabled(), false) // a bare App stays inline
	app.prepareRun()
	testutil.Equal(t, app.heraPage.AsyncEnabled(), true)
}
