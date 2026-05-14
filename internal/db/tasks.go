package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/drn/argus/internal/model"
)

// ErrTaskNotFound is the sentinel returned (wrapped) by every db.* task
// mutation when the target row is missing. Callers use errors.Is(err,
// db.ErrTaskNotFound) instead of grepping the error string — string-matching
// silently breaks on any future rename of the wrapped format. orch and the
// HTTP API both route this to a 404.
var ErrTaskNotFound = errors.New("task not found")

// taskColumns is the canonical column list for task queries. Order MUST
// match scanTask's Scan call and the INSERT/UPDATE statements below.
const taskColumns = `id, name, status, project, branch, prompt, backend, worktree, agent_pid, session_id, sandboxed, archived, pinned, base_branch, depends_on, result, plan_slug, created_at, started_at, ended_at`

// scanner is implemented by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanTask reads a task from a row using the canonical column order.
func scanTask(row scanner) (*model.Task, error) {
	t := &model.Task{}
	var status, createdAt, startedAt, endedAt, dependsOn string
	var sandboxed, archived, pinned int
	if err := row.Scan(&t.ID, &t.Name, &status, &t.Project, &t.Branch, &t.Prompt, &t.Backend, &t.Worktree, &t.AgentPID, &t.SessionID, &sandboxed, &archived, &pinned, &t.BaseBranch, &dependsOn, &t.Result, &t.PlanSlug, &createdAt, &startedAt, &endedAt); err != nil {
		return nil, err
	}
	t.Status, _ = model.ParseStatus(status)
	t.Sandboxed = sandboxed != 0
	t.Archived = archived != 0
	t.Pinned = pinned != 0
	// depends_on is stored as a JSON array string; empty means no deps.
	// A malformed value would prevent the task from loading, so on parse
	// error we leave DependsOn empty rather than failing the scan — the
	// worst case is a once-blocked task starts immediately on next tick.
	if dependsOn != "" {
		_ = json.Unmarshal([]byte(dependsOn), &t.DependsOn) //nolint:errcheck
	}
	t.CreatedAt = parseTime(createdAt)
	t.StartedAt = parseTime(startedAt)
	t.EndedAt = parseTime(endedAt)
	return t, nil
}

// encodeDependsOn returns the JSON-array representation stored in the
// depends_on column. Empty / nil slice maps to empty string so the column
// default lines up with the in-memory zero value.
func encodeDependsOn(deps []string) string {
	if len(deps) == 0 {
		return ""
	}
	b, err := json.Marshal(deps)
	if err != nil {
		return ""
	}
	return string(b)
}

func (d *DB) Tasks() ([]*model.Task, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT ` + taskColumns + ` FROM tasks ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*model.Task
	for rows.Next() {
		if t, err := scanTask(rows); err == nil {
			tasks = append(tasks, t)
		}
	}
	return tasks, nil
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

	sandboxedInt := 0
	if t.Sandboxed {
		sandboxedInt = 1
	}
	archivedInt := 0
	if t.Archived {
		archivedInt = 1
	}
	pinnedInt := 0
	if t.Pinned {
		pinnedInt = 1
	}
	_, err := d.conn.Exec(`INSERT INTO tasks (id, name, status, project, branch, prompt, backend, worktree, agent_pid, session_id, sandboxed, archived, pinned, base_branch, depends_on, result, plan_slug, created_at, started_at, ended_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Status.String(), t.Project, t.Branch, t.Prompt, t.Backend, t.Worktree, t.AgentPID, t.SessionID, sandboxedInt, archivedInt, pinnedInt,
		t.BaseBranch, encodeDependsOn(t.DependsOn), t.Result, t.PlanSlug,
		formatTime(t.CreatedAt), formatTime(t.StartedAt), formatTime(t.EndedAt))
	return err
}

func (d *DB) Update(t *model.Task) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	sandboxedInt := 0
	if t.Sandboxed {
		sandboxedInt = 1
	}
	archivedInt := 0
	if t.Archived {
		archivedInt = 1
	}
	pinnedInt := 0
	if t.Pinned {
		pinnedInt = 1
	}
	res, err := d.conn.Exec(`UPDATE tasks SET name=?, status=?, project=?, branch=?, prompt=?, backend=?, worktree=?, agent_pid=?, session_id=?, sandboxed=?, archived=?, pinned=?, base_branch=?, depends_on=?, result=?, plan_slug=?, created_at=?, started_at=?, ended_at=? WHERE id=?`,
		t.Name, t.Status.String(), t.Project, t.Branch, t.Prompt, t.Backend, t.Worktree, t.AgentPID, t.SessionID, sandboxedInt, archivedInt, pinnedInt,
		t.BaseBranch, encodeDependsOn(t.DependsOn), t.Result, t.PlanSlug,
		formatTime(t.CreatedAt), formatTime(t.StartedAt), formatTime(t.EndedAt), t.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, t.ID)
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
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return nil
}

// RenameIfName updates name only if the row's current name still equals
// expected — a compare-and-swap that closes the TOCTOU window between a
// caller's read and write. Returns false (no error) if the row exists but
// the name has changed since expected was observed; returns ErrTaskNotFound
// if the row is gone. Used by the post-creation Haiku rename so a manual
// rename racing the LLM call is preserved.
func (d *DB) RenameIfName(id, expected, newName string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`UPDATE tasks SET name=? WHERE id=? AND name=?`, newName, id, expected)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return true, nil
	}
	// Disambiguate "row gone" from "row exists but name differs".
	var exists int
	if err := d.conn.QueryRow(`SELECT 1 FROM tasks WHERE id=?`, id).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return false, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
		}
		return false, err
	}
	return false, nil
}

// SetResult writes the opaque JSON result blob for a task. The daemon does
// not parse the contents — it's the agent/orchestrator contract. Returns an
// error if the row is missing. Idempotent: last write wins.
func (d *DB) SetResult(id, result string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`UPDATE tasks SET result=? WHERE id=?`, result, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return nil
}

// SetPlanSlug writes only the orchestrator grouping label. Same partial-update
// pattern as SetResult — bypasses the full-row Update so a concurrent agent
// status flip (in_progress → in_review on session exit) is not clobbered by
// a stale read-then-write round trip. Empty string clears the slug.
func (d *DB) SetPlanSlug(id, slug string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`UPDATE tasks SET plan_slug=? WHERE id=?`, slug, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return nil
}

// SetDependsOn writes only the depends_on column. Used by orch.Link / Unlink
// so the read-modify-write cycle does not need to take the full-row Update
// path. The encoded JSON empty array is stored as the empty string by
// encodeDependsOn so the migrated/fresh-DB defaults line up.
func (d *DB) SetDependsOn(id string, deps []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.conn.Exec(`UPDATE tasks SET depends_on=? WHERE id=?`, encodeDependsOn(deps), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return nil
}

// SetArchived writes only the archived column (plus the pinned-clearing leg
// of the mutual-exclusivity invariant when archived=true). Used by
// orch.HaltDownstream so archiving a pending or in-review descendant cannot
// clobber a concurrent status flip from the depswatcher (e.g. pending →
// in_progress between the halt loop's Get and Update).
//
// pinned must be cleared when archived flips true because the rest of the
// codebase relies on the invariant "at most one of {Pinned, Archived} is
// true" — see model.Task.SetArchived. A halt cascade reaching a pinned task
// MUST yield a clean archived row, not a (pinned=1, archived=1) Frankenstein
// the task list would render in BOTH the Pinned and Archive sections.
//
// Unarchiving (archived=false) leaves pinned alone — pinning state survives
// a round trip through the archive section.
func (d *DB) SetArchived(id string, archived bool) error {
	// Both statements (tasks UPDATE + on-archive task_messages DELETE) run in
	// one SQLite transaction so a crash between them can't leave an archived
	// row with a queued inbox forever counting against the unread cap.
	return d.WithTx(func(tx *sql.Tx) error {
		var (
			res sql.Result
			err error
		)
		if archived {
			res, err = tx.Exec(`UPDATE tasks SET archived=1, pinned=0 WHERE id=?`, id)
		} else {
			res, err = tx.Exec(`UPDATE tasks SET archived=0 WHERE id=?`, id)
		}
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
		}
		// On archive, drop queued messages so a stale recipient doesn't sit on
		// the unread cap blocking other senders. Unarchive leaves messages
		// alone (there are none — the cleanup ran when the task was archived).
		if archived {
			if _, err := tx.Exec(`DELETE FROM task_messages WHERE from_task_id=? OR to_task_id=?`, id, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// FindByNameProject returns the first non-archived task matching (name,
// project), or (nil, nil) if no match. Used by task_create idempotency to
// detect duplicate orchestration sub-tasks before spawning a second worktree.
// Archived tasks are excluded so a stale stack does not block reuse of the
// same slug.
func (d *DB) FindByNameProject(name, project string) (*model.Task, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	row := d.conn.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE name=? AND project=? AND archived=0 LIMIT 1`, name, project)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (d *DB) Delete(id string) error {
	// Run both DELETEs in one transaction so a crash between them can't
	// leave orphan task_messages rows pointing at a deleted from/to ID
	// (such rows could never be acked, and would still count against the
	// surviving peer's unread cap).
	return d.WithTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM tasks WHERE id=?`, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
		}
		// SQLite doesn't enforce an FK here because tasks are soft-archivable
		// (we'd need a delete trigger that the archived rows wouldn't fire);
		// this is the app-level equivalent.
		if _, err := tx.Exec(`DELETE FROM task_messages WHERE from_task_id=? OR to_task_id=?`, id, id); err != nil {
			return err
		}
		return nil
	})
}

func (d *DB) Get(id string) (*model.Task, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	row := d.conn.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id=?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (d *DB) PruneCompleted() ([]*model.Task, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

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
