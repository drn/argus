package model

import (
	"errors"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// ScheduledTask defines a recurring task: at each cron firing the daemon
// creates a fresh task in Project using Prompt (and optionally Backend), the
// same way the new-task form or vault watcher does.
//
// Schedule is parsed by github.com/robfig/cron/v3 with ParseStandard
// (5-field cron + descriptors @hourly/@daily/@weekly/@monthly/@yearly and
// @every <duration>).
type ScheduledTask struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Project   string    `json:"project"`
	Prompt    string    `json:"prompt"`
	Backend   string    `json:"backend,omitempty"`
	Schedule  string    `json:"schedule"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`

	// Bookkeeping populated by the scheduler.
	LastRunAt  time.Time `json:"last_run_at,omitempty"`
	NextRunAt  time.Time `json:"next_run_at,omitempty"`
	LastTaskID string    `json:"last_task_id,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
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

// Validate returns nil if the schedule's required fields are set and Schedule
// parses cleanly.
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
	if _, err := ParseSchedule(s.Schedule); err != nil {
		return err
	}
	return nil
}

// NextFire returns the next time this schedule fires after `after`. Returns
// the zero time when the schedule cannot be parsed.
func (s *ScheduledTask) NextFire(after time.Time) time.Time {
	sched, err := ParseSchedule(s.Schedule)
	if err != nil {
		return time.Time{}
	}
	return sched.Next(after)
}
