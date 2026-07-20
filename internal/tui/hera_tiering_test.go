package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
)

// writeLibraryProfile writes <HOME>/.argus/profiles/<name>.toml. The caller MUST
// have already t.Setenv("HOME", ...) so db.DataDir() resolves under the temp HOME
// (CLAUDE.md: tests never touch the real ~/.argus).
func writeLibraryProfile(t *testing.T, name, content string) {
	t.Helper()
	dir := filepath.Join(db.DataDir(), "profiles")
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, name+".toml"), []byte(content), 0o644))
}

// tierTestApp builds an App over a temp-HOME *db.DB with a claude backend default
// (so KnownModels includes opus/sonnet/haiku for profile-model validation).
func tierTestApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("defaults.backend", "claude"))
	testutil.NoError(t, d.SetBackend("claude", config.Backend{Command: "claude"}))
	return New(d, agent.NewRunner(nil), false)
}

// TestResolveHeraTier_AppliesProfileModel: a role with archetype code_slice in a
// project bound to a profile mapping code_slice→sonnet resolves AppliedModel=sonnet.
func TestResolveHeraTier_AppliesProfileModel(t *testing.T) {
	app := tierTestApp(t)
	d := app.db.(*db.DB)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Profile: "lean"}))
	writeLibraryProfile(t, "lean", "[archetype.code_slice]\nmodel = \"sonnet\"\neffort = \"high\"\n")

	rv := &hera.RoleView{ArgusProject: "p", Archetype: "code_slice"}
	app.resolveHeraTier(rv)
	testutil.Equal(t, rv.AppliedModel, "sonnet")
	testutil.Equal(t, rv.AppliedEffort, "high")
	testutil.Equal(t, rv.ProfileWarning, "")
}

// TestResolveHeraTier_ExplicitMissingProfileWarns: an EXPLICIT binding to a
// profile that does not exist surfaces a ProfileWarning.
func TestResolveHeraTier_ExplicitMissingProfileWarns(t *testing.T) {
	app := tierTestApp(t)
	d := app.db.(*db.DB)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Profile: "ghost"}))

	rv := &hera.RoleView{ArgusProject: "p", Archetype: "code_slice"}
	app.resolveHeraTier(rv)
	testutil.Contains(t, rv.ProfileWarning, "ghost")
	testutil.Contains(t, rv.ProfileWarning, "missing or invalid")
}

// TestResolveHeraTier_UnboundNoWarn: an UNBOUND project (Profile=="") with no
// "default" profile on disk is silent fail-open — no warning (it is not a profile
// the operator pointed at).
func TestResolveHeraTier_UnboundNoWarn(t *testing.T) {
	app := tierTestApp(t)
	d := app.db.(*db.DB)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir()}))

	rv := &hera.RoleView{ArgusProject: "p", Archetype: "code_slice"}
	app.resolveHeraTier(rv)
	testutil.Equal(t, rv.ProfileWarning, "")
}

// TestResolveHeraTier_NoArchetypeNoModel: a role carrying no archetype gets no
// model readout (no profile consulted), and a valid bound profile yields no warning.
func TestResolveHeraTier_NoArchetypeNoModel(t *testing.T) {
	app := tierTestApp(t)
	d := app.db.(*db.DB)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Profile: "lean"}))
	writeLibraryProfile(t, "lean", "[archetype.code_slice]\nmodel = \"sonnet\"\n")

	rv := &hera.RoleView{ArgusProject: "p", Archetype: ""}
	app.resolveHeraTier(rv)
	testutil.Equal(t, rv.AppliedModel, "")
	testutil.Equal(t, rv.ProfileWarning, "")
}

// TestResolveHeraTier_ContextPercent_Worker pins the rail-context-high
// correction: a worker's ContextPercent is computed against
// Hera.WorkerContextWindow (the DefaultConfig value, 1000000, here) — NOT
// CoordinatorContextBudget, which is a coordinator-specific recycle-nudge
// policy threshold, not a context window size.
func TestResolveHeraTier_ContextPercent_Worker(t *testing.T) {
	app := tierTestApp(t)

	rv := &hera.RoleView{Kind: db.HeraKindWorker, ContextSize: 400000} // 40% of 1000000
	app.resolveHeraTier(rv)
	testutil.Equal(t, rv.ContextPercent, 40)
}

// TestResolveHeraTier_ContextPercent_Freelance mirrors the worker test for
// the other non-coordinator kind, confirming the denominator switch is
// "coordinator vs everything else", not "worker specifically".
func TestResolveHeraTier_ContextPercent_Freelance(t *testing.T) {
	app := tierTestApp(t)

	rv := &hera.RoleView{Kind: db.HeraKindFreelance, ContextSize: 650000} // 65% of 1000000
	app.resolveHeraTier(rv)
	testutil.Equal(t, rv.ContextPercent, 65)
}

// TestResolveHeraTier_ContextPercent_Coordinator pins that a coordinator's
// ContextPercent still uses CoordinatorContextBudget (300000), unchanged —
// rail.go, not resolveHeraTier, is what excludes coordinators from ever
// rendering the indicator, so this value is computed but never displayed.
func TestResolveHeraTier_ContextPercent_Coordinator(t *testing.T) {
	app := tierTestApp(t)

	rv := &hera.RoleView{Kind: db.HeraKindCoordinator, ContextSize: 30000} // 10% of 300000
	app.resolveHeraTier(rv)
	testutil.Equal(t, rv.ContextPercent, 10)
}

// TestResolveHeraTier_ContextPercent_SameSizeDifferentKind_DifferentPercent
// makes the denominator split concrete: the IDENTICAL raw ContextSize reads
// as a much higher percentage for a coordinator (small policy budget) than
// for a worker (large real context window).
func TestResolveHeraTier_ContextPercent_SameSizeDifferentKind_DifferentPercent(t *testing.T) {
	app := tierTestApp(t)

	coord := &hera.RoleView{Kind: db.HeraKindCoordinator, ContextSize: 300000}
	app.resolveHeraTier(coord)
	testutil.Equal(t, coord.ContextPercent, 100) // at its 300000 budget

	worker := &hera.RoleView{Kind: db.HeraKindWorker, ContextSize: 300000}
	app.resolveHeraTier(worker)
	testutil.Equal(t, worker.ContextPercent, 30) // only 30% of its 1000000 window
}

// TestContextPercent_ZeroBudget pins the divide-by-zero guard: an
// unconfigured (zero, or otherwise non-positive) budget resolves to 0 rather
// than panicking or reporting a nonsensical percentage.
func TestContextPercent_ZeroBudget(t *testing.T) {
	testutil.Equal(t, contextPercent(80000, 0), 0)
}

// TestContextPercent_CapsAtHundred pins the clamp: a worker's context_size
// can run past the coordinator-oriented budget (workers carry no hard-stop),
// and the percentage caps at 100 rather than reporting e.g. 150.
func TestContextPercent_CapsAtHundred(t *testing.T) {
	testutil.Equal(t, contextPercent(300000, 200000), 100)
}

// TestValidProfileNames_OnlyValid: validProfileNames returns only profiles that
// pass validation — a malformed profile (unknown archetype) is excluded.
func TestValidProfileNames_OnlyValid(t *testing.T) {
	app := tierTestApp(t)
	writeLibraryProfile(t, "good", "[archetype.code_slice]\nmodel = \"sonnet\"\n")
	writeLibraryProfile(t, "bad", "[archetype.not_a_real_archetype]\nmodel = \"sonnet\"\n")

	names := app.validProfileNames("")
	has := func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
	testutil.Equal(t, has("good"), true)
	testutil.Equal(t, has("bad"), false)
}
