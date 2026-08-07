package db

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestGetHeraBindingCostTotals_NeverAccrued_ReadsZero(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "orch")
	r := mkRole(t, d, o.ID, "worker-1", HeraKindWorker)
	b := mkBinding(t, d, r.ID, "task-1", "/tmp/task-1")

	got, err := d.GetHeraBindingCostTotals(b.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, *got, HeraBindingCostTotals{})
}

func TestGetHeraBindingCostTotals_MissingBinding_NotFound(t *testing.T) {
	d := heraTestDB(t)
	_, err := d.GetHeraBindingCostTotals(99999)
	testutil.ErrorIs(t, err, ErrHeraNotFound)
}

func TestUpdateHeraBindingCostTotals_RoundTrips(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "orch")
	r := mkRole(t, d, o.ID, "worker-1", HeraKindWorker)
	b := mkBinding(t, d, r.ID, "task-1", "/tmp/task-1")

	want := HeraBindingCostTotals{
		TokensInput: 100, TokensCacheWrite1h: 200, TokensCacheWrite5m: 300,
		TokensCacheRead: 400, TokensOutput: 500, CostUSDAccrued: 1.25,
	}
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(b.ID, want))

	got, err := d.GetHeraBindingCostTotals(b.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, *got, want)
}

func TestUpdateHeraBindingCostTotals_MissingBinding_NotFound(t *testing.T) {
	d := heraTestDB(t)
	err := d.UpdateHeraBindingCostTotals(99999, HeraBindingCostTotals{})
	testutil.ErrorIs(t, err, ErrHeraNotFound)
}

// TestUpdateHeraBindingCostTotals_SurvivesTaskArchive pins the whole reason
// this data lives on hera_bindings instead of task_meta (design.md Decision
// 2): task_meta rows are wiped by DeleteMetaForTask on SetArchived(true), but
// hera_bindings has no such coupling.
func TestUpdateHeraBindingCostTotals_SurvivesTaskArchive(t *testing.T) {
	d := heraTestDB(t)
	task := &model.Task{Name: "archivable-worker"}
	testutil.NoError(t, d.Add(task))

	o := mkOrch(t, d, "orch")
	r := mkRole(t, d, o.ID, "worker-1", HeraKindWorker)
	b := mkBinding(t, d, r.ID, task.ID, "/tmp/"+task.ID)

	want := HeraBindingCostTotals{TokensInput: 42, CostUSDAccrued: 0.5}
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(b.ID, want))

	testutil.NoError(t, d.SetArchived(task.ID, true))

	got, err := d.GetHeraBindingCostTotals(b.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, *got, want)
}

func TestSumHeraRoleCostAccrued_NoBindings_Zero(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "orch")
	r := mkRole(t, d, o.ID, "worker-1", HeraKindWorker)

	sum, err := d.SumHeraRoleCostAccrued(r.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, sum, 0.0)
}

// TestSumHeraRoleCostAccrued_SpansEveryIncarnation pins the recycled-role
// requirement (design.md Decision 2): a role re-bound one or more times
// accrues across ALL its bindings, live and ended alike.
func TestSumHeraRoleCostAccrued_SpansEveryIncarnation(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "orch")
	r := mkRole(t, d, o.ID, "worker-1", HeraKindWorker)

	b1 := mkBinding(t, d, r.ID, "task-1", "/tmp/task-1")
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(b1.ID, HeraBindingCostTotals{CostUSDAccrued: 1.0}))
	testutil.NoError(t, d.EndHeraBinding(b1.ID, "task_ended"))

	b2 := mkBinding(t, d, r.ID, "task-1", "/tmp/task-1-v2")
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(b2.ID, HeraBindingCostTotals{CostUSDAccrued: 2.5}))

	sum, err := d.SumHeraRoleCostAccrued(r.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, sum, 3.5)
}

func TestSumHeraRoleCostAccruedByOrchestrator_BulkMatchesPerRole(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "orch")
	r1 := mkRole(t, d, o.ID, "worker-1", HeraKindWorker)
	r2 := mkRole(t, d, o.ID, "worker-2", HeraKindWorker)
	r3 := mkRole(t, d, o.ID, "worker-3-no-cost", HeraKindWorker) // never accrues anything

	b1 := mkBinding(t, d, r1.ID, "task-1", "/tmp/task-1")
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(b1.ID, HeraBindingCostTotals{CostUSDAccrued: 1.5}))
	b2 := mkBinding(t, d, r2.ID, "task-2", "/tmp/task-2")
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(b2.ID, HeraBindingCostTotals{CostUSDAccrued: 2.25}))
	_ = r3

	byRole, err := d.SumHeraRoleCostAccruedByOrchestrator(o.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, byRole[r1.ID], 1.5)
	testutil.Equal(t, byRole[r2.ID], 2.25)
	testutil.Equal(t, byRole[r3.ID], 0.0)
}

func TestSumHeraRoleRawTokensByOrchestrator_SumsAllFiveClasses(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "orch")
	r := mkRole(t, d, o.ID, "worker-1", HeraKindWorker)

	b1 := mkBinding(t, d, r.ID, "task-1", "/tmp/task-1")
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(b1.ID, HeraBindingCostTotals{
		TokensInput: 10, TokensCacheWrite1h: 20, TokensCacheWrite5m: 30, TokensCacheRead: 40, TokensOutput: 50,
	}))
	testutil.NoError(t, d.EndHeraBinding(b1.ID, "task_ended"))

	b2 := mkBinding(t, d, r.ID, "task-1", "/tmp/task-1-v2")
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(b2.ID, HeraBindingCostTotals{
		TokensInput: 1, TokensCacheWrite1h: 2, TokensCacheWrite5m: 3, TokensCacheRead: 4, TokensOutput: 5,
	}))

	byRole, err := d.SumHeraRoleRawTokensByOrchestrator(o.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, byRole[r.ID], HeraBindingCostTotals{
		TokensInput: 11, TokensCacheWrite1h: 22, TokensCacheWrite5m: 33, TokensCacheRead: 44, TokensOutput: 55,
	})
}

// TestSumNukedHeraRolesCostByOrchestrator_IncludesOnlyNukedRoles pins the
// deliberate divergence from every other hera rollup (design.md Decision
// 4): a nuked role's recorded cost is included here, and a non-nuked role's
// cost is excluded — the exact opposite of ListHeraRoles' own filter.
func TestSumNukedHeraRolesCostByOrchestrator_IncludesOnlyNukedRoles(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "orch")
	liveRole := mkRole(t, d, o.ID, "still-active", HeraKindWorker)
	nukedRole := mkRole(t, d, o.ID, "since-nuked", HeraKindWorker)

	liveBinding := mkBinding(t, d, liveRole.ID, "task-live", "/tmp/task-live")
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(liveBinding.ID, HeraBindingCostTotals{CostUSDAccrued: 9.0}))

	nukedBinding := mkBinding(t, d, nukedRole.ID, "task-nuked", "/tmp/task-nuked")
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(nukedBinding.ID, HeraBindingCostTotals{CostUSDAccrued: 4.0}))
	testutil.NoError(t, d.NukeHeraRole(nukedRole.ID))

	sum, err := d.SumNukedHeraRolesCostByOrchestrator(o.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, sum, 4.0) // only the nuked role's cost, not the live role's 9.0
}
