package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/drn/argus/internal/model"
)

// ErrScheduleNotFound is returned by GetSchedule and DeleteSchedule when the
// requested ID has no row.
var ErrScheduleNotFound = errors.New("scheduled task not found")

// Schedules returns all scheduled tasks ordered by name.
func (d *DB) Schedules() ([]*model.ScheduledTask, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT id, name, project, prompt, backend, schedule, enabled, created_at, last_run_at, next_run_at, last_task_id, last_error FROM scheduled_tasks ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query schedules: %w", err)
	}
	defer rows.Close()

	var out []*model.ScheduledTask
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// GetSchedule returns the schedule with the given ID, or ErrScheduleNotFound.
func (d *DB) GetSchedule(id string) (*model.ScheduledTask, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	row := d.conn.QueryRow(`SELECT id, name, project, prompt, backend, schedule, enabled, created_at, last_run_at, next_run_at, last_task_id, last_error FROM scheduled_tasks WHERE id=?`, id)
	s, err := scanSchedule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScheduleNotFound
	}
	return s, err
}

// AddSchedule inserts a new schedule, generating an ID and CreatedAt if
// unset. The caller's struct is updated with the assigned values.
func (d *DB) AddSchedule(s *model.ScheduledTask) error {
	if s.ID == "" {
		s.ID = generateID()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`INSERT INTO scheduled_tasks (id, name, project, prompt, backend, schedule, enabled, created_at, last_run_at, next_run_at, last_task_id, last_error) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.Project, s.Prompt, s.Backend, s.Schedule, boolToInt(s.Enabled), formatTime(s.CreatedAt), formatTime(s.LastRunAt), formatTime(s.NextRunAt), s.LastTaskID, s.LastError)
	if err != nil {
		return fmt.Errorf("insert schedule: %w", err)
	}
	return nil
}

// UpdateSchedule writes all fields for the given schedule.
func (d *DB) UpdateSchedule(s *model.ScheduledTask) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`UPDATE scheduled_tasks SET name=?, project=?, prompt=?, backend=?, schedule=?, enabled=?, last_run_at=?, next_run_at=?, last_task_id=?, last_error=? WHERE id=?`,
		s.Name, s.Project, s.Prompt, s.Backend, s.Schedule, boolToInt(s.Enabled), formatTime(s.LastRunAt), formatTime(s.NextRunAt), s.LastTaskID, s.LastError, s.ID)
	if err != nil {
		return fmt.Errorf("update schedule: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrScheduleNotFound
	}
	return nil
}

// DeleteSchedule removes the schedule with the given ID.
func (d *DB) DeleteSchedule(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`DELETE FROM scheduled_tasks WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrScheduleNotFound
	}
	return nil
}

// scanSchedule reads one row from a *sql.Row or *sql.Rows in the column order
// used by Schedules / GetSchedule above.
func scanSchedule(scanner interface {
	Scan(...any) error
}) (*model.ScheduledTask, error) {
	var s model.ScheduledTask
	var enabled int
	var createdAt, lastRunAt, nextRunAt string
	if err := scanner.Scan(&s.ID, &s.Name, &s.Project, &s.Prompt, &s.Backend, &s.Schedule, &enabled, &createdAt, &lastRunAt, &nextRunAt, &s.LastTaskID, &s.LastError); err != nil {
		return nil, err
	}
	s.Enabled = enabled != 0
	s.CreatedAt = parseTime(createdAt)
	s.LastRunAt = parseTime(lastRunAt)
	s.NextRunAt = parseTime(nextRunAt)
	return &s, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
