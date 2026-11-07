package hera

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/uxlog"
)

var errBuildForTest = errors.New("boom")

// --- harness -------------------------------------------------------------

// fakeQueue stands in for tview's QueueUpdateDraw: the build goroutine queues
// a publish closure and the TEST goroutine (playing the tview goroutine) runs
// it with pump.
type fakeQueue struct{ ch chan func() }

func newFakeQueue() *fakeQueue { return &fakeQueue{ch: make(chan func(), 32)} }

func (q *fakeQueue) queue(fn func()) { q.ch <- fn }

func (q *fakeQueue) pump(t *testing.T) {
	t.Helper()
	select {
	case fn := <-q.ch:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a queued publish")
	}
}

func (q *fakeQueue) pending() int { return len(q.ch) }

// gatedBuild wraps the page's real build function so a test can hold a build
// in flight (after it has computed its result, before it returns it) and
// observe how many builds ran.
type gatedBuild struct {
	inner   func(*rebuildSnapshot) rebuildResult
	started chan *rebuildSnapshot
	release chan struct{}
	calls   atomic.Int32
}

func gate(p *HeraPage) *gatedBuild {
	g := &gatedBuild{
		inner:   p.buildFromSnapshot,
		started: make(chan *rebuildSnapshot, 16),
		release: make(chan struct{}),
	}
	p.buildFn = func(s *rebuildSnapshot) rebuildResult {
		g.calls.Add(1)
		r := g.inner(s) // result is fixed at build time, like a slow real build
		g.started <- s
		<-g.release
		return r
	}
	return g
}

// gateLate holds the build BEFORE it reads anything, so the build's reads happen
// after the UI has kept mutating its own state — the interleaving that exposes
// any read of live (non-snapshot) data.
func gateLate(p *HeraPage) *gatedBuild {
	g := &gatedBuild{
		inner:   p.buildFromSnapshot,
		started: make(chan *rebuildSnapshot, 16),
		release: make(chan struct{}),
	}
	p.buildFn = func(s *rebuildSnapshot) rebuildResult {
		g.calls.Add(1)
		g.started <- s
		<-g.release
		return g.inner(s)
	}
	return g
}

func (g *gatedBuild) waitStarted(t *testing.T) *rebuildSnapshot {
	t.Helper()
	select {
	case s := <-g.started:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("build never started")
		return nil
	}
}

// asyncPage returns a page on a real file DB with a fake UI queue wired.
func asyncPage(t *testing.T) (*HeraPage, *db.DB, *fakeQueue) {
	t.Helper()
	d := openFileDB(t)
	p := NewHeraPage(d)
	q := newFakeQueue()
	p.SetUIQueue(q.queue)
	return p, d, q
}

// seedTeam creates one orchestrator with a bound coordinator and a bound worker.
func seedTeam(t *testing.T, d *db.DB, orch string) int64 {
	t.Helper()
	id := seedOrch(t, d, orch)
	seedBoundRole(t, d, id, "coord", db.HeraKindCoordinator, "t-coord-"+orch)
	seedBoundRole(t, d, id, "w1", db.HeraKindWorker, "t-w1-"+orch)
	return id
}

func railOrchNames(p *HeraPage) []string {
	m := p.rail.Model()
	var out []string
	for _, secs := range [][]heramodel.OrchView{m.Pinned, m.Active, m.Archived} {
		for _, o := range secs {
			out = append(out, o.Name)
		}
	}
	return out
}

// crossConnWriter opens a second connection to d's file (a different process's
// view of the store: the only kind of write PRAGMA data_version can see).
func crossConnWriter(t *testing.T, path string) *db.DB {
	t.Helper()
	w, err := db.Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func dbPath(t *testing.T) string { return filepath.Join(t.TempDir(), "data.sql") }

// --- the tick path builds off the UI goroutine, publishes on it -----------

func TestAsyncRefresh_BuildsOffThreadAndPublishesOnPump(t *testing.T) {
	p, d, q := asyncPage(t)
	seedTeam(t, d, "alpha")
	g := gate(p)

	p.doRefresh()                               // tick opportunity: returns without building inline
	g.waitStarted(t)                            // build is running on its own goroutine
	testutil.Equal(t, len(railOrchNames(p)), 0) // nothing published yet
	testutil.Equal(t, p.asyncStats.builds, 1)

	close(g.release)
	q.pump(t) // the publish runs on the "UI" goroutine

	testutil.DeepEqual(t, railOrchNames(p), []string{"alpha"})
	testutil.Equal(t, p.asyncStats.published, 1)
}

// --- snapshot isolation ----------------------------------------------------

// The build must read value copies taken at launch. This is the regression for
// the reverted off-thread attempt: *model.Task pointers shared with the UI.
func TestAsyncRefresh_BuildReadsSnapshotNotLiveTasks(t *testing.T) {
	p, d, q := asyncPage(t)
	seedTeam(t, d, "alpha")
	tasks, err := d.Tasks()
	testutil.NoError(t, err)
	p.SetTasks(tasks)
	p.SetNeedsInput([]string{"t-w1-alpha"})
	g := gateLate(p)

	p.doRefresh()
	g.waitStarted(t)
	// The UI keeps going: it mutates the shared pointers and swaps the maps.
	for _, tk := range tasks {
		tk.Status = model.StatusComplete
	}
	p.SetNeedsInput(nil)
	p.SetTasks(nil)
	close(g.release)
	q.pump(t)

	var w *heramodel.RoleView
	for _, o := range p.rail.Model().Active {
		for i := range o.Roles {
			if o.Roles[i].Name == "w1" {
				w = &o.Roles[i]
			}
		}
	}
	if w == nil {
		t.Fatal("worker role missing from published model")
	}
	testutil.Equal(t, w.TaskStatus, model.StatusInProgress.String()) // snapshot-time value
	testutil.Equal(t, w.NeedsInput, true)                            // the snapshot-time needs-input set, though the UI cleared it mid-build
}

// Run under -race. The UI goroutine (this test) keeps rewriting the shared
// task pointers it owns while each real build is in flight; any read of a live
// *model.Task from the build goroutine is a reported data race. (The snapshot
// itself is taken on the UI goroutine, the only writer in production, so
// mutating between snapshot and publish is exactly the real interleaving.)
func TestAsyncRefresh_NoRaceWithUIMutatingTasks(t *testing.T) {
	p, d, q := asyncPage(t)
	seedTeam(t, d, "alpha")
	seedTeam(t, d, "beta")
	tasks, err := d.Tasks()
	testutil.NoError(t, err)
	p.SetTasks(tasks)

	for i := 0; i < 30; i++ {
		p.InvalidateChangeGate()
		p.doRefresh() // launches a build on its own goroutine
		for q.pending() == 0 {
			for _, tk := range tasks { // UI keeps mutating while the build runs
				tk.Status = model.StatusInReview
				tk.Name = "hammer"
			}
		}
		q.pump(t)
	}
	testutil.Equal(t, p.asyncStats.published >= 1, true)
}

// --- generation guard ------------------------------------------------------

func TestAsyncRefresh_StalePublishIsDropped(t *testing.T) {
	p, d, q := asyncPage(t)
	seedTeam(t, d, "alpha")
	g := gate(p)

	p.doRefresh()
	g.waitStarted(t) // build A holds an alpha-only result

	// A newer rebuild lands first: the forced, synchronous path.
	seedTeam(t, d, "beta")
	p.Refresh()
	testutil.DeepEqual(t, railOrchNames(p), []string{"alpha", "beta"})

	close(g.release) // older build A finishes after the newer one
	q.pump(t)

	testutil.DeepEqual(t, railOrchNames(p), []string{"alpha", "beta"}) // not rolled back
	testutil.Equal(t, p.asyncStats.staleDrops, 1)
	testutil.Equal(t, p.asyncStats.published, 0)
}

func TestAsyncRefresh_GateInvalidationAlsoStalesInFlightBuild(t *testing.T) {
	p, d, q := asyncPage(t)
	seedTeam(t, d, "alpha")
	g := gate(p)

	p.doRefresh()
	g.waitStarted(t)
	p.InvalidateChangeGate() // what App.heraRefresh does around a local mutation
	close(g.release)
	q.pump(t)

	testutil.Equal(t, len(railOrchNames(p)), 0)
	testutil.Equal(t, p.asyncStats.staleDrops, 1)
	// The dropped build must not leave the gate believing it was built.
	testutil.Equal(t, p.shouldRebuild(), true)
}

func TestAsyncRefresh_ForcedRefreshIsSynchronousAndBumpsGeneration(t *testing.T) {
	p, d, q := asyncPage(t)
	seedTeam(t, d, "alpha")
	before := p.gen

	p.Refresh()

	testutil.DeepEqual(t, railOrchNames(p), []string{"alpha"}) // visible with no pump
	testutil.Equal(t, q.pending(), 0)
	testutil.Equal(t, p.gen > before, true)
	testutil.Equal(t, p.asyncStats.builds, 0) // it never used the async path

	// Refresh must not leave the page stuck in synchronous mode: the next
	// tick-driven opportunity goes back to the background path.
	p.InvalidateChangeGate()
	p.doRefresh()
	testutil.Equal(t, p.asyncStats.builds, 1)
	q.pump(t)
}

// --- D3: writes during a build are never lost ------------------------------

func TestAsyncRefresh_WriteDuringBuildTriggersFollowUp(t *testing.T) {
	path := dbPath(t)
	d, err := db.Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	p := NewHeraPage(d)
	q := newFakeQueue()
	p.SetUIQueue(q.queue)
	w := crossConnWriter(t, path)
	seedTeam(t, w, "alpha")
	g := gate(p)

	p.doRefresh()
	g.waitStarted(t) // snapshot (and its fingerprint) already taken

	seedTeam(t, w, "beta") // lands AFTER the snapshot, from another connection
	p.doRefresh()          // the next tick sees the moved fingerprint → follow-up queued
	testutil.Equal(t, g.calls.Load(), int32(1))

	close(g.release)
	q.pump(t) // publishes build #1, which launches the single follow-up
	testutil.Equal(t, p.asyncStats.reruns, 1)
	g.waitStarted(t)
	q.pump(t)

	testutil.DeepEqual(t, railOrchNames(p), []string{"alpha", "beta"})
	testutil.Equal(t, g.calls.Load(), int32(2))
}

func TestAsyncRefresh_QuiescentOpportunityDuringBuildQueuesNothing(t *testing.T) {
	p, d, q := asyncPage(t)
	seedTeam(t, d, "alpha")
	g := gate(p)

	p.doRefresh()
	g.waitStarted(t)
	p.doRefresh() // nothing changed since the in-flight snapshot
	p.doRefresh()
	close(g.release)
	q.pump(t)

	testutil.Equal(t, g.calls.Load(), int32(1))
	testutil.Equal(t, p.asyncStats.reruns, 0)
	testutil.Equal(t, q.pending(), 0)
}

func TestAsyncRefresh_ManyOpportunitiesYieldOneFollowUp(t *testing.T) {
	path := dbPath(t)
	d, err := db.Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	p := NewHeraPage(d)
	q := newFakeQueue()
	p.SetUIQueue(q.queue)
	w := crossConnWriter(t, path)
	seedTeam(t, w, "alpha")
	g := gate(p)

	p.doRefresh()
	g.waitStarted(t)
	for i := 0; i < 4; i++ {
		seedTeam(t, w, "extra"+string(rune('a'+i)))
		p.doRefresh()
	}
	close(g.release)
	q.pump(t)
	g.waitStarted(t)
	q.pump(t)

	testutil.Equal(t, g.calls.Load(), int32(2)) // not 5
	testutil.Equal(t, p.asyncStats.reruns, 1)
}

// --- D5: an unchanged model is dropped without UI work ---------------------

// publishedSets counts rail model replacements by comparing the rail's backing
// slice identity: SetModel stores the model it is handed, so a dropped publish
// leaves the exact same slice header in place.
func activeSliceID(p *HeraPage) *heramodel.OrchView {
	m := p.rail.Model()
	if len(m.Active) == 0 {
		return nil
	}
	return &m.Active[0]
}

// buildAndPump runs one forced-gate async cycle and pumps its publish.
func buildAndPump(t *testing.T, p *HeraPage, q *fakeQueue) {
	t.Helper()
	p.InvalidateChangeGate()
	p.doRefresh()
	q.pump(t)
}

func seedPRMeta(t *testing.T, d *db.DB, taskID, state, url string) {
	t.Helper()
	testutil.NoError(t, d.SetMetaBatch(taskID, "pr", map[string]string{"state": state, "url": url}))
}

// The three cases below use REAL commits (two connections, real data_version),
// not hand-built models: a volatile field sneaking into the model would make
// the equality gate silently never fire, and only real rows prove it does.
func TestAsyncRefresh_EqualDrop_RealCommits(t *testing.T) {
	path := dbPath(t)
	reader, err := db.Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })
	writer := crossConnWriter(t, path)
	p := NewHeraPage(reader)
	q := newFakeQueue()
	p.SetUIQueue(q.queue)
	seedTeam(t, writer, "alpha")
	seedPRMeta(t, writer, "t-w1-alpha", "none", "")

	buildAndPump(t, p, q)
	testutil.Equal(t, p.asyncStats.published, 1)
	first := activeSliceID(p)

	t.Run("old-style identical rewrite is equal", func(t *testing.T) {
		time.Sleep(3 * time.Millisecond)
		fp0, _ := p.dbFingerprint()
		seedPRMeta(t, writer, "t-w1-alpha", "none", "") // SetMetaBatch: commits, bumps updated_at
		fp1, _ := p.dbFingerprint()
		testutil.Equal(t, fp1 != fp0, true) // the gate DOES fire (data_version moved)...
		testutil.Equal(t, p.shouldRebuild(), true)
		p.doRefresh()
		q.pump(t)
		testutil.Equal(t, p.asyncStats.equalDrops, 1) // ...and the equality gate swallows it
		testutil.Equal(t, p.asyncStats.published, 1)
		testutil.Equal(t, activeSliceID(p) == first, true) // SetModel not called
	})

	t.Run("no-op poller commit through IfChanged is equal and never even fires the gate", func(t *testing.T) {
		wrote, err := writer.SetMetaBatchIfChanged("t-w1-alpha", "pr", map[string]string{"state": "none", "url": ""})
		testutil.NoError(t, err)
		testutil.Equal(t, wrote, false)
		testutil.Equal(t, p.shouldRebuild(), false)
		// And if a rebuild is forced anyway the models are still equal.
		buildAndPump(t, p, q)
		testutil.Equal(t, p.asyncStats.equalDrops, 2)
		testutil.Equal(t, activeSliceID(p) == first, true)
	})

	t.Run("a real PR state flip is NOT equal", func(t *testing.T) {
		seedPRMeta(t, writer, "t-w1-alpha", "awaiting-review", "https://example/pr/1")
		testutil.Equal(t, p.shouldRebuild(), true)
		p.doRefresh()
		q.pump(t)
		testutil.Equal(t, p.asyncStats.published, 2)
		testutil.Equal(t, p.prMeta["t-w1-alpha"]["state"], "awaiting-review")
	})

	t.Run("a real role change is NOT equal", func(t *testing.T) {
		seedBoundRole(t, writer, mustOrchID(t, writer, "alpha"), "w2", db.HeraKindWorker, "t-w2-alpha")
		p.doRefresh()
		q.pump(t)
		testutil.Equal(t, p.asyncStats.published, 3)
	})
}

func mustOrchID(t *testing.T, d *db.DB, name string) int64 {
	t.Helper()
	orchs, err := d.ListHeraOrchestrators(true)
	testutil.NoError(t, err)
	for _, o := range orchs {
		if o.Name == name {
			return o.ID
		}
	}
	t.Fatalf("orchestrator %q not found", name)
	return 0
}

// --- fallbacks and failure handling ----------------------------------------

func TestAsyncRefresh_NoQueueRunsInline(t *testing.T) {
	d := openFileDB(t)
	p := NewHeraPage(d) // no SetUIQueue
	seedTeam(t, d, "alpha")

	p.doRefresh()

	testutil.DeepEqual(t, railOrchNames(p), []string{"alpha"})
	testutil.Equal(t, p.asyncStats.builds, 0)
}

func TestAsyncRefresh_RemoteModeStaysInline(t *testing.T) {
	p := NewHeraPage(nil)
	q := newFakeQueue()
	p.SetUIQueue(q.queue)

	p.doRefresh() // must not spawn a build goroutine for a nil reader

	testutil.Equal(t, p.asyncStats.builds, 0)
	testutil.Equal(t, q.pending(), 0)
}

func TestAsyncRefresh_BuildErrorLeavesGateOpenForRetry(t *testing.T) {
	p, d, q := asyncPage(t)
	seedTeam(t, d, "alpha")
	p.buildFn = func(*rebuildSnapshot) rebuildResult {
		return rebuildResult{err: errBuildForTest}
	}

	p.doRefresh()
	q.pump(t)

	testutil.Equal(t, len(railOrchNames(p)), 0)
	testutil.Equal(t, p.asyncStats.errors, 1)
	testutil.Equal(t, p.shouldRebuild(), true) // next tick retries
}

// --- rail semantics survive an async publish -------------------------------

func TestAsyncRefresh_PublishKeepsSelectionAndFold(t *testing.T) {
	p, d, q := asyncPage(t)
	seedTeam(t, d, "alpha")
	seedTeam(t, d, "beta")
	buildAndPump(t, p, q)
	testutil.Equal(t, p.rail.SelectByTaskID("t-w1-beta"), true)
	sel := p.rail.Selected()
	if sel == nil {
		t.Fatal("no selection")
	}
	want := sel.RoleID

	alphaID := mustOrchID(t, d, "alpha")
	p.rail.collapsed[alphaID] = true // fold state the operator set

	// A change elsewhere forces a real (non-equal) publish.
	seedBoundRole(t, d, mustOrchID(t, d, "alpha"), "w9", db.HeraKindWorker, "t-w9-alpha")
	p.InvalidateChangeGate()
	p.doRefresh()
	q.pump(t)

	got := p.rail.Selected()
	if got == nil {
		t.Fatal("selection lost across async publish")
	}
	testutil.Equal(t, got.RoleID, want)
	testutil.Equal(t, p.rail.collapsed[alphaID], true) // fold state survives the publish
}

func TestAsyncRefresh_BuildPanicIsRecoveredAndRetried(t *testing.T) {
	p, d, q := asyncPage(t)
	seedTeam(t, d, "alpha")
	p.buildFn = func(*rebuildSnapshot) rebuildResult { panic("kaboom") }

	p.doRefresh()
	q.pump(t) // the publish still arrives, carrying the error

	testutil.Equal(t, p.asyncStats.errors, 1)
	testutil.Equal(t, p.building, false) // single-flight flag released
	testutil.Equal(t, p.shouldRebuild(), true)

	p.buildFn = nil // the next opportunity builds normally
	p.doRefresh()
	q.pump(t)
	testutil.DeepEqual(t, railOrchNames(p), []string{"alpha"})
}

// failingReader makes the model projection fail at its first read.
type failingReader struct{ heramodel.HeraReader }

func (failingReader) ListHeraOrchestrators(bool) ([]*db.HeraOrchestrator, error) {
	return nil, errBuildForTest
}

func TestRebuildSync_ErrorKeepsPreviousModelAndRetries(t *testing.T) {
	d := openFileDB(t)
	seedTeam(t, d, "alpha")
	p := NewHeraPage(d)
	p.doRefresh() // inline (no queue): populates the rail
	testutil.DeepEqual(t, railOrchNames(p), []string{"alpha"})

	p.reader = failingReader{HeraReader: d}
	p.InvalidateChangeGate()
	p.doRefresh()

	testutil.DeepEqual(t, railOrchNames(p), []string{"alpha"}) // not blanked by the failure
	testutil.Equal(t, p.shouldRebuild(), true)                 // and the gate stays open
}

// The async path's outcomes are the only way to see it work in ux.log (the A/B
// measurement reads them), so each one must actually be written.
func TestAsyncRefresh_LogsEveryOutcome(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	testutil.NoError(t, uxlog.Init(logPath))
	t.Cleanup(uxlog.Close)

	p, d, q := asyncPage(t)
	seedTeam(t, d, "alpha")

	buildAndPump(t, p, q) // publish
	buildAndPump(t, p, q) // equal-drop

	g := gate(p)
	p.InvalidateChangeGate()
	p.doRefresh()
	g.waitStarted(t)
	p.InvalidateChangeGate() // makes the in-flight build stale
	close(g.release)
	q.pump(t) // stale-drop

	// A follow-up build: a cross-connection-free way is to queue rerun directly.
	g2 := gate(p)
	p.InvalidateChangeGate()
	p.doRefresh()
	g2.waitStarted(t)
	p.rerun = true
	close(g2.release)
	q.pump(t) // publishes, then launches the follow-up
	g2.waitStarted(t)
	q.pump(t)

	data, err := os.ReadFile(logPath)
	testutil.NoError(t, err)
	log := string(data)
	for _, want := range []string{
		"rail build start",
		"rail refreshed",
		"async=true",
		"rail publish dropped: model unchanged",
		"rail publish dropped: stale",
		"rail follow-up build",
	} {
		testutil.Contains(t, log, want)
	}
}
