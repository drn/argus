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
)

// HeraRoleKind enumerates the valid kinds for a hera_roles row.
type HeraRoleKind string

const (
	HeraKindCoordinator HeraRoleKind = "coordinator"
	HeraKindWorker      HeraRoleKind = "worker"
	HeraKindFreelance   HeraRoleKind = "freelance"
)

// HeraRoleStatusValue enumerates the valid hera_role_status strings.
type HeraRoleStatusValue string

const (
	HeraStatusIdle    HeraRoleStatusValue = "idle"
	HeraStatusWorking HeraRoleStatusValue = "working"
	HeraStatusBlocked HeraRoleStatusValue = "blocked"
	HeraStatusDone    HeraRoleStatusValue = "done"
)

// HeraOrchestrator is one coordination group. ArchivedAt is non-nil for
// archived rows; PinnedAt is non-nil for pinned rows. Pin and archive are
// mutually exclusive — the Pin/Archive verbs clear the other.
type HeraOrchestrator struct {
	ID         int64
	Name       string
	CreatedAt  time.Time
	ArchivedAt *time.Time
	PinnedAt   *time.Time
}

// HeraRole is a participant in an orchestrator. Prompt is the only free-form
// field. ArchivedAt / PinnedAt mirror HeraOrchestrator.
type HeraRole struct {
	ID             int64
	OrchestratorID int64
	Name           string
	Kind           HeraRoleKind
	ArgusProject   string
	Prompt         string
	CreatedAt      time.Time
	ArchivedAt     *time.Time
	PinnedAt       *time.Time
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
type CreateHeraRoleInput struct {
	OrchestratorID int64
	Name           string
	Kind           HeraRoleKind
	ArgusProject   string
	Prompt         string
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
// (idempotent). An archived row with the same name does NOT block creation —
// a fresh active row is inserted.
func (d *DB) CreateHeraOrchestrator(name string) (*HeraOrchestrator, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if existing, err := d.heraOrchestratorByActiveName(name); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrHeraNotFound) {
		return nil, err
	}

	now := formatTime(time.Now())
	res, err := d.conn.Exec(`INSERT INTO hera_orchestrators (name, created_at) VALUES (?, ?)`, name, now)
	if err != nil {
		return nil, fmt.Errorf("create hera orchestrator: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create hera orchestrator: last insert id: %w", err)
	}
	return &HeraOrchestrator{ID: id, Name: name, CreatedAt: parseTime(now)}, nil
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

	query := `SELECT id, name, created_at, archived_at, pinned_at FROM hera_orchestrators`
	if !includeArchived {
		query += ` WHERE archived_at IS NULL`
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
	row := d.conn.QueryRow(`SELECT id, name, created_at, archived_at, pinned_at FROM hera_orchestrators WHERE id=?`, id)
	return scanHeraOrchestrator(row)
}

func (d *DB) heraOrchestratorByActiveName(name string) (*HeraOrchestrator, error) {
	row := d.conn.QueryRow(
		`SELECT id, name, created_at, archived_at, pinned_at FROM hera_orchestrators WHERE name=? AND archived_at IS NULL`,
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

	query := `SELECT id, orchestrator_id, name, kind, argus_project, prompt, created_at, archived_at, pinned_at
	          FROM hera_roles WHERE orchestrator_id=?`
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
		`SELECT id, orchestrator_id, name, kind, argus_project, prompt, created_at, archived_at, pinned_at
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
		`SELECT id, orchestrator_id, name, kind, argus_project, prompt, created_at, archived_at, pinned_at
		 FROM hera_roles WHERE id=?`, id)
	return scanHeraRole(row)
}

func (d *DB) heraRoleByActiveName(orchID int64, name string) (*HeraRole, error) {
	row := d.conn.QueryRow(
		`SELECT id, orchestrator_id, name, kind, argus_project, prompt, created_at, archived_at, pinned_at
		 FROM hera_roles WHERE orchestrator_id=? AND name=? AND archived_at IS NULL`, orchID, name)
	return scanHeraRole(row)
}

func insertHeraRole(ex execer, in CreateHeraRoleInput, now string) (*HeraRole, error) {
	res, err := ex.Exec(
		`INSERT INTO hera_roles (orchestrator_id, name, kind, argus_project, prompt, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		in.OrchestratorID, in.Name, string(in.Kind), in.ArgusProject, in.Prompt, now)
	if err != nil {
		return nil, fmt.Errorf("insert hera role: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("insert hera role: last insert id: %w", err)
	}
	return &HeraRole{
		ID:             id,
		OrchestratorID: in.OrchestratorID,
		Name:           in.Name,
		Kind:           in.Kind,
		ArgusProject:   in.ArgusProject,
		Prompt:         in.Prompt,
		CreatedAt:      parseTime(now),
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
	if mErr := d.SetMeta(taskID, HeraMetaNamespace, HeraMetaKeyReadyToClose, "true"); mErr != nil {
		slog.Warn("[hera] ready_to_close stamp failed (flip stands)", "task", taskID, "err", mErr)
	}
	return true, nil
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
	var createdAt string
	var archivedAt, pinnedAt sql.NullString
	if err := s.Scan(&o.ID, &o.Name, &createdAt, &archivedAt, &pinnedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrHeraNotFound
		}
		return nil, fmt.Errorf("scan hera orchestrator: %w", err)
	}
	o.CreatedAt = parseTime(createdAt)
	o.ArchivedAt = nullTimePtr(archivedAt)
	o.PinnedAt = nullTimePtr(pinnedAt)
	return &o, nil
}

func scanHeraRole(s rowScanner) (*HeraRole, error) {
	var r HeraRole
	var kind, createdAt string
	var archivedAt, pinnedAt sql.NullString
	if err := s.Scan(&r.ID, &r.OrchestratorID, &r.Name, &kind, &r.ArgusProject, &r.Prompt,
		&createdAt, &archivedAt, &pinnedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrHeraNotFound
		}
		return nil, fmt.Errorf("scan hera role: %w", err)
	}
	r.Kind = HeraRoleKind(kind)
	r.CreatedAt = parseTime(createdAt)
	r.ArchivedAt = nullTimePtr(archivedAt)
	r.PinnedAt = nullTimePtr(pinnedAt)
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
