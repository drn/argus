package hera

import (
	"sync"
	"testing"

	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/testutil"
)

// TestHeraPage_TierResolverFactoryRunsOncePerBuild: the factory is called once
// per doRefresh (not once per role) and its closure annotates every role.
func TestHeraPage_TierResolverFactoryRunsOncePerBuild(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "w1", db.HeraKindWorker, "t-w1")
	seedBoundRole(t, d, orch, "w2", db.HeraKindWorker, "t-w2")

	p := NewHeraPage(d)
	builds, annotated := 0, 0
	dones := 0
	p.SetTierResolverFactory(func() (func(*heramodel.RoleView), func()) {
		builds++
		return func(rv *heramodel.RoleView) {
				annotated++
				rv.AppliedModel = "m"
			}, func() {
				// done runs after every role of the build was annotated
				testutil.Equal(t, annotated, builds*3)
				dones++
			}
	})
	p.Refresh()
	testutil.Equal(t, builds, 1)
	testutil.Equal(t, annotated, 3)
	testutil.Equal(t, roleByName(t, modelPtr(p), orch, "w1").AppliedModel, "m")

	p.Refresh() // forced refresh bypasses the change gate: a new build, a new closure
	testutil.Equal(t, builds, 2)
	testutil.Equal(t, dones, 2)
	testutil.Equal(t, annotated, 6)
}

// TestHeraPage_TierResolverFactoryPrecedence: the factory wins over the legacy
// per-role resolver, which still works alone.
func TestHeraPage_TierResolverFactoryPrecedence(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "t-w")

	p := NewHeraPage(d)
	p.SetTierResolver(func(rv *heramodel.RoleView) { rv.AppliedModel = "legacy" })
	p.Refresh()
	testutil.Equal(t, roleByName(t, modelPtr(p), orch, "w").AppliedModel, "legacy")

	p.SetTierResolverFactory(func() (func(*heramodel.RoleView), func()) {
		return func(rv *heramodel.RoleView) { rv.AppliedModel = "factory" }, nil // nil done is allowed
	})
	p.Refresh()
	testutil.Equal(t, roleByName(t, modelPtr(p), orch, "w").AppliedModel, "factory")
}

func modelPtr(p *HeraPage) *heramodel.Model { m := p.Rail().Model(); return &m }

// TestAsyncRefresh_TierFactoryRunsOncePerBackgroundBuild: on the off-thread tick
// path the factory runs once per BUILD on the build goroutine (never on the UI
// goroutine), its closure is only used by that build, and the annotated model is
// what publishes. Run under -race: a closure memo shared across builds or touched
// from the UI goroutine would trip it.
func TestAsyncRefresh_TierFactoryRunsOncePerBackgroundBuild(t *testing.T) {
	p, d, q := asyncPage(t)
	orch := seedOrch(t, d, "alpha")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "w1", db.HeraKindWorker, "t-w1")
	seedBoundRole(t, d, orch, "w2", db.HeraKindWorker, "t-w2")

	var mu sync.Mutex
	var builds, annotated, dones int
	var closures []*int // one private counter per closure: proves no sharing across builds
	p.SetTierResolverFactory(func() (func(*heramodel.RoleView), func()) {
		mu.Lock()
		builds++
		mu.Unlock()
		memo := new(int) // stands in for the per-build memo map
		mu.Lock()
		closures = append(closures, memo)
		mu.Unlock()
		return func(rv *heramodel.RoleView) {
				*memo++ // unsynchronized on purpose: must stay goroutine-confined
				mu.Lock()
				annotated++
				mu.Unlock()
				rv.AppliedModel = "bg"
			}, func() {
				mu.Lock()
				dones++
				mu.Unlock()
			}
	})

	buildAndPump(t, p, q) // build 1
	buildAndPump(t, p, q) // build 2 (gate invalidated: a fresh build, a fresh closure)

	mu.Lock()
	defer mu.Unlock()
	testutil.Equal(t, builds, 2)
	testutil.Equal(t, dones, 2)
	testutil.Equal(t, annotated, 6)
	testutil.Equal(t, *closures[0], 3)
	testutil.Equal(t, *closures[1], 3) // second build did not reuse the first's memo
	testutil.Equal(t, roleByName(t, modelPtr(p), orch, "w1").AppliedModel, "bg")
}
