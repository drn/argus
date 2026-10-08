package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

type fakeReapRunner struct {
	running, idle []string
	pending       map[string]bool
	stopErr       error
	stopped       []string
}

func (f *fakeReapRunner) RunningAndIdle() ([]string, []string) { return f.running, f.idle }
func (f *fakeReapRunner) HasPendingRestart(id string) bool     { return f.pending[id] }
func (f *fakeReapRunner) Stop(id string) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stopped = append(f.stopped, id)
	return nil
}

type fakeReapTasks map[string]*model.Task

func (f fakeReapTasks) Get(id string) (*model.Task, error) { return f[id], nil }

func task(id string, st model.Status, archived bool) *model.Task {
	return &model.Task{ID: id, Status: st, Archived: archived}
}

func newReaper(r *fakeReapRunner, tasks fakeReapTasks) *finishedSessionReaper {
	return newFinishedSessionReaper(r, tasks)
}

func TestFinishedReaper_StopsAfterTwoIdleTicks(t *testing.T) {
	r := &fakeReapRunner{running: []string{"a"}, idle: []string{"a"}}
	tasks := fakeReapTasks{"a": task("a", model.StatusInProgress, false)}
	f := newReaper(r, tasks)

	f.tick() // known while in progress
	tasks["a"].Status = model.StatusComplete
	testutil.Equal(t, len(f.tick()), 0) // armed
	testutil.DeepEqual(t, f.tick(), []string{"a"})
	testutil.DeepEqual(t, r.stopped, []string{"a"})
}

func TestFinishedReaper_SelfCompletingAgentFinishesReplyFirst(t *testing.T) {
	r := &fakeReapRunner{running: []string{"a"}}
	tasks := fakeReapTasks{"a": task("a", model.StatusInProgress, false)}
	f := newReaper(r, tasks)
	f.tick()
	tasks["a"].Status = model.StatusComplete // agent called task_complete on itself
	f.tick()                                 // still writing its reply: not idle
	r.idle = []string{"a"}
	f.tick() // idle once: armed only
	r.idle = nil
	f.tick() // output again: disarmed
	r.idle = []string{"a"}
	f.tick() // armed again
	testutil.Equal(t, len(r.stopped), 0)
	testutil.DeepEqual(t, f.tick(), []string{"a"})
}

func TestFinishedReaper_ArchivedTaskStopped(t *testing.T) {
	r := &fakeReapRunner{running: []string{"a"}, idle: []string{"a"}}
	tasks := fakeReapTasks{"a": task("a", model.StatusInProgress, false)}
	f := newReaper(r, tasks)
	f.tick()
	tasks["a"].SetArchived(true)
	f.tick()
	testutil.DeepEqual(t, f.tick(), []string{"a"})
}

func TestFinishedReaper_UnfinishedNeverStopped(t *testing.T) {
	for _, st := range []model.Status{model.StatusPending, model.StatusInProgress, model.StatusInReview} {
		t.Run(st.String(), func(t *testing.T) {
			r := &fakeReapRunner{running: []string{"a"}, idle: []string{"a"}}
			f := newReaper(r, fakeReapTasks{"a": task("a", st, false)})
			for range 4 {
				f.tick()
			}
			testutil.Equal(t, len(r.stopped), 0)
		})
	}
}

func TestFinishedReaper_StartupBacklogIsSwept(t *testing.T) {
	r := &fakeReapRunner{running: []string{"c", "a"}, idle: []string{"c", "a"}}
	f := newReaper(r, fakeReapTasks{
		"c": task("c", model.StatusComplete, false),
		"a": task("a", model.StatusInReview, true),
	})
	f.tick()
	got := f.tick()
	testutil.Equal(t, len(got), 2)
}

func TestFinishedReaper_DeliberateRestartOfFinishedTaskExempt(t *testing.T) {
	r := &fakeReapRunner{}
	tasks := fakeReapTasks{"a": task("a", model.StatusInReview, true)}
	f := newReaper(r, tasks)
	f.tick()                                         // first tick: nothing running
	r.running, r.idle = []string{"a"}, []string{"a"} // operator started the archived task
	for range 4 {
		f.tick()
	}
	testutil.Equal(t, len(r.stopped), 0)

	// Unfinishing then re-finishing clears the exemption.
	tasks["a"].SetArchived(false)
	f.tick()
	tasks["a"].SetArchived(true)
	f.tick()
	testutil.DeepEqual(t, f.tick(), []string{"a"})
}

func TestFinishedReaper_SessionGoneForgetsState(t *testing.T) {
	r := &fakeReapRunner{running: []string{"a"}, idle: []string{"a"}}
	tasks := fakeReapTasks{"a": task("a", model.StatusInProgress, false)}
	f := newReaper(r, tasks)
	f.tick()
	tasks["a"].Status = model.StatusComplete
	f.tick() // armed
	r.running, r.idle = nil, nil
	f.tick() // session exited on its own
	// A new session for the already-complete task is a deliberate restart.
	r.running, r.idle = []string{"a"}, []string{"a"}
	f.tick()
	f.tick()
	testutil.Equal(t, len(r.stopped), 0)
}

func TestFinishedReaper_PendingRestartNotStopped(t *testing.T) {
	r := &fakeReapRunner{running: []string{"a"}, idle: []string{"a"}, pending: map[string]bool{"a": true}}
	f := newReaper(r, fakeReapTasks{"a": task("a", model.StatusComplete, false)})
	for range 3 {
		f.tick()
	}
	testutil.Equal(t, len(r.stopped), 0)
}

func TestFinishedReaper_StopFailureRetries(t *testing.T) {
	r := &fakeReapRunner{running: []string{"a"}, idle: []string{"a"}, stopErr: errors.New("rpc down")}
	f := newReaper(r, fakeReapTasks{"a": task("a", model.StatusComplete, false)})
	f.tick()
	testutil.Equal(t, len(f.tick()), 0)
	r.stopErr = nil
	testutil.DeepEqual(t, f.tick(), []string{"a"})
}

func TestFinishedReaper_SessionNotFoundIsQuiet(t *testing.T) {
	r := &fakeReapRunner{running: []string{"a"}, idle: []string{"a"}, stopErr: agent.ErrSessionNotFound}
	f := newReaper(r, fakeReapTasks{"a": task("a", model.StatusComplete, false)})
	f.tick()
	testutil.Equal(t, len(f.tick()), 0)
}

func TestFinishedReaper_UnknownTaskRowSkipped(t *testing.T) {
	r := &fakeReapRunner{running: []string{"ghost"}, idle: []string{"ghost"}}
	f := newReaper(r, fakeReapTasks{})
	f.tick()
	f.tick()
	testutil.Equal(t, len(r.stopped), 0)
}

func TestRunFinishedSessionReaper_TicksAndExitsOnDone(t *testing.T) {
	prev := finishedReapInterval
	finishedReapInterval = 5 * time.Millisecond
	t.Cleanup(func() { finishedReapInterval = prev })

	d, _ := testDaemon(t)
	tk := &model.Task{ID: "fin", Name: "fin", Status: model.StatusComplete}
	testutil.NoError(t, d.db.Add(tk))
	exited := make(chan struct{})
	go func() { d.runFinishedSessionReaper(); close(exited) }()
	time.Sleep(30 * time.Millisecond) // several no-op ticks: no sessions running
	close(d.done)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("reaper loop did not exit on done")
	}
}
