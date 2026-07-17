package db

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// heraTestDB opens a fresh in-memory DB with FK enforcement on (OpenInMemory
// sets the pragma) and registers cleanup.
func heraTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestCreateHeraTables_MigratesPreNukedAtDB pins the BUG-022 migration-order fix:
// a DB created before the nuked_at column (hera_orchestrators / hera_roles lacking
// it) must be migrated in place, not error with "no such column: nuked_at". The
// nuked_at ADD COLUMN ALTERs run BEFORE the DDL block — the DDL builds
// `CREATE INDEX ... (nuked_at)` and the `CREATE TABLE IF NOT EXISTS` is a no-op on
// the pre-existing table, so the column must already be present by then.
func TestCreateHeraTables_MigratesPreNukedAtDB(t *testing.T) {
	d := heraTestDB(t)

	// Reset to the pre-BUG-022 shape: drop the current tables and recreate the two
	// that gained nuked_at WITHOUT the column (and without the nuked indexes). FK
	// enforcement is off for the destructive reset so dropping parent tables that
	// child tables (hera_bindings, hera_role_status) still reference doesn't error.
	_, err := d.conn.Exec(`
		PRAGMA foreign_keys=off;
		DROP TABLE IF EXISTS hera_role_status;
		DROP TABLE IF EXISTS hera_bindings;
		DROP TABLE IF EXISTS tree_read_cursors;
		DROP TABLE IF EXISTS hera_roles;
		DROP TABLE IF EXISTS hera_orchestrators;
		CREATE TABLE hera_orchestrators (
			id          INTEGER PRIMARY KEY,
			name        TEXT NOT NULL,
			created_at  TEXT NOT NULL,
			archived_at TEXT,
			pinned_at   TEXT
		);
		CREATE TABLE hera_roles (
			id              INTEGER PRIMARY KEY,
			orchestrator_id INTEGER NOT NULL,
			name            TEXT NOT NULL,
			kind            TEXT NOT NULL,
			argus_project   TEXT NOT NULL,
			prompt          TEXT NOT NULL DEFAULT '',
			created_at      TEXT NOT NULL,
			archived_at     TEXT,
			pinned_at       TEXT
		);
		PRAGMA foreign_keys=on;
	`)
	testutil.NoError(t, err)

	// The actual regression: this used to fail with "no such column: nuked_at".
	testutil.NoError(t, d.createHeraTables())

	// nuked_at is now usable on both tables.
	_, err = d.conn.Exec(`SELECT nuked_at FROM hera_orchestrators`)
	testutil.NoError(t, err)
	_, err = d.conn.Exec(`SELECT nuked_at FROM hera_roles`)
	testutil.NoError(t, err)

	// base_branch (add-hera-plan-base-branch) is added by the same idempotent
	// ALTER migration on a pre-existing orchestrators table that never had it.
	// The legacy shape above omits the column, so this is a real regression guard.
	_, err = d.conn.Exec(`SELECT base_branch FROM hera_orchestrators`)
	testutil.NoError(t, err)

	// cancelled_at (make-hera-plan-living) is added by the same idempotent ALTER
	// migration on a pre-existing hera_roles table that never had it.
	_, err = d.conn.Exec(`SELECT cancelled_at FROM hera_roles`)
	testutil.NoError(t, err)
}

// mkOrch creates an active orchestrator and fails the test on error.
func mkOrch(t *testing.T, d *DB, name string) *HeraOrchestrator {
	t.Helper()
	o, err := d.CreateHeraOrchestrator(name, "")
	testutil.NoError(t, err)
	return o
}

// mkRole creates an active role under orch and fails the test on error.
func mkRole(t *testing.T, d *DB, orchID int64, name string, kind HeraRoleKind) *HeraRole {
	t.Helper()
	r, err := d.CreateHeraRole(CreateHeraRoleInput{
		OrchestratorID: orchID,
		Name:           name,
		Kind:           kind,
		ArgusProject:   "proj",
		Prompt:         "do the thing",
	})
	testutil.NoError(t, err)
	return r
}

func TestHeraOrchestratorCRUD(t *testing.T) {
	t.Run("create get list", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "alpha")
		testutil.Equal(t, o.Name, "alpha")
		testutil.Nil(t, o.ArchivedAt)
		testutil.Nil(t, o.PinnedAt)

		got, err := d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.Name, "alpha")

		byName, err := d.HeraOrchestratorByName("alpha")
		testutil.NoError(t, err)
		testutil.Equal(t, byName.ID, o.ID)

		list, err := d.ListHeraOrchestrators(false)
		testutil.NoError(t, err)
		testutil.Equal(t, len(list), 1)
	})

	t.Run("create is idempotent on active name", func(t *testing.T) {
		d := heraTestDB(t)
		a := mkOrch(t, d, "dup")
		b := mkOrch(t, d, "dup")
		testutil.Equal(t, a.ID, b.ID)
		list, err := d.ListHeraOrchestrators(false)
		testutil.NoError(t, err)
		testutil.Equal(t, len(list), 1)
	})

	t.Run("base branch round-trips through all read paths", func(t *testing.T) {
		d := heraTestDB(t)
		o, err := d.CreateHeraOrchestrator("based", "feature/seed")
		testutil.NoError(t, err)
		testutil.Equal(t, o.BaseBranch, "feature/seed")

		got, err := d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.BaseBranch, "feature/seed")

		byName, err := d.HeraOrchestratorByName("based")
		testutil.NoError(t, err)
		testutil.Equal(t, byName.BaseBranch, "feature/seed")

		list, err := d.ListHeraOrchestrators(false)
		testutil.NoError(t, err)
		testutil.Equal(t, len(list), 1)
		testutil.Equal(t, list[0].BaseBranch, "feature/seed")
	})

	t.Run("base branch defaults to empty when none supplied", func(t *testing.T) {
		d := heraTestDB(t)
		o, err := d.CreateHeraOrchestrator("plain", "")
		testutil.NoError(t, err)
		testutil.Equal(t, o.BaseBranch, "")

		got, err := d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.BaseBranch, "")
	})

	t.Run("not found by id and name", func(t *testing.T) {
		d := heraTestDB(t)
		_, err := d.HeraOrchestrator(999)
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		_, err = d.HeraOrchestratorByName("ghost")
		testutil.ErrorIs(t, err, ErrHeraNotFound)
	})

	t.Run("archived name can be reused by fresh active row", func(t *testing.T) {
		d := heraTestDB(t)
		first := mkOrch(t, d, "reuse")
		testutil.NoError(t, d.ArchiveHeraOrchestrator(first.ID))

		// Archived row invisible to active-name lookup.
		_, err := d.HeraOrchestratorByName("reuse")
		testutil.ErrorIs(t, err, ErrHeraNotFound)

		// A new active row with the same name is allowed and is distinct.
		second := mkOrch(t, d, "reuse")
		testutil.Equal(t, second.ID != first.ID, true)

		all, err := d.ListHeraOrchestrators(true)
		testutil.NoError(t, err)
		testutil.Equal(t, len(all), 2)
		active, err := d.ListHeraOrchestrators(false)
		testutil.NoError(t, err)
		testutil.Equal(t, len(active), 1)
	})

	t.Run("rename success and conflict", func(t *testing.T) {
		d := heraTestDB(t)
		a := mkOrch(t, d, "one")
		mkOrch(t, d, "two")

		// No-op rename to same name.
		testutil.NoError(t, d.RenameHeraOrchestrator(a.ID, "one"))
		// Conflict against active "two".
		err := d.RenameHeraOrchestrator(a.ID, "two")
		testutil.ErrorIs(t, err, ErrHeraNameConflict)
		// Successful rename to a free name.
		testutil.NoError(t, d.RenameHeraOrchestrator(a.ID, "three"))
		got, err := d.HeraOrchestrator(a.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.Name, "three")
		// Rename a missing row.
		testutil.ErrorIs(t, d.RenameHeraOrchestrator(999, "x"), ErrHeraNotFound)
	})

	t.Run("delete removes row", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "del")
		testutil.NoError(t, d.DeleteHeraOrchestrator(o.ID))
		_, err := d.HeraOrchestrator(o.ID)
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		testutil.ErrorIs(t, d.DeleteHeraOrchestrator(o.ID), ErrHeraNotFound)
	})
}

func TestHeraOrchestratorPinArchiveExclusivity(t *testing.T) {
	t.Run("pin clears archive, archive clears pin", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "px")

		testutil.NoError(t, d.PinHeraOrchestrator(o.ID))
		got, err := d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.PinnedAt != nil, true)
		testutil.Nil(t, got.ArchivedAt)

		// Archiving a pinned row clears the pin.
		testutil.NoError(t, d.ArchiveHeraOrchestrator(o.ID))
		got, err = d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.ArchivedAt != nil, true)
		testutil.Nil(t, got.PinnedAt)

		// Pinning an archived row clears the archive (and unarchives).
		testutil.NoError(t, d.PinHeraOrchestrator(o.ID))
		got, err = d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.PinnedAt != nil, true)
		testutil.Nil(t, got.ArchivedAt)
	})

	t.Run("idempotent pin preserves original timestamp", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "ipin")
		testutil.NoError(t, d.PinHeraOrchestrator(o.ID))
		first, err := d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
		testutil.NoError(t, d.PinHeraOrchestrator(o.ID))
		second, err := d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, second.PinnedAt.Equal(*first.PinnedAt), true)
	})

	t.Run("unarchive and unpin idempotent + not found", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "u")
		// Unarchive an already-active row: no-op, no error.
		testutil.NoError(t, d.UnarchiveHeraOrchestrator(o.ID))
		// Unpin an already-unpinned row: no-op, no error.
		testutil.NoError(t, d.UnpinHeraOrchestrator(o.ID))
		// Missing rows.
		testutil.ErrorIs(t, d.ArchiveHeraOrchestrator(999), ErrHeraNotFound)
		testutil.ErrorIs(t, d.UnarchiveHeraOrchestrator(999), ErrHeraNotFound)
		testutil.ErrorIs(t, d.PinHeraOrchestrator(999), ErrHeraNotFound)
		testutil.ErrorIs(t, d.UnpinHeraOrchestrator(999), ErrHeraNotFound)
	})

	t.Run("re-archive preserves original archived_at", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "rear")
		testutil.NoError(t, d.ArchiveHeraOrchestrator(o.ID))
		first, err := d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
		testutil.NoError(t, d.ArchiveHeraOrchestrator(o.ID)) // idempotent no-op
		second, err := d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, second.ArchivedAt.Equal(*first.ArchivedAt), true)
	})
}

func TestHeraRoleCRUD(t *testing.T) {
	t.Run("create get list by orchestrator", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		coord := mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		mkRole(t, d, o.ID, "w2", HeraKindWorker)
		mkRole(t, d, o.ID, "w1", HeraKindWorker)
		mkRole(t, d, o.ID, "free", HeraKindFreelance)

		got, err := d.HeraRole(coord.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.Kind, HeraKindCoordinator)
		testutil.Equal(t, got.Prompt, "do the thing")

		byName, err := d.HeraRoleByName(o.ID, "w1")
		testutil.NoError(t, err)
		testutil.Equal(t, byName.Name, "w1")

		// Ordered coordinator, worker (by name), freelance.
		list, err := d.ListHeraRoles(o.ID, false)
		testutil.NoError(t, err)
		names := make([]string, len(list))
		for i, r := range list {
			names[i] = r.Name
		}
		testutil.DeepEqual(t, names, []string{"coord", "w1", "w2", "free"})
	})

	t.Run("create idempotent same kind, conflict different kind", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		a := mkRole(t, d, o.ID, "r", HeraKindWorker)
		// Same (orch,name,kind) returns existing; prompt ignored.
		b, err := d.CreateHeraRole(CreateHeraRoleInput{
			OrchestratorID: o.ID, Name: "r", Kind: HeraKindWorker, ArgusProject: "other", Prompt: "ignored",
		})
		testutil.NoError(t, err)
		testutil.Equal(t, b.ID, a.ID)
		testutil.Equal(t, b.Prompt, "do the thing") // original preserved

		// Different kind is a conflict.
		_, err = d.CreateHeraRole(CreateHeraRoleInput{
			OrchestratorID: o.ID, Name: "r", Kind: HeraKindCoordinator, ArgusProject: "p",
		})
		testutil.ErrorIs(t, err, ErrHeraRoleKindConflict)
	})

	t.Run("list by kind", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		mkRole(t, d, o.ID, "coord", HeraKindCoordinator)
		mkRole(t, d, o.ID, "wa", HeraKindWorker)
		mkRole(t, d, o.ID, "wb", HeraKindWorker)
		workers, err := d.ListHeraRolesByKind(o.ID, HeraKindWorker)
		testutil.NoError(t, err)
		testutil.Equal(t, len(workers), 2)
		coords, err := d.ListHeraRolesByKind(o.ID, HeraKindCoordinator)
		testutil.NoError(t, err)
		testutil.Equal(t, len(coords), 1)
	})

	t.Run("archived name reuse + inclusive list", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		first := mkRole(t, d, o.ID, "dup", HeraKindWorker)
		testutil.NoError(t, d.ArchiveHeraRole(first.ID))
		_, err := d.HeraRoleByName(o.ID, "dup")
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		second := mkRole(t, d, o.ID, "dup", HeraKindWorker)
		testutil.Equal(t, second.ID != first.ID, true)

		all, err := d.ListHeraRoles(o.ID, true)
		testutil.NoError(t, err)
		testutil.Equal(t, len(all), 2)
	})

	t.Run("same role name across different orchestrators", func(t *testing.T) {
		d := heraTestDB(t)
		o1 := mkOrch(t, d, "o1")
		o2 := mkOrch(t, d, "o2")
		mkRole(t, d, o1.ID, "coord", HeraKindCoordinator)
		mkRole(t, d, o2.ID, "coord", HeraKindCoordinator)
		r1, err := d.HeraRoleByName(o1.ID, "coord")
		testutil.NoError(t, err)
		r2, err := d.HeraRoleByName(o2.ID, "coord")
		testutil.NoError(t, err)
		testutil.Equal(t, r1.ID != r2.ID, true)
	})

	t.Run("rename, delete, not found", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		a := mkRole(t, d, o.ID, "a", HeraKindWorker)
		mkRole(t, d, o.ID, "b", HeraKindWorker)
		testutil.NoError(t, d.RenameHeraRole(a.ID, "a")) // no-op
		testutil.ErrorIs(t, d.RenameHeraRole(a.ID, "b"), ErrHeraNameConflict)
		testutil.NoError(t, d.RenameHeraRole(a.ID, "c"))
		testutil.ErrorIs(t, d.RenameHeraRole(999, "x"), ErrHeraNotFound)

		_, err := d.HeraRole(999)
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		testutil.NoError(t, d.DeleteHeraRole(a.ID))
		testutil.ErrorIs(t, d.DeleteHeraRole(a.ID), ErrHeraNotFound)
	})

	t.Run("role pin archive exclusivity", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		r := mkRole(t, d, o.ID, "r", HeraKindWorker)
		testutil.NoError(t, d.PinHeraRole(r.ID))
		got, err := d.HeraRole(r.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.PinnedAt != nil, true)
		testutil.NoError(t, d.ArchiveHeraRole(r.ID))
		got, err = d.HeraRole(r.ID)
		testutil.NoError(t, err)
		testutil.Nil(t, got.PinnedAt)
		testutil.Equal(t, got.ArchivedAt != nil, true)
		testutil.NoError(t, d.UnarchiveHeraRole(r.ID))
		testutil.NoError(t, d.UnpinHeraRole(r.ID))
		// Missing role flag ops.
		testutil.ErrorIs(t, d.ArchiveHeraRole(999), ErrHeraNotFound)
		testutil.ErrorIs(t, d.UnarchiveHeraRole(999), ErrHeraNotFound)
		testutil.ErrorIs(t, d.PinHeraRole(999), ErrHeraNotFound)
		testutil.ErrorIs(t, d.UnpinHeraRole(999), ErrHeraNotFound)
	})
}

func TestHeraBindingLifecycle(t *testing.T) {
	t.Run("create lookups and end", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		r := mkRole(t, d, o.ID, "r", HeraKindWorker)
		b, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: r.ID, ArgusTaskID: "task-1", WorktreePath: "/wt/1",
		})
		testutil.NoError(t, err)
		testutil.Equal(t, b.OrchestratorID, o.ID) // derived from role
		testutil.Nil(t, b.EndedAt)

		byTask, err := d.HeraLiveBindingByTask("task-1")
		testutil.NoError(t, err)
		testutil.Equal(t, byTask.ID, b.ID)

		byTO, err := d.HeraLiveBindingByTaskAndOrchestrator("task-1", o.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, byTO.ID, b.ID)

		byRole, err := d.HeraLiveBindingByRole(r.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, byRole.ID, b.ID)

		byWt, err := d.HeraLiveBindingByWorktree("/wt/1")
		testutil.NoError(t, err)
		testutil.Equal(t, byWt.ID, b.ID)

		live, err := d.ListHeraLiveBindings()
		testutil.NoError(t, err)
		testutil.Equal(t, len(live), 1)

		// End it.
		testutil.NoError(t, d.EndHeraBinding(b.ID, "manual"))
		_, err = d.HeraLiveBindingByTask("task-1")
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		_, err = d.HeraLiveBindingByRole(r.ID)
		testutil.ErrorIs(t, err, ErrHeraNotFound)

		// Ended binding still listed in full history.
		hist, err := d.ListHeraBindingsByRole(r.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, len(hist), 1)
		testutil.Equal(t, hist[0].EndReason, "manual")
		testutil.Equal(t, hist[0].EndedAt != nil, true)

		// Ending again (no live row) is ErrHeraNotFound.
		testutil.ErrorIs(t, d.EndHeraBinding(b.ID, "again"), ErrHeraNotFound)
	})

	t.Run("explicit orchestrator id is honored", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		r := mkRole(t, d, o.ID, "r", HeraKindWorker)
		b, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: r.ID, OrchestratorID: o.ID, ArgusTaskID: "t", WorktreePath: "/w",
		})
		testutil.NoError(t, err)
		testutil.Equal(t, b.OrchestratorID, o.ID)
	})

	t.Run("derive orchestrator for missing role errors", func(t *testing.T) {
		d := heraTestDB(t)
		_, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: 999, ArgusTaskID: "t", WorktreePath: "/w",
		})
		testutil.ErrorIs(t, err, ErrHeraNotFound)
	})

	t.Run("lookups not found", func(t *testing.T) {
		d := heraTestDB(t)
		_, err := d.HeraLiveBindingByTask("nope")
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		_, err = d.HeraLiveBindingByTaskAndOrchestrator("nope", 1)
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		_, err = d.HeraLiveBindingByRole(123)
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		_, err = d.HeraLiveBindingByWorktree("/nope")
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		empty, err := d.ListHeraLiveBindingsByTask("nope")
		testutil.NoError(t, err)
		testutil.Equal(t, len(empty), 0)
	})
}

func TestListHeraBindingsByTask(t *testing.T) {
	t.Run("returns live and ended for a task, newest first", func(t *testing.T) {
		d := heraTestDB(t)
		oa := mkOrch(t, d, "A")
		ob := mkOrch(t, d, "B")
		ra := mkRole(t, d, oa.ID, "ra", HeraKindWorker)
		rb := mkRole(t, d, ob.ID, "rb", HeraKindWorker)

		// First binding under A, then end it.
		b1, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: ra.ID, ArgusTaskID: "shared", WorktreePath: "/wt",
		})
		testutil.NoError(t, err)
		testutil.NoError(t, d.EndHeraBinding(b1.ID, "reparented"))
		// Second binding under B, still live.
		b2, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: rb.ID, ArgusTaskID: "shared", WorktreePath: "/wt",
		})
		testutil.NoError(t, err)

		all, err := d.ListHeraBindingsByTask("shared")
		testutil.NoError(t, err)
		testutil.Equal(t, len(all), 2) // live AND ended
		// Newest first (id DESC tiebreak): b2 before b1.
		testutil.Equal(t, all[0].ID, b2.ID)
		testutil.Equal(t, all[1].ID, b1.ID)
		testutil.Equal(t, all[1].EndReason, "reparented")
	})

	t.Run("empty for unknown task", func(t *testing.T) {
		d := heraTestDB(t)
		got, err := d.ListHeraBindingsByTask("nope")
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 0)
	})
}

func TestHeraMultiBinding(t *testing.T) {
	t.Run("same task live in two orchestrators is allowed", func(t *testing.T) {
		d := heraTestDB(t)
		oa := mkOrch(t, d, "A")
		ob := mkOrch(t, d, "B")
		ra := mkRole(t, d, oa.ID, "worker", HeraKindWorker)
		rb := mkRole(t, d, ob.ID, "coord", HeraKindCoordinator)

		_, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: ra.ID, ArgusTaskID: "shared", WorktreePath: "/wt/a",
		})
		testutil.NoError(t, err)
		// Worker in A and coordinator in B simultaneously — different orchestrator,
		// so the per-(task,orchestrator) unique index permits it.
		_, err = d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: rb.ID, ArgusTaskID: "shared", WorktreePath: "/wt/b",
		})
		testutil.NoError(t, err)

		all, err := d.ListHeraLiveBindingsByTask("shared")
		testutil.NoError(t, err)
		testutil.Equal(t, len(all), 2)

		// The orchestrator-agnostic single lookup is now ambiguous.
		_, err = d.HeraLiveBindingByTask("shared")
		testutil.ErrorIs(t, err, ErrHeraAmbiguous)

		// Per-orchestrator lookups disambiguate.
		inA, err := d.HeraLiveBindingByTaskAndOrchestrator("shared", oa.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, inA.OrchestratorID, oa.ID)
		inB, err := d.HeraLiveBindingByTaskAndOrchestrator("shared", ob.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, inB.OrchestratorID, ob.ID)
	})

	t.Run("second live binding in SAME orchestrator rejected", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		r1 := mkRole(t, d, o.ID, "r1", HeraKindWorker)
		r2 := mkRole(t, d, o.ID, "r2", HeraKindWorker)
		_, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: r1.ID, ArgusTaskID: "t", WorktreePath: "/w1",
		})
		testutil.NoError(t, err)
		// Same (task, orchestrator) with a different role/worktree still violates
		// the per-(task,orchestrator) live-uniqueness index.
		_, err = d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: r2.ID, ArgusTaskID: "t", WorktreePath: "/w2",
		})
		testutil.Equal(t, err != nil, true)
	})

	t.Run("one live binding per role enforced on double-bind", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		r := mkRole(t, d, o.ID, "r", HeraKindWorker)
		_, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: r.ID, ArgusTaskID: "t1", WorktreePath: "/w1",
		})
		testutil.NoError(t, err)
		// Binding the same role to a second task violates the per-role unique index.
		_, err = d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: r.ID, ArgusTaskID: "t2", WorktreePath: "/w2",
		})
		testutil.Equal(t, err != nil, true)
	})

	t.Run("worktree live-uniqueness per orchestrator", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		r1 := mkRole(t, d, o.ID, "r1", HeraKindWorker)
		r2 := mkRole(t, d, o.ID, "r2", HeraKindWorker)
		_, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: r1.ID, ArgusTaskID: "t1", WorktreePath: "/shared",
		})
		testutil.NoError(t, err)
		// Same worktree, same orchestrator, different task → rejected.
		_, err = d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: r2.ID, ArgusTaskID: "t2", WorktreePath: "/shared",
		})
		testutil.Equal(t, err != nil, true)
	})

	t.Run("worktree ambiguous across orchestrators", func(t *testing.T) {
		d := heraTestDB(t)
		oa := mkOrch(t, d, "A")
		ob := mkOrch(t, d, "B")
		ra := mkRole(t, d, oa.ID, "r", HeraKindWorker)
		rb := mkRole(t, d, ob.ID, "r", HeraKindWorker)
		_, err := d.CreateHeraBinding(CreateHeraBindingInput{RoleID: ra.ID, ArgusTaskID: "ta", WorktreePath: "/shared"})
		testutil.NoError(t, err)
		_, err = d.CreateHeraBinding(CreateHeraBindingInput{RoleID: rb.ID, ArgusTaskID: "tb", WorktreePath: "/shared"})
		testutil.NoError(t, err)
		_, err = d.HeraLiveBindingByWorktree("/shared")
		testutil.ErrorIs(t, err, ErrHeraAmbiguous)
	})
}

func TestHeraRoleWithBindingTransaction(t *testing.T) {
	t.Run("commits role and binding together", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		role, binding, err := d.CreateHeraRoleWithBinding(CreateHeraRoleInput{
			OrchestratorID: o.ID, Name: "born-bound", Kind: HeraKindWorker, ArgusProject: "p", Prompt: "go",
		}, "task-x", "/wt/x")
		testutil.NoError(t, err)
		testutil.Equal(t, binding.RoleID, role.ID)
		testutil.Equal(t, binding.OrchestratorID, o.ID)

		got, err := d.HeraRoleByName(o.ID, "born-bound")
		testutil.NoError(t, err)
		testutil.Equal(t, got.ID, role.ID)
		live, err := d.HeraLiveBindingByRole(role.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, live.ID, binding.ID)
	})

	t.Run("rolls back role when binding insert fails", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		// Occupy (task, orchestrator) so the combined create's binding insert
		// violates the per-(task,orchestrator) live-uniqueness index.
		seed := mkRole(t, d, o.ID, "seed", HeraKindWorker)
		_, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: seed.ID, ArgusTaskID: "occupied", WorktreePath: "/wt/seed",
		})
		testutil.NoError(t, err)

		rolesBefore, err := d.ListHeraRoles(o.ID, true)
		testutil.NoError(t, err)

		_, _, err = d.CreateHeraRoleWithBinding(CreateHeraRoleInput{
			OrchestratorID: o.ID, Name: "doomed", Kind: HeraKindWorker, ArgusProject: "p",
		}, "occupied", "/wt/doomed")
		testutil.Equal(t, err != nil, true)

		// The "doomed" role must NOT exist — the whole tx rolled back.
		_, err = d.HeraRoleByName(o.ID, "doomed")
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		rolesAfter, err := d.ListHeraRoles(o.ID, true)
		testutil.NoError(t, err)
		testutil.Equal(t, len(rolesAfter), len(rolesBefore))
	})
}

func TestMoveHeraBinding(t *testing.T) {
	t.Run("happy path ends old binding and creates new one", func(t *testing.T) {
		d := heraTestDB(t)
		oa := mkOrch(t, d, "A")
		ob := mkOrch(t, d, "B")
		_, oldBinding, err := d.CreateHeraRoleWithBinding(CreateHeraRoleInput{
			OrchestratorID: oa.ID, Name: "w1", Kind: HeraKindWorker, ArgusProject: "p",
		}, "task-x", "/wt/x")
		testutil.NoError(t, err)

		result, err := d.MoveHeraBinding(oldBinding.ID, CreateHeraRoleInput{
			OrchestratorID: ob.ID, Name: "w2", Kind: HeraKindWorker, ArgusProject: "p",
		}, "task-x", "/wt/x")
		testutil.NoError(t, err)
		testutil.Equal(t, result.OldOrchestratorName, "A")
		testutil.Equal(t, result.OldRoleName, "w1")
		testutil.Equal(t, result.NewRole.Name, "w2")
		testutil.Equal(t, result.NewBinding.OrchestratorID, ob.ID)

		// Old binding is ended with end_reason "moved" — unreachable as live.
		old, err := d.HeraRole(oldBinding.RoleID) // role itself is untouched (not archived)
		testutil.NoError(t, err)
		testutil.Nil(t, old.ArchivedAt)
		_, err = d.HeraLiveBindingByRole(oldBinding.RoleID)
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		hist, err := d.ListHeraBindingsByRole(oldBinding.RoleID)
		testutil.NoError(t, err)
		testutil.Equal(t, len(hist), 1)
		testutil.Equal(t, hist[0].EndReason, HeraEndReasonMoved)
		testutil.Equal(t, hist[0].EndedAt != nil, true)

		// ListHeraLiveBindingsByTask no longer includes the old binding, only new.
		live, err := d.ListHeraLiveBindingsByTask("task-x")
		testutil.NoError(t, err)
		testutil.Equal(t, len(live), 1)
		testutil.Equal(t, live[0].ID, result.NewBinding.ID)
		testutil.Equal(t, live[0].OrchestratorID, ob.ID)
	})

	t.Run("not found when old binding already ended", func(t *testing.T) {
		d := heraTestDB(t)
		oa := mkOrch(t, d, "A")
		ob := mkOrch(t, d, "B")
		_, oldBinding, err := d.CreateHeraRoleWithBinding(CreateHeraRoleInput{
			OrchestratorID: oa.ID, Name: "w1", Kind: HeraKindWorker, ArgusProject: "p",
		}, "task-x", "/wt/x")
		testutil.NoError(t, err)
		testutil.NoError(t, d.EndHeraBinding(oldBinding.ID, "manual"))

		_, err = d.MoveHeraBinding(oldBinding.ID, CreateHeraRoleInput{
			OrchestratorID: ob.ID, Name: "w2", Kind: HeraKindWorker, ArgusProject: "p",
		}, "task-x", "/wt/x")
		testutil.ErrorIs(t, err, ErrHeraNotFound)

		// No role/binding created under B.
		_, err = d.HeraRoleByName(ob.ID, "w2")
		testutil.ErrorIs(t, err, ErrHeraNotFound)
	})

	t.Run("not found for unknown binding id", func(t *testing.T) {
		d := heraTestDB(t)
		ob := mkOrch(t, d, "B")
		_, err := d.MoveHeraBinding(999999, CreateHeraRoleInput{
			OrchestratorID: ob.ID, Name: "w2", Kind: HeraKindWorker, ArgusProject: "p",
		}, "task-x", "/wt/x")
		testutil.ErrorIs(t, err, ErrHeraNotFound)
	})

	t.Run("rolls back the end when the new binding insert fails", func(t *testing.T) {
		d := heraTestDB(t)
		oa := mkOrch(t, d, "A")
		ob := mkOrch(t, d, "B")
		_, oldBinding, err := d.CreateHeraRoleWithBinding(CreateHeraRoleInput{
			OrchestratorID: oa.ID, Name: "w1", Kind: HeraKindWorker, ArgusProject: "p",
		}, "task-x", "/wt/x")
		testutil.NoError(t, err)

		// Occupy (task-x, B) so the new binding insert violates the
		// per-(task,orchestrator) live-uniqueness index.
		seed := mkRole(t, d, ob.ID, "seed", HeraKindWorker)
		_, err = d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: seed.ID, ArgusTaskID: "task-x", WorktreePath: "/wt/seed",
		})
		testutil.NoError(t, err)

		_, err = d.MoveHeraBinding(oldBinding.ID, CreateHeraRoleInput{
			OrchestratorID: ob.ID, Name: "doomed", Kind: HeraKindWorker, ArgusProject: "p",
		}, "task-x", "/wt/x")
		testutil.Equal(t, err != nil, true)

		// The old binding under A must still be live — the whole tx rolled back.
		stillLive, err := d.HeraLiveBindingByRole(oldBinding.RoleID)
		testutil.NoError(t, err)
		testutil.Equal(t, stillLive.ID, oldBinding.ID)
		testutil.Nil(t, stillLive.EndedAt)

		// The "doomed" role must not exist.
		_, err = d.HeraRoleByName(ob.ID, "doomed")
		testutil.ErrorIs(t, err, ErrHeraNotFound)
	})
}

func TestHeraConstraintErrors(t *testing.T) {
	t.Run("invalid role kind rejected by CHECK", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		_, err := d.CreateHeraRole(CreateHeraRoleInput{
			OrchestratorID: o.ID, Name: "bad", Kind: HeraRoleKind("manager"), ArgusProject: "p",
		})
		testutil.Equal(t, err != nil, true)
	})

	t.Run("binding with explicit orch but missing role violates FK", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		// OrchestratorID set so the derive path is skipped; role_id 999 doesn't
		// exist, so the role_id FK rejects the insert.
		_, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: 999, OrchestratorID: o.ID, ArgusTaskID: "t", WorktreePath: "/w",
		})
		testutil.Equal(t, err != nil, true)
	})

	t.Run("invalid role status rejected by CHECK", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		r := mkRole(t, d, o.ID, "r", HeraKindWorker)
		err := d.UpsertHeraRoleStatus(r.ID, HeraRoleStatusValue("napping"))
		testutil.Equal(t, err != nil, true)
	})

	t.Run("role+binding tx rolls back when role insert fails", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		// Invalid kind fails the role insert (first statement), so the tx aborts
		// before the binding insert — exercises the role-insert error branch.
		_, _, err := d.CreateHeraRoleWithBinding(CreateHeraRoleInput{
			OrchestratorID: o.ID, Name: "x", Kind: HeraRoleKind("nope"), ArgusProject: "p",
		}, "t", "/w")
		testutil.Equal(t, err != nil, true)
		all, err := d.ListHeraRoles(o.ID, true)
		testutil.NoError(t, err)
		testutil.Equal(t, len(all), 0)
	})
}

func TestHeraRoleStatus(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "o")
	r := mkRole(t, d, o.ID, "r", HeraKindWorker)

	_, err := d.HeraRoleStatusFor(r.ID)
	testutil.ErrorIs(t, err, ErrHeraNotFound)

	testutil.NoError(t, d.UpsertHeraRoleStatus(r.ID, HeraStatusWorking))
	got, err := d.HeraRoleStatusFor(r.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, HeraStatusWorking)

	// Upsert overwrites.
	testutil.NoError(t, d.UpsertHeraRoleStatus(r.ID, HeraStatusDone))
	got, err = d.HeraRoleStatusFor(r.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, HeraStatusDone)
}

func TestHeraFKCascade(t *testing.T) {
	t.Run("delete orchestrator cascades roles bindings status", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		r := mkRole(t, d, o.ID, "r", HeraKindWorker)
		b, err := d.CreateHeraBinding(CreateHeraBindingInput{RoleID: r.ID, ArgusTaskID: "t", WorktreePath: "/w"})
		testutil.NoError(t, err)
		testutil.NoError(t, d.UpsertHeraRoleStatus(r.ID, HeraStatusIdle))

		testutil.NoError(t, d.DeleteHeraOrchestrator(o.ID))

		// Everything below the orchestrator is gone.
		_, err = d.HeraRole(r.ID)
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		_, err = d.HeraRoleStatusFor(r.ID)
		testutil.ErrorIs(t, err, ErrHeraNotFound)
		hist, err := d.ListHeraBindingsByRole(r.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, len(hist), 0)
		_ = b
	})

	t.Run("delete role cascades its bindings and status", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		r := mkRole(t, d, o.ID, "r", HeraKindWorker)
		_, err := d.CreateHeraBinding(CreateHeraBindingInput{RoleID: r.ID, ArgusTaskID: "t", WorktreePath: "/w"})
		testutil.NoError(t, err)
		testutil.NoError(t, d.UpsertHeraRoleStatus(r.ID, HeraStatusIdle))

		testutil.NoError(t, d.DeleteHeraRole(r.ID))

		hist, err := d.ListHeraBindingsByRole(r.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, len(hist), 0)
		_, err = d.HeraRoleStatusFor(r.ID)
		testutil.ErrorIs(t, err, ErrHeraNotFound)

		// The orchestrator survives — cascade flows downward only.
		_, err = d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
	})
}

func TestHeraTaskDeleteCascadeHook(t *testing.T) {
	t.Run("task delete ends live bindings; archive leaves them", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "o")
		r1 := mkRole(t, d, o.ID, "r1", HeraKindWorker)

		// Add a real task row so SetArchived/Delete find it.
		testutil.NoError(t, d.Add(&model.Task{ID: "task-9", Name: "t", Status: model.StatusInProgress}))
		b, err := d.CreateHeraBinding(CreateHeraBindingInput{
			RoleID: r1.ID, ArgusTaskID: "task-9", WorktreePath: "/wt/9",
		})
		testutil.NoError(t, err)

		// Archive is non-destructive: binding stays live (resumable).
		testutil.NoError(t, d.SetArchived("task-9", true))
		stillLive, err := d.HeraLiveBindingByTask("task-9")
		testutil.NoError(t, err)
		testutil.Equal(t, stillLive.ID, b.ID)
		testutil.Nil(t, stillLive.EndedAt)

		testutil.NoError(t, d.SetArchived("task-9", false))

		// Delete ends the binding.
		testutil.NoError(t, d.Delete("task-9"))
		_, err = d.HeraLiveBindingByTask("task-9")
		testutil.ErrorIs(t, err, ErrHeraNotFound)

		hist, err := d.ListHeraBindingsByRole(r1.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, len(hist), 1)
		testutil.Equal(t, hist[0].EndReason, heraEndReasonTaskDeleted)
		testutil.Equal(t, hist[0].EndedAt != nil, true)
	})

	t.Run("EndHeraBindingsForTask ends all and counts", func(t *testing.T) {
		d := heraTestDB(t)
		oa := mkOrch(t, d, "A")
		ob := mkOrch(t, d, "B")
		ra := mkRole(t, d, oa.ID, "r", HeraKindWorker)
		rb := mkRole(t, d, ob.ID, "r", HeraKindWorker)
		_, err := d.CreateHeraBinding(CreateHeraBindingInput{RoleID: ra.ID, ArgusTaskID: "multi", WorktreePath: "/a"})
		testutil.NoError(t, err)
		_, err = d.CreateHeraBinding(CreateHeraBindingInput{RoleID: rb.ID, ArgusTaskID: "multi", WorktreePath: "/b"})
		testutil.NoError(t, err)

		n, err := d.EndHeraBindingsForTask("multi", "bulk")
		testutil.NoError(t, err)
		testutil.Equal(t, n, 2)

		// Idempotent: no live bindings left.
		n, err = d.EndHeraBindingsForTask("multi", "bulk")
		testutil.NoError(t, err)
		testutil.Equal(t, n, 0)
	})
}

// TestNukeHeraRole pins the BUG-022 Tier-2 nuke for a role: nuked_at + archived_at
// stamped, pinned_at cleared, invisible to ListHeraRoles, still returned by id,
// idempotent, name freed for reuse, never a hard delete.
func TestNukeHeraRole(t *testing.T) {
	t.Run("stamps nuked_at + archived_at, clears pin, hides from list, keeps id lookup", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "alpha")
		r := mkRole(t, d, o.ID, "w1", HeraKindWorker)
		testutil.NoError(t, d.PinHeraRole(r.ID))

		testutil.NoError(t, d.NukeHeraRole(r.ID))

		got, err := d.HeraRole(r.ID) // id lookup still returns it
		testutil.NoError(t, err)
		if got.NukedAt == nil {
			t.Fatal("expected nuked_at set")
		}
		if got.ArchivedAt == nil {
			t.Fatal("expected archived_at set on a nuked role")
		}
		testutil.Nil(t, got.PinnedAt)

		// Invisible to the rail-feeding list even with includeArchived.
		list, err := d.ListHeraRoles(o.ID, true)
		testutil.NoError(t, err)
		testutil.Equal(t, len(list), 0)

		// Invisible to the by-kind list too.
		byKind, err := d.ListHeraRolesByKind(o.ID, HeraKindWorker)
		testutil.NoError(t, err)
		testutil.Equal(t, len(byKind), 0)
	})

	t.Run("frees the active-name index for reuse", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "alpha")
		r := mkRole(t, d, o.ID, "w1", HeraKindWorker)
		testutil.NoError(t, d.NukeHeraRole(r.ID))

		// A fresh active role can reuse the nuked role's name.
		r2, err := d.CreateHeraRole(CreateHeraRoleInput{
			OrchestratorID: o.ID, Name: "w1", Kind: HeraKindWorker, ArgusProject: "proj",
		})
		testutil.NoError(t, err)
		if r2.ID == r.ID {
			t.Fatal("expected a distinct new role row, not the nuked one")
		}
	})

	t.Run("idempotent and preserves original nuked_at", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "alpha")
		r := mkRole(t, d, o.ID, "w1", HeraKindWorker)
		testutil.NoError(t, d.NukeHeraRole(r.ID))
		first, err := d.HeraRole(r.ID)
		testutil.NoError(t, err)
		testutil.NoError(t, d.NukeHeraRole(r.ID)) // second call is a no-op
		second, err := d.HeraRole(r.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, second.NukedAt.Equal(*first.NukedAt), true)
	})

	t.Run("missing row returns ErrHeraNotFound", func(t *testing.T) {
		d := heraTestDB(t)
		testutil.ErrorIs(t, d.NukeHeraRole(9999), ErrHeraNotFound)
	})
}

// TestNukeHeraOrchestrator pins the orchestrator-level nuke.
func TestNukeHeraOrchestrator(t *testing.T) {
	t.Run("stamps markers, hides from list, keeps id lookup, frees name", func(t *testing.T) {
		d := heraTestDB(t)
		o := mkOrch(t, d, "alpha")
		testutil.NoError(t, d.PinHeraOrchestrator(o.ID))

		testutil.NoError(t, d.NukeHeraOrchestrator(o.ID))

		got, err := d.HeraOrchestrator(o.ID)
		testutil.NoError(t, err)
		if got.NukedAt == nil || got.ArchivedAt == nil {
			t.Fatal("expected nuked_at and archived_at set")
		}
		testutil.Nil(t, got.PinnedAt)

		list, err := d.ListHeraOrchestrators(true)
		testutil.NoError(t, err)
		testutil.Equal(t, len(list), 0)

		// Name freed: a fresh active orchestrator can reuse it.
		o2, err := d.CreateHeraOrchestrator("alpha", "")
		testutil.NoError(t, err)
		if o2.ID == o.ID {
			t.Fatal("expected a distinct new orchestrator, not the nuked one")
		}
	})

	t.Run("missing row returns ErrHeraNotFound", func(t *testing.T) {
		d := heraTestDB(t)
		testutil.ErrorIs(t, d.NukeHeraOrchestrator(9999), ErrHeraNotFound)
	})
}
