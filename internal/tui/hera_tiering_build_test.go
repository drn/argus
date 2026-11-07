package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/profiles"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/store"
	"github.com/drn/argus/internal/uxlog"
)

// configCountingStore counts Config() calls so tests can prove the per-build
// snapshot. *db.DB is embedded via the interface, so every other method passes through.
type configCountingStore struct {
	store.Store
	configCalls int
}

func (c *configCountingStore) Config() config.Config {
	c.configCalls++
	return c.Store.Config()
}

// countProfileLoads wraps the profile-validation seam and returns a counter.
func countProfileLoads(t *testing.T) *int {
	t.Helper()
	n := 0
	orig := heraTierValidate
	heraTierValidate = func(l *profiles.Loader, name string, cfg config.Config) (*profiles.Profile, []error) {
		n++
		return orig(l, name, cfg)
	}
	t.Cleanup(func() { heraTierValidate = orig })
	return &n
}

func tierBuildApp(t *testing.T) (*App, *configCountingStore, *db.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("defaults.backend", "claude"))
	testutil.NoError(t, d.SetBackend("claude", config.Backend{Command: "claude"}))
	cs := &configCountingStore{Store: d}
	return New(cs, agent.NewRunner(nil), false), cs, d
}

const leanProfile = "[archetype.code_slice]\nmodels = { claude = \"sonnet\" }\neffort = \"high\"\n"

func manyRoles(project string, n int) []hera.RoleView {
	rvs := make([]hera.RoleView, n)
	for i := range rvs {
		rvs[i] = hera.RoleView{ArgusProject: project, Archetype: "code_slice", Kind: db.HeraKindWorker}
	}
	return rvs
}

// TestTierResolverForBuild_ConfigAndProfileOncePerBuild: 50 roles of one project
// cost one Config() read and one profile decode for the whole build.
func TestTierResolverForBuild_ConfigAndProfileOncePerBuild(t *testing.T) {
	app, cs, d := tierBuildApp(t)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Profile: "lean"}))
	writeLibraryProfile(t, "lean", leanProfile)
	loads := countProfileLoads(t)
	cs.configCalls = 0

	resolve, _ := app.heraTierResolverForBuild()
	rvs := manyRoles("p", 50)
	for i := range rvs {
		rvs[i].WorktreePath = t.TempDir() // no .argus/profiles dir → shares the memo entry
		resolve(&rvs[i])
	}
	testutil.Equal(t, cs.configCalls, 1)
	testutil.Equal(t, *loads, 1)
	for _, rv := range rvs {
		testutil.Equal(t, rv.AppliedModel, "sonnet")
		testutil.Equal(t, rv.AppliedEffort, "high")
	}
}

// TestTierResolverForBuild_MatchesIndependentResolution: a shared build closure
// yields exactly what resolving each role alone yields, across projects,
// archetypes, explicit-missing profiles and a task model override.
func TestTierResolverForBuild_MatchesIndependentResolution(t *testing.T) {
	app, _, d := tierBuildApp(t)
	testutil.NoError(t, d.SetProject("lean", config.Project{Path: t.TempDir(), Profile: "lean"}))
	testutil.NoError(t, d.SetProject("ghost", config.Project{Path: t.TempDir(), Profile: "ghost"}))
	testutil.NoError(t, d.SetProject("plain", config.Project{Path: t.TempDir()}))
	writeLibraryProfile(t, "lean", leanProfile)
	testutil.NoError(t, d.Add(&model.Task{ID: "t-ovr", Name: "ovr", Project: "lean", Model: "opus", Archetype: "code_slice"}))

	roles := []hera.RoleView{
		{ArgusProject: "lean", Archetype: "code_slice"},
		{ArgusProject: "lean", Archetype: "review"},
		{ArgusProject: "lean", Archetype: "code_slice", TaskID: "t-ovr"},
		{ArgusProject: "ghost", Archetype: "code_slice"},
		{ArgusProject: "plain", Archetype: "code_slice"},
		{ArgusProject: "", Archetype: ""},
		{ArgusProject: "lean", Archetype: "code_slice", Kind: db.HeraKindCoordinator, ContextSize: 100000},
	}
	shared, _ := app.heraTierResolverForBuild()
	var results []hera.RoleView
	for i := range roles {
		got := roles[i]
		want := roles[i]
		shared(&got)
		app.resolveHeraTier(&want)
		testutil.DeepEqual(t, got, want)
		results = append(results, got)
	}
	// Guard against a vacuous pass: the fixtures really exercise distinct outcomes.
	testutil.Equal(t, results[0].AppliedModel, "sonnet")
	testutil.Equal(t, results[2].AppliedModel, "opus") // task model override
	testutil.Equal(t, results[3].ProfileWarning, `profile "ghost" missing or invalid`)
}

// TestTierResolverForBuild_KeysSeparateResults: roles differing in a memo-key
// field must not share a result.
func TestTierResolverForBuild_KeysSeparateResults(t *testing.T) {
	app, _, d := tierBuildApp(t)
	testutil.NoError(t, d.SetProject("a", config.Project{Path: t.TempDir(), Profile: "lean"}))
	testutil.NoError(t, d.SetProject("b", config.Project{Path: t.TempDir(), Profile: "opusy"}))
	writeLibraryProfile(t, "lean", leanProfile)
	writeLibraryProfile(t, "opusy", "[archetype.code_slice]\nmodels = { claude = \"opus\" }\neffort = \"low\"\n")

	resolve, _ := app.heraTierResolverForBuild()
	ra := hera.RoleView{ArgusProject: "a", Archetype: "code_slice"}
	rb := hera.RoleView{ArgusProject: "b", Archetype: "code_slice"}
	resolve(&ra)
	resolve(&rb)
	testutil.Equal(t, ra.AppliedModel, "sonnet")
	testutil.Equal(t, rb.AppliedModel, "opus")
	testutil.Equal(t, rb.AppliedEffort, "low")
}

// TestTierResolverForBuild_InRepoProfileKeepsPrecedence: a worktree with its own
// .argus/profiles dir overrides the library profile for that role only.
func TestTierResolverForBuild_InRepoProfileKeepsPrecedence(t *testing.T) {
	app, _, d := tierBuildApp(t)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Profile: "lean"}))
	writeLibraryProfile(t, "lean", leanProfile)
	wt := t.TempDir()
	dir := filepath.Join(wt, ".argus", "profiles")
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "lean.toml"),
		[]byte("[archetype.code_slice]\nmodels = { claude = \"opus\" }\neffort = \"medium\"\n"), 0o644))

	resolve, _ := app.heraTierResolverForBuild()
	plain := hera.RoleView{ArgusProject: "p", Archetype: "code_slice", WorktreePath: t.TempDir()}
	inRepo := hera.RoleView{ArgusProject: "p", Archetype: "code_slice", WorktreePath: wt}
	plain2 := hera.RoleView{ArgusProject: "p", Archetype: "code_slice", WorktreePath: t.TempDir()}
	resolve(&plain)
	resolve(&inRepo)
	resolve(&plain2)
	testutil.Equal(t, plain.AppliedModel, "sonnet")
	testutil.Equal(t, inRepo.AppliedModel, "opus")
	testutil.Equal(t, inRepo.AppliedEffort, "medium")
	testutil.Equal(t, plain2.AppliedModel, "sonnet") // not polluted by the in-repo entry
}

// TestTierResolverForBuild_EditsVisibleNextBuild: nothing survives a build — a
// profile edit and a project rebinding both show on the very next build.
func TestTierResolverForBuild_EditsVisibleNextBuild(t *testing.T) {
	app, _, d := tierBuildApp(t)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Profile: "lean"}))
	writeLibraryProfile(t, "lean", leanProfile)

	rv := hera.RoleView{ArgusProject: "p", Archetype: "code_slice"}
	app.resolveHeraTier(&rv)
	testutil.Equal(t, rv.AppliedModel, "sonnet")

	writeLibraryProfile(t, "lean", "[archetype.code_slice]\nmodels = { claude = \"opus\" }\neffort = \"low\"\n")
	rv = hera.RoleView{ArgusProject: "p", Archetype: "code_slice"}
	app.resolveHeraTier(&rv)
	testutil.Equal(t, rv.AppliedModel, "opus")

	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Profile: "ghost"}))
	rv = hera.RoleView{ArgusProject: "p", Archetype: "code_slice"}
	app.resolveHeraTier(&rv)
	testutil.Equal(t, rv.ProfileWarning, `profile "ghost" missing or invalid`)
}

// TestTierResolverForBuild_UnboundDefaultMissingIsSilent: an unbound project with
// no "default" profile stays silent (no warning), also under the shared memo.
func TestTierResolverForBuild_UnboundDefaultMissingIsSilent(t *testing.T) {
	app, _, _ := tierBuildApp(t)
	resolve, _ := app.heraTierResolverForBuild()
	rvs := manyRoles("", 5)
	for i := range rvs {
		resolve(&rvs[i])
		testutil.Equal(t, rvs[i].ProfileWarning, "")
		testutil.Equal(t, rvs[i].AppliedModel, "")
	}
}

// TestTierResolverForBuild_NoBackendFallbackKeyedByRoleArchetype: when no backend
// resolves, the applied model is the profile's declared choice for the ROLE's
// archetype, so two roles sharing one task-derived key but differing in role
// archetype must not share a memoized model.
func TestTierResolverForBuild_NoBackendFallbackKeyedByRoleArchetype(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t) // no default backend → ResolveBackend errors
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Profile: "two"}))
	writeLibraryProfile(t, "two", "[archetype.code_slice]\nmodels = { claude = \"sonnet\" }\n[archetype.review]\nmodels = { claude = \"opus\" }\n")
	testutil.NoError(t, d.SetConfigValue("defaults.backend", "claude"))
	app := New(d, agent.NewRunner(nil), false)
	// Bound task with its own archetype shared by both roles.
	testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "t1", Project: "p", Archetype: "code_slice"}))

	resolve, _ := app.heraTierResolverForBuild()
	a := hera.RoleView{ArgusProject: "p", Archetype: "code_slice", TaskID: "t1"}
	b := hera.RoleView{ArgusProject: "p", Archetype: "review", TaskID: "t1"}
	resolve(&a)
	resolve(&b)
	want := hera.RoleView{ArgusProject: "p", Archetype: "review", TaskID: "t1"}
	app.resolveHeraTier(&want)
	testutil.Equal(t, b.AppliedModel, want.AppliedModel)
}

// TestTierResolverForBuild_DoneSummarizesBuild: done never panics and reports a
// once-per-build summary after annotation (smoke: logging path executes).
func TestTierResolverForBuild_DoneSummarizesBuild(t *testing.T) {
	app, _, _ := tierBuildApp(t)
	annotate, done := app.heraTierResolverForBuild()
	rv := hera.RoleView{}
	annotate(&rv)
	annotate(nil) // nil role is a no-op
	done()
}

// TestHeraTier_AppWiringResolvesOncePerRefresh drives the REAL wiring (buildUI →
// SetTierResolverFactory → doRefresh): three same-project workers cost one profile
// load for the refresh, every role carries the applied model, and the per-build
// summary line reaches uxlog. Reverting app.go to the per-role SetTierResolver
// makes loads==3 and fails this.
func TestHeraTier_AppWiringResolvesOncePerRefresh(t *testing.T) {
	// Plain *db.DB (not the counting wrapper): buildUI only wires the local-mode
	// Hera reader/tier seams when the store is a *db.DB.
	_, _, d := tierBuildApp(t)
	app := New(d, agent.NewRunner(nil), false)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Profile: "lean"}))
	writeLibraryProfile(t, "lean", leanProfile)
	logPath := filepath.Join(t.TempDir(), "ux.log")
	testutil.NoError(t, uxlog.Init(logPath))
	t.Cleanup(uxlog.Close)

	o, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	for _, n := range []string{"w1", "w2", "w3"} {
		role, rerr := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: n, Kind: db.HeraKindWorker, ArgusProject: "p", Archetype: "code_slice"})
		testutil.NoError(t, rerr)
		testutil.NoError(t, d.Add(&model.Task{ID: "t-" + n, Name: n, Status: model.StatusInProgress, Project: "p", Archetype: "code_slice"}))
		_, berr := d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: role.ID, ArgusTaskID: "t-" + n, WorktreePath: t.TempDir()})
		testutil.NoError(t, berr)
	}
	loads := countProfileLoads(t)

	app.heraPage.Refresh()

	testutil.Equal(t, *loads, 1)
	m := app.heraPage.Rail().Model()
	got := 0
	for _, ov := range m.Active {
		for _, rv := range ov.Roles {
			if rv.Kind == db.HeraKindWorker {
				testutil.Equal(t, rv.AppliedModel, "sonnet")
				got++
			}
		}
	}
	testutil.Equal(t, got, 3)
	b, rerr := os.ReadFile(logPath)
	testutil.NoError(t, rerr)
	testutil.Contains(t, string(b), "[hera-tier] build: roles=3 profile hits=2 misses=1")
}

// TestTierResolverForBuild_TaskOverridesKeyTheModelMemo: bound tasks that share
// project+archetype but differ in their Profile or Backend override must not
// share a memoized model (each is a field of the memo key).
func TestTierResolverForBuild_TaskOverridesKeyTheModelMemo(t *testing.T) {
	app, _, d := tierBuildApp(t)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Profile: "lean"}))
	writeLibraryProfile(t, "lean", leanProfile)
	writeLibraryProfile(t, "opusy", "[archetype.code_slice]\nmodels = { claude = \"opus\" }\n")
	testutil.NoError(t, d.Add(&model.Task{ID: "t-plain", Name: "a", Project: "p", Archetype: "code_slice"}))
	testutil.NoError(t, d.Add(&model.Task{ID: "t-prof", Name: "b", Project: "p", Archetype: "code_slice", Profile: "opusy"}))

	resolve, _ := app.heraTierResolverForBuild()
	plain := hera.RoleView{ArgusProject: "p", Archetype: "code_slice", TaskID: "t-plain"}
	prof := hera.RoleView{ArgusProject: "p", Archetype: "code_slice", TaskID: "t-prof"}
	resolve(&plain)
	resolve(&prof)
	testutil.Equal(t, plain.AppliedModel, "sonnet")
	// Alone (fresh build) is the oracle for the shared-build result.
	alone := hera.RoleView{ArgusProject: "p", Archetype: "code_slice", TaskID: "t-prof"}
	app.resolveHeraTier(&alone)
	testutil.Equal(t, prof.AppliedModel, alone.AppliedModel)
	testutil.Equal(t, prof.AppliedModel, "opus")
}
