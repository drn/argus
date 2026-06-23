package model

import (
	"errors"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// ScheduledTask defines a scheduled task that fires either on a recurring
// cron expression OR once at a specific timestamp. At each fire the daemon
// creates a fresh task in Project using Prompt (and optionally Backend and
// Model), the same way the new-task form or vault watcher does. Model is an
// optional per-schedule model override; empty means the backend's configured
// default model.
//
// Schedule is parsed by github.com/robfig/cron/v3 with ParseStandard
// (5-field cron + descriptors @hourly/@daily/@weekly/@monthly/@yearly and
// @every <duration>). One-shot rows leave Schedule empty and set RunOnceAt;
// after firing, the scheduler sets Enabled=false so the row is preserved
// for inspection but never fires again.
//
// Exactly one of Schedule and RunOnceAt must be set. Validate enforces this.
type ScheduledTask struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Project   string    `json:"project"`
	Prompt    string    `json:"prompt"`
	Backend   string    `json:"backend,omitempty"`
	Model     string    `json:"model,omitempty"`
	Schedule  string    `json:"schedule"`
	RunOnceAt time.Time `json:"run_once_at,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`

	// Bookkeeping populated by the scheduler.
	LastRunAt  time.Time `json:"last_run_at,omitempty"`
	NextRunAt  time.Time `json:"next_run_at,omitempty"`
	LastTaskID string    `json:"last_task_id,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

// IsOneShot returns true if this row fires once at RunOnceAt rather than
// on a recurring cron expression.
func (s *ScheduledTask) IsOneShot() bool {
	return !s.RunOnceAt.IsZero()
}

// scheduleParser is a package-global parser configured to accept the standard
// 5-field cron syntax plus descriptors. Reused — robfig/cron parsers are safe
// to share.
var scheduleParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// ParseSchedule validates a cron expression. It accepts standard 5-field cron
// (e.g. "0 9 * * 1-5"), descriptors (@hourly, @daily, @weekly, @monthly,
// @yearly), and intervals (@every 30m, @every 1h).
func ParseSchedule(expr string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, errors.New("schedule is required")
	}
	return scheduleParser.Parse(expr)
}

// Validate returns nil if the schedule's required fields are set, exactly
// one of Schedule and RunOnceAt is provided, and Schedule (when used) parses
// cleanly.
func (s *ScheduledTask) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(s.Project) == "" {
		return errors.New("project is required")
	}
	if strings.TrimSpace(s.Prompt) == "" {
		return errors.New("prompt is required")
	}
	hasCron := strings.TrimSpace(s.Schedule) != ""
	hasOnce := !s.RunOnceAt.IsZero()
	if hasCron && hasOnce {
		return errors.New("specify either schedule (cron) or run_once_at, not both")
	}
	if !hasCron && !hasOnce {
		return errors.New("schedule (cron) or run_once_at is required")
	}
	if hasCron {
		if _, err := ParseSchedule(s.Schedule); err != nil {
			return err
		}
	}
	return nil
}

// NextFire returns the next time this schedule fires after `after`. For
// recurring rows it consults the cron expression. For one-shot rows it
// returns RunOnceAt itself when still in the future, else the zero time.
// Returns the zero time when the schedule cannot be parsed.
func (s *ScheduledTask) NextFire(after time.Time) time.Time {
	if s.IsOneShot() {
		if s.RunOnceAt.After(after) {
			return s.RunOnceAt
		}
		return time.Time{}
	}
	sched, err := ParseSchedule(s.Schedule)
	if err != nil {
		return time.Time{}
	}
	return sched.Next(after)
}
