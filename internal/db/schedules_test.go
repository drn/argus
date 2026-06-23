package db

import (
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestScheduleCRUD(t *testing.T) {
	d := testDB(t)

	s := &model.ScheduledTask{
		Name:     "Nightly tests",
		Project:  "argus",
		Prompt:   "Run all tests",
		Schedule: "@daily",
		Enabled:  true,
	}
	if err := d.AddSchedule(s); err != nil {
		t.Fatal(err)
	}
	if s.ID == "" {
		t.Fatal("expected ID generated")
	}
	if s.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt populated")
	}

	got, err := d.GetSchedule(s.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Name, "Nightly tests")
	testutil.Equal(t, got.Project, "argus")
	testutil.Equal(t, got.Schedule, "@daily")
	testutil.Equal(t, got.Enabled, true)

	got.Enabled = false
	got.Schedule = "@hourly"
	got.LastRunAt = time.Now()
	if err := d.UpdateSchedule(got); err != nil {
		t.Fatal(err)
	}

	all, err := d.Schedules()
	testutil.NoError(t, err)
	testutil.Equal(t, len(all), 1)
	testutil.Equal(t, all[0].Enabled, false)
	testutil.Equal(t, all[0].Schedule, "@hourly")

	if err := d.DeleteSchedule(got.ID); err != nil {
		t.Fatal(err)
	}
	_, err = d.GetSchedule(got.ID)
	if !errors.Is(err, ErrScheduleNotFound) {
		t.Fatalf("expected ErrScheduleNotFound, got %v", err)
	}
}

// TestScheduleModelRoundTrip pins the per-schedule model override through
// Add → Get → Update → Get, and that a row inserted without one defaults to "".
func TestScheduleModelRoundTrip(t *testing.T) {
	d := testDB(t)

	withModel := &model.ScheduledTask{
		Name:     "sonnet watcher",
		Project:  "argus",
		Prompt:   "watch",
		Backend:  "claude",
		Model:    "sonnet",
		Schedule: "@daily",
		Enabled:  true,
	}
	noModel := &model.ScheduledTask{
		Name:     "default watcher",
		Project:  "argus",
		Prompt:   "watch",
		Schedule: "@daily",
		Enabled:  true,
	}
	testutil.NoError(t, d.AddSchedule(withModel))
	testutil.NoError(t, d.AddSchedule(noModel))

	got, err := d.GetSchedule(withModel.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Model, "sonnet")

	gotNone, err := d.GetSchedule(noModel.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotNone.Model, "")

	// Update the model and confirm it persists.
	got.Model = "opus"
	testutil.NoError(t, d.UpdateSchedule(got))
	reread, err := d.GetSchedule(withModel.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, reread.Model, "opus")

	// Clearing the model writes the empty string back.
	reread.Model = ""
	testutil.NoError(t, d.UpdateSchedule(reread))
	cleared, err := d.GetSchedule(withModel.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, cleared.Model, "")
}

func TestUpdateScheduleMissing(t *testing.T) {
	d := testDB(t)
	err := d.UpdateSchedule(&model.ScheduledTask{ID: "nope"})
	if !errors.Is(err, ErrScheduleNotFound) {
		t.Fatalf("expected ErrScheduleNotFound, got %v", err)
	}
}
