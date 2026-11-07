package hera

import (
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
