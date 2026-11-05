package daemon

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

const dsRoot = "/h/.argus/worktrees"

type fakeDSRunner struct{ running []string }

func (f *fakeDSRunner) Running() []string { return f.running }

type fakeDSTasks struct {
	rows []*model.Task
	err  error
}

func (f *fakeDSTasks) Tasks() ([]*model.Task, error) { return f.rows, f.err }

func dsTask(id, wt string, st model.Status) *model.Task {
	return &model.Task{ID: id, Worktree: wt, Status: st}
}

type dsHarness struct {
	r      *devStackReaper
	runner *fakeDSRunner
	tasks  *fakeDSTasks
	procs  []agent.DevStackProc
	scanN  int
	stops  []string
	enable bool
}

func newDSHarness(rows ...*model.Task) *dsHarness {
	h := &dsHarness{runner: &fakeDSRunner{}, tasks: &fakeDSTasks{rows: rows}, enable: true}
	h.r = newDevStackReaper(h.runner, h.tasks, func() bool { return h.enable }, dsRoot)
	h.r.scan = func() ([]agent.DevStackProc, error) { h.scanN++; return h.procs, nil }
	h.r.stop = func(wt string) { h.stops = append(h.stops, wt) }
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

func TestDevStackReaper_StopsFinishedAfterGrace(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	testutil.Equal(t, len(h.r.tick()), 0) // armed
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
	testutil.DeepEqual(t, h.stops, []string{wt})
	// Re-arms from zero afterwards.
	testutil.Equal(t, len(h.r.tick()), 0)
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
	// Task completed by a clean exit: no session was ever observed.
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.r.tick() // first tick (backlog) counts toward the grace
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_UnfinishedNeverStopped(t *testing.T) {
	for _, st := range []model.Status{model.StatusPending, model.StatusInProgress, model.StatusInReview} {
		wt := dsRoot + "/Sketch/t1"
		h := newDSHarness(dsTask("a", wt, st))
		h.proc(wt)
		ticks(h, 5)
		testutil.Equal(t, len(h.stops), 0)
		testutil.Equal(t, h.scanN, 0) // nothing eligible: no process scan at all
	}
}

func TestDevStackReaper_LiveSessionBlocksAndResetsGrace(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusInProgress))
	h.proc(wt)
	h.runner.running = []string{"a"}
	h.r.tick() // session known while unfinished
	h.tasks.rows[0].Status = model.StatusComplete
	ticks(h, 5) // finished but session still winding down: blocked
	testutil.Equal(t, len(h.stops), 0)
	h.runner.running = nil // reaper stopped it
	h.r.tick()             // armed (1)
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_ResumedFinishedTaskIsExempt(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.r.tick()                       // first tick: not running, nothing seen
	h.runner.running = []string{"a"} // first seen live while finished: deliberate
	h.r.tick()
	h.runner.running = nil
	h.proc(wt)
	ticks(h, 6)
	testutil.Equal(t, len(h.stops), 0)

	// Task becomes unfinished then finishes again: exemption cleared.
	h.tasks.rows[0].Status = model.StatusInProgress
	h.r.tick()
	h.tasks.rows[0].Status = model.StatusComplete
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_FirstTickGrantsNoExemption(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.runner.running = []string{"a"} // live finished session at startup (backlog)
	h.r.tick()
	h.runner.running = nil
	h.proc(wt)
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_MainCheckoutNeverCandidate(t *testing.T) {
	for _, wt := range []string{
		"/h/Development/thanx/sketch", // a project's main checkout
		"/h/.argus/worktrees",         // the root itself
		"/h/.argus/worktrees/Sketch",  // a bare project dir
		"/h/.claude/worktrees/p/t",    // legacy location: not the managed dir
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
	h.runner.running = []string{"b"}
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0) // unfinished claimant blocks

	h.tasks.rows[1].Status = model.StatusComplete // still live, winding down
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0)

	h.runner.running = nil
	h.r.tick()
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_SharedWorktreeExemptClaimantBlocks(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete), dsTask("b", wt, model.StatusComplete))
	h.r.tick()
	h.runner.running = []string{"b"}
	h.r.tick() // b resumed
	h.runner.running = nil
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
	h.proc(dsRoot + "/Sketch/orphan") // no row: doctor's job, not the sweeper's
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0)
}

func TestDevStackReaper_StackGoneResetsGrace(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.r.tick()
	h.procs = nil
	h.r.tick() // stack gone: counter dropped
	h.proc(wt)
	testutil.Equal(t, len(h.r.tick()), 0)
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_DisabledSignalsNothing(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.enable = false
	ticks(h, 6)
	testutil.Equal(t, len(h.stops), 0)
	testutil.Equal(t, h.scanN, 0)
	// Re-enabled: grace starts over, then it stops.
	h.enable = true
	testutil.Equal(t, len(h.r.tick()), 0)
	testutil.DeepEqual(t, h.r.tick(), []string{wt})
}

func TestDevStackReaper_ResumeTrackedWhileDisabled(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.enable = false
	h.r.tick()
	h.runner.running = []string{"a"}
	h.r.tick()
	h.runner.running = nil
	h.enable = true
	h.proc(wt)
	ticks(h, 5)
	testutil.Equal(t, len(h.stops), 0)
}

func TestDevStackReaper_ScanFailureSkipsTick(t *testing.T) {
	wt := dsRoot + "/Sketch/t1"
	h := newDSHarness(dsTask("a", wt, model.StatusComplete))
	h.proc(wt)
	h.r.scan = func() ([]agent.DevStackProc, error) { return nil, errors.New("pgrep gone") }
	testutil.Equal(t, len(h.r.tick()), 0)
	testutil.Equal(t, len(h.stops), 0)
}

func TestDevStackReaper_TaskListFailureSkipsTick(t *testing.T) {
	h := newDSHarness()
	h.tasks.err = errors.New("db locked")
	testutil.Equal(t, len(h.r.tick()), 0)
}

func TestDevStackReaper_UnderManagedRoot(t *testing.T) {
	r := newDevStackReaper(&fakeDSRunner{}, &fakeDSTasks{}, func() bool { return true }, dsRoot+"/")
	testutil.True(t, r.underManagedRoot(dsRoot+"/p/t"))
	testutil.True(t, r.underManagedRoot(dsRoot+"/p/t/sub"))
	testutil.True(t, !r.underManagedRoot(dsRoot))
	testutil.True(t, !r.underManagedRoot(dsRoot+"/p"))
	testutil.True(t, !r.underManagedRoot("/elsewhere/p/t"))
	testutil.True(t, !r.underManagedRoot(filepath.Dir(dsRoot)+"/p/t"))
}

func TestRunDevStackReaper_TicksAndExitsOnDone(t *testing.T) {
	prev := devStackReapInterval
	devStackReapInterval = 5 * time.Millisecond
	t.Cleanup(func() { devStackReapInterval = prev })

	d, _ := testDaemon(t)
	exited := make(chan struct{})
	go func() { d.runDevStackReaper(); close(exited) }()
	time.Sleep(30 * time.Millisecond) // several no-op ticks: no eligible worktrees
	close(d.done)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("dev-stack reaper loop did not exit on done")
	}
}
