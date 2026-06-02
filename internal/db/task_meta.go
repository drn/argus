package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// ErrMetaInvalidKey rejects writes with an empty task_id, namespace, or key.
// Returned by SetMeta and SetMetaBatch; the HTTP handler maps it to a 400.
var ErrMetaInvalidKey = errors.New("task_meta: task_id, namespace, and key must all be non-empty")

// TaskMetaEntry is one (namespace, key, value, updated_at) row of task
// sidecar metadata. The sidecar table lets plugins (and ad-hoc CLI use)
// annotate tasks without piling new columns onto the tasks schema. See the
// PR 3 substrate plan for the contract.
type TaskMetaEntry struct {
	Namespace string
	Key       string
	Value     string
	UpdatedAt time.Time
}

// SetMeta upserts a single (task_id, namespace, key) row to value. updated_at
// is stamped to time.Now() on every write — both insert and overwrite — so
// callers can detect liveness without re-issuing a read.
func (d *DB) SetMeta(taskID, namespace, key, value string) error {
	if taskID == "" || namespace == "" || key == "" {
		return ErrMetaInvalidKey
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.conn.Exec(
		`INSERT INTO task_meta (task_id, namespace, key, value, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(task_id, namespace, key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		taskID, namespace, key, value, formatTime(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("set meta: %w", err)
	}
	return nil
}

// SetMetaBatch upserts every (key,value) under (taskID, namespace) in one
// transaction. Validation runs before any write — an empty taskID / namespace
// / any key aborts before the first INSERT, so a partially-applied batch is
// not possible. An empty map (or nil) is a no-op.
func (d *DB) SetMetaBatch(taskID, namespace string, entries map[string]string) error {
	if len(entries) == 0 {
		return nil
	}
	if taskID == "" || namespace == "" {
		return ErrMetaInvalidKey
	}
	// Validate every key up front so the WithTx body never partial-writes.
	for k := range entries {
		if k == "" {
			return ErrMetaInvalidKey
		}
	}
	// Stable iteration order keeps the transaction's write log deterministic
	// (helpful when chasing replay-vs-reality drift).
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	now := formatTime(time.Now())
	return d.WithTx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(
			`INSERT INTO task_meta (task_id, namespace, key, value, updated_at) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(task_id, namespace, key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		)
		if err != nil {
			return fmt.Errorf("prepare batch: %w", err)
		}
		defer stmt.Close() //nolint:errcheck
		for _, k := range keys {
			if _, err := stmt.Exec(taskID, namespace, k, entries[k], now); err != nil {
				return fmt.Errorf("set meta batch: %w", err)
			}
		}
		return nil
	})
}

// ListMeta returns every metadata row for taskID. When namespace is non-empty
// the result is scoped to that namespace only; otherwise rows from every
// namespace are returned. Results are ordered by (namespace, key) so callers
// can rely on stable iteration order.
func (d *DB) ListMeta(taskID, namespace string) ([]TaskMetaEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var (
		rows *sql.Rows
		err  error
	)
	if namespace != "" {
		rows, err = d.conn.Query(
			`SELECT namespace, key, value, updated_at FROM task_meta WHERE task_id=? AND namespace=? ORDER BY namespace ASC, key ASC`,
			taskID, namespace,
		)
	} else {
		rows, err = d.conn.Query(
			`SELECT namespace, key, value, updated_at FROM task_meta WHERE task_id=? ORDER BY namespace ASC, key ASC`,
			taskID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list meta: %w", err)
	}
	defer rows.Close()
	var out []TaskMetaEntry
	for rows.Next() {
		var e TaskMetaEntry
		var updated string
		if err := rows.Scan(&e.Namespace, &e.Key, &e.Value, &updated); err != nil {
			return nil, fmt.Errorf("scan meta: %w", err)
		}
		e.UpdatedAt = parseTime(updated)
		out = append(out, e)
	}
	return out, nil
}

// DeleteMetaForTask removes every metadata row for taskID across every
// namespace. Called by Delete and SetArchived(archived=true) so a destroyed
// or archived task doesn't accumulate orphan sidecar rows. Returns the row
// count for logging.
func (d *DB) DeleteMetaForTask(taskID string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`DELETE FROM task_meta WHERE task_id=?`, taskID)
	if err != nil {
		return 0, fmt.Errorf("delete meta for task: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
