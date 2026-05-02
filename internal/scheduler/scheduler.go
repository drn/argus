// Package scheduler runs the cron-like scheduler that fires scheduled tasks
// (recurring prompts) by calling a TaskCreator. It runs as a goroutine inside
// the daemon, owns no PTY state of its own, and persists last/next-run
// bookkeeping to the DB after each tick.
package scheduler

import (
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// defaultTickInterval is how often the scheduler wakes up to check for due
// schedules. One minute matches the smallest cron resolution; finer ticking
// would not buy us anything because cron expressions can't fire more often.
const defaultTickInterval = time.Minute

// TaskCreator creates a task. Same shape used by the HTTP API so the
// scheduler plugs into the existing headless flow.
type TaskCreator func(name, prompt, project string) (*model.Task, error)

// Scheduler is the cron service. It owns its own ticker goroutine; methods
// other than Start/Stop are safe to call from any goroutine but exist mostly
// for tests.
type Scheduler struct {
	db       *db.DB
	create   TaskCreator
	interval time.Duration
	now      func() time.Time

	stopCh chan struct{}
	mu     sync.Mutex

	// fireMu serialises all fire calls — both the per-tick path and the
	// out-of-cycle RunNow path. Without it, an HTTP "run now" arriving
	// during a tick can spawn a duplicate task: tick() loads schedules with
	// stale NextRunAt, RunNow fires and persists fresh NextRunAt, then
	// tick() resumes its loop with the stale copy and fires again. The
	// fireMu hold is short (one create-task call + one DB update), so it
	// does not meaningfully delay the tick loop.
	fireMu sync.Mutex

	// onFire, if set, is called after the cron tick path successfully
	// creates a task. RunNow (user-triggered manual fire) is intentionally
	// exempt — see SetOnFire's docs. Read/written under mu.
	onFire func(*model.Task)
}

// New creates a scheduler. Call Start in a goroutine and Stop on shutdown.
func New(database *db.DB, creator TaskCreator) *Scheduler {
	return &Scheduler{
		db:       database,
		create:   creator,
		interval: defaultTickInterval,
		now:      time.Now,
		stopCh:   make(chan struct{}),
	}
}

// Start runs the scheduler tick loop until Stop is called.
//
// On each tick: load all schedules; for each enabled schedule, if NextRunAt
// has passed (or is unset and CreatedAt has passed), fire it and recompute
// next-run. Disabled schedules are left alone but still get their NextRunAt
// recomputed so the UI shows a useful preview.
func (s *Scheduler) Start() error {
	uxlog.Log("[scheduler] starting (interval=%s)", s.interval)

	// Initial seed so any schedule that never fired (NextRunAt is zero) gets
	// its NextRunAt populated immediately. This is also a no-op when the DB
	// already has next_run_at populated.
	s.tick()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			uxlog.Log("[scheduler] stopped")
			return nil
		case <-ticker.C:
			s.tick()
		}
	}
}

// Stop signals Start to exit.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.stopCh:
		// already stopped
	default:
		close(s.stopCh)
	}
}

// RunNow fires the schedule with the given ID immediately, regardless of
// when its next scheduled fire is. Bookkeeping is updated so the regular
// tick won't fire again immediately afterwards.
func (s *Scheduler) RunNow(id string) (*model.Task, error) {
	sched, err := s.db.GetSchedule(id)
	if err != nil {
		return nil, err
	}
	parsed, perr := model.ParseSchedule(sched.Schedule)
	if perr != nil {
		// Persist the parse error so the UI shows it, then bail. RunNow on a
		// malformed expression is a user error; we don't try to fire anyway.
		sched.LastError = perr.Error()
		sched.NextRunAt = time.Time{}
		_ = s.db.UpdateSchedule(sched)
		return nil, perr
	}
	s.fireMu.Lock()
	defer s.fireMu.Unlock()
	return s.fire(sched, parsed, s.now())
}

// tick runs one scheduling pass. Errors are logged but never propagated —
// a bad schedule on row N must not block rows N+1..M from firing.
func (s *Scheduler) tick() {
	schedules, err := s.db.Schedules()
	if err != nil {
		uxlog.Log("[scheduler] load schedules: %v", err)
		return
	}
	now := s.now()
	for _, sched := range schedules {
		s.tickOne(sched, now)
	}
}

// tickOne handles a single schedule: validate the cron expression, decide
// whether to fire, fire if due, and persist next/last bookkeeping.
func (s *Scheduler) tickOne(sched *model.ScheduledTask, now time.Time) {
	parsed, err := model.ParseSchedule(sched.Schedule)
	if err != nil {
		// Persist the parse error and skip; a malformed schedule shouldn't
		// disable the row outright (the user might be mid-edit), but it must
		// not get fired.
		if sched.LastError != err.Error() {
			sched.LastError = err.Error()
			sched.NextRunAt = time.Time{}
			if uErr := s.db.UpdateSchedule(sched); uErr != nil {
				uxlog.Log("[scheduler] persist parse error %s: %v", sched.ID, uErr)
			}
		}
		return
	}

	// Decide whether to fire.
	//
	// We fire when NextRunAt is set and has passed. NextRunAt is zero only
	// for brand-new schedules or when the cron expression was invalid before
	// — in those cases we just compute the next fire and wait. The first
	// fire after creation is one tick *later*, never immediate; that matches
	// cron semantics and avoids surprising the user with a task spawned the
	// instant they click Save.
	shouldFire := sched.Enabled && !sched.NextRunAt.IsZero() && !now.Before(sched.NextRunAt)

	if shouldFire {
		// Acquire fireMu before the per-fire DB reread so we can't race with
		// a concurrent RunNow on the same row. Re-check `shouldFire` after
		// the lock — RunNow may have already fired, advancing NextRunAt past
		// `now`. Without the recheck we'd duplicate the fire.
		s.fireMu.Lock()
		latest, gErr := s.db.GetSchedule(sched.ID)
		if gErr == nil {
			*sched = *latest
		}
		stillDue := sched.Enabled && !sched.NextRunAt.IsZero() && !now.Before(sched.NextRunAt)
		if !stillDue {
			s.fireMu.Unlock()
			return
		}
		task, fErr := s.fire(sched, parsed, now)
		s.fireMu.Unlock()
		if fErr != nil {
			// fire already persisted LastError; nothing else to do.
			return
		}
		// Invoke the OnFire callback OUTSIDE fireMu so a slow callback
		// (network push, log write) cannot stall the tick loop or block a
		// concurrent RunNow on a different schedule.
		if task != nil {
			if cb := s.fireCallback(); cb != nil {
				cb(task)
			}
		}
		return
	}

	// Not firing — but ensure NextRunAt is populated for the UI.
	desired := parsed.Next(now)
	if !sched.NextRunAt.Equal(desired) || sched.LastError != "" {
		sched.NextRunAt = desired
		sched.LastError = ""
		if err := s.db.UpdateSchedule(sched); err != nil {
			uxlog.Log("[scheduler] persist next_run_at %s: %v", sched.ID, err)
		}
	}
}

// fire creates the task for the given schedule and updates bookkeeping.
// The caller is responsible for honouring sched.Enabled — fire itself does
// not consult Enabled because RunNow bypasses that check. Callers MUST
// hold fireMu so concurrent invocations against the same row cannot
// double-fire.
func (s *Scheduler) fire(sched *model.ScheduledTask, parsed cron.Schedule, now time.Time) (*model.Task, error) {
	// Generate a unique-per-fire name so worktree creation can't collide
	// with the previous fire still being open.
	name := scheduleFireName(sched.Name, now)

	task, err := s.create(name, sched.Prompt, sched.Project)
	if err != nil {
		sched.LastError = err.Error()
		sched.LastRunAt = now
		sched.NextRunAt = parsed.Next(now)
		if uErr := s.db.UpdateSchedule(sched); uErr != nil {
			uxlog.Log("[scheduler] persist fire error %s: %v", sched.ID, uErr)
		}
		uxlog.Log("[scheduler] fire %s: %v", sched.ID, err)
		return nil, err
	}

	if sched.Backend != "" && task.Backend != sched.Backend {
		task.Backend = sched.Backend
		if uErr := s.db.Update(task); uErr != nil {
			uxlog.Log("[scheduler] persist backend override %s: %v", task.ID, uErr)
		}
	}

	sched.LastRunAt = now
	sched.LastTaskID = task.ID
	sched.LastError = ""
	sched.NextRunAt = parsed.Next(now)
	if uErr := s.db.UpdateSchedule(sched); uErr != nil {
		uxlog.Log("[scheduler] persist post-fire %s: %v", sched.ID, uErr)
	}
	uxlog.Log("[scheduler] fired %s -> task %s (next=%s)", sched.ID, task.ID, sched.NextRunAt.Format(time.RFC3339))
	return task, nil
}

// SetInterval changes the tick interval. Only useful for tests; ignored if
// the scheduler is already running (caller would need to Stop/Start).
func (s *Scheduler) SetInterval(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval = d
}

// SetClock overrides time.Now for tests.
func (s *Scheduler) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// SetOnFire registers a callback invoked after a cron-tick fire successfully
// creates a task. RunNow (user-triggered manual fires from the UI) does NOT
// invoke this callback: a manual "Run Now" click is an explicit user action,
// so a follow-up notification is redundant. Safe to call before or after
// Start. nil clears the callback.
func (s *Scheduler) SetOnFire(cb func(*model.Task)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onFire = cb
}

// fireCallback returns the current OnFire callback under the mutex so
// concurrent SetOnFire calls cannot race the tick path.
func (s *Scheduler) fireCallback() func(*model.Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.onFire
}

// scheduleFireName builds the per-fire task name. Exported via the package
// so the TUI's manual run-now path uses the same convention as the
// scheduler — preventing rapid manual triggers from colliding on worktree
// names. Format must stay stable; tests assert it.
func scheduleFireName(base string, now time.Time) string {
	return fmt.Sprintf("%s %s", base, now.Format("2006-01-02 15:04"))
}

// FireName is the public alias of scheduleFireName for callers outside the
// package.
func FireName(base string, now time.Time) string {
	return scheduleFireName(base, now)
}
