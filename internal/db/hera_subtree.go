package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Hera subtree TLDR roll-up + per-role tree cursors (Milestone 5 of merging
// Hera into Argus natively — see context/plans/merge-hera-into-argus.md). Ports
// Hera's internal/db/subtree.go + tree_cursors.go onto Argus's mutex-guarded
// *DB style. The tree (TLDR roll-up of descendants) and the DAG (depends_on
// edges) are ORTHOGONAL — this file builds only the tree.

// HeraMessageTLDR is the TLDR-only projection returned by HeraTreeUpdatesSince:
// id + sender/recipient role ids + tldr subject + timestamp, NO body. Token
// efficiency is the whole point (bodies require a follow-up HeraMessagesByIDs /
// hera_get_messages). The MCP layer resolves role/orchestrator names from the
// ids before returning to the agent.
type HeraMessageTLDR struct {
	ID         int64
	FromRoleID int64
	ToRoleID   int64
	Tldr       string
	SentAt     time.Time
}

// HeraTreeUpdatesLimit caps a single subtree roll-up read. With nextCursor set
// to the max id returned, repeated calls page forward through a busy subtree
// rather than returning an unbounded result. Matches Hera's LIMIT 200.
const HeraTreeUpdatesLimit = 200

// SubtreeOrchIDs returns every orchestrator id in rootOrchID's subtree,
// including the root itself, deduped and in stable BFS-discovery order.
//
// Nesting semantics (ported from Hera's subtree.go): a sub-orchestrator hangs
// off its parent because its COORDINATOR role's argus task is ALSO bound under
// the parent orchestrator (the multi-binding shape — one task is a
// worker/coordinator in the parent AND the coordinator of the child). The bridge
// keys off each role's LATEST binding regardless of liveness (the `latest` CTE =
// max binding id per role), NOT live bindings alone, so a child whose
// coordinator session has finished still nests under its parent. The parent link
// is honoured unless its latest binding was an operator TEARDOWN
// (end_reason reparented / user_deleted), which marks a stale link that must not
// nest; every other end reason leaves the structural link intact.
//
// Archived orchestrators are EXCLUDED from the subtree (WHERE child_orch
// archived_at IS NULL) and archived coordinator roles do not bridge — mirroring
// Hera. The root is ALWAYS included regardless of its archived state (the caller
// resolved it as their own live orchestrator).
//
// Cycle guard: a visited-orchestrator set is consulted before enqueueing, so a
// task that (theoretically) binds in a way that forms a loop can never re-enqueue
// an orchestrator. Over a finite orchestrator set with no re-enqueue, the BFS
// always terminates — no depth limit is needed.
func (d *DB) SubtreeOrchIDs(rootOrchID int64) ([]int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraSubtreeOrchIDs(rootOrchID)
}

// heraSubtreeOrchIDs is the unlocked BFS body; callers hold d.mu.
func (d *DB) heraSubtreeOrchIDs(rootOrchID int64) ([]int64, error) {
	seen := map[int64]struct{}{rootOrchID: {}}
	frontier := []int64{rootOrchID}
	result := []int64{rootOrchID}

	for len(frontier) > 0 {
		placeholders := make([]string, len(frontier))
		args := make([]any, len(frontier))
		for i, id := range frontier {
			placeholders[i] = "?"
			args[i] = id
		}
		//nolint:gosec // G201: placeholders are a fixed list of `?` literals; ids are bound params.
		query := fmt.Sprintf(`
WITH latest AS (
    SELECT b.id, b.role_id, b.orchestrator_id, b.argus_task_id, b.ended_at, b.end_reason
    FROM hera_bindings b
    JOIN (SELECT role_id, MAX(id) AS max_id FROM hera_bindings GROUP BY role_id) m
        ON m.role_id = b.role_id AND m.max_id = b.id
)
SELECT DISTINCT child_orch.id
FROM hera_orchestrators child_orch
JOIN hera_roles child_coord
    ON child_coord.orchestrator_id = child_orch.id
    AND child_coord.kind = 'coordinator'
    AND child_coord.archived_at IS NULL
JOIN latest child_coord_bnd
    ON child_coord_bnd.role_id = child_coord.id
JOIN latest parent_bnd
    ON parent_bnd.argus_task_id = child_coord_bnd.argus_task_id
    AND parent_bnd.orchestrator_id IN (%s)
    AND (parent_bnd.ended_at IS NULL OR COALESCE(parent_bnd.end_reason, '') NOT IN ('reparented', 'user_deleted'))
WHERE child_orch.archived_at IS NULL`, strings.Join(placeholders, ","))

		rows, err := d.conn.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("hera subtree BFS: %w", err)
		}
		var next []int64
		for rows.Next() {
			var childID int64
			if scanErr := rows.Scan(&childID); scanErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("hera subtree BFS scan: %w", scanErr)
			}
			if _, ok := seen[childID]; !ok {
				seen[childID] = struct{}{}
				result = append(result, childID)
				next = append(next, childID)
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("hera subtree BFS rows: %w", rowsErr)
		}
		_ = rows.Close()
		frontier = next
	}
	return result, nil
}

// HeraTreeUpdatesSince returns the TLDR-only roll-up of every message whose
// sender OR recipient role lives in rootOrchID's subtree, with id > since,
// ordered by id ASC and capped at HeraTreeUpdatesLimit. nextCursor is the max
// id returned (since when none), so the caller can page forward by re-passing
// it. This is the in-process equivalent of Hera's SSE-cursor scan — a direct DB
// read; the cursor is advisory and stored per-role, so at-least-once delivery
// is not needed (it is a query, not an event consumer).
func (d *DB) HeraTreeUpdatesSince(rootOrchID, since int64) ([]HeraMessageTLDR, int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	orchIDs, err := d.heraSubtreeOrchIDs(rootOrchID)
	if err != nil {
		return nil, since, err
	}
	if len(orchIDs) == 0 {
		return nil, since, nil
	}

	placeholders := make([]string, len(orchIDs))
	args := make([]any, 0, 1+2*len(orchIDs))
	args = append(args, since)
	for i, id := range orchIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	for _, id := range orchIDs {
		args = append(args, id)
	}
	inClause := strings.Join(placeholders, ",")

	//nolint:gosec // G201: inClause is a fixed list of `?` literals; ids are bound params.
	query := fmt.Sprintf(`
SELECT m.id, m.from_role_id, m.to_role_id, m.tldr, m.sent_at
FROM hera_messages m
JOIN hera_roles fr ON fr.id = m.from_role_id
JOIN hera_roles tr ON tr.id = m.to_role_id
WHERE m.id > ?
  AND (fr.orchestrator_id IN (%s) OR tr.orchestrator_id IN (%s))
ORDER BY m.id ASC
LIMIT %d`, inClause, inClause, HeraTreeUpdatesLimit)

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, since, fmt.Errorf("hera tree updates query: %w", err)
	}
	defer rows.Close()

	var out []HeraMessageTLDR
	for rows.Next() {
		var m HeraMessageTLDR
		var sentAt string
		if scanErr := rows.Scan(&m.ID, &m.FromRoleID, &m.ToRoleID, &m.Tldr, &sentAt); scanErr != nil {
			return nil, since, fmt.Errorf("hera tree updates scan: %w", scanErr)
		}
		m.SentAt = parseTime(sentAt)
		out = append(out, m)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, since, fmt.Errorf("hera tree updates rows: %w", rowsErr)
	}

	nextCursor := since
	if len(out) > 0 {
		nextCursor = out[len(out)-1].ID
	}
	return out, nextCursor, nil
}

// --- Per-role tree cursors ---

// GetHeraTreeCursor returns the stored tree-scan cursor for roleID, or 0 when
// none exists. The cursor is a disposable read bookmark (last-seen message id).
func (d *DB) GetHeraTreeCursor(roleID int64) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var cursor int64
	err := d.conn.QueryRow(`SELECT cursor FROM tree_read_cursors WHERE role_id=?`, roleID).Scan(&cursor)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("get hera tree cursor: %w", err)
	}
	return cursor, nil
}

// SetHeraTreeCursor upserts the tree-scan cursor for roleID. Re-seeded on every
// read by the auto-advance path in hera_tree_updates.
func (d *DB) SetHeraTreeCursor(roleID, cursor int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.conn.Exec(
		`INSERT INTO tree_read_cursors (role_id, cursor, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(role_id) DO UPDATE SET cursor=excluded.cursor, updated_at=excluded.updated_at`,
		roleID, cursor, formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("set hera tree cursor: %w", err)
	}
	return nil
}
