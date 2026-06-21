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
	// ErrHeraBlockCoordinator is returned when the BLOCKER endpoint of an edge is a
	// coordinator role (BUG-003). A coordinator's session is alive for the whole
	// orchestration and never reaches role-status `done`, so gating a node on it is
	// a permanently-unsatisfiable dependency — the dependent would stay planned
	// forever. Rejected at creation so a clear error beats a silently-stuck worker.
	ErrHeraBlockCoordinator = errors.New("hera: a coordinator role cannot be a blocker (it never reaches role-status done)")
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
	return d.WithTx(func(tx *sql.Tx) error {
		return insertHeraBlockTx(tx, blockedRoleID, blockerRoleID)
	})
}

// insertHeraBlockTx validates and inserts one blocking edge inside an existing
// transaction. It runs the self-edge, cross-orchestrator, and cycle guards
// against tx-scoped reads, so an edge inserted earlier in the SAME tx (by a
// prior call within a batch) is visible to the cycle check — an in-batch cycle
// is caught. Shared by AddHeraBlock (one edge per tx) and CreateHeraPlan (the
// whole batch in one tx). The caller owns d.mu via WithTx.
func insertHeraBlockTx(tx *sql.Tx, blockedRoleID, blockerRoleID int64) error {
	if blockedRoleID == blockerRoleID {
		return ErrHeraBlockSelf
	}
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
	// A coordinator never reaches role-status `done` (its session is alive for the
	// whole orchestration), so an edge gated on a coordinator-as-blocker is
	// permanently unsatisfiable — reject it (BUG-003). The check is blocker-side
	// only: a coordinator may legitimately be the blocked endpoint.
	blockerKind, err := blockKindOf(tx, blockerRoleID)
	if err != nil {
		return err
	}
	if blockerKind == HeraKindCoordinator {
		return ErrHeraBlockCoordinator
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
}

// HeraPlannedNodeSpec is one planned node in a whole-graph CreateHeraPlan call.
// It mirrors the fields CreateHeraPlannedRole consumes; Kind is forced to worker
// (D2). NodeKind is the plan-node discriminator (add-hera-subcoord-nodes):
// absent or HeraNodeKindWorker means leaf worker; HeraNodeKindSubCoord means the
// gater should materialize this node as a distinct coordinator agent.
type HeraPlannedNodeSpec struct {
	Name         string
	ArgusProject string
	Prompt       string
	NodeKind     HeraNodeKind
}

// HeraBlockSpec is one blocking edge in a whole-graph CreateHeraPlan call. Each
// endpoint is EITHER an index into the call's nodes slice (NodeIdx >= 0, resolved
// to the freshly-created role id inside the tx) OR a pre-existing role id
// (RoleID, used when NodeIdx < 0). This lets a plan edge reference both a node
// created in the same batch and a role that already exists in the orchestrator.
type HeraBlockSpec struct {
	BlockedNodeIdx int
	BlockedRoleID  int64
	BlockerNodeIdx int
	BlockerRoleID  int64
}

// CreateHeraPlan creates a whole plan graph — every node AND every edge — inside
// ONE transaction. On ANY error (a cycle, a cross-orchestrator edge, a self-edge,
// a missing endpoint, or an insert failure) the entire batch is rolled back, so
// no orphan planned nodes and no partial edges survive: either the whole graph is
// created or nothing is. Nodes are inserted first (so within-batch edges can
// reference them by the returned ids), then edges sequentially via the SAME
// tx-scoped insert+check used by AddHeraBlock — an edge added earlier in the
// batch is visible to a later edge's cycle check, so an in-batch cycle is caught.
// Returns the created roles in nodes order. Edges that reference roles outside the
// returned set (e.g. a pre-existing orchestrator role) must be resolved to ids by
// the caller. All endpoints must belong to orchID (enforced per edge by the
// cross-orchestrator guard). The caller uniquifies node names first.
func (d *DB) CreateHeraPlan(orchID int64, nodes []HeraPlannedNodeSpec, edges []HeraBlockSpec) ([]*HeraRole, error) {
	var created []*HeraRole
	err := d.WithTx(func(tx *sql.Tx) error {
		now := formatTime(time.Now())
		created = make([]*HeraRole, 0, len(nodes))
		for _, n := range nodes {
			role, err := insertHeraRole(tx, CreateHeraRoleInput{
				OrchestratorID: orchID,
				Name:           n.Name,
				Kind:           HeraKindWorker,
				NodeKind:       n.NodeKind,
				ArgusProject:   n.ArgusProject,
				Prompt:         n.Prompt,
			}, now)
			if err != nil {
				return err
			}
			created = append(created, role)
		}
		resolve := func(nodeIdx int, roleID int64) (int64, error) {
			if nodeIdx >= 0 {
				if nodeIdx >= len(created) {
					return 0, fmt.Errorf("hera plan: edge node index %d out of range: %w", nodeIdx, ErrHeraNotFound)
				}
				return created[nodeIdx].ID, nil
			}
			return roleID, nil
		}
		for _, e := range edges {
			blocked, err := resolve(e.BlockedNodeIdx, e.BlockedRoleID)
			if err != nil {
				return err
			}
			blocker, err := resolve(e.BlockerNodeIdx, e.BlockerRoleID)
			if err != nil {
				return err
			}
			if err := insertHeraBlockTx(tx, blocked, blocker); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
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

// blockKindOf returns the kind of a role within the insert tx. Used by
// insertHeraBlockTx to reject a coordinator-as-blocker edge (BUG-003).
func blockKindOf(tx *sql.Tx, roleID int64) (HeraRoleKind, error) {
	var kind string
	err := tx.QueryRow(`SELECT kind FROM hera_roles WHERE id=?`, roleID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("hera block: role %d: %w", roleID, ErrHeraNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("hera block: kind of role %d: %w", roleID, err)
	}
	return HeraRoleKind(kind), nil
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

// ListHeraBlocks returns every blocking edge whose endpoints both belong to the
// given orchestrator, as (BlockedRoleID, BlockerRoleID) pairs. It complements
// the per-role HeraBlockersOf with one bulk read for the whole orchestrator, so
// the plan view can project all edges without N per-node queries. The result is
// deterministically ordered by blocked then blocker role id, and excludes edges
// whose endpoints are archived or nuked roles (consistent with how the view
// filters roles).
func (d *DB) ListHeraBlocks(orchID int64) ([]HeraBlock, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Join both endpoints to hera_roles so we can scope to the orchestrator and
	// drop any edge whose blocked OR blocker role is archived/nuked — the same
	// archived_at IS NULL / nuked_at IS NULL filter the role list applies. Both
	// endpoints must belong to orchID (a cross-orchestrator edge cannot exist per
	// AddHeraBlock's guard, but scoping on both is defensive and free).
	// created_at is intentionally omitted from the projection — the plan view
	// consumes only the (blocked, blocker) endpoints, so HeraBlock.CreatedAt stays
	// zero here (mirroring HeraBlockersOf, which returns ids alone).
	rows, err := d.conn.Query(
		`SELECT bl.blocked_role_id, bl.blocker_role_id
		 FROM hera_blocks bl
		 JOIN hera_roles blocked ON blocked.id = bl.blocked_role_id
		 JOIN hera_roles blocker ON blocker.id = bl.blocker_role_id
		 WHERE blocked.orchestrator_id=? AND blocker.orchestrator_id=?
		   AND blocked.archived_at IS NULL AND blocked.nuked_at IS NULL
		   AND blocker.archived_at IS NULL AND blocker.nuked_at IS NULL
		 ORDER BY bl.blocked_role_id ASC, bl.blocker_role_id ASC`,
		orchID, orchID)
	if err != nil {
		return nil, fmt.Errorf("list hera blocks: %w", err)
	}
	defer rows.Close()
	var out []HeraBlock
	for rows.Next() {
		var b HeraBlock
		if err := rows.Scan(&b.BlockedRoleID, &b.BlockerRoleID); err != nil {
			return nil, fmt.Errorf("list hera blocks scan: %w", err)
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
		        r.created_at, r.archived_at, r.pinned_at, r.nuked_at, r.node_kind
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
