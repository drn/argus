package db

import (
	"database/sql"
	"fmt"
	"time"
)

// CostSweepBinding is one hera binding the Codex cost sweeper may accrue to
// (add-codex-cost-accrual), plus how many live bindings its task currently
// holds: a task live under 2+ orchestrators has no single binding to charge,
// mirroring PUT /api/tasks/{id}/hera/tokens's ErrHeraAmbiguous skip.
type CostSweepBinding struct {
	HeraBinding
	TaskLiveBindings int
}

// ListCostSweepBindings returns every binding that is live or ended after
// endedSince — the grace window that lets a sweep read the final calls made
// just before a binding ended.
func (d *DB) ListCostSweepBindings(endedSince time.Time) ([]CostSweepBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// ended_at is RFC3339Nano text with a zone offset, so the SQL comparison
	// is only a coarse prefilter (a day of slack absorbs offset changes); the
	// exact cut happens on the parsed time below.
	rows, err := d.conn.Query(
		`SELECT b.id, b.role_id, b.orchestrator_id, b.argus_task_id, b.worktree_path, b.started_at, b.ended_at, b.end_reason,
		        (SELECT COUNT(*) FROM hera_bindings x WHERE x.argus_task_id = b.argus_task_id AND x.ended_at IS NULL)
		 FROM hera_bindings b
		 WHERE b.ended_at IS NULL OR b.ended_at > ?
		 ORDER BY b.id`, formatTime(endedSince.Add(-24*time.Hour)))
	if err != nil {
		return nil, fmt.Errorf("list cost sweep bindings: %w", err)
	}
	defer rows.Close()
	var out []CostSweepBinding
	for rows.Next() {
		var c CostSweepBinding
		var startedAt string
		var endedAt, endReason sql.NullString
		if err := rows.Scan(&c.ID, &c.RoleID, &c.OrchestratorID, &c.ArgusTaskID, &c.WorktreePath,
			&startedAt, &endedAt, &endReason, &c.TaskLiveBindings); err != nil {
			return nil, fmt.Errorf("scan cost sweep binding: %w", err)
		}
		c.StartedAt = parseTime(startedAt)
		c.EndedAt = nullTimePtr(endedAt)
		c.EndReason = endReason.String
		if c.EndedAt != nil && !c.EndedAt.After(endedSince) {
			continue
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CodexCostCursor is how far the sweeper has consumed one rollout file for
// one binding: the byte offset, the running total_tokens of the last consumed
// token_count event (Codex re-emits identical events; -1 means none yet), and
// the most recent turn model seen (a call is priced under the model of the
// turn_context preceding it, which may sit before Offset).
type CodexCostCursor struct {
	RolloutPath string
	Offset      int64
	LastTotal   int64
	Model       string
}

// CodexCostCursors returns bindingID's cursors keyed by rollout path. A
// rollout with no entry has never been read for this binding.
func (d *DB) CodexCostCursors(bindingID int64) (map[string]CodexCostCursor, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		`SELECT rollout_path, byte_offset, last_total, model FROM hera_codex_cost_cursor WHERE binding_id=?`, bindingID)
	if err != nil {
		return nil, fmt.Errorf("codex cost cursors: %w", err)
	}
	defer rows.Close()
	out := make(map[string]CodexCostCursor)
	for rows.Next() {
		var c CodexCostCursor
		if err := rows.Scan(&c.RolloutPath, &c.Offset, &c.LastTotal, &c.Model); err != nil {
			return nil, fmt.Errorf("scan codex cost cursor: %w", err)
		}
		out[c.RolloutPath] = c
	}
	return out, rows.Err()
}

// AccrueCodexCalls ADDS delta (raw tokens and CostUSDAccrued, already priced
// by the caller at the current rate table) to bindingID's cost columns and
// upserts cur, in one transaction: either both land or neither does, so a
// crash between sweeps never re-prices or skips a call. Returns
// ErrHeraNotFound if the binding doesn't exist.
func (d *DB) AccrueCodexCalls(bindingID int64, delta HeraBindingCostTotals, cur CodexCostCursor) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("accrue codex calls: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is harmless
	res, err := tx.Exec(
		`UPDATE hera_bindings SET
		   tokens_input = tokens_input + ?,
		   tokens_cache_write_1h = tokens_cache_write_1h + ?,
		   tokens_cache_write_5m = tokens_cache_write_5m + ?,
		   tokens_cache_read = tokens_cache_read + ?,
		   tokens_output = tokens_output + ?,
		   cost_usd_accrued = cost_usd_accrued + ?
		 WHERE id=?`,
		delta.TokensInput, delta.TokensCacheWrite1h, delta.TokensCacheWrite5m, delta.TokensCacheRead,
		delta.TokensOutput, delta.CostUSDAccrued, bindingID)
	if err != nil {
		return fmt.Errorf("accrue codex calls: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("accrue codex calls: rows affected: %w", err)
	} else if n == 0 {
		return ErrHeraNotFound
	}
	if _, err := tx.Exec(
		`INSERT INTO hera_codex_cost_cursor(binding_id, rollout_path, byte_offset, last_total, model) VALUES(?,?,?,?,?)
		 ON CONFLICT(binding_id, rollout_path) DO UPDATE SET
		   byte_offset=excluded.byte_offset, last_total=excluded.last_total, model=excluded.model`,
		bindingID, cur.RolloutPath, cur.Offset, cur.LastTotal, cur.Model); err != nil {
		return fmt.Errorf("accrue codex calls: cursor: %w", err)
	}
	return tx.Commit()
}
