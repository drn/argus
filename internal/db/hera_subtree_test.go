package db

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// bind creates a live binding for role→task and fails on error. Worktree is
// derived from the task id so (worktree, orchestrator) stays unique per call
// while the same task can bind under multiple orchestrators (multi-binding).
func bind(t *testing.T, d *DB, roleID int64, taskID string) *HeraBinding {
	t.Helper()
	b, err := d.CreateHeraBinding(CreateHeraBindingInput{
		RoleID:       roleID,
		ArgusTaskID:  taskID,
		WorktreePath: "/wt/" + taskID,
	})
	testutil.NoError(t, err)
	return b
}

// nestOrch wires a child orchestrator under a parent: it creates child orch
// `childName` with a coordinator, binds that coordinator to `bridgeTask`, and
// binds `bridgeTask` as a worker under `parentOrchID` — the multi-binding bridge
// the subtree BFS walks. Returns the child orchestrator and its coordinator.
func nestOrch(t *testing.T, d *DB, parentOrchID int64, childName, bridgeTask string) (*HeraOrchestrator, *HeraRole) {
	t.Helper()
	child := mkOrch(t, d, childName)
	childCoord := mkRole(t, d, child.ID, "coord", HeraKindCoordinator)
	// bridgeTask is the child's coordinator AND a worker in the parent.
	bind(t, d, childCoord.ID, bridgeTask)
	parentWorker := mkRole(t, d, parentOrchID, "w-"+childName, HeraKindWorker)
	bind(t, d, parentWorker.ID, bridgeTask)
	return child, childCoord
}

func TestSubtreeOrchIDs_RootOnly(t *testing.T) {
	d := heraTestDB(t)
	root := mkOrch(t, d, "root")
	mkRole(t, d, root.ID, "coord", HeraKindCoordinator)

	ids, err := d.SubtreeOrchIDs(root.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, ids, []int64{root.ID})
}

func TestSubtreeOrchIDs_NestedThreeLevels(t *testing.T) {
	d := heraTestDB(t)
	// root coord on t-root.
	root := mkOrch(t, d, "root")
	rootCoord := mkRole(t, d, root.ID, "coord", HeraKindCoordinator)
	bind(t, d, rootCoord.ID, "t-root")

	// sub hangs off root via bridge task t-sub (worker in root, coord of sub).
	sub, _ := nestOrch(t, d, root.ID, "sub", "t-sub")
	// grand hangs off sub via bridge task t-grand.
	grand, _ := nestOrch(t, d, sub.ID, "grand", "t-grand")

	ids, err := d.SubtreeOrchIDs(root.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, ids, []int64{root.ID, sub.ID, grand.ID})

	// From sub, only sub + grand.
	subIDs, err := d.SubtreeOrchIDs(sub.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, subIDs, []int64{sub.ID, grand.ID})
}

func TestSubtreeOrchIDs_ArchivedExcluded(t *testing.T) {
	d := heraTestDB(t)
	root := mkOrch(t, d, "root")
	rootCoord := mkRole(t, d, root.ID, "coord", HeraKindCoordinator)
	bind(t, d, rootCoord.ID, "t-root")
	sub, _ := nestOrch(t, d, root.ID, "sub", "t-sub")
	grand, _ := nestOrch(t, d, sub.ID, "grand", "t-grand")

	t.Run("archived grandchild dropped", func(t *testing.T) {
		testutil.NoError(t, d.ArchiveHeraOrchestrator(grand.ID))
		ids, err := d.SubtreeOrchIDs(root.ID)
		testutil.NoError(t, err)
		testutil.DeepEqual(t, ids, []int64{root.ID, sub.ID})
		testutil.NoError(t, d.UnarchiveHeraOrchestrator(grand.ID))
	})

	t.Run("archived mid-orch prunes its descendants too", func(t *testing.T) {
		testutil.NoError(t, d.ArchiveHeraOrchestrator(sub.ID))
		ids, err := d.SubtreeOrchIDs(root.ID)
		testutil.NoError(t, err)
		// sub is excluded, so the BFS never reaches grand through it.
		testutil.DeepEqual(t, ids, []int64{root.ID})
	})
}

func TestSubtreeOrchIDs_CycleGuard(t *testing.T) {
	d := heraTestDB(t)
	// A and B mutually bridge: A's coord task binds under B and B's coord task
	// binds under A. The visited set must stop the BFS from looping forever.
	a := mkOrch(t, d, "a")
	aCoord := mkRole(t, d, a.ID, "coord", HeraKindCoordinator)
	bind(t, d, aCoord.ID, "tA")
	b := mkOrch(t, d, "b")
	bCoord := mkRole(t, d, b.ID, "coord", HeraKindCoordinator)
	bind(t, d, bCoord.ID, "tB")

	// tB is also a worker in A → B is a child of A.
	aWorker := mkRole(t, d, a.ID, "w", HeraKindWorker)
	bind(t, d, aWorker.ID, "tB")
	// tA is also a worker in B → A is a child of B (forms the cycle).
	bWorker := mkRole(t, d, b.ID, "w", HeraKindWorker)
	bind(t, d, bWorker.ID, "tA")

	ids, err := d.SubtreeOrchIDs(a.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, ids, []int64{a.ID, b.ID}) // terminates, root included once
}

func TestListHeraLatestBindings(t *testing.T) {
	d := heraTestDB(t)
	orch := mkOrch(t, d, "o")
	role := mkRole(t, d, orch.ID, "w", HeraKindWorker)

	t.Run("no bindings yields none", func(t *testing.T) {
		got, err := d.ListHeraLatestBindings()
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 0)
	})

	t.Run("latest of several rebinds wins", func(t *testing.T) {
		b1 := bind(t, d, role.ID, "t1")
		testutil.NoError(t, d.EndHeraBinding(b1.ID, "argus_deleted"))
		b2 := bind(t, d, role.ID, "t2") // newer, live

		got, err := d.ListHeraLatestBindings()
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 1)
		testutil.Equal(t, got[0].ID, b2.ID)
		testutil.Equal(t, got[0].ArgusTaskID, "t2")
		testutil.Nil(t, got[0].EndedAt) // live latest
	})

	t.Run("ended latest carries end_reason", func(t *testing.T) {
		role2 := mkRole(t, d, orch.ID, "w2", HeraKindWorker)
		b := bind(t, d, role2.ID, "tx")
		testutil.NoError(t, d.EndHeraBinding(b.ID, HeraEndReasonUserDeleted))

		got, err := d.ListHeraLatestBindings()
		testutil.NoError(t, err)
		var found *HeraBinding
		for _, x := range got {
			if x.RoleID == role2.ID {
				found = x
			}
		}
		testutil.Equal(t, found != nil, true)
		testutil.Equal(t, found.EndReason, HeraEndReasonUserDeleted)
	})
}

func TestSubtreeOrchIDs_BridgesOverEndedNonTeardown(t *testing.T) {
	d := heraTestDB(t)
	root := mkOrch(t, d, "root")
	rootCoord := mkRole(t, d, root.ID, "coord", HeraKindCoordinator)
	bind(t, d, rootCoord.ID, "t-root")

	// Child sub bridges off root via t-sub, but the child's COORDINATOR binding
	// has ended for a non-teardown reason (its task finished). The parent worker
	// binding stays live. The bridge must still nest (latest-binding semantics).
	sub := mkOrch(t, d, "sub")
	subCoord := mkRole(t, d, sub.ID, "coord", HeraKindCoordinator)
	subBnd := bind(t, d, subCoord.ID, "t-sub")
	testutil.NoError(t, d.EndHeraBinding(subBnd.ID, "argus_deleted")) // non-teardown
	parentWorker := mkRole(t, d, root.ID, "w-sub", HeraKindWorker)
	bind(t, d, parentWorker.ID, "t-sub")

	ids, err := d.SubtreeOrchIDs(root.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, ids, []int64{root.ID, sub.ID})
}

func TestSubtreeOrchIDs_TeardownLinkDoesNotBridge(t *testing.T) {
	d := heraTestDB(t)
	root := mkOrch(t, d, "root")
	rootCoord := mkRole(t, d, root.ID, "coord", HeraKindCoordinator)
	bind(t, d, rootCoord.ID, "t-root")

	sub := mkOrch(t, d, "sub")
	subCoord := mkRole(t, d, sub.ID, "coord", HeraKindCoordinator)
	bind(t, d, subCoord.ID, "t-sub") // live child coord
	// The PARENT worker link was torn down (operator reparent/delete): its latest
	// binding ended with a teardown reason, so it must NOT bridge the child.
	parentWorker := mkRole(t, d, root.ID, "w-sub", HeraKindWorker)
	pwBnd := bind(t, d, parentWorker.ID, "t-sub")
	testutil.NoError(t, d.EndHeraBinding(pwBnd.ID, HeraEndReasonReparented))

	ids, err := d.SubtreeOrchIDs(root.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, ids, []int64{root.ID}) // sub not reached
}

func TestHeraTreeUpdatesSince(t *testing.T) {
	d := heraTestDB(t)
	root := mkOrch(t, d, "root")
	coord := mkRole(t, d, root.ID, "coord", HeraKindCoordinator)
	bind(t, d, coord.ID, "t-root")
	worker := mkRole(t, d, root.ID, "w", HeraKindWorker)
	bind(t, d, worker.ID, "t-w")

	// sub-orch in the subtree with its own coord+worker, and a message there.
	sub, subCoord := nestOrch(t, d, root.ID, "sub", "t-sub")
	subWorker := mkRole(t, d, sub.ID, "sw", HeraKindWorker)
	bind(t, d, subWorker.ID, "t-sw")

	// Unrelated orchestrator — its messages must never appear in root's roll-up.
	other := mkOrch(t, d, "other")
	otherCoord := mkRole(t, d, other.ID, "coord", HeraKindCoordinator)
	otherWorker := mkRole(t, d, other.ID, "w", HeraKindWorker)

	send := func(from, to int64, tldr string) int64 {
		t.Helper()
		m, err := d.SendHeraMessage(from, to, "body of "+tldr, tldr, nil)
		testutil.NoError(t, err)
		return m.ID
	}

	m1 := send(worker.ID, coord.ID, "m1-root")      // in root
	m2 := send(subWorker.ID, subCoord.ID, "m2-sub") // in subtree
	send(otherWorker.ID, otherCoord.ID, "m-other")  // unrelated, must be filtered
	m3 := send(coord.ID, worker.ID, "m3-root")      // in root

	t.Run("since=0 returns whole subtree ordered, excludes unrelated", func(t *testing.T) {
		msgs, next, err := d.HeraTreeUpdatesSince(root.ID, 0)
		testutil.NoError(t, err)
		testutil.Equal(t, len(msgs), 3)
		testutil.Equal(t, msgs[0].ID, m1)
		testutil.Equal(t, msgs[1].ID, m2)
		testutil.Equal(t, msgs[2].ID, m3)
		testutil.Equal(t, msgs[0].Tldr, "m1-root")
		testutil.Equal(t, next, m3) // max id seen
	})

	t.Run("since cursor filters older", func(t *testing.T) {
		msgs, next, err := d.HeraTreeUpdatesSince(root.ID, m1)
		testutil.NoError(t, err)
		testutil.Equal(t, len(msgs), 2)
		testutil.Equal(t, msgs[0].ID, m2)
		testutil.Equal(t, msgs[1].ID, m3)
		testutil.Equal(t, next, m3)
	})

	t.Run("empty result keeps cursor", func(t *testing.T) {
		msgs, next, err := d.HeraTreeUpdatesSince(root.ID, m3)
		testutil.NoError(t, err)
		testutil.Equal(t, len(msgs), 0)
		testutil.Equal(t, next, m3) // unchanged
	})

	t.Run("from sub-orch sees only the sub message", func(t *testing.T) {
		msgs, _, err := d.HeraTreeUpdatesSince(sub.ID, 0)
		testutil.NoError(t, err)
		testutil.Equal(t, len(msgs), 1)
		testutil.Equal(t, msgs[0].ID, m2)
	})
}

func TestHeraTreeCursor_GetSetUpsert(t *testing.T) {
	d := heraTestDB(t)
	root := mkOrch(t, d, "root")
	coord := mkRole(t, d, root.ID, "coord", HeraKindCoordinator)

	t.Run("missing cursor reads 0", func(t *testing.T) {
		c, err := d.GetHeraTreeCursor(coord.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, c, int64(0))
	})

	t.Run("set then get", func(t *testing.T) {
		testutil.NoError(t, d.SetHeraTreeCursor(coord.ID, 42))
		c, err := d.GetHeraTreeCursor(coord.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, c, int64(42))
	})

	t.Run("upsert overwrites", func(t *testing.T) {
		testutil.NoError(t, d.SetHeraTreeCursor(coord.ID, 99))
		c, err := d.GetHeraTreeCursor(coord.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, c, int64(99))
	})
}

// TestHeraTreeCursor_FKCascade is the BUG-034 regression: a tree_read_cursors
// row must NOT pin its parent role/orchestrator. With FK enforcement on, a bare
// REFERENCES (NO ACTION) would fail orchestrator/role delete with FK 787; the
// ON DELETE CASCADE clause lets the delete clean the cursor too.
func TestHeraTreeCursor_FKCascade(t *testing.T) {
	t.Run("delete orchestrator with a cursor-holding coordinator", func(t *testing.T) {
		d := heraTestDB(t)
		orch := mkOrch(t, d, "root")
		coord := mkRole(t, d, orch.ID, "coord", HeraKindCoordinator)
		testutil.NoError(t, d.SetHeraTreeCursor(coord.ID, 7))

		// Must not raise "FOREIGN KEY constraint failed (787)".
		testutil.NoError(t, d.DeleteHeraOrchestrator(orch.ID))

		// Cursor row cascaded away with the role.
		c, err := d.GetHeraTreeCursor(coord.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, c, int64(0))
	})

	t.Run("delete role with a cursor row", func(t *testing.T) {
		d := heraTestDB(t)
		orch := mkOrch(t, d, "root")
		role := mkRole(t, d, orch.ID, "w", HeraKindWorker)
		testutil.NoError(t, d.SetHeraTreeCursor(role.ID, 3))

		testutil.NoError(t, d.DeleteHeraRole(role.ID))

		c, err := d.GetHeraTreeCursor(role.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, c, int64(0))
	})
}
