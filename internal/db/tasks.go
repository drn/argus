package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drn/argus/internal/events"
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
const taskColumns = `id, name, status, project, branch, prompt, backend, model, worktree, agent_pid, session_id, sandboxed, archived, pinned, base_branch, result, archetype, profile, created_at, started_at, ended_at`

// scanner is implemented by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanTask reads a task from a row using the canonical column order.
func scanTask(row scanner) (*model.Task, error) {
	t := &model.Task{}
	var status, createdAt, startedAt, endedAt string
	var sandboxed, archived, pinned int
	if err := row.Scan(&t.ID, &t.Name, &status, &t.Project, &t.Branch, &t.Prompt, &t.Backend, &t.Model, &t.Worktree, &t.AgentPID, &t.SessionID, &sandboxed, &archived, &pinned, &t.BaseBranch, &t.Result, &t.Archetype, &t.Profile, &createdAt, &startedAt, &endedAt); err != nil {
		return nil, err
	}
	t.Status, _ = model.ParseStatus(status)
	t.Sandboxed = sandboxed != 0
	t.Archived = archived != 0
	t.Pinned = pinned != 0
	t.CreatedAt = parseTime(createdAt)
	t.StartedAt = parseTime(startedAt)
	t.EndedAt = parseTime(endedAt)
	return t, nil
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
	if err := d.addLocked(t); err != nil {
		return err
	}
	// Emit AFTER releasing the mutex so the sink's downstream InsertEvent
	// (which re-acquires d.mu) cannot deadlock with our own write.
	events.Emit(model.EventTypeTaskCreated, t.ID, map[string]any{
		"name":    t.Name,
		"project": t.Project,
		"status":  t.Status.String(),
	})
	return nil
}

func (d *DB) addLocked(t *model.Task) error {
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
	_, err := d.conn.Exec(`INSERT INTO tasks (id, name, status, project, branch, prompt, backend, model, worktree, agent_pid, session_id, sandboxed, archived, pinned, base_branch, result, archetype, profile, created_at, started_at, ended_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Status.String(), t.Project, t.Branch, t.Prompt, t.Backend, t.Model, t.Worktree, t.AgentPID, t.SessionID, sandboxedInt, archivedInt, pinnedInt,
		t.BaseBranch, t.Result, t.Archetype, t.Profile,
		formatTime(t.CreatedAt), formatTime(t.StartedAt), formatTime(t.EndedAt))
	return err
}

func (d *DB) Update(t *model.Task) error {
	oldStatus, oldArchived, hadOld, err := d.updateLocked(t)
	if err != nil {
		return err
	}
	// Status-change events fire AFTER the lock is released to avoid deadlock
	// with the events sink (which re-acquires d.mu via InsertEvent). Compare
	// against the snapshot we captured inside updateLocked.
	if hadOld && oldStatus != t.Status {
		events.Emit(model.EventTypeTaskStatusChanged, t.ID, map[string]string{
			"from": oldStatus.String(),
			"to":   t.Status.String(),
		})
		if t.Status == model.StatusComplete {
			events.Emit(model.EventTypeTaskCompleted, t.ID, nil)
		}
	}
	// Archive transition: parity with SetArchived so hera and other consumers
	// of `task.archived` see the event regardless of which write path archived
	// the row. HTTP /api/tasks PUT and MCP task_archive both flow through
	// Update; without this they would silently flip the bit and leave any
	// downstream view of "live bindings" stale.
	if hadOld && !oldArchived && t.Archived {
		events.Emit(model.EventTypeTaskArchived, t.ID, nil)
	}
	return nil
}

func (d *DB) updateLocked(t *model.Task) (model.Status, bool, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Capture old status + archived state BEFORE the write so the post-unlock
	// emission sees the transition. A row that doesn't exist yet (hadOld=false)
	// is the "row missing" case that returns ErrTaskNotFound below; we
	// suppress the event for that.
	var (
		oldStatus    model.Status
		oldArchived  bool
		hadOld       bool
		oldStatusStr string
		oldArchInt   int
	)
	if err := d.conn.QueryRow(`SELECT status, archived FROM tasks WHERE id=?`, t.ID).Scan(&oldStatusStr, &oldArchInt); err == nil {
		oldStatus, _ = model.ParseStatus(oldStatusStr)
		oldArchived = oldArchInt != 0
		hadOld = true
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
	res, err := d.conn.Exec(`UPDATE tasks SET name=?, status=?, project=?, branch=?, prompt=?, backend=?, model=?, worktree=?, agent_pid=?, session_id=?, sandboxed=?, archived=?, pinned=?, base_branch=?, result=?, archetype=?, profile=?, created_at=?, started_at=?, ended_at=? WHERE id=?`,
		t.Name, t.Status.String(), t.Project, t.Branch, t.Prompt, t.Backend, t.Model, t.Worktree, t.AgentPID, t.SessionID, sandboxedInt, archivedInt, pinnedInt,
		t.BaseBranch, t.Result, t.Archetype, t.Profile,
		formatTime(t.CreatedAt), formatTime(t.StartedAt), formatTime(t.EndedAt), t.ID)
	if err != nil {
		return oldStatus, oldArchived, hadOld, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return oldStatus, oldArchived, hadOld, fmt.Errorf("%w: %s", ErrTaskNotFound, t.ID)
	}
	return oldStatus, oldArchived, hadOld, nil
}

// Rename updates only the name column for a task.
// Unlike Update, this does not overwrite other fields, avoiding races with
// concurrent status changes (e.g., agent exit while rename modal is open).
func (d *DB) Rename(id, name string) error {
	oldName, err := d.renameLocked(id, name)
	if err != nil {
		return err
	}
	events.Emit(model.EventTypeTaskRenamed, id, map[string]string{
		"from": oldName,
		"to":   name,
	})
	return nil
}

func (d *DB) renameLocked(id, name string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var oldName string
	_ = d.conn.QueryRow(`SELECT name FROM tasks WHERE id=?`, id).Scan(&oldName)

	res, err := d.conn.Exec(`UPDATE tasks SET name=? WHERE id=?`, name, id)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return "", fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return oldName, nil
}

// RenameIfName updates name only if the row's current name still equals
// expected — a compare-and-swap that closes the TOCTOU window between a
// caller's read and write. Returns false (no error) if the row exists but
// the name has changed since expected was observed; returns ErrTaskNotFound
// if the row is gone. Used by the post-creation Haiku rename so a manual
// rename racing the LLM call is preserved.
func (d *DB) RenameIfName(id, expected, newName string) (bool, error) {
	ok, err := d.renameIfNameLocked(id, expected, newName)
	if err != nil {
		return false, err
	}
	if ok {
		events.Emit(model.EventTypeTaskRenamed, id, map[string]string{
			"from": expected,
			"to":   newName,
		})
	}
	return ok, nil
}

func (d *DB) renameIfNameLocked(id, expected, newName string) (bool, error) {
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

// SetArchived writes only the archived column (plus the pinned-clearing leg
// of the mutual-exclusivity invariant when archived=true). Used by task
// archival so archiving a pending or in-review task cannot clobber a
// concurrent status flip (e.g. in_progress → in_review on session exit)
// between a caller's Get and Update.
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
	err := d.WithTx(func(tx *sql.Tx) error {
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
			if _, err := tx.Exec(`DELETE FROM task_meta WHERE task_id=?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && archived {
		events.Emit(model.EventTypeTaskArchived, id, nil)
	}
	return err
}

// SetPinned writes only the pinned column (plus the archived-clearing leg of
// the mutual-exclusivity invariant when pinned=true). Used by the task list's
// pin toggle so a stale in-memory snapshot can't clobber other columns —
// most importantly `name`, which a background autoname (Haiku) rename may have
// just rewritten in the DB. Mirrors model.Task.SetPinned: pinning clears
// archived; unpinning leaves archived alone.
//
// Unlike SetArchived, this emits no event — pin state has no event type in
// internal/model and no consumer (hera, idle watcher, SSE) listens for it.
// The old db.Update pin path emitted nothing pin-specific either, so this is
// deliberate parity, not an omission.
func (d *DB) SetPinned(id string, pinned bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var (
		res sql.Result
		err error
	)
	if pinned {
		res, err = d.conn.Exec(`UPDATE tasks SET pinned=1, archived=0 WHERE id=?`, id)
	} else {
		res, err = d.conn.Exec(`UPDATE tasks SET pinned=0 WHERE id=?`, id)
	}
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return nil
}

// SetStatus writes only the status column and its derived timestamps
// (started_at / ended_at), mirroring model.Task.SetStatus's timestamp rules.
// Used by the task list's manual status-cycle so a stale in-memory snapshot
// can't clobber other columns — most importantly `name`, which a background
// autoname (Haiku) rename may have just rewritten in the DB. Emits the same
// status-changed / completed events as the full-row Update path so downstream
// consumers (hera, idle watcher) see the transition regardless of write path.
func (d *DB) SetStatus(id string, s model.Status) error {
	oldStatus, err := d.setStatusLocked(id, s)
	if err != nil {
		return err
	}
	if oldStatus != s {
		events.Emit(model.EventTypeTaskStatusChanged, id, map[string]string{
			"from": oldStatus.String(),
			"to":   s.String(),
		})
		if s == model.StatusComplete {
			events.Emit(model.EventTypeTaskCompleted, id, nil)
		}
	}
	return nil
}

func (d *DB) setStatusLocked(id string, s model.Status) (model.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var oldStatusStr, startedStr, endedStr string
	if err := d.conn.QueryRow(`SELECT status, started_at, ended_at FROM tasks WHERE id=?`, id).Scan(&oldStatusStr, &startedStr, &endedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.StatusPending, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
		}
		return model.StatusPending, err
	}
	oldStatus, _ := model.ParseStatus(oldStatusStr)

	// No-op fast path: a same-status set must not touch the row. Without this,
	// model.SetStatus(Complete) on an already-complete task would re-stamp
	// ended_at to time.Now() and the UPDATE would persist the drift. The caller
	// suppresses the event when oldStatus == s, so returning here is equivalent
	// minus the wasted write.
	if oldStatus == s {
		return oldStatus, nil
	}

	// Reuse model.Task.SetStatus so the started_at/ended_at rules stay in one
	// place (set started_at on first InProgress, stamp ended_at on Complete).
	tmp := &model.Task{Status: oldStatus, StartedAt: parseTime(startedStr), EndedAt: parseTime(endedStr)}
	tmp.SetStatus(s)

	res, err := d.conn.Exec(`UPDATE tasks SET status=?, started_at=?, ended_at=? WHERE id=?`,
		tmp.Status.String(), formatTime(tmp.StartedAt), formatTime(tmp.EndedAt), id)
	if err != nil {
		return oldStatus, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return oldStatus, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return oldStatus, nil
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
		if _, err := tx.Exec(`DELETE FROM task_meta WHERE task_id=?`, id); err != nil {
			return err
		}
		// End every live hera binding for the deleted task. Deletion is the only
		// task lifecycle event that ends bindings — SetArchived deliberately
		// leaves them intact because archive is resumable. There is no FK from
		// hera_bindings.argus_task_id to tasks (argus_task_id is plain TEXT), so
		// this app-level cleanup is what severs the link. Bindings are ended
		// (ended_at stamped), not deleted, so the role's binding history survives.
		if _, err := tx.Exec(
			`UPDATE hera_bindings SET ended_at=?, end_reason=? WHERE argus_task_id=? AND ended_at IS NULL`,
			formatTime(time.Now()), heraEndReasonTaskDeleted, id,
		); err != nil {
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

// PruneCompleted deletes every task with status='complete' and returns them,
// except a task that still holds a live Hera role binding
// (hera_bindings.ended_at IS NULL) — hera_bindings has no foreign key to
// tasks, so deleting such a task's row would leave its Hera role pointing at
// a task that no longer exists instead of properly ending it. skippedHeraBound
// reports how many otherwise-eligible completed tasks were left in place for
// that reason.
func (d *DB) PruneCompleted() (pruned []*model.Task, skippedHeraBound int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	ids, err := d.taskIDsWhereLocked("status='complete'")
	if err != nil {
		return nil, 0, err
	}
	return d.pruneTasksLocked(ids)
}

// PruneTasks deletes exactly the given task IDs, re-verifying at delete time
// that each has no live Hera role binding (`hera_bindings.ended_at IS NULL`)
// — the same guard PruneCompleted has always applied, generalized to an
// explicit candidate set instead of an implicit "all status=complete" query.
// An ID that fails this guard is skipped (counted in skippedHeraBound), not
// deleted. This does NOT independently re-check status/archived — selecting
// the right set of IDs to pass in is the caller's responsibility (e.g. the
// merge-safety review popup's cached, already-reviewed candidate snapshot);
// the live-binding guard is the one invariant every caller shares and that
// is unsafe to skip (see openspec add-merge-safety-review design.md).
func (d *DB) PruneTasks(ids []string) (pruned []*model.Task, skippedHeraBound int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pruneTasksLocked(ids)
}

// StuckTaskCandidates returns every task matching the merge-safety review's
// stuck-task predicate (openspec add-merge-safety-review): archived,
// status='in_review', and holding no live Hera role binding
// (hera_bindings.ended_at IS NULL). This is the daemon-side candidate set for
// the global Cleanup action's on-demand classification — a task Hera still
// owns is excluded up front rather than merely relying on PruneTasks' own
// (re-verified) guard at delete time.
func (d *DB) StuckTaskCandidates() ([]*model.Task, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(`SELECT ` + taskColumns + ` FROM tasks t
		WHERE t.archived=1 AND t.status='in_review'
		AND NOT EXISTS (SELECT 1 FROM hera_bindings hb WHERE hb.argus_task_id=t.id AND hb.ended_at IS NULL)
		ORDER BY t.created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("query stuck task candidates: %w", err)
	}
	defer rows.Close()

	var tasks []*model.Task
	for rows.Next() {
		if t, err := scanTask(rows); err == nil {
			tasks = append(tasks, t)
		}
	}
	return tasks, rows.Err()
}

// taskIDsWhereLocked returns the IDs of every task matching the given raw
// SQL WHERE fragment. Callers control the fragment (never user input), so
// this is a plain string concatenation, matching this file's existing
// convention (e.g. the notLiveHeraBound fragment below).
func (d *DB) taskIDsWhereLocked(whereSQL string) ([]string, error) {
	//nolint:gosec // G202: whereSQL is a fixed literal supplied by the caller, never user input.
	q := `SELECT id FROM tasks WHERE ` + whereSQL
	rows, err := d.conn.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// pruneTasksLocked assumes d.mu is already held. It selects, from exactly
// the given ids, those with no live Hera binding, returns them, and deletes
// them in the same pass every existing caller relies on for atomicity.
func (d *DB) pruneTasksLocked(ids []string) (pruned []*model.Task, skippedHeraBound int, err error) {
	if len(ids) == 0 {
		return nil, 0, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	inClause := `id IN (` + strings.Join(placeholders, ",") + `)`
	const notLiveHeraBound = `id NOT IN (SELECT argus_task_id FROM hera_bindings WHERE ended_at IS NULL)`

	//nolint:gosec // G202: inClause is a fixed list of `?` literals and notLiveHeraBound is a constant; ids are bound parameters.
	selectQ := `SELECT ` + taskColumns + ` FROM tasks WHERE ` + inClause + ` AND ` + notLiveHeraBound
	rows, err := d.conn.Query(selectQ, args...)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		if t, err := scanTask(rows); err == nil {
			pruned = append(pruned, t)
		}
	}
	rows.Close()

	//nolint:gosec // G202: inClause is a fixed list of `?` literals and notLiveHeraBound is a constant; ids are bound parameters.
	countQ := `SELECT COUNT(*) FROM tasks WHERE ` + inClause + ` AND NOT (` + notLiveHeraBound + `)`
	if err := d.conn.QueryRow(countQ, args...).Scan(&skippedHeraBound); err != nil {
		return nil, 0, err
	}

	if len(pruned) == 0 {
		return nil, skippedHeraBound, nil
	}

	//nolint:gosec // G202: inClause is a fixed list of `?` literals and notLiveHeraBound is a constant; ids are bound parameters.
	deleteQ := `DELETE FROM tasks WHERE ` + inClause + ` AND ` + notLiveHeraBound
	if _, err := d.conn.Exec(deleteQ, args...); err != nil {
		return nil, 0, err
	}
	return pruned, skippedHeraBound, nil
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
