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

func TestUpdateScheduleMissing(t *testing.T) {
	d := testDB(t)
	err := d.UpdateSchedule(&model.ScheduledTask{ID: "nope"})
	if !errors.Is(err, ErrScheduleNotFound) {
		t.Fatalf("expected ErrScheduleNotFound, got %v", err)
	}
}
