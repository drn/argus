package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/drn/argus/internal/model"
)

// taskColumns is the canonical column list for task queries.
const taskColumns = `id, name, status, project, branch, prompt, backend, worktree, agent_pid, session_id, pr_url, todo_path, archived, created_at, started_at, ended_at`

// scanner is implemented by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanTask reads a task from a row using the canonical column order.
func scanTask(row scanner) (*model.Task, error) {
	t := &model.Task{}
	var status, createdAt, startedAt, endedAt string
	var archived int
	if err := row.Scan(&t.ID, &t.Name, &status, &t.Project, &t.Branch, &t.Prompt, &t.Backend, &t.Worktree, &t.AgentPID, &t.SessionID, &t.PRURL, &t.TodoPath, &archived, &createdAt, &startedAt, &endedAt); err != nil {
		return nil, err
	}
	t.Status, _ = model.ParseStatus(status)
	t.Archived = archived != 0
	t.CreatedAt = parseTime(createdAt)
	t.StartedAt = parseTime(startedAt)
	t.EndedAt = parseTime(endedAt)
	return t, nil
}

func (d *DB) Tasks() []*model.Task {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT ` + taskColumns + ` FROM tasks ORDER BY created_at ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var tasks []*model.Task
	for rows.Next() {
		if t, err := scanTask(rows); err == nil {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

func (d *DB) Add(t *model.Task) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if t.ID == "" {
		t.ID = generateID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}

	archivedInt := 0
	if t.Archived {
		archivedInt = 1
	}
	_, err := d.conn.Exec(`INSERT INTO tasks (id, name, status, project, branch, prompt, backend, worktree, agent_pid, session_id, pr_url, todo_path, archived, created_at, started_at, ended_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Status.String(), t.Project, t.Branch, t.Prompt, t.Backend, t.Worktree, t.AgentPID, t.SessionID, t.PRURL, t.TodoPath, archivedInt,
		formatTime(t.CreatedAt), formatTime(t.StartedAt), formatTime(t.EndedAt))
	return err
}

func (d *DB) Update(t *model.Task) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	archivedInt := 0
	if t.Archived {
		archivedInt = 1
	}
	res, err := d.conn.Exec(`UPDATE tasks SET name=?, status=?, project=?, branch=?, prompt=?, backend=?, worktree=?, agent_pid=?, session_id=?, pr_url=?, todo_path=?, archived=?, created_at=?, started_at=?, ended_at=? WHERE id=?`,
		t.Name, t.Status.String(), t.Project, t.Branch, t.Prompt, t.Backend, t.Worktree, t.AgentPID, t.SessionID, t.PRURL, t.TodoPath, archivedInt,
		formatTime(t.CreatedAt), formatTime(t.StartedAt), formatTime(t.EndedAt), t.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task not found: %s", t.ID)
	}
	return nil
}

// Rename updates only the name column for a task.
// Unlike Update, this does not overwrite other fields, avoiding races with
// concurrent status changes (e.g., agent exit while rename modal is open).
func (d *DB) Rename(id, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`UPDATE tasks SET name=? WHERE id=?`, name, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task not found: %s", id)
	}
	return nil
}

func (d *DB) Delete(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`DELETE FROM tasks WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task not found: %s", id)
	}
	return nil
}

func (d *DB) Get(id string) (*model.Task, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	row := d.conn.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id=?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (d *DB) PruneCompleted() ([]*model.Task, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Fetch completed tasks first
	rows, err := d.conn.Query(`SELECT ` + taskColumns + ` FROM tasks WHERE status='complete'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pruned []*model.Task
	for rows.Next() {
		if t, err := scanTask(rows); err == nil {
			pruned = append(pruned, t)
		}
	}

	if len(pruned) == 0 {
		return nil, nil
	}

	_, err = d.conn.Exec(`DELETE FROM tasks WHERE status='complete'`)
	if err != nil {
		return nil, err
	}
	return pruned, nil
}

// WorktreePaths returns the set of all non-empty worktree paths currently in the DB.
// Returns an error if the query fails — callers should skip orphan sweep on error
// to avoid treating all worktrees as orphans.
func (d *DB) WorktreePaths() (map[string]bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT worktree FROM tasks WHERE worktree != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	paths := make(map[string]bool)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err == nil {
			paths[p] = true
		}
	}
	return paths, nil
}

// TasksByTodoPath returns a map from todo_path to the most recent task with that path.
// Only tasks with a non-empty todo_path are included. Ordered by created_at ASC so
// later tasks overwrite earlier ones (most recent wins).
func (d *DB) TasksByTodoPath() map[string]*model.Task {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT ` + taskColumns + ` FROM tasks WHERE todo_path != '' ORDER BY created_at ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	m := make(map[string]*model.Task)
	for rows.Next() {
		if t, err := scanTask(rows); err == nil {
			m[t.TodoPath] = t
		}
	}
	return m
}
