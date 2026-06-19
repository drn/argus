package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Plan-DAG substrate (add-hera-plan-substrate). A *planned node* is a
// worker-kind hera role that has NEVER held a binding — no agent, no worktree,
// no inbox, costing one DB row. Blocking edges live in the hera_blocks table.
// The gater (internal/heragater) materializes a planned node into a live worker
// (via agent.CreateAndStart against the pre-created role) once ALL its blockers
// reach hera ROLE-status `done`. This file owns ONLY the store layer.

// Plan-DAG sentinel errors. Prefixed like the rest of the hera store.
var (
	// ErrHeraBlockCycle is returned by AddHeraBlock when the edge would close a
	// cycle in the blocking graph (DFS reachability check inside the insert tx).
	ErrHeraBlockCycle = errors.New("hera: blocking edge would create a cycle")
	// ErrHeraBlockCrossOrchestrator is returned when the two endpoints of a
	// blocking edge belong to different orchestrators (v1 is single-orchestrator).
	ErrHeraBlockCrossOrchestrator = errors.New("hera: blocking edge endpoints are in different orchestrators")
	// ErrHeraBlockSelf is returned when an edge points a role at itself.
	ErrHeraBlockSelf = errors.New("hera: a role cannot block itself")
)

// HeraBlock is one directed blocking edge: BlockedRoleID waits on BlockerRoleID.
type HeraBlock struct {
	BlockedRoleID int64
	BlockerRoleID int64
	CreatedAt     time.Time
}

// CreateHeraPlannedRole inserts a worker-kind role with NO binding — a planned
// node. Unlike CreateHeraRoleWithBinding (born-bound spawn), no binding row is
// written, so the role has no agent, worktree, or inbox until the gater
// materializes it. The name is inserted as supplied (callers uniquify via
// UniqueHeraRoleName first, like born-bound spawn); the partial unique index on
// (orchestrator_id, name) is the race backstop. Kind is forced to worker — a
// planned node is always a worker (a coordinator is born-bound at orchestrator
// creation). The supplied short-id-prefixed name, project, and delivery prompt
// are persisted on the role for use at materialization.
func (d *DB) CreateHeraPlannedRole(in CreateHeraRoleInput) (*HeraRole, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	in.Kind = HeraKindWorker
	return insertHeraRole(d.conn, in, formatTime(time.Now()))
}

// AddHeraBlock inserts a blocking edge (blockedRoleID waits on blockerRoleID).
// Inside ONE transaction it: (1) loads both roles and rejects a cross-orchestrator
// pair (ErrHeraBlockCrossOrchestrator) or a self-edge (ErrHeraBlockSelf); (2) runs
// a DFS reachability check and rejects any edge that would close a cycle
// (ErrHeraBlockCycle); (3) inserts the edge. The cycle check runs in the SAME
// transaction as the insert so a concurrent edge insert cannot slip a cycle past
// the check. A duplicate edge (same pair) is idempotent — INSERT OR IGNORE.
func (d *DB) AddHeraBlock(blockedRoleID, blockerRoleID int64) error {
	if blockedRoleID == blockerRoleID {
		return ErrHeraBlockSelf
	}
	return d.WithTx(func(tx *sql.Tx) error {
		blockedOrch, err := blockOrchOf(tx, blockedRoleID)
		if err != nil {
			return err
		}
		blockerOrch, err := blockOrchOf(tx, blockerRoleID)
		if err != nil {
			return err
		}
		if blockedOrch != blockerOrch {
			return ErrHeraBlockCrossOrchestrator
		}
		// Cycle check: an edge blocked->blocker closes a cycle iff blocker is
		// already (transitively) blocked by blocked. Walk the existing blocking
		// graph FROM blocker following blocked->blocker edges; if we reach
		// blocked, the new edge would create a cycle.
		reaches, err := blockReaches(tx, blockerRoleID, blockedRoleID)
		if err != nil {
			return err
		}
		if reaches {
			return ErrHeraBlockCycle
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO hera_blocks (blocked_role_id, blocker_role_id, created_at) VALUES (?, ?, ?)`,
			blockedRoleID, blockerRoleID, formatTime(time.Now())); err != nil {
			return fmt.Errorf("insert hera block: %w", err)
		}
		return nil
	})
}

// blockOrchOf returns the orchestrator id of a role within the insert tx.
func blockOrchOf(tx *sql.Tx, roleID int64) (int64, error) {
	var orchID int64
	err := tx.QueryRow(`SELECT orchestrator_id FROM hera_roles WHERE id=?`, roleID).Scan(&orchID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("hera block: role %d: %w", roleID, ErrHeraNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("hera block: orchestrator of role %d: %w", roleID, err)
	}
	return orchID, nil
}

// blockReaches reports whether `target` is reachable from `start` by following
// blocked->blocker edges (i.e. start is transitively blocked by target). Used by
// AddHeraBlock to detect cycles before inserting blocked->blocker. Iterative DFS
// with a visited set so a malformed pre-existing graph cannot loop forever.
func blockReaches(tx *sql.Tx, start, target int64) (bool, error) {
	visited := map[int64]struct{}{}
	stack := []int64{start}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == target {
			return true, nil
		}
		if _, ok := visited[cur]; ok {
			continue
		}
		visited[cur] = struct{}{}
		rows, err := tx.Query(`SELECT blocker_role_id FROM hera_blocks WHERE blocked_role_id=?`, cur)
		if err != nil {
			return false, fmt.Errorf("hera block reaches: %w", err)
		}
		var blockers []int64
		for rows.Next() {
			var b int64
			if scanErr := rows.Scan(&b); scanErr != nil {
				_ = rows.Close()
				return false, fmt.Errorf("hera block reaches scan: %w", scanErr)
			}
			blockers = append(blockers, b)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return false, fmt.Errorf("hera block reaches rows: %w", rowsErr)
		}
		_ = rows.Close()
		stack = append(stack, blockers...)
	}
	return false, nil
}

// HeraBlockersOf returns the blocker role ids of blockedRoleID. A missing
// blocker row (a blocker role deleted mid-plan) is pruned by the FK cascade, so
// this only ever returns extant blocker roles — the gater treats the absence of
// a blocker as "no longer blocked by it" (missing-blocker prune).
func (d *DB) HeraBlockersOf(blockedRoleID int64) ([]int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		`SELECT blocker_role_id FROM hera_blocks WHERE blocked_role_id=? ORDER BY blocker_role_id ASC`,
		blockedRoleID)
	if err != nil {
		return nil, fmt.Errorf("hera blockers of: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var b int64
		if err := rows.Scan(&b); err != nil {
			return nil, fmt.Errorf("hera blockers of scan: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// HeraPlannedNode is a worker role with no binding ever (a planned node), joined
// with its orchestrator id for the gater's per-orchestrator branch resolution.
type HeraPlannedNode struct {
	Role *HeraRole
}

// ListHeraPlannedNodes returns every active worker-kind role that has NEVER held
// a binding — the planned nodes the gater evaluates. A role that was once
// materialized (it has a binding row, live or ended) is NOT a planned node, even
// if its binding has since ended; the gater never re-materializes. Archived
// roles are excluded.
func (d *DB) ListHeraPlannedNodes() ([]*HeraRole, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		`SELECT r.id, r.orchestrator_id, r.name, r.kind, r.argus_project, r.prompt,
		        r.created_at, r.archived_at, r.pinned_at
		 FROM hera_roles r
		 WHERE r.kind=? AND r.archived_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM hera_bindings b WHERE b.role_id = r.id)
		 ORDER BY r.id ASC`,
		string(HeraKindWorker))
	if err != nil {
		return nil, fmt.Errorf("list hera planned nodes: %w", err)
	}
	defer rows.Close()
	var out []*HeraRole
	for rows.Next() {
		r, err := scanHeraRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HeraRoleHasBinding reports whether a role has EVER held a binding (live or
// ended). The gater's idempotency guard: a node that already has a binding has
// been (or is being) materialized and must never be materialized again.
func (d *DB) HeraRoleHasBinding(roleID int64) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var n int
	err := d.conn.QueryRow(`SELECT COUNT(*) FROM hera_bindings WHERE role_id=?`, roleID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("hera role has binding: %w", err)
	}
	return n > 0, nil
}
