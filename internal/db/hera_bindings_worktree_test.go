package db

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// These tests pin the worktree-keyed binding lookup added for BUG-059 and the
// raw DAO-level mechanism behind the claim-vs-attach paradox: identity keyed
// by argus_task_id disagrees with the (worktree_path, orchestrator_id)
// uniqueness the INSERT is actually constrained by.

func TestHeraLiveBindingByWorktreeAndOrchestrator_HappyPath(t *testing.T) {
	d := heraTestDB(t)
	orch := mkOrch(t, d, "o")
	role := mkRole(t, d, orch.ID, "w", HeraKindWorker)
	want, err := d.CreateHeraBinding(CreateHeraBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-1", WorktreePath: "/wt",
	})
	testutil.NoError(t, err)

	got, err := d.HeraLiveBindingByWorktreeAndOrchestrator("/wt", orch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, want.ID)
}

func TestHeraLiveBindingByWorktreeAndOrchestrator_NotFoundAndOrchScoped(t *testing.T) {
	d := heraTestDB(t)
	orchA := mkOrch(t, d, "A")
	orchB := mkOrch(t, d, "B")
	roleA := mkRole(t, d, orchA.ID, "w", HeraKindWorker)
	_, err := d.CreateHeraBinding(CreateHeraBindingInput{
		RoleID: roleA.ID, ArgusTaskID: "t-1", WorktreePath: "/wt",
	})
	testutil.NoError(t, err)

	// Same worktree, DIFFERENT orchestrator → must not resolve orch-A's
	// binding. This scoping is what makes the CallerRole worktree fallback
	// safe against a stale binding from another orchestrator.
	_, err = d.HeraLiveBindingByWorktreeAndOrchestrator("/wt", orchB.ID)
	testutil.ErrorIs(t, err, ErrHeraNotFound)

	// A worktree with no binding at all under either orchestrator.
	_, err = d.HeraLiveBindingByWorktreeAndOrchestrator("/nope", orchA.ID)
	testutil.ErrorIs(t, err, ErrHeraNotFound)
}

func TestHeraLiveBindingByWorktreeAndOrchestrator_IgnoresEnded(t *testing.T) {
	d := heraTestDB(t)
	orch := mkOrch(t, d, "o")
	role := mkRole(t, d, orch.ID, "w", HeraKindWorker)
	bnd, err := d.CreateHeraBinding(CreateHeraBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-1", WorktreePath: "/wt",
	})
	testutil.NoError(t, err)
	testutil.NoError(t, d.EndHeraBinding(bnd.ID, "test"))

	_, err = d.HeraLiveBindingByWorktreeAndOrchestrator("/wt", orch.ID)
	testutil.ErrorIs(t, err, ErrHeraNotFound)
}

func TestHeraLiveBindingByWorktreeAndOrchestrator_MultiOrchDoesNotAmbiguate(t *testing.T) {
	// Unlike the unscoped HeraLiveBindingByWorktree (which returns
	// ErrHeraAmbiguous when 2+ orchestrators share a live binding at the same
	// worktree), the orchestrator-scoped lookup always resolves to at most one
	// row per call — that is exactly what keeps the BUG-059 fallback safe.
	d := heraTestDB(t)
	orchA := mkOrch(t, d, "A")
	orchB := mkOrch(t, d, "B")
	roleA := mkRole(t, d, orchA.ID, "r", HeraKindWorker)
	roleB := mkRole(t, d, orchB.ID, "r", HeraKindWorker)
	bndA, err := d.CreateHeraBinding(CreateHeraBindingInput{RoleID: roleA.ID, ArgusTaskID: "ta", WorktreePath: "/shared"})
	testutil.NoError(t, err)
	bndB, err := d.CreateHeraBinding(CreateHeraBindingInput{RoleID: roleB.ID, ArgusTaskID: "tb", WorktreePath: "/shared"})
	testutil.NoError(t, err)

	gotA, err := d.HeraLiveBindingByWorktreeAndOrchestrator("/shared", orchA.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotA.ID, bndA.ID)

	gotB, err := d.HeraLiveBindingByWorktreeAndOrchestrator("/shared", orchB.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotB.ID, bndB.ID)

	// The unscoped lookup, by contrast, IS ambiguous here.
	_, err = d.HeraLiveBindingByWorktree("/shared")
	testutil.ErrorIs(t, err, ErrHeraAmbiguous)
}

// TestHeraBinding_ClaimVsAttachMechanism reproduces, at the DAO level, the
// exact disagreement behind BUG-059: with a live binding rooted at (taskA,
// worktreeP, orchO), a task-keyed lookup for a DIFFERENT task id resolves
// nothing ("claim says none"), yet inserting a fresh binding for that other
// task at the SAME worktree+orchestrator is rejected by the
// idx_hera_bindings_live_worktree_orch index ("attach says exists"). The
// worktree-keyed lookup is what reconciles the two views.
func TestHeraBinding_ClaimVsAttachMechanism(t *testing.T) {
	d := heraTestDB(t)
	orch := mkOrch(t, d, "o")
	role := mkRole(t, d, orch.ID, "w", HeraKindWorker)
	live, err := d.CreateHeraBinding(CreateHeraBindingInput{
		RoleID: role.ID, ArgusTaskID: "task-A", WorktreePath: "/shared/wt",
	})
	testutil.NoError(t, err)

	// "claim": a task-keyed lookup for the OTHER (colliding) task id → nothing.
	_, err = d.HeraLiveBindingByTaskAndOrchestrator("task-B", orch.ID)
	testutil.ErrorIs(t, err, ErrHeraNotFound)

	// "attach": inserting a fresh binding for task-B at the same worktree+orch
	// is rejected by the (worktree_path, orchestrator_id) uniqueness.
	role2 := mkRole(t, d, orch.ID, "w2", HeraKindWorker)
	_, err = d.CreateHeraBinding(CreateHeraBindingInput{
		RoleID: role2.ID, ArgusTaskID: "task-B", WorktreePath: "/shared/wt",
	})
	if err == nil {
		t.Fatal("attach INSERT: expected UNIQUE constraint violation, got nil")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
		t.Fatalf("attach INSERT: expected a UNIQUE-constraint error, got %v", err)
	}

	// Reconciliation: the worktree-keyed lookup resolves the same binding the
	// INSERT collided with, so both views now agree on identity.
	got, err := d.HeraLiveBindingByWorktreeAndOrchestrator("/shared/wt", orch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, live.ID)
}
