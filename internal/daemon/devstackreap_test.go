package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

const dsRoot = "/h/.argus/worktrees"

func dsTask(id, wt string, st model.Status) *model.Task {
	return &model.Task{ID: id, Worktree: wt, Status: st}
}

type fakeDSTasks struct {
	rows []*model.Task
	err  error
}

func (f *fakeDSTasks) TasksChecked() ([]*model.Task, error) { return f.rows, f.err }

type dsHarness struct {
	r       *devStackReaper
	tasks   *fakeDSTasks
	running []string
	runErr  error
	enabled bool
	cfgErr  error
	procs   []agent.DevStackProc
	scanErr error
	scanN   int
	stops   []string
	opts    []agent.StopOptions
	onStop  func(wt string)
}

func newDSHarness(rows ...*model.Task) *dsHarness {
	h := &dsHarness{tasks: &fakeDSTasks{rows: rows}, enabled: true}
	h.r = newDevStackReaper(
		func() ([]string, error) { return h.running, h.runErr },
		h.tasks,
		func() (bool, error) { return h.enabled, h.cfgErr },
		dsRoot)
	h.r.scan = func() ([]agent.DevStackProc, error) { h.scanN++; return h.procs, h.scanErr }
	h.r.stop = func(wt string, o agent.StopOptions) {
		h.stops = append(h.stops, wt)
		h.opts = append(h.opts, o)
		if h.onStop != nil {
			h.onStop(wt)
		}
	}
	h.r.graceTicks = 2
	return h
}

func (h *dsHarness) proc(wt string) {
	h.procs = append(h.procs, agent.DevStackProc{PID: 1, Name: "mysqld", WorktreePath: wt})
}

func ticks(h *dsHarness, n int) {
	for i := 0; i < n; i++ {
		h.r.tick()
	}
}

func TestDevStackReaper_ProductionDefaults(t *testing.T) {
	// Every other test shortens graceTicks; these pin the shipped values.
	testutil.Equal(t, devStackReapInterval, 10*time.Second)
	testutil.Equal(t, devStackReapGraceTicks, 6)
	testutil.Equal(t, devStackSweepKillGrace, 30*time.Second)
	r := newDevStackReaper(nil, nil, nil, dsRoot)
	testutil.Equal(t, r.graceTicks, 6)
	testutil.Equal(t, r.killGrace, 30*time.Second)
}

func TestDevStackReaper_StopGetsLongGraceCancelAndProceed(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	cancel := make(chan struct{})
	h.r.cancel = cancel
	h.r.killGrace = 30 * time.Second
	h.proc(wt)
	ticks(h, 2)
	testutil.Equal(t, len(h.opts), 1)
	testutil.Equal(t, h.opts[0].KillGrace, 30*time.Second)
	testutil.True(t, h.opts[0].Cancel == (<-chan struct{})(cancel))
	testutil.True(t, h.opts[0].Proceed())
	// Proceed re-reads live state: a resumed task turns it false.
	h.running = []string{"a"}
	testutil.True(t, !h.opts[0].Proceed())
}

func TestDevStackReaper_StopsFinishedAfterGrace(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	testutil.Equal(t, len(h.r.tick()), 0) // armed
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
	testutil.DeepEqual(t, h.stops, []string{wt})
	testutil.Equal(t, len(h.r.tick()), 0) // re-arms from zero
}

func TestDevStackReaper_ArchivedTaskStopped(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	tk := dsTask("a", wt, model.StatusInReview)
	tk.Archived = true
	h := newDSHarness(tk)
	h.proc(wt)
	ticks(h, 2)
	testutil.DeepEqual(t, h.stops, []string{wt})
}

func TestDevStackReaper_SessionAlreadyGone_NeverSeenByReaper(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_UnfinishedNeverStopped(t *testing.T) {
	for _, st := range []model.Status{model.StatusPending, model.StatusInProgress, model.StatusInReview} {
		wt := dsRoot + "/Sketch/t1"
		h := newDSHarness(dsTask("a", wt, st))
		h.proc(wt)
		ticks(h, 5)
		testutil.Equal(t, len(h.stops), 0)
		testutil.Equal(t, h.scanN, 0)
	}
}

func TestDevStackReaper_LiveSessionBlocksAndResetsGrace(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusInProgress))
	h.proc(wt)
	h.running = []string{"a"}
	h.r.tick()
	h.tasks.rows[0].Status = model.StatusComplete
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0)
	h.running = nil
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_ResumedFinishedTaskIsExempt(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.r.tick()
	h.running = []string{"a"}
	h.r.tick()
	h.running = nil
	h.proc(wt)
	ticks(h, 6)
	testutil.Equal(t, len(h.stops), 0)

	h.tasks.rows[0].Status = model.StatusInProgress
	h.r.tick()
	h.tasks.rows[0].Status = model.StatusComplete
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_FirstTickGrantsNoExemption(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.running = []string{"a"}
	h.r.tick()
	h.running = nil
	h.proc(wt)
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_MainCheckoutNeverCandidate(t *testing.T) {
	for _, wt := range []string{
		"/h/Development/thanx/sketch",
		"/h/.argus/worktrees",
		"/h/.argus/worktrees/Sketch",
		"/h/.claude/worktrees/p/t",
		"/h/.argus/worktrees-evil/p/t",
	} {
		h := newDSHarness(dsTask("a", wt, model.StatusComplete))
		h.proc(wt)
		ticks(h, 5)
		testutil.Equal(t, len(h.stops), 0)
	}
}

func TestDevStackReaper_EmptyWorktreeIgnored(t *testing.T) {
	h := newDSHarness(dsTask("a", "", model.StatusComplete))
	h.proc("")
	ticks(h, 4)
	testutil.Equal(t, len(h.stops), 0)
}

func TestDevStackReaper_SharedWorktreeAllClaimantsMustBeFinished(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete), dsTask("b", wt+"/", model.StatusInProgress))
	h.proc(wt)
	h.running = []string{"b"}
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0)
	h.tasks.rows[1].Status = model.StatusComplete
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0)
	h.running = nil
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_SharedWorktreeExemptClaimantBlocks(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete), dsTask("b", wt, model.StatusComplete))
	h.r.tick()
	h.running = []string{"b"}
	h.r.tick()
	h.running = nil
	h.proc(wt)
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0)
}

func TestDevStackReaper_PrefixSiblingUntouched(t *testing.T) {
	a, b := dsRoot+"/Sherlock/3b", dsRoot+"/Sherlock/3b-more"
	h := newDSHarness(dsTask("a", a, model.StatusComplete), dsTask("b", b, model.StatusInProgress))
	h.proc(b)
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0)
	h.proc(a)
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{a})
}

func TestDevStackReaper_ProcessWithNoTaskRowSkipped(t *testing.T) {
	h := newDSHarness(dsTask("a", dsRoot+"/Sketch/t1", model.StatusComplete))
	h.proc(dsRoot + "/Sketch/orphan")
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0)
}

func TestDevStackReaper_StackGoneResetsGrace(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.r.tick()
	h.procs = nil
	h.r.tick()
	h.proc(wt)
	testutil.Equal(t, len(h.r.tick()), 0)
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_DisabledSignalsNothing(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.enabled = false
	ticks(h, 6)
	testutil.Equal(t, len(h.stops), 0)
	testutil.Equal(t, h.scanN, 0)
	h.enabled = true
	testutil.Equal(t, len(h.r.tick()), 0)
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_ResumeTrackedWhileDisabled(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.enabled = false
	h.r.tick()
	h.running = []string{"a"}
	h.r.tick()
	h.running = nil
	h.enabled = true
	h.proc(wt)
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0)
}

// --- failure handling: every input can fail; a failed tick stops nothing and
// changes no state. Each case sets up a stack that WOULD be stopped if the
// error were swallowed (rows/ids are still returned alongside the error).

func TestDevStackReaper_ScanFailureSkipsTick(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.scanErr = errors.New("pgrep gone")
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0)
	h.scanErr = nil
	// Counts held, not reset: one good tick after a failure is not enough
	// from zero (needs 2) — but a prior good tick plus this one is.
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_TaskListErrorSkipsAndHoldsCounts(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.r.graceTicks = 3
	h.r.tick() // 1
	h.tasks.err = errors.New("db locked")
	ticks(h, 4)                   // rows are still returned alongside the error: must be ignored
	testutil.Equal(t, h.scanN, 1) // a failed tick never reaches the scan
	h.tasks.err = nil
	h.r.tick() // 2: counts were held, not advanced by failures, not reset
	testutil.Equal(t, len(h.stops), 0)
	testutil.DeepEqual(t, h.r.tick(), []string{wt}) // 3
}

func TestDevStackReaper_PartialTaskReadNeverUnblocksSharedWorktree(t *testing.T) {
	// TasksChecked returns a PARTIAL list (the unfinished claimant is missing)
	// plus an error: the tick must be skipped, not act on the remainder.
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.tasks.err = errors.New("scan task: boom")
	h.proc(wt)
	ticks(h, 6)
	testutil.Equal(t, len(h.stops), 0)
}

func TestDevStackReaper_RunningErrorSkipsTick(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.runErr = errors.New("rpc closed") // an empty list alongside it must not read as "no sessions"
	ticks(h, 6)
	testutil.Equal(t, len(h.stops), 0)
	testutil.Equal(t, h.scanN, 0)
}

func TestDevStackReaper_FailedFirstTickDoesNotTurnBacklogIntoExemptions(t *testing.T) {
	// Startup: the first tick's reads fail. The first SUCCESSFUL tick must
	// still be the backlog tick (no exemption), even though the finished
	// task's session is live on it.
	wt := dsRoot + "/Sketch/t1"
	for _, failTasks := range []bool{true, false} {
		h := newDSHarness(dsTask("a", wt, model.StatusComplete))
		if failTasks {
			h.tasks.err = errors.New("db locked")
		} else {
			h.runErr = errors.New("rpc closed")
		}
		h.r.tick() // failed
		h.tasks.err, h.runErr = nil, nil
		h.running = []string{"a"} // finished task's session, live at startup
		h.r.tick()                // first successful tick: backlog
		h.running = nil
		h.proc(wt)
		h.r.tick()
		testutil.DeepEqual(t, h.r.tick(), []string{wt})
	}
}

func TestDevStackReaper_RunningFailureOnJustCompletedTaskGrantsNoExemption(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusInProgress))
	h.running = []string{"a"}
	h.r.tick() // session known while unfinished
	h.tasks.rows[0].Status = model.StatusComplete
	h.runErr = errors.New("rpc closed")
	h.r.tick() // failed: known must NOT become empty
	h.runErr = nil
	h.r.tick() // session still running: known last good tick -> not "first seen"
	h.running = nil
	h.proc(wt)
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_UnreadableConfigStopsNothing(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.cfgErr = errors.New("parsing config.toml: bad")
	ticks(h, 6) // enabled == true alongside the error: the fallback must not arm the kill
	testutil.Equal(t, len(h.stops), 0)
	testutil.Equal(t, h.scanN, 0)
	h.cfgErr = nil // fixed
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_ReReadBeforeEachStop(t *testing.T) {
	// Stops run sequentially from a tick-start snapshot; a task resumed while
	// an earlier worktree is being stopped must not be hit.
	w1, w2 := dsRoot+"/p/one", dsRoot+"/p/two"
	h := newDSHarness(dsTask("a", w1, model.StatusComplete), dsTask("b", w2, model.StatusComplete))
	h.proc(w1)
	h.proc(w2)
	h.r.tick()
	h.onStop = func(wt string) {
		if wt == w1 {
			h.running = []string{"b"} // b resumed during a's (slow) stop
		}
	}
	got := h.r.tick()
	testutil.DeepEqual(t, h.stops, []string{w1})
	testutil.DeepEqual(t, got, []string{w1})
}

func TestDevStackReaper_ReReadSeesConfigTurnedOff(t *testing.T) {
	w1, w2 := dsRoot+"/p/one", dsRoot+"/p/two"
	h := newDSHarness(dsTask("a", w1, model.StatusComplete), dsTask("b", w2, model.StatusComplete))
	h.proc(w1)
	h.proc(w2)
	h.r.tick()
	h.onStop = func(string) { h.cfgErr = errors.New("config now unreadable") }
	h.r.tick()
	testutil.DeepEqual(t, h.stops, []string{w1})
}

func TestDevStackReaper_ReReadReadErrorSkipsStop(t *testing.T) {
	wt := dsRoot + "/p/one"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.r.tick()
	calls := 0
	h.r.tasks = &flakyDSTasks{rows: h.tasks.rows, failFrom: 2, calls: &calls}
	testutil.Equal(t, len(h.r.tick()), 0) // 1st read ok, re-read before the stop fails
	testutil.Equal(t, len(h.stops), 0)
}

type flakyDSTasks struct {
	rows     []*model.Task
	failFrom int
	calls    *int
}

func (f *flakyDSTasks) TasksChecked() ([]*model.Task, error) {
	*f.calls++
	if *f.calls >= f.failFrom {
		return nil, errors.New("db locked")
	}
	return f.rows, nil
}

func TestDevStackReaper_CancelledBeforeStopStopsNothing(t *testing.T) {
	wt := dsRoot + "/p/one"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	cancel := make(chan struct{})
	h.r.cancel = cancel
	h.proc(wt)
	h.r.tick()
	close(cancel)
	testutil.Equal(t, len(h.r.tick()), 0)
	testutil.Equal(t, len(h.stops), 0)
}

func TestDevStackReaper_UnderManagedRoot(t *testing.T) {
	r := newDevStackReaper(nil, nil, nil, dsRoot+"/")
	testutil.True(t, r.underManagedRoot(dsRoot+"/p/t"))
	testutil.True(t, r.underManagedRoot(dsRoot+"/p/t/sub"))
	testutil.True(t, !r.underManagedRoot(dsRoot))
	testutil.True(t, !r.underManagedRoot(dsRoot+"/p"))
	testutil.True(t, !r.underManagedRoot("/elsewhere/p/t"))
	testutil.True(t, !r.underManagedRoot(filepath.Dir(dsRoot)+"/p/t"))
}

// fakeCheckedRunner implements only what runningFn touches.
type fakeCheckedRunner struct {
	agent.SessionRunner
	ids []string
	err error
}

func (f *fakeCheckedRunner) Running() []string                 { return f.ids }
func (f *fakeCheckedRunner) RunningChecked() ([]string, error) { return f.ids, f.err }

type fakePlainRunner struct {
	agent.SessionRunner
	ids []string
}

func (f *fakePlainRunner) Running() []string { return f.ids }

func TestRunningFn(t *testing.T) {
	ids, err := runningFn(&fakeCheckedRunner{ids: []string{"a"}, err: errors.New("rpc")})()
	if err == nil {
		t.Fatal("a runner that reports an error must have it surfaced")
	}
	testutil.DeepEqual(t, ids, []string{"a"})

	ids, err = runningFn(&fakePlainRunner{ids: []string{"b"}})()
	testutil.NoError(t, err)
	testutil.DeepEqual(t, ids, []string{"b"})
}

// --- loop / wiring -------------------------------------------------------

// dsServeDaemon builds a daemon over a FILE-backed DB so config.toml is live,
// rooted in a temp HOME so DataDir() and the managed worktree root are temp.
func dsServeDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	database, err := db.Open(filepath.Join(db.DataDir(), "data.sql"))
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	d := New(database)
	d.prDisableFlagPath = filepath.Join(t.TempDir(), prPollDisableFlag)
	d.usageBudgetProbe = func(context.Context) error { return nil }
	d.codexProbe = func(context.Context) error { return nil }
	return d, filepath.Join(t.TempDir(), "t.sock")
}

func TestServe_DSReaper(t *testing.T) {
	prev := devStackReapInterval
	devStackReapInterval = 5 * time.Millisecond
	t.Cleanup(func() { devStackReapInterval = prev })

	d, sock := dsServeDaemon(t)
	wt := filepath.Join(db.DataDir(), "worktrees", "proj", "fin")
	testutil.NoError(t, d.db.Add(&model.Task{ID: "fin", Name: "fin", Status: model.StatusComplete, Worktree: wt}))
	d.devStackScan = func() ([]agent.DevStackProc, error) {
		return []agent.DevStackProc{{PID: 1, Name: "mysqld", WorktreePath: wt}}, nil
	}
	stops := make(chan string, 1000)
	d.devStackStop = func(w string, _ agent.StopOptions) {
		select {
		case stops <- w:
		default:
		}
	}
	errCh := make(chan error, 1)
	go func() { errCh <- d.Serve(sock) }()
	waitForSocket(t, sock)

	drain := func() {
		for {
			select {
			case <-stops:
			default:
				return
			}
		}
	}
	expectStop := func(why string) {
		t.Helper()
		select {
		case got := <-stops:
			testutil.Equal(t, got, wt)
		case <-time.After(3 * time.Second):
			t.Fatalf("no stop: %s", why)
		}
	}
	expectQuiet := func(why string) {
		t.Helper()
		time.Sleep(60 * time.Millisecond) // let any in-flight tick finish
		drain()
		select {
		case got := <-stops:
			t.Fatalf("unexpected stop of %s: %s", got, why)
		case <-time.After(250 * time.Millisecond):
		}
	}
	cfg := filepath.Join(db.DataDir(), "config.toml")

	// Default on: the loop is wired by Serve and stops the finished stack under
	// the managed root, through the real db.Config() read.
	expectStop("default config")

	// stop_finished = false is read LIVE, no restart.
	testutil.NoError(t, os.WriteFile(cfg, []byte("[devstack]\nstop_finished = false\n"), 0o600))
	expectQuiet("stop_finished = false")

	// Back on.
	testutil.NoError(t, os.WriteFile(cfg, []byte("[devstack]\nstop_finished = true\n"), 0o600))
	expectStop("stop_finished = true again")

	// A typo elsewhere in the file makes the whole file unparseable and Config()
	// falls back to defaults (on). The sweeper must stop nothing.
	testutil.NoError(t, os.WriteFile(cfg, []byte("[devstack]\nstop_finished = false\n[[[ typo\n"), 0o600))
	expectQuiet("unparseable config.toml")

	d.Shutdown()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestRunDevStackReaper_TicksAndExitsOnDone(t *testing.T) {
	prev := devStackReapInterval
	devStackReapInterval = 5 * time.Millisecond
	t.Cleanup(func() { devStackReapInterval = prev })

	d, _ := dsServeDaemon(t)
	scans := make(chan struct{}, 100)
	wt := filepath.Join(db.DataDir(), "worktrees", "p", "t")
	testutil.NoError(t, d.db.Add(&model.Task{ID: "x", Name: "x", Status: model.StatusComplete, Worktree: wt}))
	d.devStackScan = func() ([]agent.DevStackProc, error) {
		select {
		case scans <- struct{}{}:
		default:
		}
		return nil, nil
	}
	exited := make(chan struct{})
	go func() { d.runDevStackReaper(); close(exited) }()
	select {
	case <-scans: // a tick really ran against the real DB, with the real root and switch
	case <-time.After(3 * time.Second):
		t.Fatal("loop never ticked")
	}
	close(d.done)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("dev-stack reaper loop did not exit on done")
	}
}
