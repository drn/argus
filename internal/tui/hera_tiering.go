package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/profiles"
	"github.com/drn/argus/internal/tui/hera"
)

// hera_tiering.go is the App side of the plan-view diligence-tiering readout
// (add-diligence-profiles, D-VIEW). HeraPage.SetTierResolver is wired (local mode
// only) to resolveHeraTier, which stamps each RoleView's AppliedModel/Effort and
// ProfileWarning during the debounced doRefresh — OFF the Draw path, because
// resolution reads disk (profile files + the task row) and the pure projection
// must not. The plan view then renders the archetype + applied model/effort per
// node and a ⚠ warning when a project explicitly points at a missing/invalid
// profile (the runtime fail-open surfacing).

// resolveHeraTier fills in a role's diligence-tiering readout for the plan view.
// It is the SetTierResolver closure (local mode). The archetype is already on the
// role (no I/O); this adds the applied model/effort (via agent.ResolveModel, which
// honors a task.Model override + backend validation) and a warning when the
// project's EXPLICIT profile binding is missing or invalid. An unbound project
// falling back to an absent "default" is silent fail-open — never a warning (it is
// not a profile the operator pointed at).
func (a *App) resolveHeraTier(rv *hera.RoleView) {
	if rv == nil {
		return
	}
	cfg := a.db.Config()

	// Unconditional, regardless of archetype/profile — ContextPercent has
	// nothing to do with diligence tiering, it just needs cfg (rail-context-
	// high). Computed here rather than in buildRoleView because cfg is only
	// available in local mode; rail.go's contextIndicator, not this function,
	// is what excludes coordinators from ever rendering the indicator.
	rv.ContextPercent = contextPercent(rv.ContextSize, cfg.Hera.CoordinatorContextBudget)

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

	loader := &profiles.Loader{LibraryDir: filepath.Join(db.DataDir(), "profiles")}
	if rv.WorktreePath != "" {
		loader.RepoDir = filepath.Join(rv.WorktreePath, ".argus", "profiles")
	}
	// panelValidator is nil: the plan-view tiering readout only consults
	// archetype/rigor fields, so panel-grammar enforcement is left to the
	// daemon-side consumers (spawn-time resolution, profile_resolve).
	prof, errs := loader.ValidateName(name, cfg, agent.KnownModels, nil)
	if prof == nil || len(errs) > 0 {
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

	rv.AppliedEffort = strings.TrimSpace(prof.Archetype[rv.Archetype].Effort)

	t := a.heraTierTask(rv)
	backend, err := agent.ResolveBackend(t, cfg)
	if err != nil {
		// Can't resolve a backend → show the profile's declared model as the intent.
		rv.AppliedModel = strings.TrimSpace(prof.Archetype[rv.Archetype].Model)
		return
	}
	applied, _ := agent.ResolveModel(t, backend, cfg)
	rv.AppliedModel = applied
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
