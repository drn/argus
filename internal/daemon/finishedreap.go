package daemon

import (
	"errors"
	"log/slog"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// finishedReapInterval is the finished-session reaper's tick. A session is
// stopped on its second consecutive idle tick, so an agent gets at least this
// long of quiet after finishing before it is stopped. A var only so the loop
// test can shorten it.
var finishedReapInterval = 10 * time.Second

// finishedReapRunner is the slice of agent.SessionRunner the reaper uses.
type finishedReapRunner interface {
	RunningAndIdle() (running, idle []string)
	HasPendingRestart(taskID string) bool
	Stop(taskID string) error
}

// finishedReapTasks is the slice of *db.DB the reaper uses.
type finishedReapTasks interface {
	Get(id string) (*model.Task, error)
}

type finishedReapEntry struct {
	exempt bool // session started while its task was already finished
	armed  bool // idle + finished on the previous tick
}

// finishedSessionReaper stops the agent session of a task that is complete
// or archived once the session has been idle on two consecutive ticks
// (stop-finished-task-sessions). Every path that finishes a task — MCP, REST,
// TUI, hera_accept, the gater — is covered by observing the task row instead
// of hooking each call site, and the idle requirement means an agent that
// completes its own task finishes its reply before being stopped. Not
// goroutine-safe: driven by one loop.
type finishedSessionReaper struct {
	runner finishedReapRunner
	tasks  finishedReapTasks
	state  map[string]finishedReapEntry // running sessions only
	ticked bool
}

func newFinishedSessionReaper(runner finishedReapRunner, tasks finishedReapTasks) *finishedSessionReaper {
	return &finishedSessionReaper{runner: runner, tasks: tasks, state: map[string]finishedReapEntry{}}
}

func taskFinished(t *model.Task) bool {
	return t.Status == model.StatusComplete || t.Archived
}

// tick runs one pass and returns the task IDs whose sessions it stopped.
func (f *finishedSessionReaper) tick() []string {
	running, idle := f.runner.RunningAndIdle()
	idleSet := make(map[string]bool, len(idle))
	for _, id := range idle {
		idleSet[id] = true
	}
	firstTick := !f.ticked
	f.ticked = true

	next := make(map[string]finishedReapEntry, len(running))
	var stopped []string
	for _, id := range running {
		t, err := f.tasks.Get(id)
		if err != nil || t == nil {
			continue // no row (or unreadable): never touch a session we can't classify
		}
		e, known := f.state[id]
		finished := taskFinished(t)
		if !known {
			// A session first seen on a finished task after startup was
			// started deliberately on that task — leave it alone. On the
			// first tick it is the backlog this reaper exists to clear.
			e = finishedReapEntry{exempt: finished && !firstTick}
		}
		switch {
		case !finished:
			e = finishedReapEntry{}
		case e.exempt:
		case !idleSet[id] || f.runner.HasPendingRestart(id):
			e.armed = false
		case !e.armed:
			e.armed = true
		default:
			if err := f.runner.Stop(id); err != nil {
				if !errors.Is(err, agent.ErrSessionNotFound) {
					slog.Warn("finished-session reaper: stop failed (retrying next tick)", "task", id, "err", err)
					uxlog.Log("[finishedreap] stop task=%s failed: %v", id, err)
				}
				break // stays armed: retried next tick
			}
			slog.Info("finished-session reaper: stopped idle session of finished task", "task", id, "status", t.Status.String(), "archived", t.Archived)
			uxlog.Log("[finishedreap] stopped task=%s status=%s archived=%t", id, t.Status, t.Archived)
			stopped = append(stopped, id)
			continue // session is going away; forget it
		}
		next[id] = e
	}
	f.state = next
	return stopped
}

// runFinishedSessionReaper is the reaper's d.done-gated loop.
func (d *Daemon) runFinishedSessionReaper() {
	f := newFinishedSessionReaper(d.runner, d.db)
	ticker := time.NewTicker(finishedReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			if stopped := f.tick(); len(stopped) > 0 {
				slog.Info("finished-session reaper tick", "stopped", len(stopped))
			}
		}
	}
}
