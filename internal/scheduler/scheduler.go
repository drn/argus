// Package scheduler runs the cron-like scheduler that fires scheduled tasks
// (recurring prompts) by calling a TaskCreator. It runs as a goroutine inside
// the daemon, owns no PTY state of its own, and persists last/next-run
// bookkeeping to the DB after each tick.
package scheduler

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// defaultTickInterval is how often the scheduler wakes up to check for due
// schedules. One minute matches the smallest cron resolution; finer ticking
// would not buy us anything because cron expressions can't fire more often.
const defaultTickInterval = time.Minute

// TaskCreator creates a task. Same shape used by the vault watcher and HTTP
// API so the scheduler plugs into the existing headless flow.
type TaskCreator func(name, prompt, project, todoPath string) (*model.Task, error)

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
	log.Printf("[scheduler] starting (interval=%s)", s.interval)

	// Initial seed so any schedule that never fired (NextRunAt is zero) gets
	// its NextRunAt populated immediately. This is also a no-op when the DB
	// already has next_run_at populated.
	s.tick()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			log.Printf("[scheduler] stopped")
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
	return s.fire(sched, s.now())
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
		if _, err := s.fire(sched, now); err != nil {
			// fire already persisted LastError; nothing else to do.
			return
		}
		// fire updated LastRunAt/NextRunAt and persisted; reload so we don't
		// stomp those fields below.
		fresh, gErr := s.db.GetSchedule(sched.ID)
		if gErr == nil {
			*sched = *fresh
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
// not consult Enabled because RunNow bypasses that check.
func (s *Scheduler) fire(sched *model.ScheduledTask, now time.Time) (*model.Task, error) {
	parsed, perr := model.ParseSchedule(sched.Schedule)
	if perr != nil {
		sched.LastError = perr.Error()
		sched.NextRunAt = time.Time{}
		_ = s.db.UpdateSchedule(sched)
		return nil, perr
	}

	// Generate a unique-per-fire name so worktree creation can't collide
	// with the previous fire still being open.
	name := fmt.Sprintf("%s %s", sched.Name, now.Format("2006-01-02 15:04"))

	task, err := s.create(name, sched.Prompt, sched.Project, "")
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
