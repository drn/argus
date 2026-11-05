package daemon

import (
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// devStackReapInterval is the dev-stack sweeper's tick. It matches the
// finished-session reaper's cadence so "a session was first seen live while
// its task was already finished" (the deliberate-resume signal) is observed
// within seconds, not a minute — a slower tick would misread a short-lived
// task as a resume. A var only so the loop test can shorten it.
var devStackReapInterval = 10 * time.Second

// devStackReapGraceTicks is how many consecutive ticks a worktree must stay
// eligible before its stack is stopped (~60s at the default interval), so an
// agent that just finished — or a hera_accept check-in — can wrap up first.
const devStackReapGraceTicks = 6

// devStackSweepKillGrace is the SIGTERM->SIGKILL wait on the sweeper path.
// Longer than removal's 5s: the sweeper keeps the worktree, and a later resume
// reuses the datadir, so a database gets time to shut down cleanly.
const devStackSweepKillGrace = 30 * time.Second

// devStackReapTasks is the slice of *db.DB the sweeper uses. TasksChecked
// returns every row, archived included, and an error for any row it could not
// read — a partial list could lose an unfinished claimant of a shared worktree.
type devStackReapTasks interface {
	TasksChecked() ([]*model.Task, error)
}

// devStackReaper stops the dev stack (mysqld/redis/postgres/caddy/
// process-compose) left running in the managed worktree of a finished task
// (stop-finished-task-devstacks). It is process-table-driven rather than
// session-driven so a task whose session exited before anyone saw it
// (clean exit -> complete, hera_accept, prune) is still covered. Every input
// it acts on can fail (task rows, the running set, config.toml, the process
// scan); a failed tick changes no state and stops nothing. Not goroutine-safe:
// driven by one loop.
type devStackReaper struct {
	running func() ([]string, error) // live session task IDs, incl. pending restart where the runner reports it
	tasks   devStackReapTasks
	enabled func() (bool, error) // devstack.stop_finished; error = config unreadable
	root    string               // <DataDir>/worktrees; only paths under it qualify
	scan    func() ([]agent.DevStackProc, error)
	stop    func(worktree string, o agent.StopOptions)
	cancel  <-chan struct{} // daemon shutdown

	graceTicks int
	killGrace  time.Duration
	armed      map[string]int  // worktree -> consecutive eligible ticks
	exempt     map[string]bool // task ID -> deliberately resumed while finished
	known      map[string]bool // task IDs whose session was running last good tick
	ticked     bool
	skipLogged map[string]bool // worktrees already logged as not-managed
	lastCfgErr string          // dedupes the unreadable-config log line
}

func newDevStackReaper(running func() ([]string, error), tasks devStackReapTasks, enabled func() (bool, error), root string) *devStackReaper {
	return &devStackReaper{
		running: running, tasks: tasks, enabled: enabled, root: filepath.Clean(root),
		scan: agent.ScanDevStackProcesses, stop: agent.StopDevStack,
		graceTicks: devStackReapGraceTicks, killGrace: devStackSweepKillGrace,
		armed: map[string]int{}, exempt: map[string]bool{},
		known: map[string]bool{}, skipLogged: map[string]bool{},
	}
}

// underManagedRoot reports whether wt is a project/task directory inside the
// argus worktrees root. A project's main checkout, the root itself, or a bare
// project dir never qualifies.
func (r *devStackReaper) underManagedRoot(wt string) bool {
	rel, err := filepath.Rel(r.root, filepath.Clean(wt))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return strings.Contains(rel, string(filepath.Separator)) // <project>/<task>[/...]
}

// gather reads the two inputs every decision needs. Any failure aborts the
// caller's tick before it touches state.
func (r *devStackReaper) gather() (rows []*model.Task, running map[string]bool, ok bool) {
	rows, err := r.tasks.TasksChecked()
	if err != nil {
		slog.Warn("devstack reaper: task list failed (skipping tick)", "err", err)
		uxlog.Log("[devstackreap] task list failed: %v", err)
		return nil, nil, false
	}
	ids, err := r.running()
	if err != nil {
		slog.Warn("devstack reaper: running-session list failed (skipping tick)", "err", err)
		uxlog.Log("[devstackreap] running list failed: %v", err)
		return nil, nil, false
	}
	running = make(map[string]bool, len(ids))
	for _, id := range ids {
		running[id] = true
	}
	return rows, running, true
}

// eligibleWorktrees returns the managed worktrees whose every claimant task is
// finished, idle and not exempt. logSkips logs (once per path) a worktree
// outside the managed dir.
func (r *devStackReaper) eligibleWorktrees(rows []*model.Task, running map[string]bool, logSkips bool) map[string]bool {
	type claim struct{ blocked bool }
	claims := map[string]*claim{}
	for _, t := range rows {
		if t.Worktree == "" {
			continue
		}
		wt := filepath.Clean(t.Worktree)
		c := claims[wt]
		if c == nil {
			c = &claim{}
			claims[wt] = c
		}
		if !taskFinished(t) || running[t.ID] || r.exempt[t.ID] {
			c.blocked = true
		}
	}
	eligible := map[string]bool{}
	for wt, c := range claims {
		if c.blocked {
			continue
		}
		if !r.underManagedRoot(wt) {
			if logSkips && !r.skipLogged[wt] {
				r.skipLogged[wt] = true
				slog.Info("devstack reaper: skip worktree not under managed dir", "worktree", wt)
				uxlog.Log("[devstackreap] skip %q: not under managed worktree dir", wt)
			}
			continue
		}
		eligible[wt] = true
	}
	return eligible
}

// switchOn reads devstack.stop_finished. An unreadable config.toml means the
// setting is unknown, not "on": nothing is stopped (an automatic kill must not
// be armed by a typo that silently reverted stop_finished = false).
func (r *devStackReaper) switchOn() bool {
	on, err := r.enabled()
	if err != nil {
		if msg := err.Error(); msg != r.lastCfgErr {
			r.lastCfgErr = msg
			slog.Warn("devstack reaper: config.toml unreadable, stopping nothing", "err", err)
			uxlog.Log("[devstackreap] config unreadable, stopping nothing: %v", err)
		}
		return false
	}
	r.lastCfgErr = ""
	return on
}

// stillEligible re-reads task rows, live sessions and the setting right now.
// Stops run sequentially and each can take tens of seconds, so the tick-start
// snapshot is stale by the time a later worktree's turn comes.
func (r *devStackReaper) stillEligible(wt string) bool {
	rows, running, ok := r.gather()
	if !ok || !r.switchOn() {
		return false
	}
	return r.eligibleWorktrees(rows, running, false)[wt]
}

// tick runs one pass and returns the worktrees whose stacks it stopped.
func (r *devStackReaper) tick() []string {
	// Read everything first: a failed read must leave ticked, exemptions,
	// known sessions and grace counters exactly as they were (a failed first
	// tick would otherwise turn the startup backlog into exemptions).
	rows, running, ok := r.gather()
	if !ok {
		return nil
	}
	firstTick := !r.ticked
	r.ticked = true

	// Resume tracking (mirrors finishedSessionReaper's exempt): a session
	// first seen while its task is already finished — not on the first tick,
	// which is the startup backlog — was started deliberately. Cleared when
	// the task becomes unfinished again. Tracked even while the sweep is off.
	for _, t := range rows {
		switch {
		case !taskFinished(t):
			delete(r.exempt, t.ID)
		case running[t.ID] && !r.known[t.ID] && !firstTick:
			r.exempt[t.ID] = true
			slog.Info("devstack reaper: finished task resumed, stack exempt", "task", t.ID)
			uxlog.Log("[devstackreap] task=%s resumed while finished: stack exempt", t.ID)
		}
	}
	r.known = running

	if !r.switchOn() {
		r.armed = map[string]int{}
		return nil
	}

	eligible := r.eligibleWorktrees(rows, running, true)
	if len(eligible) == 0 {
		r.armed = map[string]int{}
		return nil // nothing could qualify: skip the process scan
	}

	procs, err := r.scan()
	if err != nil {
		// Counts neither advance nor reset: whether the stack is still there
		// is unknown, and stillEligible re-checks before any stop anyway.
		slog.Warn("devstack reaper: process scan failed (skipping tick)", "err", err)
		uxlog.Log("[devstackreap] scan failed: %v", err)
		return nil
	}
	next := map[string]int{}
	var stopped []string
	seen := map[string]bool{}
	for _, p := range procs {
		wt := p.WorktreePath
		if !eligible[wt] || seen[wt] {
			continue
		}
		seen[wt] = true
		next[wt] = r.armed[wt] + 1
		if next[wt] < r.graceTicks {
			continue
		}
		select {
		case <-r.cancel:
			r.armed = next
			return stopped
		default:
		}
		if !r.stillEligible(wt) {
			slog.Info("devstack reaper: worktree no longer eligible, not stopping", "worktree", wt)
			uxlog.Log("[devstackreap] skip %q: no longer eligible at stop time", wt)
			delete(next, wt)
			continue
		}
		slog.Info("devstack reaper: stopping dev stack of finished task", "worktree", wt)
		uxlog.Log("[devstackreap] stopping stack worktree=%s", wt)
		r.stop(wt, agent.StopOptions{
			KillGrace: r.killGrace,
			Cancel:    r.cancel,
			Proceed:   func() bool { return r.stillEligible(wt) },
		})
		delete(next, wt)
		stopped = append(stopped, wt)
	}
	r.armed = next
	return stopped
}

// runningFn adapts d.runner: the supervisor client reports an RPC failure
// (which must skip the tick, not read as "no sessions"); the in-process runner
// cannot fail.
func runningFn(runner agent.SessionRunner) func() ([]string, error) {
	if rc, ok := runner.(interface{ RunningChecked() ([]string, error) }); ok {
		return rc.RunningChecked
	}
	return func() ([]string, error) { return runner.Running(), nil }
}

// runDevStackReaper is the sweeper's d.done-gated loop.
func (d *Daemon) runDevStackReaper() {
	r := newDevStackReaper(runningFn(d.runner), d.db,
		func() (bool, error) {
			on := d.db.Config().DevStack.StopFinished
			return on, d.db.ConfigFileErr() // read AFTER Config(), which re-parses
		},
		filepath.Join(db.DataDir(), "worktrees"))
	if d.devStackScan != nil {
		r.scan = d.devStackScan
	}
	if d.devStackStop != nil {
		r.stop = d.devStackStop
	}
	r.cancel = d.done
	ticker := time.NewTicker(devStackReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			if stopped := r.tick(); len(stopped) > 0 {
				slog.Info("devstack reaper tick", "stopped", len(stopped))
			}
		}
	}
}
