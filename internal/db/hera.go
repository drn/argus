package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/drn/argus/internal/model"
)

// Hera role/binding/orchestrator store (Milestone 1 of merging Hera into Argus
// natively — see context/plans/merge-hera-into-argus.md). These methods port
// the DAO semantics of Hera's orchestrators/roles/bindings/role_status DAOs
// onto Argus's mutex-guarded *DB style. Net-new: no UI, MCP, or daemon wiring
// (later milestones). The schema lives in createHeraTables (schema.go).

// Sentinel errors for the hera store. Prefixed to avoid colliding with the
// task-side ErrTaskNotFound and friends already on the db package.
var (
	// ErrHeraNotFound is returned when a row lookup finds nothing.
	ErrHeraNotFound = errors.New("hera: row not found")
	// ErrHeraNameConflict is returned when a rename collides with an existing
	// active row in the same scope.
	ErrHeraNameConflict = errors.New("hera: name already in use by an active row")
	// ErrHeraAmbiguous is returned by single-row lookups that match 2+ live
	// bindings (the multi-binding case: a task/worktree live under multiple
	// orchestrators). Callers must disambiguate with the per-orchestrator
	// variant or the List form.
	ErrHeraAmbiguous = errors.New("hera: lookup ambiguous (multiple live bindings match)")
	// ErrHeraRoleKindConflict is returned when CreateHeraRole finds an active
	// role with the requested (orchestrator, name) but a different kind.
	ErrHeraRoleKindConflict = errors.New("hera: role exists with a different kind")
)

// heraEndReasonTaskDeleted is stamped on bindings ended by the task-delete
// cascade hook in tasks.go Delete.
const heraEndReasonTaskDeleted = "argus_deleted"

// HeraEndReasonTaskMissing is stamped on bindings ended by the daemon-startup
// reconciliation sweep (M4) when their argus task row no longer exists — the
// row was deleted while the daemon was down so the delete-cascade never fired.
const HeraEndReasonTaskMissing = "task_missing"

// HeraEndReasonReparented / HeraEndReasonUserDeleted are the operator-TEARDOWN
// end reasons (ported from Hera's ops.EndReasonReparented/UserDeleted). They
// mark a parent→child link the operator explicitly tore down — re-parenting the
// child under a different coordinator, or deleting the link/subtree. The rail
// bridge (SubtreeOrchIDs / workerTaskSet) treats these as STALE: a binding ended
// for one of these reasons must NOT nest its child, whereas every OTHER end
// reason (argus_deleted / task_missing / normal session end) is a task-lifecycle
// event that leaves the structural parent link intact, so the child still nests.
// Native has no reparent/teardown path that stamps these today; the constants
// exist so the bridge guard is faithful to the plugin and forward-compatible.
const (
	HeraEndReasonReparented  = "reparented"
	HeraEndReasonUserDeleted = "user_deleted"
)

// HeraEndReasonIsTeardown reports whether an ended binding's end_reason marks an
// operator-teardown link (reparented / user_deleted) that must not bridge its
// child orchestrator. Every other reason leaves the structural link intact.
func HeraEndReasonIsTeardown(reason string) bool {
	return reason == HeraEndReasonReparented || reason == HeraEndReasonUserDeleted
}

// Task-meta mirror keys (namespace "hera"). The role layer mirrors a small
// amount of state into the task_meta sidecar (best-effort, soft-fail) so
// display predicates and other plugins can read it without joining the hera
// tables. Authoritative state always lives in the hera_* tables.
const (
	// HeraMetaNamespace is the task_meta namespace for hera mirror keys.
	HeraMetaNamespace = "hera"
	// HeraMetaKeyRole mirrors a bound task's role kind. The auto-adopt watcher
	// (rule D4) keys on the value "worker".
	HeraMetaKeyRole = "role"
	// HeraMetaKeyThreadStatus mirrors a role's status (idle/working/...).
	HeraMetaKeyThreadStatus = "thread_status"
	// HeraMetaKeyReadyToClose marks a finished worker task that is awaiting
	// coordinator/human close-out (BUG-050). The session-exit finish policy
	// stamps it "true"; the M6 rail + task-list rendering consume it.
	HeraMetaKeyReadyToClose = "ready_to_close"
	// HeraMetaKeyPrompt optionally carries a worker's verbatim prompt so the
	// auto-adopt path can populate the adopted role's prompt. Tolerated absent.
	HeraMetaKeyPrompt = "prompt"
	// HeraMetaKeyHandoffNote carries a coordinator's distilled context ahead of
	// a recycle (add-coordinator-context-management D5) — the new session's
	// seed prompt reads it so the successor never needs a tool call to
	// reconstruct why a non-obvious call was made.
	HeraMetaKeyHandoffNote = "handoff_note"
	// HeraMetaKeyPendingRecycle marks a coordinator's self-service recycle
	// request ("true"). The recycle_coord primitive consumes it and defers the
	// actual kill-and-restart until the session goes idle.
	HeraMetaKeyPendingRecycle = "pending_recycle"
	// HeraMetaKeyContextSize mirrors a coordinator's last-observed
	// cache_read_input_tokens count (add-coordinator-context-management D1),
	// overwritten on every Stop-hook invocation (`argus coord-hook`) — a
	// single scalar, not a time series.
	HeraMetaKeyContextSize = "context_size"
	// HeraMetaKeyLastNudgedContextSize mirrors the context_size at which the
	// over-budget Stop-hook nudge (argus coord-hook) last fired
	// (throttle-coord-hook-nudge) — a single scalar, not history, overwritten
	// each time the nudge fires. Used to throttle nudge recurrence to once per
	// coordinator_nudge_increment of growth, rather than firing every turn.
	HeraMetaKeyLastNudgedContextSize = "last_nudged_context_size"
)

// HeraRoleKind enumerates the valid kinds for a hera_roles row.
type HeraRoleKind string

const (
	HeraKindCoordinator HeraRoleKind = "coordinator"
	HeraKindWorker      HeraRoleKind = "worker"
	HeraKindFreelance   HeraRoleKind = "freelance"
)

// HeraNodeKind is the plan-node discriminator stored on a planned role
// (add-hera-subcoord-nodes). It steers how the gater materializes the node:
// worker nodes materialize as leaf agents; subcoord nodes materialize as a
// distinct coordinator agent that owns its own sub-orchestrator.
//
// IMPORTANT: HeraNodeKind is SEPARATE from HeraRoleKind. A planned node's
// hera_roles.kind is ALWAYS worker (D2 — the node occupies a worker slot in
// the parent DAG regardless of node_kind). node_kind is only consulted at
// materialize time.
type HeraNodeKind string

const (
	// HeraNodeKindWorker is the default: materialize as a leaf worker agent.
	// Absent or NULL node_kind maps to this value on scan.
	HeraNodeKindWorker HeraNodeKind = "worker"
	// HeraNodeKindSubCoord means: materialize as a distinct coordinator agent
	// with its own sub-orchestrator. The planned role's Prompt IS the goal
	// delivered to that coordinator.
	HeraNodeKindSubCoord HeraNodeKind = "subcoord"
)

// HeraRoleStatusValue enumerates the valid hera_role_status strings.
type HeraRoleStatusValue string

const (
	HeraStatusIdle    HeraRoleStatusValue = "idle"
	HeraStatusWorking HeraRoleStatusValue = "working"
	HeraStatusBlocked HeraRoleStatusValue = "blocked"
	HeraStatusDone    HeraRoleStatusValue = "done"
	HeraStatusFailed  HeraRoleStatusValue = "failed"
)

// HeraKanbanStatus is the independent, operator-set "where does this
// orchestration effort stand" axis for a TOP-LEVEL coordinator
// (add-hera-kanban-status) — active/backlog/blocked/done, default active.
// Deliberately its own type, never HeraRoleStatusValue: two of its four
// values ("blocked", "done") share a NAME with hera_role_status values but
// are a completely different column (kanban_status on hera_orchestrators,
// not hera_role_status on a role) with different semantics (an operator-set
// project-tracking marker, not a role's live progress) and a different
// stepping rule (the rail's m/M keys wrap; s/S clamps). See
// context/knowledge/gotchas/hera-view.md.
type HeraKanbanStatus string

const (
	HeraKanbanActive  HeraKanbanStatus = "active"
	HeraKanbanBacklog HeraKanbanStatus = "backlog"
	HeraKanbanBlocked HeraKanbanStatus = "blocked"
	HeraKanbanDone    HeraKanbanStatus = "done"
)

// HeraOrchestrator is one coordination group. ArchivedAt is non-nil for
// archived rows; PinnedAt is non-nil for pinned rows. Pin and archive are
// mutually exclusive — the Pin/Archive verbs clear the other. NukedAt is the
// Tier-2 end-of-life marker (BUG-022): a nuked orchestrator is REMOVED from the
// rail entirely (invisible to the rail-feeding lists), its worktrees reclaimed,
// but its row + inbox + task retained for DB-only recovery. A nuked row always
// also carries ArchivedAt (so it leaves the active-name index).
type HeraOrchestrator struct {
	ID         int64
	Name       string
	CreatedAt  time.Time
	ArchivedAt *time.Time
	PinnedAt   *time.Time
	NukedAt    *time.Time
	// BaseBranch is the optional explicit base branch a plan-DAG's ROOT nodes
	// stack on (add-hera-plan-base-branch). Empty means root nodes default to the
	// coordinator's branch, then the project default. Set once at bootstrap.
	BaseBranch string
	// KanbanStatus (add-hera-kanban-status) is the independent kanban axis — see
	// HeraKanbanStatus. Always non-empty (NOT NULL DEFAULT 'active' at the schema
	// level); reads as HeraKanbanActive for every orchestrator that predates this
	// column.
	KanbanStatus HeraKanbanStatus
}

// HeraRole is a participant in an orchestrator. Prompt is the only free-form
// field. ArchivedAt / PinnedAt / NukedAt mirror HeraOrchestrator.
// NodeKind is the plan-node discriminator (add-hera-subcoord-nodes): only
// meaningful on planned roles (no binding ever); defaults to HeraNodeKindWorker
// when the DB column is NULL or absent.
// CancelledAt (make-hera-plan-living) is non-nil when the coordinator has
// cancelled a planned node; the node is kept in the DB but excluded from
// materialization and treated as non-blocking by the gater.
type HeraRole struct {
	ID             int64
	OrchestratorID int64
	Name           string
	Kind           HeraRoleKind
	NodeKind       HeraNodeKind
	ArgusProject   string
	Prompt         string
	CreatedAt      time.Time
	ArchivedAt     *time.Time
	PinnedAt       *time.Time
	NukedAt        *time.Time
	CancelledAt    *time.Time
	// Archetype is the planned node's intended diligence archetype
	// (add-diligence-profiles), mirrored onto the live role for display. Empty
	// means no archetype. Stored as the nullable hera_roles.archetype column;
	// the gater copies it into the materialized task's archetype.
	Archetype string
}

// HeraBinding is one (role, argus task) incarnation. OrchestratorID is
// denormalized from the role so the bindings table can enforce per-orchestrator
// live-uniqueness without a JOIN.
type HeraBinding struct {
	ID             int64
	RoleID         int64
	OrchestratorID int64
	ArgusTaskID    string
	WorktreePath   string
	StartedAt      time.Time
	EndedAt        *time.Time
	EndReason      string
}

// HeraRoleStatus is the current status for a role.
type HeraRoleStatus struct {
	RoleID    int64
	Status    HeraRoleStatusValue
	UpdatedAt time.Time
}

// CreateHeraRoleInput captures the fields a role create must supply.
// NodeKind is the plan-node discriminator (add-hera-subcoord-nodes); it is
// stored on the hera_roles row as node_kind. Callers that do not set NodeKind
// get HeraNodeKindWorker (the zero-value default). Only CreateHeraPlannedRole
// persists this field; CreateHeraRole and CreateHeraRoleWithBinding ignore it
// (born-bound roles are never plan nodes and have no node_kind semantics).
type CreateHeraRoleInput struct {
	OrchestratorID int64
	Name           string
	Kind           HeraRoleKind
	NodeKind       HeraNodeKind
	ArgusProject   string
	Prompt         string
	// Archetype is the optional diligence archetype (add-diligence-profiles)
	// persisted on the role's hera_roles.archetype column. Empty stores NULL.
	Archetype string
}

// CreateHeraBindingInput captures the fields needed to start a binding.
// OrchestratorID may be left zero — Create derives it from the role.
type CreateHeraBindingInput struct {
	RoleID         int64
	OrchestratorID int64
	ArgusTaskID    string
	WorktreePath   string
}

// execer is the subset of *sql.DB / *sql.Tx used by the shared insert helpers,
// so the standalone (mutex-guarded) and transactional create paths reuse one
// INSERT implementation.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// --- Orchestrators ---

// CreateHeraOrchestrator inserts a new active orchestrator. If an active
// orchestrator with the same name already exists it is returned unchanged
// (idempotent) — the supplied baseBranch is NOT re-applied to an existing row.
// An archived row with the same name does NOT block creation — a fresh active
// row is inserted. baseBranch is the optional explicit base branch a plan-DAG's
// root nodes stack on (add-hera-plan-base-branch); pass "" when none.
func (d *DB) CreateHeraOrchestrator(name, baseBranch string) (*HeraOrchestrator, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if existing, err := d.heraOrchestratorByActiveName(name); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrHeraNotFound) {
		return nil, err
	}

	now := formatTime(time.Now())
	res, err := d.conn.Exec(`INSERT INTO hera_orchestrators (name, created_at, base_branch) VALUES (?, ?, ?)`, name, now, baseBranch)
	if err != nil {
		return nil, fmt.Errorf("create hera orchestrator: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create hera orchestrator: last insert id: %w", err)
	}
	return &HeraOrchestrator{ID: id, Name: name, CreatedAt: parseTime(now), BaseBranch: baseBranch, KanbanStatus: HeraKanbanActive}, nil
}

// HeraOrchestrator loads an orchestrator by id. Archived rows are returned —
// primary-key lookups are not filtered on archived_at.
func (d *DB) HeraOrchestrator(id int64) (*HeraOrchestrator, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraOrchestratorByID(id)
}

// HeraOrchestratorByName loads the active orchestrator with the given name.
// Archived rows are invisible to this lookup; use HeraOrchestrator (by id) to
// address an archived row.
func (d *DB) HeraOrchestratorByName(name string) (*HeraOrchestrator, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraOrchestratorByActiveName(name)
}

// ListHeraOrchestrators returns orchestrators ordered by name. When
// includeArchived is false only active rows are returned.
func (d *DB) ListHeraOrchestrators(includeArchived bool) ([]*HeraOrchestrator, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Nuked rows (Tier-2 EOL) are invisible to the rail-feeding lists regardless
	// of includeArchived — they are recoverable only by primary-key id lookup.
	query := `SELECT id, name, created_at, archived_at, pinned_at, nuked_at, base_branch, kanban_status FROM hera_orchestrators WHERE nuked_at IS NULL`
	if !includeArchived {
		query += ` AND archived_at IS NULL`
	}
	query += ` ORDER BY name ASC`

	rows, err := d.conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("list hera orchestrators: %w", err)
	}
	defer rows.Close()

	var out []*HeraOrchestrator
	for rows.Next() {
		o, err := scanHeraOrchestrator(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ArchiveHeraOrchestrator stamps archived_at (current time) and CLEARS
// pinned_at — pin and archive are mutually exclusive. Idempotent: re-archiving
// preserves the original archived_at. Returns ErrHeraNotFound if no row matches.
func (d *DB) ArchiveHeraOrchestrator(id int64) error {
	return d.heraSetFlag(`UPDATE hera_orchestrators SET archived_at=?, pinned_at=NULL WHERE id=? AND archived_at IS NULL`,
		heraOrchExistsProbe, id, formatTime(time.Now()))
}

// UnarchiveHeraOrchestrator clears archived_at. Idempotent. Returns
// ErrHeraNotFound if no row matches id.
func (d *DB) UnarchiveHeraOrchestrator(id int64) error {
	return d.heraSetFlag(`UPDATE hera_orchestrators SET archived_at=NULL WHERE id=? AND archived_at IS NOT NULL`,
		heraOrchExistsProbe, id)
}

// PinHeraOrchestrator stamps pinned_at (preserving an existing value via
// COALESCE, so pin is idempotent) AND clears archived_at — pin issued against
// an archived row both pins and unarchives. Returns ErrHeraNotFound if no row
// matches id.
func (d *DB) PinHeraOrchestrator(id int64) error {
	return d.heraSetFlag(`UPDATE hera_orchestrators SET pinned_at=COALESCE(pinned_at, ?), archived_at=NULL WHERE id=?`,
		heraOrchExistsProbe, id, formatTime(time.Now()))
}

// UnpinHeraOrchestrator clears pinned_at. Idempotent. Returns ErrHeraNotFound
// if no row matches id.
func (d *DB) UnpinHeraOrchestrator(id int64) error {
	return d.heraSetFlag(`UPDATE hera_orchestrators SET pinned_at=NULL WHERE id=? AND pinned_at IS NOT NULL`,
		heraOrchExistsProbe, id)
}

// NukeHeraOrchestrator marks an orchestrator NUKED (BUG-022 Tier-2 EOL): it
// stamps nuked_at (idempotent via COALESCE) AND ensures archived_at is set (so
// the row leaves the active-name partial unique index and frees its name for
// reuse), clearing pinned_at. A nuked orchestrator is invisible to the
// rail-feeding lists (ListHeraOrchestrators) but its row is retained and still
// returned by id (HeraOrchestrator) for DB-only recovery. NEVER a hard delete.
// Returns ErrHeraNotFound if no row matches id.
func (d *DB) NukeHeraOrchestrator(id int64) error {
	now := formatTime(time.Now())
	return d.heraSetFlag(
		`UPDATE hera_orchestrators SET nuked_at=COALESCE(nuked_at, ?), archived_at=COALESCE(archived_at, ?), pinned_at=NULL WHERE id=?`,
		heraOrchExistsProbe, id, now, now)
}

// SetHeraOrchestratorKanbanStatus sets the orchestrator's independent kanban
// status (add-hera-kanban-status) — a data axis wholly separate from
// pinned_at/archived_at and from any role's hera_role_status; setting it never
// touches either. Idempotent (re-setting the current value is a harmless
// no-op write). Returns ErrHeraNotFound if no row matches id.
func (d *DB) SetHeraOrchestratorKanbanStatus(id int64, status HeraKanbanStatus) error {
	return d.heraSetFlag(`UPDATE hera_orchestrators SET kanban_status=? WHERE id=?`,
		heraOrchExistsProbe, id, string(status))
}

// RenameHeraOrchestrator updates the name. The new name must be free among
// active orchestrators; archived rows with the same name do not block. Returns
// ErrHeraNotFound if no row matches id, or ErrHeraNameConflict on collision.
func (d *DB) RenameHeraOrchestrator(id int64, newName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	cur, err := d.heraOrchestratorByID(id)
	if err != nil {
		return err
	}
	if cur.Name == newName {
		return nil
	}
	var existsID int64
	err = d.conn.QueryRow(`SELECT id FROM hera_orchestrators WHERE name=? AND archived_at IS NULL AND id!=?`,
		newName, id).Scan(&existsID)
	if err == nil {
		return fmt.Errorf("%w: %q held by id=%d", ErrHeraNameConflict, newName, existsID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("rename hera orchestrator: %w", err)
	}
	if _, err := d.conn.Exec(`UPDATE hera_orchestrators SET name=? WHERE id=?`, newName, id); err != nil {
		return fmt.Errorf("rename hera orchestrator: %w", err)
	}
	return nil
}

// DeleteHeraOrchestrator permanently removes an orchestrator. Child roles and
// their bindings / role_status are removed automatically via ON DELETE CASCADE
// (FK enforcement is enabled on the connection). Unlike Archive, Delete is not
// recoverable. Returns ErrHeraNotFound if no row matches id.
func (d *DB) DeleteHeraOrchestrator(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`DELETE FROM hera_orchestrators WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete hera orchestrator: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrHeraNotFound
	}
	return nil
}

func (d *DB) heraOrchestratorByID(id int64) (*HeraOrchestrator, error) {
	row := d.conn.QueryRow(`SELECT id, name, created_at, archived_at, pinned_at, nuked_at, base_branch, kanban_status FROM hera_orchestrators WHERE id=?`, id)
	return scanHeraOrchestrator(row)
}

func (d *DB) heraOrchestratorByActiveName(name string) (*HeraOrchestrator, error) {
	row := d.conn.QueryRow(
		`SELECT id, name, created_at, archived_at, pinned_at, nuked_at, base_branch, kanban_status FROM hera_orchestrators WHERE name=? AND archived_at IS NULL`,
		name)
	return scanHeraOrchestrator(row)
}

// --- Roles ---

// CreateHeraRole inserts a new active role under an orchestrator. If an active
// role with the same (orchestrator_id, name) already exists it is returned
// unchanged when the kind matches (roles are write-once on prompt/project — the
// supplied values are ignored), or ErrHeraRoleKindConflict when the kind
// differs. An archived row with the same key does NOT block creation.
func (d *DB) CreateHeraRole(in CreateHeraRoleInput) (*HeraRole, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	existing, err := d.heraRoleByActiveName(in.OrchestratorID, in.Name)
	if err != nil && !errors.Is(err, ErrHeraNotFound) {
		return nil, err
	}
	if existing != nil {
		if existing.Kind != in.Kind {
			return nil, fmt.Errorf("%w: %q is %q, not %q", ErrHeraRoleKindConflict, in.Name, existing.Kind, in.Kind)
		}
		return existing, nil
	}
	return insertHeraRole(d.conn, in, formatTime(time.Now()))
}

// HeraRole loads a role by id. Archived rows are returned.
func (d *DB) HeraRole(id int64) (*HeraRole, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraRoleByID(id)
}

// HeraRoleByName loads the active role with the given (orchestrator_id, name).
// Archived rows are invisible to this lookup.
func (d *DB) HeraRoleByName(orchID int64, name string) (*HeraRole, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraRoleByActiveName(orchID, name)
}

// ListHeraRoles returns roles under an orchestrator ordered by kind
// (coordinator, worker, freelance) then name. When includeArchived is false
// only active rows are returned.
func (d *DB) ListHeraRoles(orchID int64, includeArchived bool) ([]*HeraRole, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Nuked roles (Tier-2 EOL) are invisible to the rail-feeding list regardless
	// of includeArchived — recoverable only by id lookup (HeraRole).
	query := `SELECT id, orchestrator_id, name, kind, argus_project, prompt, created_at, archived_at, pinned_at, nuked_at, node_kind, cancelled_at, archetype
	          FROM hera_roles WHERE orchestrator_id=? AND nuked_at IS NULL`
	if !includeArchived {
		query += ` AND archived_at IS NULL`
	}
	query += ` ORDER BY (CASE kind WHEN 'coordinator' THEN 0 WHEN 'worker' THEN 1 ELSE 2 END), name ASC`

	rows, err := d.conn.Query(query, orchID)
	if err != nil {
		return nil, fmt.Errorf("list hera roles: %w", err)
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

// ListHeraRolesByKind returns active roles of a single kind under an
// orchestrator, ordered by name.
func (d *DB) ListHeraRolesByKind(orchID int64, kind HeraRoleKind) ([]*HeraRole, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	rows, err := d.conn.Query(
		`SELECT id, orchestrator_id, name, kind, argus_project, prompt, created_at, archived_at, pinned_at, nuked_at, node_kind, cancelled_at, archetype
		 FROM hera_roles WHERE orchestrator_id=? AND kind=? AND archived_at IS NULL ORDER BY name ASC`,
		orchID, string(kind))
	if err != nil {
		return nil, fmt.Errorf("list hera roles by kind: %w", err)
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

// ArchiveHeraRole stamps archived_at and clears pinned_at. Idempotent. Returns
// ErrHeraNotFound if no row matches id.
func (d *DB) ArchiveHeraRole(id int64) error {
	return d.heraSetFlag(`UPDATE hera_roles SET archived_at=?, pinned_at=NULL WHERE id=? AND archived_at IS NULL`,
		heraRoleExistsProbe, id, formatTime(time.Now()))
}

// UnarchiveHeraRole clears archived_at. Idempotent. Returns ErrHeraNotFound if
// no row matches id.
func (d *DB) UnarchiveHeraRole(id int64) error {
	return d.heraSetFlag(`UPDATE hera_roles SET archived_at=NULL WHERE id=? AND archived_at IS NOT NULL`,
		heraRoleExistsProbe, id)
}

// PinHeraRole stamps pinned_at (idempotent via COALESCE) and clears
// archived_at. Returns ErrHeraNotFound if no row matches id.
func (d *DB) PinHeraRole(id int64) error {
	return d.heraSetFlag(`UPDATE hera_roles SET pinned_at=COALESCE(pinned_at, ?), archived_at=NULL WHERE id=?`,
		heraRoleExistsProbe, id, formatTime(time.Now()))
}

// UnpinHeraRole clears pinned_at. Idempotent. Returns ErrHeraNotFound if no row
// matches id.
func (d *DB) UnpinHeraRole(id int64) error {
	return d.heraSetFlag(`UPDATE hera_roles SET pinned_at=NULL WHERE id=? AND pinned_at IS NOT NULL`,
		heraRoleExistsProbe, id)
}

// NukeHeraRole marks a role NUKED (BUG-022 Tier-2 EOL): stamps nuked_at
// (idempotent via COALESCE) AND ensures archived_at is set (freeing its name in
// the active-name index), clearing pinned_at. A nuked role is invisible to the
// rail-feeding lists (ListHeraRoles / ListHeraRolesByKind) but its row, bindings,
// status, inbox messages, and bound argus task are all retained — recovery is
// via the DB only. NEVER a hard delete. Returns ErrHeraNotFound if no row matches.
func (d *DB) NukeHeraRole(id int64) error {
	now := formatTime(time.Now())
	return d.heraSetFlag(
		`UPDATE hera_roles SET nuked_at=COALESCE(nuked_at, ?), archived_at=COALESCE(archived_at, ?), pinned_at=NULL WHERE id=?`,
		heraRoleExistsProbe, id, now, now)
}

// RenameHeraRole updates a role's name. The new name must be free among active
// roles under the SAME orchestrator; archived siblings and roles in other
// orchestrators do not block. Returns ErrHeraNotFound or ErrHeraNameConflict.
func (d *DB) RenameHeraRole(id int64, newName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	cur, err := d.heraRoleByID(id)
	if err != nil {
		return err
	}
	if cur.Name == newName {
		return nil
	}
	var existsID int64
	err = d.conn.QueryRow(
		`SELECT id FROM hera_roles WHERE orchestrator_id=? AND name=? AND archived_at IS NULL AND id!=?`,
		cur.OrchestratorID, newName, id).Scan(&existsID)
	if err == nil {
		return fmt.Errorf("%w: %q held by role id=%d under orchestrator %d",
			ErrHeraNameConflict, newName, existsID, cur.OrchestratorID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("rename hera role: %w", err)
	}
	if _, err := d.conn.Exec(`UPDATE hera_roles SET name=? WHERE id=?`, newName, id); err != nil {
		return fmt.Errorf("rename hera role: %w", err)
	}
	return nil
}

// DeleteHeraRole permanently removes a role. Its bindings and role_status row
// cascade. Returns ErrHeraNotFound if no row matches id.
func (d *DB) DeleteHeraRole(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(`DELETE FROM hera_roles WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete hera role: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrHeraNotFound
	}
	return nil
}

func (d *DB) heraRoleByID(id int64) (*HeraRole, error) {
	row := d.conn.QueryRow(
		`SELECT id, orchestrator_id, name, kind, argus_project, prompt, created_at, archived_at, pinned_at, nuked_at, node_kind, cancelled_at, archetype
		 FROM hera_roles WHERE id=?`, id)
	return scanHeraRole(row)
}

func (d *DB) heraRoleByActiveName(orchID int64, name string) (*HeraRole, error) {
	row := d.conn.QueryRow(
		`SELECT id, orchestrator_id, name, kind, argus_project, prompt, created_at, archived_at, pinned_at, nuked_at, node_kind, cancelled_at, archetype
		 FROM hera_roles WHERE orchestrator_id=? AND name=? AND archived_at IS NULL`, orchID, name)
	return scanHeraRole(row)
}

func insertHeraRole(ex execer, in CreateHeraRoleInput, now string) (*HeraRole, error) {
	// node_kind: store NULL for the default worker kind so pre-existing rows
	// (which have no node_kind column value) scan identically to a newly
	// inserted worker. Storing NULL for worker and "subcoord" for subcoord
	// keeps the column sparse and makes the default unambiguous on scan.
	var nodeKindVal *string
	if in.NodeKind == HeraNodeKindSubCoord {
		s := string(in.NodeKind)
		nodeKindVal = &s
	}
	// archetype: store NULL when empty (sparse column, same pattern as node_kind)
	// so pre-existing rows that never had the column scan identically.
	var archetypeVal *string
	if in.Archetype != "" {
		s := in.Archetype
		archetypeVal = &s
	}
	res, err := ex.Exec(
		`INSERT INTO hera_roles (orchestrator_id, name, kind, argus_project, prompt, created_at, node_kind, archetype)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.OrchestratorID, in.Name, string(in.Kind), in.ArgusProject, in.Prompt, now, nodeKindVal, archetypeVal)
	if err != nil {
		return nil, fmt.Errorf("insert hera role: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("insert hera role: last insert id: %w", err)
	}
	nodeKind := in.NodeKind
	if nodeKind == "" {
		nodeKind = HeraNodeKindWorker
	}
	return &HeraRole{
		ID:             id,
		OrchestratorID: in.OrchestratorID,
		Name:           in.Name,
		Kind:           in.Kind,
		NodeKind:       nodeKind,
		ArgusProject:   in.ArgusProject,
		Prompt:         in.Prompt,
		CreatedAt:      parseTime(now),
		Archetype:      in.Archetype,
	}, nil
}

// --- Bindings ---

// CreateHeraBinding inserts a new live binding (ended_at NULL). When
// OrchestratorID is zero it is derived from the role. Live-uniqueness is
// enforced at the index level: a second live binding for the same (task,
// orchestrator), (worktree, orchestrator), or role fails with a constraint
// error — the deterministic backstop behind any app-level pre-check.
func (d *DB) CreateHeraBinding(in CreateHeraBindingInput) (*HeraBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if in.OrchestratorID == 0 {
		orchID, err := deriveHeraOrchestratorID(d.conn, in.RoleID)
		if err != nil {
			return nil, err
		}
		in.OrchestratorID = orchID
	}
	return insertHeraBinding(d.conn, in, formatTime(time.Now()))
}

// CreateHeraRoleWithBinding inserts a role and its first binding in ONE
// transaction. If the binding insert fails (e.g. it violates a live-uniqueness
// index), the role insert is rolled back too — no orphan role row. This fixes
// the deferred Hera debt where role+binding creation was two non-transactional
// execs (orphan risk). The role is inserted fresh (not idempotent); callers
// (e.g. born-bound spawn) supply a name made unique within the orchestrator,
// and the partial unique index is the backstop.
func (d *DB) CreateHeraRoleWithBinding(roleIn CreateHeraRoleInput, taskID, worktreePath string) (*HeraRole, *HeraBinding, error) {
	var role *HeraRole
	var binding *HeraBinding
	err := d.WithTx(func(tx *sql.Tx) error {
		now := formatTime(time.Now())
		r, err := insertHeraRole(tx, roleIn, now)
		if err != nil {
			return err
		}
		b, err := insertHeraBinding(tx, CreateHeraBindingInput{
			RoleID:         r.ID,
			OrchestratorID: r.OrchestratorID,
			ArgusTaskID:    taskID,
			WorktreePath:   worktreePath,
		}, now)
		if err != nil {
			return err
		}
		role, binding = r, b
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return role, binding, nil
}

// HeraEndReasonMoved is stamped on a binding ended by MoveHeraBinding — the
// caller relocated to a different orchestrator via the explicit hera_move
// tool (fix-hera-join-move-binding), as opposed to a normal leave/delete.
const HeraEndReasonMoved = "moved"

// MoveHeraBindingResult reports the outcome of MoveHeraBinding: the ended
// binding's orchestrator and role name (so the MCP layer can report what
// moved) alongside the newly created role and binding under the target
// orchestrator.
type MoveHeraBindingResult struct {
	OldOrchestratorName string
	OldRoleName         string
	NewRole             *HeraRole
	NewBinding          *HeraBinding
}

// MoveHeraBinding ends oldBindingID (stamping end_reason "moved") and inserts
// a new role+binding under the target orchestrator, in ONE transaction — the
// move-capable counterpart to CreateHeraRoleWithBinding: same insert-role,
// insert-binding pattern, with an extra end-binding step first so a caller
// never ends up bound in neither place, nor bound in both. Returns
// ErrHeraNotFound if oldBindingID does not name a currently-live binding
// (already ended, or never existed); the whole transaction rolls back on any
// failure, so a not-found or a doomed role/binding insert leaves the old
// binding live and untouched.
func (d *DB) MoveHeraBinding(oldBindingID int64, roleIn CreateHeraRoleInput, taskID, worktreePath string) (*MoveHeraBindingResult, error) {
	var result MoveHeraBindingResult
	err := d.WithTx(func(tx *sql.Tx) error {
		now := formatTime(time.Now())

		var oldRoleID int64
		if err := tx.QueryRow(
			`SELECT role_id FROM hera_bindings WHERE id=? AND ended_at IS NULL`, oldBindingID,
		).Scan(&oldRoleID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrHeraNotFound
			}
			return fmt.Errorf("move hera binding: lookup old binding: %w", err)
		}

		var oldRoleName string
		var oldOrchID int64
		if err := tx.QueryRow(
			`SELECT name, orchestrator_id FROM hera_roles WHERE id=?`, oldRoleID,
		).Scan(&oldRoleName, &oldOrchID); err != nil {
			return fmt.Errorf("move hera binding: lookup old role: %w", err)
		}
		var oldOrchName string
		if err := tx.QueryRow(
			`SELECT name FROM hera_orchestrators WHERE id=?`, oldOrchID,
		).Scan(&oldOrchName); err != nil {
			return fmt.Errorf("move hera binding: lookup old orchestrator: %w", err)
		}

		res, err := tx.Exec(
			`UPDATE hera_bindings SET ended_at=?, end_reason=? WHERE id=? AND ended_at IS NULL`,
			now, HeraEndReasonMoved, oldBindingID)
		if err != nil {
			return fmt.Errorf("move hera binding: end old binding: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrHeraNotFound
		}

		r, err := insertHeraRole(tx, roleIn, now)
		if err != nil {
			return err
		}
		b, err := insertHeraBinding(tx, CreateHeraBindingInput{
			RoleID:         r.ID,
			OrchestratorID: r.OrchestratorID,
			ArgusTaskID:    taskID,
			WorktreePath:   worktreePath,
		}, now)
		if err != nil {
			return err
		}

		result = MoveHeraBindingResult{
			OldOrchestratorName: oldOrchName,
			OldRoleName:         oldRoleName,
			NewRole:             r,
			NewBinding:          b,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// EndHeraBinding stamps ended_at and end_reason on a live binding. Returns
// ErrHeraNotFound if no live binding has that id.
func (d *DB) EndHeraBinding(bindingID int64, reason string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(
		`UPDATE hera_bindings SET ended_at=?, end_reason=? WHERE id=? AND ended_at IS NULL`,
		formatTime(time.Now()), reason, bindingID)
	if err != nil {
		return fmt.Errorf("end hera binding: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrHeraNotFound
	}
	return nil
}

// EndHeraBindingsForTask ends every live binding for an argus task id, stamping
// the given reason. Returns the number of bindings ended. This is the public
// form of the task-delete cascade (tasks.go Delete inlines the same UPDATE
// inside its transaction because WithTx holds the DB mutex and cannot re-enter).
func (d *DB) EndHeraBindingsForTask(taskID, reason string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.conn.Exec(
		`UPDATE hera_bindings SET ended_at=?, end_reason=? WHERE argus_task_id=? AND ended_at IS NULL`,
		formatTime(time.Now()), reason, taskID)
	if err != nil {
		return 0, fmt.Errorf("end hera bindings for task: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// HeraLiveBindingByTask returns the single live binding for a task. Returns
// ErrHeraNotFound when none exists and ErrHeraAmbiguous when 2+ exist (the
// multi-binding case — the task is live under multiple orchestrators). Callers
// in that case use HeraLiveBindingByTaskAndOrchestrator or ListHeraLiveBindingsByTask.
func (d *DB) HeraLiveBindingByTask(taskID string) (*HeraBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraSingleLiveBinding(
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM hera_bindings WHERE argus_task_id=? AND ended_at IS NULL`, taskID)
}

// HeraLiveBindingByTaskAndOrchestrator returns the live binding for
// (taskID, orchID), or ErrHeraNotFound. The primary lookup in the
// multi-binding world: resolve the orchestrator context, then ask whether THIS
// task is bound under THAT orchestrator.
func (d *DB) HeraLiveBindingByTaskAndOrchestrator(taskID string, orchID int64) (*HeraBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	row := d.conn.QueryRow(
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM hera_bindings WHERE argus_task_id=? AND orchestrator_id=? AND ended_at IS NULL`, taskID, orchID)
	return scanHeraBinding(row)
}

// HeraLiveBindingByWorktreeAndOrchestrator returns the live binding for
// (worktreePath, orchID), or ErrHeraNotFound. The worktree-keyed twin of
// HeraLiveBindingByTaskAndOrchestrator: the (worktree_path, orchestrator_id)
// live-uniqueness index caps this at one row, so it maps exactly onto the
// identity an attach INSERT for that (worktree, orchestrator) would collide
// against.
//
// It exists because cwd→argus_task_id resolution (resolveTask) can land on
// the WRONG task when two argus tasks reuse the same worktree_path across
// their lifecycles — a task name/branch reused after the prior task moved to
// in_review/complete/archived without its worktree being cleared. When that
// happens the task-keyed lookup misses the live binding that the
// (worktree_path, orchestrator_id) uniqueness nonetheless rejects on INSERT —
// the claim-says-none / attach-says-exists paradox (BUG-059). The caller's
// worktree_path IS its cwd (ground truth) regardless of which task cwd
// resolution happened to return, so this lookup resolves the same binding an
// attach INSERT would collide with, keeping claim and attach in agreement.
// Orchestrator scoping keeps the fallback safe: a stale binding under a
// DIFFERENT orchestrator sharing the worktree is never returned here.
func (d *DB) HeraLiveBindingByWorktreeAndOrchestrator(worktreePath string, orchID int64) (*HeraBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	row := d.conn.QueryRow(
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM hera_bindings WHERE worktree_path=? AND orchestrator_id=? AND ended_at IS NULL`, worktreePath, orchID)
	return scanHeraBinding(row)
}

// ListHeraLiveBindingsByTask returns every live binding for a task, oldest
// first. Empty slice if none.
func (d *DB) ListHeraLiveBindingsByTask(taskID string) ([]*HeraBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraListBindings(
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM hera_bindings WHERE argus_task_id=? AND ended_at IS NULL ORDER BY started_at ASC, id ASC`, taskID)
}

// TaskHoldsLiveHeraWorkerBinding reports whether taskID has at least one live
// binding whose role is worker-kind. The session-exit finish policy (BUG-050)
// uses this to force a worker task to in_review even on a clean exit — workers
// are closed out by the coordinator/human, never self-completing. Coordinator
// and freelance bindings do NOT count. A query error is returned so the caller
// can soft-fail (preserve the default PR #707 behaviour) rather than guessing.
func (d *DB) TaskHoldsLiveHeraWorkerBinding(taskID string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var n int
	err := d.conn.QueryRow(
		`SELECT COUNT(*) FROM hera_bindings b JOIN hera_roles r ON r.id = b.role_id
		 WHERE b.argus_task_id=? AND b.ended_at IS NULL AND r.kind=?`,
		taskID, string(HeraKindWorker)).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("task holds live worker binding: %w", err)
	}
	return n > 0, nil
}

// ManagedTaskIDs returns the set of argus task IDs that currently hold at least
// one live hera binding (ended_at IS NULL) to a coordinator- or worker-kind
// role. Freelance-kind bindings do NOT count. Used by the Tasks tab
// freelancers-only filter to identify tasks that are managed (and therefore
// not freelancers). Returns a non-nil map even when no rows match.
func (d *DB) ManagedTaskIDs() (map[string]bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		`SELECT DISTINCT b.argus_task_id
		 FROM hera_bindings b
		 JOIN hera_roles r ON r.id = b.role_id
		 WHERE b.ended_at IS NULL AND r.kind IN (?, ?)`,
		string(HeraKindCoordinator), string(HeraKindWorker))
	if err != nil {
		return nil, fmt.Errorf("managed task ids: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("managed task ids: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("managed task ids: %w", err)
	}
	return out, nil
}

// rollHeraWorkerToReviewInner is the shared implementation behind
// RollHeraWorkerToReview and RollHeraWorkerFailed. Both roll a live worker's
// bound task from in_progress to in_review; they differ only in whether
// ready_to_close is stamped (done → stamp; failed → no stamp, the task is not
// ready to close). Invariants enforced here so neither call-site can drift:
//   - worker-kind only (coordinators/freelance are no-ops)
//   - no-op unless the task is currently in_progress (never clobbers human state)
//   - DB status + meta only — the live session is never touched
//   - idempotent (second call is a no-op; task is already in_review)
//
// Returns (true, nil) when flipped, (false, nil) on any no-op path.
func (d *DB) rollHeraWorkerToReviewInner(taskID string, stampReadyToClose bool) (bool, error) {
	worker, err := d.TaskHoldsLiveHeraWorkerBinding(taskID)
	if err != nil {
		return false, err
	}
	if !worker {
		return false, nil
	}
	t, err := d.Get(taskID)
	if err != nil {
		return false, err
	}
	if t == nil || t.Status != model.StatusInProgress {
		return false, nil // never clobber a human-set in_review/complete
	}
	if err := d.SetStatus(taskID, model.StatusInReview); err != nil {
		return false, err
	}
	if stampReadyToClose {
		if mErr := d.SetMeta(taskID, HeraMetaNamespace, HeraMetaKeyReadyToClose, "true"); mErr != nil {
			slog.Warn("[hera] ready_to_close stamp failed (flip stands)", "task", taskID, "err", mErr)
		}
	}
	return true, nil
}

// RollHeraWorkerToReview implements the BUG-050 worker close-out roll: it moves
// a worker-bound task to in_review and stamps meta:hera.ready_to_close=true. It
// is the SINGLE shared helper behind BOTH close-out triggers — the session-exit
// hooks (backstop) and the hera_status("done") hook (primary path, since Claude
// workers finish their report and go idle rather than exiting) — so the two can
// never drift.
//
// It acts ONLY when the task currently holds a live worker-kind binding AND is
// in StatusInProgress. It never auto-completes, never clobbers a human-set
// in_review/complete (the in_progress guard), and never touches the agent
// session (DB status + meta only — callers must not stop/restart). Returns
// (true, nil) when it flipped, (false, nil) on a no-op (not worker-bound, or not
// in_progress). Idempotent: a second call is a no-op because the task is no
// longer in_progress.
//
// SetStatus emits task.status_changed OUTSIDE the DB mutex (events.md); the
// ready_to_close stamp is best-effort soft-fail — a meta failure is logged and
// the flip still stands.
func (d *DB) RollHeraWorkerToReview(taskID string) (bool, error) {
	return d.rollHeraWorkerToReviewInner(taskID, true)
}

// RollHeraWorkerFailed rolls a failed worker's bound task to in_review WITHOUT
// stamping ready_to_close. A failed task surfaces for coordinator attention
// (in_review) but is NOT ready to check off — the coordinator must decide
// whether to retry, reassign, or close it. Shares all invariants with
// RollHeraWorkerToReview: worker-kind only, no-op unless in_progress, idempotent,
// soft-fail (the roll succeeds even if the meta write fails).
func (d *DB) RollHeraWorkerFailed(taskID string) (bool, error) {
	return d.rollHeraWorkerToReviewInner(taskID, false)
}

// ReviveHeraWorkerToInProgress is the precise inverse of RollHeraWorkerToReview
// (BUG-B): it restores a worker-bound task from in_review back to in_progress
// when its session is genuinely revived/resumed and working again. It is the
// SINGLE shared helper behind BOTH revive triggers — the TUI's reviveHeraWorker
// (KickRerender on a suspended worker) and the daemon's supervisor-mode startup
// reattach (a session the supervisor confirms still alive) — so the two cannot
// drift.
//
// It acts ONLY when the task holds a live worker-kind binding AND is currently
// in StatusInReview AND is NOT awaiting close-out. A worker is awaiting close-out
// — and is LEFT in in_review — when it carries meta:hera.ready_to_close (the
// BUG-050 done / clean-exit stamp set by RollHeraWorkerToReview) OR any of its
// live worker roles has a terminal role-status (done or failed). That guard is
// what preserves the #707 / BUG-050 invariant: a genuinely-finished worker never
// auto-resumes — even though its idle session is still alive — because a worker
// never self-completes; the coordinator/human closes it out or decides on a
// failure. It never clobbers a complete/pending/in_progress task and never
// touches the live session (DB status only). Returns (true, nil) when it
// restored the task, (false, nil) on any no-op. Idempotent.
func (d *DB) ReviveHeraWorkerToInProgress(taskID string) (bool, error) {
	worker, err := d.TaskHoldsLiveHeraWorkerBinding(taskID)
	if err != nil {
		return false, err
	}
	if !worker {
		return false, nil
	}
	t, err := d.Get(taskID)
	if err != nil {
		return false, err
	}
	if t == nil || t.Status != model.StatusInReview {
		return false, nil // only un-roll a review-parked worker; never clobber complete/pending/in_progress
	}
	awaiting, err := d.heraWorkerAwaitingCloseout(taskID)
	if err != nil {
		return false, err
	}
	if awaiting {
		return false, nil // genuinely done/failed — leave for coordinator close-out (#707 / BUG-050)
	}
	if err := d.SetStatus(taskID, model.StatusInProgress); err != nil {
		return false, err
	}
	return true, nil
}

// heraWorkerAwaitingCloseout reports whether a worker-bound task is in the
// terminal "awaiting coordinator close-out" state: either it carries
// meta:hera.ready_to_close=true (RollHeraWorkerToReview's done / clean-exit
// stamp) or any of its live worker roles has a terminal role-status (done or
// failed). Used by ReviveHeraWorkerToInProgress to refuse to un-roll a
// genuinely-finished worker. A role with no status row, or whose status is
// non-terminal (idle/working/blocked), does not count.
func (d *DB) heraWorkerAwaitingCloseout(taskID string) (bool, error) {
	meta, err := d.ListMeta(taskID, HeraMetaNamespace)
	if err != nil {
		return false, err
	}
	for _, e := range meta {
		if e.Key == HeraMetaKeyReadyToClose && e.Value == "true" {
			return true, nil
		}
	}
	bindings, err := d.ListHeraLiveBindingsByTask(taskID)
	if err != nil {
		return false, err
	}
	for _, b := range bindings {
		rs, err := d.HeraRoleStatusFor(b.RoleID)
		if err != nil {
			if errors.Is(err, ErrHeraNotFound) {
				continue // no status row yet — not terminal
			}
			return false, err
		}
		if rs.Status == HeraStatusDone || rs.Status == HeraStatusFailed {
			return true, nil
		}
	}
	return false, nil
}

// ClearHeraReadyToClose removes the meta:hera.ready_to_close mark on taskID —
// the inverse of the stamp RollHeraWorkerToReview sets when a worker reaches
// `done`. Stepping a worker's hera status back DOWN the ladder (out of `done`)
// uses this so the rail glyph reflects the new status instead of staying pinned
// to the review ✓ (ready_to_close wins over status in the glyph precedence, so
// an uncleared mark masks every subsequent step — BUG-024). It writes "false"
// rather than deleting the row, matching how the flag is read (== "true"); a
// task that never carried the flag is an idempotent no-op. Touches meta only —
// never the task's workflow status (owned by the session lifecycle).
func (d *DB) ClearHeraReadyToClose(taskID string) error {
	return d.SetMeta(taskID, HeraMetaNamespace, HeraMetaKeyReadyToClose, "false")
}

// UniqueHeraRoleName returns base unchanged when no ACTIVE role under orchID
// already uses it, else base-2, base-3, … until a free slot is found. Mirrors
// Hera's ops.uniqueWorkerName. Archived roles do NOT block (they don't occupy
// the idx_hera_roles_active_name partial unique index, so a fresh active role
// can reuse an archived sibling's name). An empty base defaults to "worker".
//
// The returned name is a best-effort pre-check computed under the DB mutex; the
// partial unique index is the actual race backstop, so two concurrent creates
// racing on the same computed name resolve deterministically (the loser's
// INSERT fails) rather than duplicating.
func (d *DB) UniqueHeraRoleName(orchID int64, base string) (string, error) {
	if base == "" {
		base = "worker"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		`SELECT name FROM hera_roles WHERE orchestrator_id=? AND archived_at IS NULL`, orchID)
	if err != nil {
		return "", fmt.Errorf("unique hera role name: %w", err)
	}
	defer rows.Close()
	used := make(map[string]bool)
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return "", fmt.Errorf("unique hera role name: scan: %w", err)
		}
		used[n] = true
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("unique hera role name: rows: %w", err)
	}
	if !used[base] {
		return base, nil
	}
	for i := 2; i <= len(used)+2; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !used[cand] {
			return cand, nil
		}
	}
	return fmt.Sprintf("%s-%d", base, len(used)+3), nil
}

// UniqueHeraOrchestratorName returns base when no ACTIVE orchestrator already
// holds it, otherwise the first free `base-N` suffix (N starting at 2). Only
// active orchestrators occupy names (CreateHeraOrchestrator's idempotency check
// keys on the active-name index), so an archived orchestrator never blocks
// reuse. Empty base defaults to "orchestrator". This lets the rail `n` key
// create a genuinely NEW top-level orchestrator instead of idempotently
// re-fetching an existing one with the same name.
func (d *DB) UniqueHeraOrchestratorName(base string) (string, error) {
	if base == "" {
		base = "orchestrator"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(`SELECT name FROM hera_orchestrators WHERE archived_at IS NULL`)
	if err != nil {
		return "", fmt.Errorf("unique hera orchestrator name: %w", err)
	}
	defer rows.Close()
	used := make(map[string]bool)
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return "", fmt.Errorf("unique hera orchestrator name: scan: %w", err)
		}
		used[n] = true
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("unique hera orchestrator name: rows: %w", err)
	}
	if !used[base] {
		return base, nil
	}
	for i := 2; i <= len(used)+2; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !used[cand] {
			return cand, nil
		}
	}
	return fmt.Sprintf("%s-%d", base, len(used)+3), nil
}

// HeraLiveBindingByRole returns the live binding for a role, or ErrHeraNotFound.
func (d *DB) HeraLiveBindingByRole(roleID int64) (*HeraBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	row := d.conn.QueryRow(
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM hera_bindings WHERE role_id=? AND ended_at IS NULL`, roleID)
	return scanHeraBinding(row)
}

// HeraLiveBindingByWorktree returns the single live binding for a worktree
// path. Returns ErrHeraNotFound when none and ErrHeraAmbiguous when 2+ (the
// multi-binding case).
func (d *DB) HeraLiveBindingByWorktree(worktreePath string) (*HeraBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraSingleLiveBinding(
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM hera_bindings WHERE worktree_path=? AND ended_at IS NULL`, worktreePath)
}

// ListHeraLiveBindings returns every live binding across all roles and
// orchestrators, oldest first. Used by startup-seed / reconciliation callers.
func (d *DB) ListHeraLiveBindings() ([]*HeraBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraListBindings(
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM hera_bindings WHERE ended_at IS NULL ORDER BY started_at ASC, id ASC`)
}

// ListHeraLatestBindings returns the single most-recent binding per role across
// all roles, regardless of liveness — one row per role, the one with the highest
// id (autoincrement, monotonic with creation order, so "highest id" == "latest
// binding"). Used to compute the structural rail bridge off a role's LATEST
// binding (live OR ended-but-not-torn-down), distinct from ListHeraLiveBindings
// which only sees live bindings. A live binding is always its role's latest (the
// partial unique index forbids two live bindings per role), so a live role's
// latest row carries its live task and an empty end_reason.
func (d *DB) ListHeraLatestBindings() ([]*HeraBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraListBindings(
		`SELECT b.id, b.role_id, b.orchestrator_id, b.argus_task_id, b.worktree_path, b.started_at, b.ended_at, b.end_reason
		 FROM hera_bindings b
		 JOIN (SELECT role_id, MAX(id) AS max_id FROM hera_bindings GROUP BY role_id) latest
		   ON latest.role_id = b.role_id AND latest.max_id = b.id
		 ORDER BY b.role_id ASC`)
}

// ListHeraBindingsByRole returns every binding for a role — live and ended —
// most recent first.
func (d *DB) ListHeraBindingsByRole(roleID int64) ([]*HeraBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraListBindings(
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM hera_bindings WHERE role_id=? ORDER BY started_at DESC, id DESC`, roleID)
}

// ListHeraBindingsByTask returns every binding pointing at the given argus task
// — live AND ended — most recent first. The `J` re-parent teardown (BUG-026)
// needs the full set so it can delete EVERY prior parent-link role of a
// coordinator's task by role id, not just the live ones; an ended link role
// left behind by the resync reconciler would otherwise force the next
// re-parent's de-collide into a duplicate "name-2" link.
func (d *DB) ListHeraBindingsByTask(taskID string) ([]*HeraBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.heraListBindings(
		`SELECT id, role_id, orchestrator_id, argus_task_id, worktree_path, started_at, ended_at, end_reason
		 FROM hera_bindings WHERE argus_task_id=? ORDER BY started_at DESC, id DESC`, taskID)
}

// heraSingleLiveBinding runs a live-binding query expected to match at most one
// row, mapping zero rows to ErrHeraNotFound and 2+ rows to ErrHeraAmbiguous.
// query is always a package-internal constant (callers above), never derived
// from user input.
func (d *DB) heraSingleLiveBinding(query string, args ...any) (*HeraBinding, error) {
	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query hera binding: %w", err)
	}
	defer rows.Close()
	var found *HeraBinding
	for rows.Next() {
		b, err := scanHeraBindingRows(rows)
		if err != nil {
			return nil, err
		}
		if found != nil {
			return nil, ErrHeraAmbiguous
		}
		found = b
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrHeraNotFound
	}
	return found, nil
}

func (d *DB) heraListBindings(query string, args ...any) ([]*HeraBinding, error) {
	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list hera bindings: %w", err)
	}
	defer rows.Close()
	var out []*HeraBinding
	for rows.Next() {
		b, err := scanHeraBindingRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func deriveHeraOrchestratorID(ex interface {
	QueryRow(query string, args ...any) *sql.Row
}, roleID int64) (int64, error) {
	var orchID int64
	err := ex.QueryRow(`SELECT orchestrator_id FROM hera_roles WHERE id=?`, roleID).Scan(&orchID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("derive orchestrator for role %d: %w", roleID, ErrHeraNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("derive orchestrator for role %d: %w", roleID, err)
	}
	return orchID, nil
}

func insertHeraBinding(ex execer, in CreateHeraBindingInput, now string) (*HeraBinding, error) {
	res, err := ex.Exec(
		`INSERT INTO hera_bindings (role_id, orchestrator_id, argus_task_id, worktree_path, started_at)
		 VALUES (?, ?, ?, ?, ?)`,
		in.RoleID, in.OrchestratorID, in.ArgusTaskID, in.WorktreePath, now)
	if err != nil {
		return nil, fmt.Errorf("insert hera binding: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("insert hera binding: last insert id: %w", err)
	}
	return &HeraBinding{
		ID:             id,
		RoleID:         in.RoleID,
		OrchestratorID: in.OrchestratorID,
		ArgusTaskID:    in.ArgusTaskID,
		WorktreePath:   in.WorktreePath,
		StartedAt:      parseTime(now),
	}, nil
}

// --- Role status ---

// UpsertHeraRoleStatus sets the status for a role, inserting or updating.
func (d *DB) UpsertHeraRoleStatus(roleID int64, status HeraRoleStatusValue) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.conn.Exec(
		`INSERT INTO hera_role_status (role_id, status, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(role_id) DO UPDATE SET status=excluded.status, updated_at=excluded.updated_at`,
		roleID, string(status), formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("upsert hera role status: %w", err)
	}
	return nil
}

// HeraRoleStatusFor returns the current status for a role, or ErrHeraNotFound
// when none is set.
func (d *DB) HeraRoleStatusFor(roleID int64) (*HeraRoleStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	row := d.conn.QueryRow(`SELECT role_id, status, updated_at FROM hera_role_status WHERE role_id=?`, roleID)
	var rs HeraRoleStatus
	var status, updatedAt string
	if err := row.Scan(&rs.RoleID, &status, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrHeraNotFound
		}
		return nil, fmt.Errorf("get hera role status: %w", err)
	}
	rs.Status = HeraRoleStatusValue(status)
	rs.UpdatedAt = parseTime(updatedAt)
	return &rs, nil
}

// --- shared flag-setter + scanners ---

// Existence probes for the zero-rows disambiguation in heraSetFlag. Constant
// literals (never concatenated from input) so the SQL is fully static.
const (
	heraOrchExistsProbe = `SELECT 1 FROM hera_orchestrators WHERE id=?`
	heraRoleExistsProbe = `SELECT 1 FROM hera_roles WHERE id=?`
)

// heraSetFlag runs an UPDATE that affects at most one row by id and maps a
// zero-rows result to either nil (the row exists but the guard short-circuited,
// i.e. an idempotent no-op) or ErrHeraNotFound (no such row). probeQuery is the
// constant existence probe for the target table (heraOrchExistsProbe /
// heraRoleExistsProbe), used only on the zero-rows path.
func (d *DB) heraSetFlag(query, probeQuery string, id int64, args ...any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	// args carry the timestamp(s) that precede id in the statement.
	full := append(append([]any{}, args...), id)
	res, err := d.conn.Exec(query, full...)
	if err != nil {
		return fmt.Errorf("hera set flag: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int64
		err := d.conn.QueryRow(probeQuery, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHeraNotFound
		}
		return err // nil when the row exists (idempotent no-op)
	}
	return nil
}

// rowScanner unifies *sql.Row and *sql.Rows for the scan helpers.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanHeraOrchestrator(s rowScanner) (*HeraOrchestrator, error) {
	var o HeraOrchestrator
	var createdAt, kanbanStatus string
	var archivedAt, pinnedAt, nukedAt, baseBranch sql.NullString
	if err := s.Scan(&o.ID, &o.Name, &createdAt, &archivedAt, &pinnedAt, &nukedAt, &baseBranch, &kanbanStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrHeraNotFound
		}
		return nil, fmt.Errorf("scan hera orchestrator: %w", err)
	}
	o.CreatedAt = parseTime(createdAt)
	o.ArchivedAt = nullTimePtr(archivedAt)
	o.PinnedAt = nullTimePtr(pinnedAt)
	o.NukedAt = nullTimePtr(nukedAt)
	o.BaseBranch = baseBranch.String
	o.KanbanStatus = HeraKanbanStatus(kanbanStatus)
	return &o, nil
}

func scanHeraRole(s rowScanner) (*HeraRole, error) {
	var r HeraRole
	var kind, createdAt string
	var archivedAt, pinnedAt, nukedAt, nodeKind, cancelledAt, archetype sql.NullString
	if err := s.Scan(&r.ID, &r.OrchestratorID, &r.Name, &kind, &r.ArgusProject, &r.Prompt,
		&createdAt, &archivedAt, &pinnedAt, &nukedAt, &nodeKind, &cancelledAt, &archetype); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrHeraNotFound
		}
		return nil, fmt.Errorf("scan hera role: %w", err)
	}
	r.Kind = HeraRoleKind(kind)
	r.CreatedAt = parseTime(createdAt)
	r.ArchivedAt = nullTimePtr(archivedAt)
	r.PinnedAt = nullTimePtr(pinnedAt)
	r.NukedAt = nullTimePtr(nukedAt)
	// NULL or absent node_kind → HeraNodeKindWorker (default). Only "subcoord"
	// is stored explicitly; worker nodes store NULL to keep the column sparse.
	if nodeKind.Valid && nodeKind.String == string(HeraNodeKindSubCoord) {
		r.NodeKind = HeraNodeKindSubCoord
	} else {
		r.NodeKind = HeraNodeKindWorker
	}
	r.CancelledAt = nullTimePtr(cancelledAt)
	if archetype.Valid {
		r.Archetype = archetype.String
	}
	return &r, nil
}

func scanHeraBinding(s rowScanner) (*HeraBinding, error) {
	b, err := scanHeraBindingInto(s)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrHeraNotFound
	}
	return b, err
}

func scanHeraBindingRows(s rowScanner) (*HeraBinding, error) {
	return scanHeraBindingInto(s)
}

func scanHeraBindingInto(s rowScanner) (*HeraBinding, error) {
	var b HeraBinding
	var startedAt string
	var endedAt, endReason sql.NullString
	if err := s.Scan(&b.ID, &b.RoleID, &b.OrchestratorID, &b.ArgusTaskID, &b.WorktreePath,
		&startedAt, &endedAt, &endReason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("scan hera binding: %w", err)
	}
	b.StartedAt = parseTime(startedAt)
	b.EndedAt = nullTimePtr(endedAt)
	if endReason.Valid {
		b.EndReason = endReason.String
	}
	return &b, nil
}

func nullTimePtr(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t := parseTime(ns.String)
	return &t
}
