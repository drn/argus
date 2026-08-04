package hera

import (
	"fmt"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// seedBoundRoleForBench mirrors seedBoundRole (model_test.go) but takes
// testing.TB so it's usable from a *testing.B — kept separate rather than
// widening seedBoundRole's signature, which many existing *testing.T tests
// already call.
func seedBoundRoleForBench(tb testing.TB, d *db.DB, orchID int64, name string, kind db.HeraRoleKind, taskID string) {
	tb.Helper()
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchID, Name: name, Kind: kind, ArgusProject: "p"})
	if err != nil {
		tb.Fatal(err)
	}
	if err := d.Add(&model.Task{ID: taskID, Name: taskID, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}); err != nil {
		tb.Fatal(err)
	}
	if _, err := d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: role.ID, ArgusTaskID: taskID, WorktreePath: "/wt/" + taskID}); err != nil {
		tb.Fatal(err)
	}
}

// seedLargeHistory populates an in-memory DB with numOrchs archived
// orchestrators (each with a coordinator + worker role bound to distinct
// tasks) — approximating Aaron's real dogfood scale (~900 historical
// orchestrators/roles/bindings, per the fix-hera-tick-and-kick-perf mission)
// — plus one small ACTIVE orchestrator so the model isn't degenerate. Kept in
// this package (not a throwaway script) so the measurement in
// openspec/changes/fix-hera-tick-and-kick-perf/tasks.md §5 is reproducible
// via `go test -bench` rather than a one-off transcript.
func seedLargeHistory(b *testing.B, numOrchs int) *db.DB {
	b.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = d.Close() })
	for i := 0; i < numOrchs; i++ {
		name := fmt.Sprintf("archived-orch-%d", i)
		o, err := d.CreateHeraOrchestrator(name, "")
		if err != nil {
			b.Fatal(err)
		}
		coordTask := fmt.Sprintf("t-coord-%d", i)
		wkrTask := fmt.Sprintf("t-wkr-%d", i)
		seedBoundRoleForBench(b, d, o.ID, "coord", db.HeraKindCoordinator, coordTask)
		seedBoundRoleForBench(b, d, o.ID, "wkr", db.HeraKindWorker, wkrTask)
		if err := d.ArchiveHeraOrchestrator(o.ID); err != nil {
			b.Fatal(err)
		}
	}
	// One small active orchestrator so the model isn't entirely archived —
	// mirrors Aaron's "5-6 active agents" alongside the large history.
	active, err := d.CreateHeraOrchestrator("active-orch", "")
	if err != nil {
		b.Fatal(err)
	}
	seedBoundRoleForBench(b, d, active.ID, "coord", db.HeraKindCoordinator, "t-active-coord")
	seedBoundRoleForBench(b, d, active.ID, "wkr", db.HeraKindWorker, "t-active-wkr")
	return d
}

// BenchmarkDoRefresh_AlwaysRebuild reproduces the cost of the PRE-FIX
// behavior: BuildModel+SetModel ran unconditionally on every tick opportunity
// regardless of whether anything changed. InvalidateChangeGate before each
// iteration forces exactly that.
func BenchmarkDoRefresh_AlwaysRebuild(b *testing.B) {
	d := seedLargeHistory(b, 450) // ~900 roles + ~900 bindings, matching Aaron's reported ~900-row scale
	p := NewHeraPage(d)
	p.Refresh()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.InvalidateChangeGate()
		p.doRefresh()
	}
}

// BenchmarkDoRefresh_SteadyStateGated reproduces the cost of the POST-FIX
// behavior with nothing changing between rebuild opportunities: only the
// FIRST doRefresh call (folded into setup, excluded via ResetTimer after one
// warm call) pays the full cost; every subsequent call should be the
// near-zero shouldRebuild() check alone.
func BenchmarkDoRefresh_SteadyStateGated(b *testing.B) {
	d := seedLargeHistory(b, 450)
	p := NewHeraPage(d)
	p.Refresh() // pay the one real rebuild before timing starts
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.doRefresh() // nothing changed — should be gated to a near-instant skip
	}
}
