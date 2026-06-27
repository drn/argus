package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// heraactions.go owns the App side of the M6c Hera-view mutation keyset: the
// modal / confirm / refresh orchestration that the rail's key handler triggers
// via HeraPage's OnXxx callbacks. The actual DB writes live in hera.Ops
// (thin adapters over M1 store methods) and the worker spawn reuses the shared
// agent.SpawnHeraWorker primitive — this file adds no orchestration logic of
// its own beyond UI flow and target resolution. Every handler runs on the tview
// main thread; the only slow op (spawn) is dispatched to a goroutine.

// heraRefresh rebuilds the rail immediately after a mutation and repaints.
func (a *App) heraRefresh() {
	a.heraPage.Refresh()
	a.forceRedraw("hera mutation")
}

// heraSpawnWorker opens a prompt input modal for a new worker under the selected
// orchestrator's coordinator, then spawns it via the shared primitive.
func (a *App) heraSpawnWorker(sel hera.Selection) {
	if a.heraOps == nil {
		return
	}
	orch := sel.Orch
	coordTaskID := sel.CoordTaskID()
	if orch == nil || coordTaskID == "" {
		uxlog.Log("[hera-view] spawn: no live coordinator for selection — ignored")
		a.statusbar.SetError("Spawn worker: orchestrator has no live coordinator")
		return
	}
	// Resolve the project from the coordinator's OWN task row (authoritative —
	// matches the MCP arm's resolution, never trusts role.ArgusProject).
	project := ""
	if ct, err := a.db.Get(coordTaskID); err == nil && ct != nil {
		project = ct.Project
	}
	if project == "" {
		uxlog.Log("[hera-view] spawn: coordinator task %s has no project — ignored", coordTaskID)
		a.statusbar.SetError("Spawn worker: coordinator task has no project")
		return
	}
	orchID, orchName := orch.ID, orch.Name
	coordName := heraCoordRoleName(orch)
	// BUG-005: spawn from the FULL new-task modal (project/branch/backend/model/
	// prompt), project defaulting to the coordinator's. On submit, spawn a
	// born-bound worker under the current coordinator via the shared primitive.
	a.openHeraNewTaskForm(fmt.Sprintf(" Spawn worker under %s ", orchName), project, func(task *model.Task, name string) {
		a.heraDoSpawnWorker(orchID, orchName, coordName, name, task)
	})
}

// heraSpawnName resolves the name for a hera worker/coordinator spawn: the
// user-entered name (from the modal's optional name field) when non-blank, else
// the prompt-derived slug. Shared by the rail `w` and `n` handlers so an explicit
// name overrides the auto-derived one while a blank field reproduces the prior
// (prompt-derived) behavior exactly.
func heraSpawnName(entered, prompt string) string {
	if entered != "" {
		return entered
	}
	return agent.DeriveHeraWorkerName(prompt)
}

// heraCoordRoleName returns the orchestrator's coordinator role name (for the
// worker orientation prefix), or "coordinator" as a fallback.
func heraCoordRoleName(o *hera.OrchView) string {
	for i := range o.Roles {
		if o.Roles[i].Kind == db.HeraKindCoordinator {
			return o.Roles[i].Name
		}
	}
	return "coordinator"
}

// heraDoSpawnWorker runs the transactional spawn off the main thread (worktree +
// session creation can take a second), then refreshes on the main thread. The
// task carries the new-task form's project/branch/backend/model/prompt.
func (a *App) heraDoSpawnWorker(orchID int64, orchName, coordName, name string, task *model.Task) {
	d, ok := a.db.(*db.DB)
	if !ok {
		return
	}
	prompt := task.Prompt
	baseName := heraSpawnName(name, prompt)
	uxlog.Log("[hera-view] spawn worker: orch=%d (%s) project=%s name=%s", orchID, orchName, task.Project, baseName)
	go func() {
		res, err := agent.SpawnHeraWorker(d, a.runner, agent.HeraWorkerSpawnInput{
			OrchestratorID: orchID,
			BaseName:       baseName,
			TaskPrompt:     agent.HeraWorkerOrientation(orchName, coordName) + "\n\n---\n\n" + prompt,
			RolePrompt:     prompt,
			Project:        task.Project,
			Branch:         task.Branch,
			Backend:        task.Backend,
			Model:          task.Model,
		})
		a.tapp.QueueUpdateDraw(func() {
			if err != nil {
				uxlog.Log("[hera-view] spawn worker failed: %v", err)
				a.statusbar.SetError("Spawn worker failed: " + err.Error())
				return
			}
			a.recentStarts[res.Task.ID] = a.nowFn()
			uxlog.Log("[hera-view] spawn worker ok: role=%s task=%s", res.Role.Name, res.Task.ID)
			a.heraRefresh()
		})
	}()
}

// --- `n` new root coordinator (BUG-006) -------------------------------------

// heraNewCoordinator opens the full new-task modal (independent of the current
// selection) to create a NEW top-level orchestrator + coordinator. The project
// defaults to the current selection's coordinator project when resolvable, else
// the last-selected Tasks-tab project.
func (a *App) heraNewCoordinator(sel hera.Selection) {
	if a.heraOps == nil {
		return
	}
	project := a.tasklist.SelectedProject()
	if ct := sel.CoordTaskID(); ct != "" {
		if t, err := a.db.Get(ct); err == nil && t != nil && t.Project != "" {
			project = t.Project
		}
	}
	a.openHeraNewTaskForm(" New coordinator ", project, func(task *model.Task, name string) {
		a.heraDoNewCoordinator(name, task)
	})
}

// heraDoNewCoordinator runs the transactional root-coordinator spawn off the
// main thread, then refreshes. When name is non-blank it names BOTH the new
// orchestrator (the rail label) and the coordinator task (TaskName is left ""
// so SpawnHeraCoordinator defaults it to the de-collided orchestrator name,
// keeping the two consistent); blank derives the name from the prompt and
// de-collides as before. The coordinator role is always named "coord".
func (a *App) heraDoNewCoordinator(name string, task *model.Task) {
	d, ok := a.db.(*db.DB)
	if !ok {
		return
	}
	prompt := task.Prompt
	base := heraSpawnName(name, prompt)
	uxlog.Log("[hera-view] new coordinator: base=%s project=%s", base, task.Project)
	go func() {
		res, err := agent.SpawnHeraCoordinator(d, a.runner, agent.HeraCoordinatorSpawnInput{
			OrchestratorBaseName: base,
			CoordRoleName:        "coord",
			TaskPrompt:           agent.HeraCoordinatorOrientation(base) + "\n\n---\n\n" + prompt,
			RolePrompt:           prompt,
			Project:              task.Project,
			Branch:               task.Branch,
			Backend:              task.Backend,
			Model:                task.Model,
		})
		a.tapp.QueueUpdateDraw(func() {
			if err != nil {
				uxlog.Log("[hera-view] new coordinator failed: %v", err)
				a.statusbar.SetError("New coordinator failed: " + err.Error())
				return
			}
			a.recentStarts[res.Task.ID] = a.nowFn()
			uxlog.Log("[hera-view] new coordinator ok: orch=%s role=%s task=%s", res.Orchestrator.Name, res.Role.Name, res.Task.ID)
			a.heraRefresh()
		})
	}()
}

// heraOpenRename opens the input modal pre-filled with the selected row's name.
func (a *App) heraOpenRename(sel hera.Selection) {
	if a.heraOps == nil {
		return
	}
	var title, current string
	switch {
	case sel.Role != nil:
		title, current = "Rename role", sel.Role.Name
	case sel.Orch != nil:
		title, current = "Rename orchestrator", sel.Orch.Name
	default:
		return
	}
	a.openHeraInput(title, current, func(newName string) {
		if err := a.heraOps.Rename(sel, newName); err != nil {
			if errors.Is(err, db.ErrHeraNameConflict) {
				a.statusbar.SetError("Rename failed: name already in use")
			} else {
				a.statusbar.SetError("Rename failed: " + err.Error())
			}
			return
		}
		a.heraRefresh()
	})
}

// heraHide is the `a` key: Tier-1 HIDE/unhide of the selected WORKER (BUG-022).
// Hiding archives the role row so it nests in its PARENT coordinator's "Archive
// (N)" expando; the session + worktree stay ALIVE (no detach) and it is a
// reversible toggle (pressing `a` on a hidden worker unhides it exactly), so
// there is NO confirmation. Hide applies to workers / sub-coordinators only — on
// a top-level coordinator or orchestrator header (no parent archive to nest
// under) it surfaces feedback and is a no-op. A sub-coordinator rendered as a
// bridging worker row is worker-kind, so it hides through this same path; its
// whole subtree collapses into the parent's Archive expando (Q3 — see rail.go
// appendOrchWorkers), structure retained.
//
// Q2 (LOCKED): HIDE is RAIL-ONLY — it archives the hera ROLE row only and does
// NOT db.SetArchived the bound argus task. The worker keeps running and still
// shows in the Tasks tab; only NUKE (Ctrl+D/C) archives the task + reclaims the
// worktree. Un-hide just clears archived_at and the rail row returns exactly.
func (a *App) heraHide(sel hera.Selection) {
	if a.heraOps == nil {
		return
	}
	r := sel.Role
	if r == nil || r.Kind != db.HeraKindWorker {
		a.statusbar.SetError("Hide applies to workers and sub-coordinators")
		return
	}
	if err := a.heraOps.ArchiveToggle(sel); err != nil {
		a.statusbar.SetError("Hide failed: " + err.Error())
		return
	}
	a.heraRefresh()
}

// heraPinToggle pins/unpins the selected row immediately (no confirm).
func (a *App) heraPinToggle(sel hera.Selection) {
	if a.heraOps == nil {
		return
	}
	if err := a.heraOps.PinToggle(sel); err != nil {
		a.statusbar.SetError("Pin failed: " + err.Error())
		return
	}
	a.heraRefresh()
}

// heraStatusStep advances/reverts the selected role's status (no confirm).
func (a *App) heraStatusStep(sel hera.Selection, dir int) {
	if a.heraOps == nil {
		return
	}
	if sel.StatusRole() == nil {
		return // empty selection, or a header over a coordinator-less orchestrator
	}
	if err := a.heraOps.StepStatus(sel, dir); err != nil {
		a.statusbar.SetError("Status step failed: " + err.Error())
		return
	}
	a.heraRefresh()
}

// heraOpenDelete is the `Ctrl+D` key: Tier-2 NUKE of the selected role or
// orchestrator (BUG-022). Nuke NEVER hard-deletes a DB row: it stamps the hera
// rows NUKED (nuked_at, so they leave the rail ENTIRELY — not shown in any
// archive) and reclaims only the real resources — it stops the session and
// removes the worktree + local AND remote branch, and ARCHIVES the bound argus
// task. The role/orchestrator rows, the role's inbox, and the task all survive
// in the DB (recover by re-spinning a fresh worktree). The difference from the
// `a` HIDE key: `a` archives but KEEPS the worktree/session alive and stays
// visible in the parent's archive expando (reversible); `Ctrl+D` removes the row
// from the rail and reclaims the worktree/session.
//
// A coordinator / orchestrator HEADER selection — like a nested-bridge worker row
// — cascades the FULL subtree rooted at it: this orchestrator, every nested
// sub-coordinator, and all their agents are nuked + their worktrees reclaimed,
// preserving any task bound live OUTSIDE the subtree (multi-binding safety — that
// task is left fully alone, not archived, worktree kept).
func (a *App) heraOpenDelete(sel hera.Selection) {
	if a.heraOps == nil {
		return
	}
	switch {
	case sel.Role != nil && sel.BridgeChildOrchID != 0:
		// The selected row bridges a nested sub-team — Ctrl+D tears down the whole
		// subtree (the child orchestrator and every orchestrator nested beneath
		// it), confirm-gated with a destructive warning that enumerates how many
		// orchestrators / agents / worktrees go away.
		a.heraCascadeNukeFrom(sel.BridgeChildOrchID)
	case sel.Role != nil:
		r := sel.Role
		msg := "Removes the role from the rail and ends its binding (row retained for DB recovery)"
		if r.Live && a.heraTaskSolelyBoundTo(r) {
			// Sole binding → reclaim the worktree and archive the task too.
			msg = "Stops the session, reclaims its worktree + branch, removes the role from the rail, and archives the task (all rows retained for DB recovery)"
		} else if r.Live {
			// Multi-bound task → preserve it (archiving/reclaiming it would strand the
			// SAME task's binding in another orchestrator — violates isolation).
			msg = "Removes this role from the rail + ends its binding; the task stays (it is bound elsewhere)"
		}
		a.openHeraConfirm("Nuke role "+r.Name+"?", msg+".", func() {
			a.heraNukeRole(r)
		})
	case sel.Orch != nil:
		// A coordinator / orchestrator HEADER nuke runs the SAME full-subtree
		// cascade as a nested-bridge worker row — it tears down this coordinator,
		// its agents, AND every nested sub-coordinator + their agents (stopping
		// sessions, reclaiming worktrees+branches, removing them from the rail).
		a.heraCascadeNukeFrom(sel.Orch.ID)
	}
}

// heraTaskSolelyBoundTo reports whether the role's bound task has exactly one
// live binding (this role's) — i.e. destroying the task is safe and won't end
// another orchestrator's binding to the same task (multi-binding isolation).
func (a *App) heraTaskSolelyBoundTo(r *hera.RoleView) bool {
	d, ok := a.db.(*db.DB)
	if !ok || r.TaskID == "" {
		return false
	}
	live, err := d.ListHeraLiveBindingsByTask(r.TaskID)
	if err != nil {
		return false // err on the side of preserving the task
	}
	return len(live) == 1
}

// heraNukeRole NUKES a role (NO hard delete). When the role's task is solely
// bound to this role, the App reclaims the real resources — stop session + remove
// worktree + branch — and ARCHIVES the task row (heraReclaimAndArchiveTask). Then
// Ops.NukeRole ends the role's binding + stamps the role row NUKED (nuked_at), so
// it leaves the rail entirely. When the task is bound under MULTIPLE orchestrators
// it is PRESERVED (left fully alone, worktree kept) — only this role's row is
// nuked + its binding ended; the other orchestrator's binding to the SAME task is
// untouched. Zero hard deletes: the role row, its inbox, and the task all survive.
func (a *App) heraNukeRole(r *hera.RoleView) {
	if r.Live && r.TaskID != "" && a.heraTaskSolelyBoundTo(r) {
		a.heraReclaimAndArchiveTask(r.TaskID)
	}
	if err := a.heraOps.NukeRole(r); err != nil {
		a.statusbar.SetError("Nuke role failed: " + err.Error())
	}
	a.heraRefresh()
}

// heraReclaimAndArchiveTask reclaims a task's REAL resources and archives its row
// — the nuke teardown for a task whose live bindings are all within the subtree
// (or sole-bound to a single nuked role). It stops the session, removes the
// worktree + LOCAL and REMOTE branch (background — git is slow), and ARCHIVES the
// task row (db.SetArchived, NEVER db.Delete). The task survives in the Archive
// section as history; only the worktree directory + git branch are reclaimed.
// Returns whether a worktree-removal was kicked off (for the prune count/log).
func (a *App) heraReclaimAndArchiveTask(taskID string) (reclaimed bool) {
	t, err := a.db.Get(taskID)
	if err != nil || t == nil {
		uxlog.Log("[hera-view] nuke: task %s not found, skip reclaim: %v", taskID, err)
		return false
	}
	if a.runner.HasSession(t.ID) {
		if sErr := a.runner.Stop(t.ID); sErr != nil {
			uxlog.Log("[hera-view] nuke: stop session failed task=%s: %v", t.ID, sErr)
		}
	}
	cfg := a.db.Config()
	repoDir := agent.ResolveDir(t, cfg)
	wt, br := t.Worktree, t.Branch
	if wt != "" {
		reclaimed = true
		go func() { agent.RemoveWorktreeAndBranch(wt, br, repoDir) }()
	} else if br != "" && repoDir != "" {
		go func() {
			agent.DeleteBranch(repoDir, br)
			agent.DeleteRemoteBranch(repoDir, br)
		}()
	}
	if aErr := a.db.SetArchived(t.ID, true); aErr != nil {
		uxlog.Log("[hera-view] nuke: archive task failed task=%s: %v", t.ID, aErr)
	} else {
		uxlog.Log("[hera-view] nuke: reclaimed worktree + archived task %s", t.ID)
	}
	return reclaimed
}

// heraCascadeNukeFrom confirms then NUKES the entire subtree rooted at rootID —
// the orchestrator itself plus every orchestrator nested beneath it through the
// worker→coordinator bridge — and reclaims their worktrees. rootID may be a
// TOP-LEVEL coordinator (the Ctrl+D-on-header path) or a nested sub-coordinator
// reached through a bridging worker row; BridgeSubtree walks the same hierarchy
// either way. NOTHING is hard-deleted: the orchestrator + role rows are stamped
// NUKED (removed from the rail, inboxes preserved, recoverable via the DB) and
// each sole-bound task row is archived; only the worktree + branch are reclaimed.
// The confirm modal is explicit about the RECLAIM: it tells the operator how many
// orchestrators and agents are removed and how many worktrees+branches are
// reclaimed. Tasks bound in another (non-subtree) orchestrator are preserved —
// multi-binding safety (left fully alone).
func (a *App) heraCascadeNukeFrom(rootID int64) {
	if a.heraOps == nil {
		return
	}
	// The subtree snapshot is captured point-in-time (modal-open). The app tick
	// can rebuild the rail model while the confirm is open, but every op below
	// re-validates against the LIVE DB (heraTaskBoundOutside re-queries,
	// heraReclaimAndArchiveTask re-fetches, NukeOrchestrator keys on the stable
	// id), so a stale pointer never nukes the wrong row — at worst a role added
	// after modal-open keeps its task (recoverable from the Tasks tab).
	subtree := a.heraPage.Rail().Model().BridgeSubtree(rootID)
	if len(subtree) == 0 {
		return
	}
	subtreeIDs := make(map[int64]bool, len(subtree))
	for _, o := range subtree {
		subtreeIDs[o.ID] = true
	}
	// Count distinct managed tasks and, accurately, how many worktrees the cascade
	// reclaims: a task's worktree is reclaimed iff it has NO live binding OUTSIDE
	// the subtree (an internal bridge task between two subtree orchestrators counts
	// as in-subtree and IS reclaimed). This matches the action below exactly, so the
	// modal never undercounts internal-bridge worktrees in a deep (≥2 level) subtree.
	agents, worktrees, preserved := 0, 0, 0
	seen := make(map[string]bool)
	for _, o := range subtree {
		for i := range o.Roles {
			r := &o.Roles[i]
			if !r.Live || r.TaskID == "" {
				continue
			}
			if r.Kind != db.HeraKindCoordinator {
				agents++
			}
			if seen[r.TaskID] {
				continue
			}
			seen[r.TaskID] = true
			if a.heraTaskBoundOutside(r.TaskID, subtreeIDs) {
				preserved++
			} else {
				worktrees++
			}
		}
	}
	title := "Nuke " + subtree[0].Name + " and its whole team?"
	msg := fmt.Sprintf(
		"This removes %d orchestrator(s) and %d agent(s) from the rail and reclaims %d worktree(s) + branch(es), stopping their sessions. Rows are retained (recoverable via the DB). %d task(s) bound in another orchestrator are preserved.",
		len(subtree), agents, worktrees, preserved)
	a.openHeraConfirm(title, msg, func() { a.heraDoCascadeNuke(subtree, subtreeIDs) })
}

// heraTaskBoundOutside reports whether taskID holds at least one LIVE binding to
// an orchestrator NOT in the subtree set — i.e. it must be preserved on cascade
// (multi-binding safety: left fully alone, not archived, worktree kept). A task
// bound only within the subtree (including an internal bridge between two subtree
// orchestrators) returns false and is reclaimed + archived. A query error errs on
// the side of preserving (returns true).
func (a *App) heraTaskBoundOutside(taskID string, subtreeIDs map[int64]bool) bool {
	d, ok := a.db.(*db.DB)
	if !ok {
		return true
	}
	live, err := d.ListHeraLiveBindingsByTask(taskID)
	if err != nil {
		return true
	}
	for _, b := range live {
		if !subtreeIDs[b.OrchestratorID] {
			return true
		}
	}
	return false
}

// heraDoCascadeNuke performs the subtree teardown after the operator confirms.
// NOTHING is hard-deleted. For each orchestrator it:
//   - reclaims the worktree + branch and ARCHIVES every managed task whose live
//     bindings are ALL within the subtree (heraReclaimAndArchiveTask: stop session
//   - RemoveWorktreeAndBranch + db.SetArchived). A task bound OUTSIDE the subtree
//     (e.g. a bridge task still held by a non-subtree parent) is preserved — left
//     fully alone (not archived, worktree kept).
//   - NUKES every LIVE role row + ends its binding (Ops.NukeRole — stamps nuked_at,
//     removes the row from the rail; never DeleteHeraRole);
//   - NUKES the orchestrator row (Ops.NukeOrchestrator — NOT DeleteHeraOrchestrator).
//
// The task reclaim decision is made BEFORE ending that role's binding so the
// multi-binding check sees full binding state. The reclaimed set guards against
// double-archiving a task reached via two roles. Net: zero hard deletes — every
// role/orchestrator/inbox/task row is retained (recoverable via the DB), only the
// worktree dir + git branch are reclaimed.
func (a *App) heraDoCascadeNuke(subtree []*hera.OrchView, subtreeIDs map[int64]bool) {
	reclaimed := make(map[string]bool)
	for _, o := range subtree {
		for i := range o.Roles {
			r := &o.Roles[i]
			if r.Live && r.TaskID != "" && !reclaimed[r.TaskID] && !a.heraTaskBoundOutside(r.TaskID, subtreeIDs) {
				reclaimed[r.TaskID] = true
				a.heraReclaimAndArchiveTask(r.TaskID)
			}
			if r.Live {
				if err := a.heraOps.NukeRole(r); err != nil {
					uxlog.Log("[hera-view] cascade: nuke role %d failed: %v", r.RoleID, err)
				}
			}
		}
		if err := a.heraOps.NukeOrchestrator(o.ID); err != nil {
			a.statusbar.SetError("Nuke sub-team failed: " + err.Error())
		}
	}
	a.heraRefresh()
}

// --- `C` clear-this-coordinator's-archive (BUG-022) -------------------------

// heraTaskReclaimable reports whether a task's worktree can be reclaimed during a
// nuke: it is reclaimable iff no LIVE binding belongs to a DIFFERENT role (so a
// task still bound live elsewhere — multi-binding — is preserved). A query error
// errs on the side of preserving (returns false).
func (a *App) heraTaskReclaimable(taskID string, roleID int64) bool {
	d, ok := a.db.(*db.DB)
	if !ok {
		return false
	}
	live, err := d.ListHeraLiveBindingsByTask(taskID)
	if err != nil {
		return false
	}
	for _, b := range live {
		if b.RoleID != roleID {
			return false
		}
	}
	return true
}

// roleReclaimTask returns the role's reclaim-target task id (its latest binding
// task, covering hidden/archived roles whose live binding already ended), or "".
func roleReclaimTask(r *hera.RoleView) string {
	if r.BridgeTaskID != "" {
		return r.BridgeTaskID
	}
	return r.TaskID
}

// heraCountReclaimable splits roles into those whose worktree will be reclaimed
// vs those preserved because the task is bound live elsewhere. Roles with no task
// are neither (just a role-row nuke).
func (a *App) heraCountReclaimable(roles []hera.RoleView) (reclaim, preserved int) {
	for i := range roles {
		tid := roleReclaimTask(&roles[i])
		if tid == "" {
			continue
		}
		if a.heraTaskReclaimable(tid, roles[i].RoleID) {
			reclaim++
		} else {
			preserved++
		}
	}
	return
}

// heraNukeArchivedRole NUKES a Tier-1 hidden (archived) role: it reclaims the
// task's worktree+branch and ARCHIVES the task when reclaimable (no live binding
// belongs to another role), then stamps the role row NUKED (Ops.NukeRole). A task
// bound live elsewhere keeps its task/worktree (only this role row is nuked —
// multi-binding isolation). NO hard delete. Returns whether a worktree reclaim
// was kicked off (for the count/log).
func (a *App) heraNukeArchivedRole(r *hera.RoleView) (reclaimed bool) {
	taskID := roleReclaimTask(r)
	if taskID != "" && a.heraTaskReclaimable(taskID, r.RoleID) {
		reclaimed = a.heraReclaimAndArchiveTask(taskID)
	}
	if err := a.heraOps.NukeRole(r); err != nil {
		uxlog.Log("[hera-view] clear archive: nuke role %d failed: %v", r.RoleID, err)
	}
	return reclaimed
}

// heraClearArchive (`C`) confirms then NUKES every Tier-1 hidden (archived)
// descendant WORKER in the selected coordinator's subtree — equivalent to Ctrl+D
// on each: reclaim its worktree+branch (unless bound live elsewhere), archive its
// task, and mark its role row NUKED (removed from the rail, retained in the DB).
// Scoped to the SELECTED coordinator's archive, never global. An empty archive
// surfaces "nothing to clear".
func (a *App) heraClearArchive(sel hera.Selection) {
	if a.heraOps == nil {
		return
	}
	orch := sel.Orch
	if orch == nil {
		a.statusbar.SetError("Clear archive: select a coordinator")
		return
	}
	// Scope to the SELECTED coordinator. When the cursor rests on a bridging
	// sub-coordinator row, sel.Orch is the PARENT orchestrator but the archive we
	// clear belongs to the nested CHILD — use its own orch id (mirrors the exact
	// disambiguation in heraOpenDelete's Ctrl+D cascade). A plain top-level
	// coordinator (no BridgeChildOrchID) scopes to its own orch, unchanged.
	scopeID, scopeName := orch.ID, orch.Name
	model := a.heraPage.Rail().Model()
	if sel.Role != nil && sel.BridgeChildOrchID != 0 {
		scopeID = sel.BridgeChildOrchID
		if child := model.OrchByID(scopeID); child != nil {
			scopeName = child.Name
		}
	}
	workers := model.SubtreeArchivedWorkers(scopeID)
	if len(workers) == 0 {
		a.statusbar.SetInfo("Nothing to clear")
		return
	}
	reclaim, preserved := a.heraCountReclaimable(workers)
	msg := fmt.Sprintf(
		"Nukes %d hidden agent(s) and reclaims %d worktree(s)+branch(es). %d preserved (bound elsewhere). Rows are retained (recoverable via the DB).",
		len(workers), reclaim, preserved)
	a.openHeraConfirm("Clear "+scopeName+"'s archive?", msg, func() {
		a.heraDoClearArchive(workers)
	})
}

// heraDoClearArchive nukes each hidden role in the set and refreshes.
func (a *App) heraDoClearArchive(roles []hera.RoleView) {
	n, wt := 0, 0
	for i := range roles {
		if a.heraNukeArchivedRole(&roles[i]) {
			wt++
		}
		n++
	}
	uxlog.Log("[hera-view] clear archive: %d hidden role(s) nuked, %d worktree(s) reclaimed", n, wt)
	a.heraRefresh()
}

// heraReattach revives the session backing the selected role's task. The page
// fires it on Enter for a dead session (any role) or a live worker/freelance
// role. Two branches:
//
//   - DEAD session (runner has no live session) → restart it via startSession
//     (resumes via --session-id when a SessionID exists). Same as before, and
//     the only path used for coordinators.
//   - LIVE worker/freelance session → it may be SIGTSTP-suspended or otherwise
//     stuck while still "alive" (Alive() can't tell a stopped process from a
//     running one). Revive it the same way the Tasks pane reconnects a live
//     session: an idle-gated in-place stop+resume (reviveHeraWorker). A busy
//     worker, or one parked at a user prompt, is left untouched.
func (a *App) heraReattach(sel hera.Selection) {
	// FocusTaskID resolves the coordinator task for a header (folded coordinator)
	// selection, where the selected role is nil.
	taskID := sel.FocusTaskID()
	if taskID == "" {
		return
	}
	t, err := a.db.Get(taskID)
	if err != nil || t == nil {
		uxlog.Log("[hera-view] reattach: task %s not found: %v", taskID, err)
		return
	}
	sess := a.runner.Get(taskID)
	if sess == nil || !sess.Alive() {
		uxlog.Log("[hera-view] reattach: restarting dead session for task %s (%s)", t.ID, t.Name)
		a.startSession(t)
		a.heraRefresh()
		return
	}
	// Live coordinator: navigate-only (operator-interactive), never auto-restart.
	if sel.IsCoordinator() {
		uxlog.Log("[hera-view] reattach: live coordinator task %s — navigate only", t.ID)
		return
	}
	a.reviveHeraWorker(t, sess)
}

// reviveHeraWorker brings a live-but-stuck worker session back. A SIGTSTP'd or
// otherwise stalled agent is idle (no recent output) and not parked at a user
// prompt; that is the signature we revive. The revive reuses the runner's
// KickRerender — the SAME stop-and-resume primitive the Tasks pane's rerender
// path uses — which owns the stop + resume entirely inside the runner/daemon.
// We deliberately do NOT reuse the TUI-side pendingRerenderRestart path
// (handleSessionExitUI), because that only restarts while the operator is
// viewing the task in the agent view; from the Hera tab it would settle the
// worker at InReview instead of resuming it. KickRerender resumes regardless of
// which tab is active, so the worker comes straight back via --session-id.
//
// A busy worker (still producing output) or one parked at a user prompt is left
// alone, so we never interrupt real work or dismiss a pending question. The
// liveness/idle reads hit the daemon over RPC, so they run on a background
// goroutine and the decision is dispatched back via QueueUpdateDraw — never
// block the tview main goroutine (mirrors maybeKickRerender).
func (a *App) reviveHeraWorker(task *model.Task, sess agent.SessionHandle) {
	if task == nil || sess == nil || !sess.Alive() {
		return
	}
	if task.SessionID == "" {
		uxlog.Log("[hera-view] revive: task %s has no session ID — cannot resume in place", task.ID)
		return
	}
	runner, ok := a.runner.(agent.SessionRunner)
	if !ok {
		uxlog.Log("[hera-view] revive: runner has no KickRerender (remote?) — skipping task %s", task.ID)
		return
	}
	if runner.HasPendingRestart(task.ID) {
		return // a kick is already in flight
	}
	taskID := task.ID
	cfg := a.db.Config()
	rows, cols := a.computePTYSize()
	go func() {
		idle := sess.IsIdle()
		blocked := sessionBlockedOnPrompt(taskID, idle)
		a.tapp.QueueUpdateDraw(func() {
			if !sess.Alive() || runner.HasPendingRestart(taskID) {
				return
			}
			if !idle {
				uxlog.Log("[hera-view] revive: task=%s busy — not interrupting", taskID)
				return
			}
			if blocked {
				uxlog.Log("[hera-view] revive: task=%s blocked on prompt — preserving question", taskID)
				return
			}
			uxlog.Log("[hera-view] revive: kicking suspended/stuck session task=%s session=%s", taskID, task.SessionID)
			a.statusbar.SetInfo("Reviving session…")
			if err := runner.KickRerender(task, cfg, rows, cols); err != nil {
				a.statusbar.SetError("Revive failed: " + err.Error())
				uxlog.Log("[hera-view] revive: kick failed task=%s: %v", taskID, err)
			} else {
				a.heraRefresh()
			}
		})
	}()
}

// --- `J` adopt / reparent ---------------------------------------------------

// heraOpenAdopt is the `J` key: adopt a freelancer into, or re-parent a
// coordinator under, a chosen orchestrator. It dispatches on the current rail
// selection (coordinator first, then freelancer), opens the orchestrator
// picker, and runs the mutation on selection. Every non-adoptable selection
// gets visible statusbar feedback — never a silent no-op. Inert in remote mode
// (heraAdoptOps is nil).
func (a *App) heraOpenAdopt(sel hera.Selection) {
	if a.heraAdoptOps == nil {
		return
	}
	// Coordinator selection (role row OR orchestrator header) → re-parent.
	if childOrchID, name, coordTaskID, ok := heraCoordReparentTarget(sel); ok {
		a.heraAdoptCoordinator(childOrchID, name, coordTaskID)
		return
	}
	// Freelancer selection → adopt as a worker.
	if r := sel.Role; r != nil && r.Kind == db.HeraKindFreelance && !r.Archived {
		a.heraAdoptFreelancer(r)
		return
	}
	a.statusbar.SetError("J: select a freelancer or a coordinator to adopt")
}

// heraCoordReparentTarget reports whether sel is a coordinator that `J` can
// re-parent, returning the child orchestrator id, a display name, and the
// coordinator's argus task hint (may be empty for a dormant coordinator — the
// op resolves it from the coord role's latest binding). Two shapes qualify: a
// coordinator-kind role row, or a non-archived orchestrator header whose
// orchestrator has a coordinator role.
func heraCoordReparentTarget(sel hera.Selection) (childOrchID int64, name, coordTaskID string, ok bool) {
	if r := sel.Role; r != nil {
		if r.Kind == db.HeraKindCoordinator && !r.Archived && sel.Orch != nil && !sel.Orch.Archived {
			return sel.Orch.ID, r.Name, r.TaskID, true
		}
		return 0, "", "", false
	}
	if o := sel.Orch; o != nil && !o.Archived {
		for i := range o.Roles {
			// Skip an archived coordinator role for symmetry with the role-row
			// branch above (BuildModel includes archived roles in OrchView.Roles).
			if o.Roles[i].Kind == db.HeraKindCoordinator && !o.Roles[i].Archived {
				return o.ID, o.Name, o.CoordTaskID(), true
			}
		}
	}
	return 0, "", "", false
}

// heraAdoptFreelancer opens the orchestrator picker for a freelancer and, on
// selection, creates the worker role + binding. Project + worktree are resolved
// from the freelancer's task row (authoritative — matches heraSpawnWorker).
func (a *App) heraAdoptFreelancer(r *hera.RoleView) {
	if r.TaskID == "" {
		a.statusbar.SetError("J: this freelancer has no argus task to adopt")
		return
	}
	project, worktree := "", ""
	if t, err := a.db.Get(r.TaskID); err == nil && t != nil {
		project, worktree = t.Project, t.Worktree
	}
	orchs, err := a.heraAdoptOps.ListActiveOrchestrators()
	if err != nil {
		a.statusbar.SetError("Adopt failed: " + err.Error())
		return
	}
	if len(orchs) == 0 {
		a.statusbar.SetError("J: no active coordinators to adopt into — create one with hera_new_orchestrator first")
		return
	}
	name, taskID := r.Name, r.TaskID
	a.openHeraOrchPicker(fmt.Sprintf("Adopt %q into…", name), orchs, func(o *db.HeraOrchestrator) {
		if _, err := a.heraAdoptOps.AdoptTaskIntoOrchestrator(hera.AdoptInput{
			ArgusTaskID:    taskID,
			OrchestratorID: o.ID,
			RoleName:       name,
			ArgusProject:   project,
			WorktreePath:   worktree,
		}); err != nil {
			a.statusbar.SetError("Adopt failed: " + err.Error())
			return
		}
		a.heraRefresh()
	})
}

// heraAdoptCoordinator opens the orchestrator picker (excluding the coordinator
// itself) and, on selection, re-parents the coordinator under the chosen parent.
// Descendant cycles are rejected authoritatively by ReparentCoordinator.
func (a *App) heraAdoptCoordinator(childOrchID int64, name, coordTaskID string) {
	project := ""
	if coordTaskID != "" {
		if t, err := a.db.Get(coordTaskID); err == nil && t != nil {
			project = t.Project
		}
	}
	orchs, err := a.heraAdoptOps.ListActiveOrchestrators()
	if err != nil {
		a.statusbar.SetError("Adopt failed: " + err.Error())
		return
	}
	targets := make([]*db.HeraOrchestrator, 0, len(orchs))
	for _, o := range orchs {
		if o.ID != childOrchID {
			targets = append(targets, o)
		}
	}
	if len(targets) == 0 {
		a.statusbar.SetError("J: no other active coordinator to adopt this coordinator under — create one with hera_new_orchestrator first")
		return
	}
	a.openHeraOrchPicker(fmt.Sprintf("Adopt coordinator %q under…", name), targets, func(o *db.HeraOrchestrator) {
		if _, err := a.heraAdoptOps.ReparentCoordinator(hera.ReparentInput{
			ChildOrchestratorID:  childOrchID,
			CoordTaskID:          coordTaskID,
			ParentOrchestratorID: o.ID,
			RoleName:             name,
			ArgusProject:         project,
		}); err != nil {
			a.statusbar.SetError("Adopt failed: " + err.Error())
			return
		}
		a.heraRefresh()
	})
}

// openHeraOrchPicker shows the `J` orchestrator picker; pick is called with the
// chosen orchestrator on Enter (Esc cancels with no change).
func (a *App) openHeraOrchPicker(title string, orchs []*db.HeraOrchestrator, pick func(*db.HeraOrchestrator)) {
	a.heraOrchPicker = NewOrchPickerModal(title, orchs)
	a.heraOrchPickerPick = pick
	a.mode = modeHeraOrchPicker
	a.pages.AddPage("heraorchpicker", a.heraOrchPicker, true, true)
	a.pages.SwitchToPage("heraorchpicker")
	a.tapp.SetFocus(a.heraOrchPicker)
}

func (a *App) handleHeraOrchPickerKey(event *tcell.EventKey) {
	a.heraOrchPicker.InputHandler()(event, func(tview.Primitive) {})
	if a.heraOrchPicker.Canceled() {
		a.closeHeraOrchPicker()
		return
	}
	if a.heraOrchPicker.Selected() {
		chosen := a.heraOrchPicker.SelectedOrch()
		pick := a.heraOrchPickerPick
		a.closeHeraOrchPicker()
		if pick != nil && chosen != nil {
			pick(chosen)
		}
	}
}

func (a *App) closeHeraOrchPicker() {
	a.mode = modeTaskList
	a.heraOrchPicker = nil
	a.heraOrchPickerPick = nil
	a.pages.RemovePage("heraorchpicker")
	a.pages.SwitchToPage("hera")
	a.tapp.SetFocus(a.heraPage)
}

// --- modal plumbing ---------------------------------------------------------

// openHeraInput shows a single-field input modal (rename / spawn prompt). submit
// is called with the trimmed value on Enter; an empty value re-prompts.
func (a *App) openHeraInput(title, initial string, submit func(string)) {
	a.heraInputModal = NewInputForm(title, initial)
	a.heraInputSubmit = submit
	a.mode = modeHeraInput
	a.pages.AddPage("herainput", a.heraInputModal, true, true)
	a.pages.SwitchToPage("herainput")
	a.tapp.SetFocus(a.heraInputModal)
}

func (a *App) handleHeraInputKey(event *tcell.EventKey) {
	a.heraInputModal.HandleKey(event)
	if a.heraInputModal.Canceled() {
		a.closeHeraInput()
		return
	}
	if a.heraInputModal.Done() {
		val := strings.TrimSpace(a.heraInputModal.Name())
		if val == "" {
			a.heraInputModal.ResetDone()
			a.heraInputModal.SetError("Cannot be empty")
			return
		}
		submit := a.heraInputSubmit
		a.closeHeraInput()
		if submit != nil {
			submit(val)
		}
	}
}

func (a *App) closeHeraInput() {
	a.mode = modeTaskList
	a.heraInputModal = nil
	a.heraInputSubmit = nil
	a.pages.RemovePage("herainput")
	a.pages.SwitchToPage("hera")
	a.tapp.SetFocus(a.heraPage)
}

// openHeraConfirm shows a y/N confirm; do runs on accept.
func (a *App) openHeraConfirm(title, message string, do func()) {
	a.heraConfirmModal = modal.NewConfirmModal(title, message)
	a.heraConfirmDo = do
	a.mode = modeHeraConfirm
	a.pages.AddPage("heraconfirm", a.heraConfirmModal, true, true)
	a.pages.SwitchToPage("heraconfirm")
	a.tapp.SetFocus(a.heraConfirmModal)
}

func (a *App) handleHeraConfirmKey(event *tcell.EventKey) {
	a.heraConfirmModal.InputHandler()(event, func(tview.Primitive) {})
	if a.heraConfirmModal.Confirmed() {
		do := a.heraConfirmDo
		a.closeHeraConfirm()
		if do != nil {
			do()
		}
		return
	}
	if a.heraConfirmModal.Canceled() {
		uxlog.Log("[hera-view] confirm canceled")
		a.closeHeraConfirm()
	}
}

func (a *App) closeHeraConfirm() {
	a.mode = modeTaskList
	a.heraConfirmModal = nil
	a.heraConfirmDo = nil
	a.pages.RemovePage("heraconfirm")
	a.pages.SwitchToPage("hera")
	a.tapp.SetFocus(a.heraPage)
}

// heraSelName returns a human label for the selected row (for modal titles).
func heraSelName(sel hera.Selection) string {
	if sel.Role != nil {
		return "role " + sel.Role.Name
	}
	if sel.Orch != nil {
		return "orchestrator " + sel.Orch.Name
	}
	return "selection"
}
