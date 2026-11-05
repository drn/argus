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

// devStackReapRunner is the slice of agent.SessionRunner the sweeper uses.
// Running includes tasks between exit and a queued kick-restart.
type devStackReapRunner interface {
	Running() []string
}

// devStackReapTasks is the slice of *db.DB the sweeper uses. Tasks returns
// every row, archived included.
type devStackReapTasks interface {
	Tasks() ([]*model.Task, error)
}

// devStackReaper stops the dev stack (mysqld/redis/postgres/caddy/
// process-compose) left running in the managed worktree of a finished task
// (stop-finished-task-devstacks). It is process-table-driven rather than
// session-driven so a task whose session exited before anyone saw it
// (clean exit -> complete, hera_accept, prune) is still covered. Not
// goroutine-safe: driven by one loop.
type devStackReaper struct {
	runner  devStackReapRunner
	tasks   devStackReapTasks
	enabled func() bool
	root    string // <DataDir>/worktrees; only paths under it qualify
	scan    func() ([]agent.DevStackProc, error)
	stop    func(worktree string)

	graceTicks int
	armed      map[string]int  // worktree -> consecutive eligible ticks
	exempt     map[string]bool // task ID -> deliberately resumed while finished
	known      map[string]bool // task IDs whose session was running last tick
	ticked     bool
	skipLogged map[string]bool // worktrees already logged as not-managed
}

func newDevStackReaper(runner devStackReapRunner, tasks devStackReapTasks, enabled func() bool, root string) *devStackReaper {
	return &devStackReaper{
		runner: runner, tasks: tasks, enabled: enabled, root: filepath.Clean(root),
		scan: agent.ScanDevStackProcesses, stop: agent.StopDevStack,
		graceTicks: devStackReapGraceTicks,
		armed:      map[string]int{}, exempt: map[string]bool{},
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

// tick runs one pass and returns the worktrees whose stacks it stopped.
func (r *devStackReaper) tick() []string {
	firstTick := !r.ticked
	r.ticked = true

	rows, err := r.tasks.Tasks()
	if err != nil {
		slog.Warn("devstack reaper: task list failed (skipping tick)", "err", err)
		uxlog.Log("[devstackreap] task list failed: %v", err)
		return nil
	}
	running := map[string]bool{}
	for _, id := range r.runner.Running() {
		running[id] = true
	}

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
			uxlog.Log("[devstackreap] task=%s resumed while finished: stack exempt", t.ID)
		}
	}
	r.known = running

	if !r.enabled() {
		r.armed = map[string]int{}
		return nil
	}

	// Group rows by cleaned worktree path; a worktree is eligible only when
	// every claimant is finished, idle and not exempt.
	type claim struct {
		any, blocked bool
	}
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
		c.any = true
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
			if !r.skipLogged[wt] {
				r.skipLogged[wt] = true
				uxlog.Log("[devstackreap] skip %q: not under managed worktree dir", wt)
			}
			continue
		}
		eligible[wt] = true
	}
	if len(eligible) == 0 {
		r.armed = map[string]int{}
		return nil // nothing could qualify: skip the process scan
	}

	procs, err := r.scan()
	if err != nil {
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
		slog.Info("devstack reaper: stopping dev stack of finished task", "worktree", wt)
		uxlog.Log("[devstackreap] stopping stack worktree=%s", wt)
		r.stop(wt)
		delete(next, wt)
		stopped = append(stopped, wt)
	}
	r.armed = next
	return stopped
}

// runDevStackReaper is the sweeper's d.done-gated loop.
func (d *Daemon) runDevStackReaper() {
	r := newDevStackReaper(d.runner, d.db,
		func() bool { return d.db.Config().DevStack.StopFinished },
		filepath.Join(db.DataDir(), "worktrees"))
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
