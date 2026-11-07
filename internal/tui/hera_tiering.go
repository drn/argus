package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/profiles"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/uxlog"
)

// hera_tiering.go is the App side of the plan-view diligence-tiering readout
// (add-diligence-profiles, D-VIEW). HeraPage.SetTierResolver is wired (local mode
// only) to resolveHeraTier, which stamps each RoleView's AppliedModel/Effort and
// ProfileWarning during the debounced doRefresh — OFF the Draw path, because
// resolution reads disk (profile files + the task row) and the pure projection
// must not. The plan view then renders the archetype + applied model/effort per
// node and a ⚠ warning when a project explicitly points at a missing/invalid
// profile (the runtime fail-open surfacing).

// resolveHeraTier annotates ONE role in isolation. It is a thin wrapper over a
// single-use build closure, kept for callers/tests that resolve a lone role; the
// rail refresh uses heraTierResolverForBuild directly so the work below is shared
// across every role of a build (hera-tier-resolve-once).
func (a *App) resolveHeraTier(rv *hera.RoleView) {
	annotate, _ := a.heraTierResolverForBuild()
	annotate(rv)
}

// heraTierValidate loads and validates a named profile. A package var only so
// tests can count loads; production always uses Loader.ValidateName. panelValidator
// is nil: the plan-view readout only consults archetype/rigor fields, so
// panel-grammar enforcement is left to the daemon-side consumers.
var heraTierValidate = func(l *profiles.Loader, name string, cfg config.Config) (*profiles.Profile, []error) {
	return l.ValidateName(name, cfg, agent.KnownModels, nil)
}

// tierProfileKey identifies a profile load: the name plus the in-repo directory
// (empty when the worktree has none, so most roles share one entry).
type tierProfileKey struct{ name, repoDir string }

type tierProfileEntry struct {
	prof *profiles.Profile
	ok   bool
}

// tierModelKey is every input to the applied-model resolution below that can
// differ between roles. Two roles with equal keys resolve identically.
//
// It includes the readout role's own archetype and tiering-profile key (pk): the
// no-backend fallback path reads prof.Archetype[rv.Archetype], and a bound task's
// archetype/project may differ from the role's.
type tierModelKey struct {
	model, profile, backend, account, project, archetype, repoDir string
	roleArchetype                                                 string
	prof                                                          tierProfileKey
}

// tierRepoDir returns the in-repo profile directory for a worktree, or "" when
// the worktree has none. An absent directory behaves exactly like RepoDir==""
// (Loader.locate would stat a missing file), so collapsing it lets every role
// without one share a memo entry for one os.Stat instead of several plus a decode.
func tierRepoDir(worktree string) string {
	if worktree == "" {
		return ""
	}
	dir := filepath.Join(worktree, ".argus", "profiles")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// heraTierResolverForBuild returns the per-role annotation closure for ONE model
// build. It snapshots Config() once and memoizes profile validation and applied-
// model resolution for the lifetime of the closure only — nothing outlives the
// build, so settings, project bindings and profile files are re-read on the next
// build (no cache to invalidate, no staleness window) and the closure is
// goroutine-confined to the build that created it (race-free off the UI thread).
//
// Per role it stamps the applied model/effort (via agent.ResolveModel, which
// honors a task.Model override + backend validation), a warning when the
// project's EXPLICIT profile binding is missing or invalid, and ContextPercent.
// An unbound project falling back to an absent "default" is silent fail-open —
// never a warning (it is not a profile the operator pointed at).
func (a *App) heraTierResolverForBuild() (annotate func(*hera.RoleView), done func()) {
	cfg := a.db.Config()
	libraryDir := filepath.Join(db.DataDir(), "profiles")
	profMemo := map[tierProfileKey]tierProfileEntry{}
	modelMemo := map[tierModelKey]string{}
	// One os.Stat per distinct worktree per build, not per lookup.
	repoDirMemo := map[string]string{}
	repoDirOf := func(wt string) string {
		if d, ok := repoDirMemo[wt]; ok {
			return d
		}
		d := tierRepoDir(wt)
		repoDirMemo[wt] = d
		return d
	}
	var calls, profHits, profMisses, modelHits, modelMisses int
	annotate = func(rv *hera.RoleView) {
		if rv == nil {
			return
		}
		calls++

		// Unconditional, regardless of archetype/profile — ContextPercent has
		// nothing to do with diligence tiering, it just needs cfg (rail-context-
		// high). rail.go's contextIndicator, not this function, is what excludes
		// coordinators from ever rendering the indicator.
		//
		// The denominator is kind-dependent: CoordinatorContextBudget (200000) is
		// a coordinator-specific recycle-nudge POLICY threshold, deliberately set
		// well below a coordinator's real context window so it has room to wrap up
		// gracefully — it is not a context window size, and reusing it for a
		// worker/freelance role would make the indicator's 40/65/90 tiers fire at
		// 80k/130k/180k tokens instead of the intended 400k/650k/900k against a
		// worker's actual (much larger) context window.
		budget := cfg.Hera.CoordinatorContextBudget
		if rv.Kind != db.HeraKindCoordinator {
			budget = cfg.Hera.WorkerContextWindow
		}
		rv.ContextPercent = contextPercent(rv.ContextSize, budget)

		explicit := ""
		if rv.ArgusProject != "" {
			if p, ok := cfg.Projects[rv.ArgusProject]; ok {
				explicit = strings.TrimSpace(p.Profile)
			}
		}
		name := explicit
		if name == "" {
			name = "default"
		}

		repoDir := repoDirOf(rv.WorktreePath)
		pk := tierProfileKey{name: name, repoDir: repoDir}
		entry, seen := profMemo[pk]
		if seen {
			profHits++
		} else {
			profMisses++
			loader := &profiles.Loader{LibraryDir: libraryDir, RepoDir: repoDir}
			uxlog.Log("[hera-tier] profile load name=%q repoDir=%q", name, repoDir)
			prof, errs := heraTierValidate(loader, name, cfg)
			entry = tierProfileEntry{prof: prof, ok: prof != nil && len(errs) == 0}
			profMemo[pk] = entry
		}
		if !entry.ok {
			// Only surface a warning for an EXPLICIT binding; an unbound project that
			// falls back to a missing "default" is the silent fail-open case.
			if explicit != "" {
				rv.ProfileWarning = fmt.Sprintf("profile %q missing or invalid", name)
			}
			return
		}
		if rv.Archetype == "" {
			return // no archetype → no profile consult → no model readout
		}
		prof := entry.prof

		rv.AppliedEffort = strings.TrimSpace(prof.Archetype[rv.Archetype].Effort)

		t := a.heraTierTask(rv)
		mk := tierModelKey{
			model: t.Model, profile: t.Profile, backend: t.Backend, account: t.Account,
			project: t.Project, archetype: t.Archetype, repoDir: repoDirOf(t.Worktree),
			roleArchetype: rv.Archetype, prof: pk,
		}
		if applied, ok := modelMemo[mk]; ok {
			modelHits++
			rv.AppliedModel = applied
			return
		}
		modelMisses++
		rv.AppliedModel = tierAppliedModel(t, rv.Archetype, prof, cfg)
		modelMemo[mk] = rv.AppliedModel
	}
	// done logs one summary line per build (never one per role): memo hits are the
	// work this change removes; misses are the loads that still happened.
	done = func() {
		uxlog.Log("[hera-tier] build: roles=%d profile hits=%d misses=%d model hits=%d misses=%d (1 config snapshot)",
			calls, profHits, profMisses, modelHits, modelMisses)
	}
	return annotate, done
}

// tierAppliedModel resolves a task's applied model for the readout: the backend's
// ResolveModel result, or — when no backend resolves — the profile's declared
// choice for the task's requested/default backend as intent.
func tierAppliedModel(t *model.Task, archetype string, prof *profiles.Profile, cfg config.Config) string {
	backend, err := agent.ResolveBackend(t, cfg)
	if err != nil {
		backendName := t.Backend
		if backendName == "" {
			backendName = cfg.Defaults.Backend
		}
		return strings.TrimSpace(prof.Archetype[archetype].Models[backendName])
	}
	applied, _ := agent.ResolveModel(t, backend, cfg)
	return applied
}

// contextPercent converts a raw context_size token count into a 0-100
// percentage of the project's configured coordinator_context_budget. budget
// <= 0 (unconfigured) and size <= 0 both resolve to 0 rather than dividing by
// zero or reporting a negative percentage. A worker carries no hard-stop
// (unlike a coordinator), so size can run past budget — the result caps at
// 100 rather than reporting e.g. 150.
func contextPercent(size, budget int) int {
	if budget <= 0 || size <= 0 {
		return 0
	}
	pct := size * 100 / budget
	if pct > 100 {
		pct = 100
	}
	return pct
}

// validProfileNames returns the names of on-disk diligence profiles that pass
// validation, discovered across the per-user library (~/.argus/profiles) and the
// project's in-repo directory (<projectPath>/.argus/profiles). Only valid profiles
// are returned, so the Settings project select-list can never bind a project to a
// malformed profile (settings-view requirement). A profile that fails to load or
// validate is dropped (the currently-bound name is re-added by the form's
// SetProfileOptions so an explicitly-bound-but-now-invalid name stays visible).
func (a *App) validProfileNames(projectPath string) []string {
	cfg := a.db.Config()
	loader := &profiles.Loader{LibraryDir: filepath.Join(db.DataDir(), "profiles")}
	if strings.TrimSpace(projectPath) != "" {
		loader.RepoDir = filepath.Join(projectPath, ".argus", "profiles")
	}
	var valid []string
	for _, name := range loader.Discover() {
		if p, errs := loader.ValidateName(name, cfg, agent.KnownModels, nil); p != nil && len(errs) == 0 {
			valid = append(valid, name)
		}
	}
	return valid
}

// heraTierTask returns the model.Task that drives a role's model resolution: the
// live bound task (authoritative — carries any task.Model override + backend) when
// the role is bound, else a synthesized task carrying just the resolution inputs a
// planned node has (archetype + project + worktree), so a never-materialized
// planned node still shows its intended tiering.
func (a *App) heraTierTask(rv *hera.RoleView) *model.Task {
	if rv.TaskID != "" {
		if t, err := a.db.Get(rv.TaskID); err == nil && t != nil {
			return t
		}
	}
	return &model.Task{Archetype: rv.Archetype, Project: rv.ArgusProject, Worktree: rv.WorktreePath}
}
