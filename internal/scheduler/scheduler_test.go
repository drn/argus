package scheduler

import (
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// fakeClock returns a controllable time source for the scheduler.
type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time { return f.now }
func (f *fakeClock) Advance(d time.Duration) {
	f.now = f.now.Add(d)
}

// recordingCreator implements TaskCreator and records each invocation. By
// default it returns a fresh task; failNext lets a single test simulate a
// failure on the next call.
type recordingCreator struct {
	calls    []model.Task
	failNext error
}

func (r *recordingCreator) Create(name, prompt, project, backend string) (*model.Task, error) {
	if r.failNext != nil {
		err := r.failNext
		r.failNext = nil
		return nil, err
	}
	t := &model.Task{
		ID:      "t" + name,
		Name:    name,
		Project: project,
		Prompt:  prompt,
		Backend: backend,
	}
	r.calls = append(r.calls, *t)
	return t, nil
}

func newTestScheduler(t *testing.T) (*Scheduler, *db.DB, *recordingCreator, *fakeClock) {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	rec := &recordingCreator{}
	clk := &fakeClock{now: time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)}
	s := New(d, rec.Create)
	s.SetClock(clk.Now)
	return s, d, rec, clk
}

func TestSchedulerNoFireOnFirstTick(t *testing.T) {
	s, d, rec, _ := newTestScheduler(t)

	sched := &model.ScheduledTask{
		Name:     "every-10m",
		Project:  "p",
		Prompt:   "do",
		Schedule: "@every 10m",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}

	// First tick: schedule has no NextRunAt, so we just compute it.
	s.tick()
	testutil.Equal(t, len(rec.calls), 0)

	got, _ := d.GetSchedule(sched.ID)
	if got.NextRunAt.IsZero() {
		t.Fatal("expected NextRunAt populated after first tick")
	}
}

func TestSchedulerFiresWhenDue(t *testing.T) {
	s, d, rec, clk := newTestScheduler(t)
	sched := &model.ScheduledTask{
		Name:     "every-1m",
		Project:  "p",
		Prompt:   "go",
		Schedule: "@every 1m",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}

	s.tick() // populate NextRunAt
	clk.Advance(2 * time.Minute)
	s.tick() // should fire

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 fire, got %d", len(rec.calls))
	}
	testutil.Equal(t, rec.calls[0].Project, "p")
	testutil.Equal(t, rec.calls[0].Prompt, "go")

	got, _ := d.GetSchedule(sched.ID)
	if got.LastRunAt.IsZero() {
		t.Fatal("expected LastRunAt populated")
	}
	if got.NextRunAt.Before(clk.Now()) {
		t.Fatalf("expected NextRunAt in the future, got %v (now=%v)", got.NextRunAt, clk.Now())
	}
}

func TestSchedulerSkipsDisabled(t *testing.T) {
	s, d, rec, clk := newTestScheduler(t)
	sched := &model.ScheduledTask{
		Name:     "off",
		Project:  "p",
		Prompt:   "go",
		Schedule: "@every 1m",
		Enabled:  false,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}

	s.tick()
	clk.Advance(10 * time.Minute)
	s.tick()
	testutil.Equal(t, len(rec.calls), 0)

	// Even though disabled, NextRunAt should still get populated for UI display.
	got, _ := d.GetSchedule(sched.ID)
	if got.NextRunAt.IsZero() {
		t.Fatal("expected NextRunAt populated for disabled schedule (UI preview)")
	}
}

func TestSchedulerInvalidExprPersistsError(t *testing.T) {
	s, d, rec, _ := newTestScheduler(t)
	sched := &model.ScheduledTask{
		Name:     "bad",
		Project:  "p",
		Prompt:   "go",
		Schedule: "not-a-cron",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}

	s.tick()
	testutil.Equal(t, len(rec.calls), 0)
	got, _ := d.GetSchedule(sched.ID)
	if got.LastError == "" {
		t.Fatal("expected LastError populated for invalid expression")
	}
}

func TestRunNowFiresOnceAndUpdatesNext(t *testing.T) {
	s, d, rec, _ := newTestScheduler(t)
	sched := &model.ScheduledTask{
		Name:     "manual",
		Project:  "p",
		Prompt:   "go",
		Schedule: "@every 1h",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}
	task, err := s.RunNow(sched.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, task.Project, "p")
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 fire, got %d", len(rec.calls))
	}
	got, _ := d.GetSchedule(sched.ID)
	if got.LastRunAt.IsZero() {
		t.Fatal("expected LastRunAt populated")
	}
}

func TestRunNowMissing(t *testing.T) {
	s, _, _, _ := newTestScheduler(t)
	_, err := s.RunNow("nope")
	if !errors.Is(err, db.ErrScheduleNotFound) {
		t.Fatalf("expected ErrScheduleNotFound, got %v", err)
	}
}

func TestFireName(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 5, 0, 0, time.UTC)
	got := FireName("Nightly", now)
	want := "Nightly 2026-04-28 12:05"
	if got != want {
		t.Fatalf("FireName mismatch: got %q want %q", got, want)
	}
}

// TestSchedulerOnFireFiredFromTick verifies the OnFire callback runs after
// a tick-fired schedule successfully creates a task. The callback is the
// hook the daemon uses to send a "scheduled task started" push notification.
func TestSchedulerOnFireFiredFromTick(t *testing.T) {
	s, d, _, clk := newTestScheduler(t)
	var firedTasks []string
	s.SetOnFire(func(task *model.Task) { firedTasks = append(firedTasks, task.ID) })

	sched := &model.ScheduledTask{
		Name:     "every-1m",
		Project:  "p",
		Prompt:   "go",
		Schedule: "@every 1m",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}

	s.tick()
	clk.Advance(2 * time.Minute)
	s.tick() // fires
	if len(firedTasks) != 1 {
		t.Fatalf("expected OnFire called once after tick fire, got %d invocations", len(firedTasks))
	}
}

// TestSchedulerOnFireSkippedFromRunNow verifies the OnFire callback is NOT
// invoked when the user manually triggers a schedule via RunNow. RunNow is
// a UI action, so a follow-up notification would be redundant.
func TestSchedulerOnFireSkippedFromRunNow(t *testing.T) {
	s, d, _, _ := newTestScheduler(t)
	var fired int
	s.SetOnFire(func(*model.Task) { fired++ })

	sched := &model.ScheduledTask{
		Name:     "manual",
		Project:  "p",
		Prompt:   "go",
		Schedule: "@every 1h",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunNow(sched.ID); err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, fired, 0)
}

// TestSchedulerOnFireSkippedOnFireError verifies the callback is not invoked
// when fire returns an error (e.g. task creator failed). Pushing for a
// failed fire would mislead the user.
func TestSchedulerOnFireSkippedOnFireError(t *testing.T) {
	s, d, rec, clk := newTestScheduler(t)
	rec.failNext = errors.New("create failed")

	var fired int
	s.SetOnFire(func(*model.Task) { fired++ })

	sched := &model.ScheduledTask{
		Name:     "every-1m",
		Project:  "p",
		Prompt:   "go",
		Schedule: "@every 1m",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}
	s.tick()
	clk.Advance(2 * time.Minute)
	s.tick()
	testutil.Equal(t, fired, 0)
}

// Regression: a schedule that has already fired (NextRunAt advanced past
// now) must not be re-fired by tick() if RunNow snuck in first. See
// review-20260428.md WARNING #6.
func TestSchedulerNoDoubleFireAfterRunNow(t *testing.T) {
	s, d, rec, clk := newTestScheduler(t)
	sched := &model.ScheduledTask{
		Name:     "race",
		Project:  "p",
		Prompt:   "go",
		Schedule: "@every 1m",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}
	s.tick() // populate NextRunAt
	clk.Advance(2 * time.Minute)

	// Simulate RunNow firing first, then tick reading the (post-fire) row.
	if _, err := s.RunNow(sched.ID); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 fire after RunNow, got %d", len(rec.calls))
	}
	s.tick() // would re-fire if the race-prevention check is missing
	if len(rec.calls) != 1 {
		t.Fatalf("expected still 1 fire after tick (RunNow already advanced NextRunAt), got %d", len(rec.calls))
	}
}

func TestStopExits(t *testing.T) {
	s, _, _, _ := newTestScheduler(t)
	s.SetInterval(50 * time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- s.Start() }()

	// Give the loop a moment to enter its select.
	time.Sleep(20 * time.Millisecond)
	s.Stop()
	select {
	case err := <-done:
		testutil.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("scheduler.Start did not return after Stop")
	}
}

// Regression: sched.Backend must reach TaskCreator at fire time so the agent
// process is launched with the override. The previous implementation set
// task.Backend AFTER agent.CreateAndStart had already resolved the backend
// inside its transactional flow, so the live session ran on the default
// backend even though the row read correctly.
func TestSchedulerFiresWithBackendOverride(t *testing.T) {
	s, d, rec, clk := newTestScheduler(t)
	sched := &model.ScheduledTask{
		Name:     "with-codex",
		Project:  "p",
		Prompt:   "go",
		Backend:  "codex",
		Schedule: "@every 1m",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}
	s.tick()
	clk.Advance(2 * time.Minute)
	s.tick()
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 fire, got %d", len(rec.calls))
	}
	testutil.Equal(t, rec.calls[0].Backend, "codex")
}

// RunNow must also pass sched.Backend through. The cron-tick path and the
// manual-fire path both go through Scheduler.fire; this test pins down the
// manual path explicitly so a future refactor can't silently bypass the
// override on RunNow.
func TestSchedulerRunNowWithBackendOverride(t *testing.T) {
	s, d, rec, _ := newTestScheduler(t)
	sched := &model.ScheduledTask{
		Name:     "manual-codex",
		Project:  "p",
		Prompt:   "go",
		Backend:  "codex",
		Schedule: "@every 1h",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunNow(sched.ID); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 fire, got %d", len(rec.calls))
	}
	testutil.Equal(t, rec.calls[0].Backend, "codex")
}
