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
	a.openHeraInput(fmt.Sprintf("Spawn worker under %s", orchName), "", func(prompt string) {
		a.heraDoSpawnWorker(orchID, orchName, coordName, project, prompt)
	})
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
// session creation can take a second), then refreshes on the main thread.
func (a *App) heraDoSpawnWorker(orchID int64, orchName, coordName, project, prompt string) {
	d, ok := a.db.(*db.DB)
	if !ok {
		return
	}
	uxlog.Log("[hera-view] spawn worker: orch=%d (%s) project=%s", orchID, orchName, project)
	go func() {
		res, err := agent.SpawnHeraWorker(d, a.runner, agent.HeraWorkerSpawnInput{
			OrchestratorID: orchID,
			BaseName:       agent.DeriveHeraWorkerName(prompt),
			TaskPrompt:     agent.HeraWorkerOrientation(orchName, coordName) + "\n\n---\n\n" + prompt,
			RolePrompt:     prompt,
			Project:        project,
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

// heraArchiveToggle archives/unarchives the selected row. Archiving a row that
// currently holds live work (a live role, or an orchestrator with live roles)
// is confirmed first; unarchiving and archiving dormant rows are immediate.
func (a *App) heraArchiveToggle(sel hera.Selection) {
	if a.heraOps == nil {
		return
	}
	apply := func() {
		if err := a.heraOps.ArchiveToggle(sel); err != nil {
			a.statusbar.SetError("Archive failed: " + err.Error())
			return
		}
		a.heraRefresh()
	}
	if a.heraArchivingLive(sel) {
		name := heraSelName(sel)
		a.openHeraConfirm("Archive "+name+"?", "It holds live work — archiving hides it but keeps the session running.", apply)
		return
	}
	apply()
}

// heraArchivingLive reports whether this toggle would ARCHIVE (not unarchive) a
// row that currently holds live work.
func (a *App) heraArchivingLive(sel hera.Selection) bool {
	if r := sel.Role; r != nil {
		return !r.Archived && r.Live
	}
	if o := sel.Orch; o != nil {
		if o.Archived {
			return false
		}
		for i := range o.Roles {
			if o.Roles[i].Live {
				return true
			}
		}
	}
	return false
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
	if sel.Role == nil {
		return // orchestrator header has no status
	}
	if err := a.heraOps.StepStatus(sel, dir); err != nil {
		a.statusbar.SetError("Status step failed: " + err.Error())
		return
	}
	a.heraRefresh()
}

// heraOpenDelete confirms then deletes the selected role or orchestrator. A role
// bound to a live argus task reuses the App's transactional deleteTask (stop
// session + remove worktree/branch + end binding) before removing the role row;
// an orchestrator delete cascades its hera rows but PRESERVES the argus tasks.
func (a *App) heraOpenDelete(sel hera.Selection) {
	if a.heraOps == nil {
		return
	}
	switch {
	case sel.Role != nil:
		r := sel.Role
		msg := "Removes the role and ends its binding"
		if r.Live && a.heraTaskSolelyBoundTo(r) {
			// Sole binding → safe to destroy the underlying task too.
			msg = "Stops the session and removes its worktree, branch, and role"
		} else if r.Live {
			// Multi-bound task → preserve it (deleting it would end the SAME
			// task's binding in another orchestrator — violates isolation).
			msg = "Removes this role + binding; the task stays (it is bound elsewhere)"
		}
		a.openHeraConfirm("Delete role "+r.Name+"?", msg+".", func() {
			a.heraDeleteRole(r)
		})
	case sel.Orch != nil:
		o := sel.Orch
		a.openHeraConfirm("Delete orchestrator "+o.Name+"?",
			"Removes the orchestrator and all its roles. The underlying argus tasks are preserved.",
			func() { a.heraDeleteOrchestrator(o.ID) })
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

// heraDeleteRole removes a role. When the role's task is solely bound to this
// role, the App's transactional deleteTask tears down the task + worktree +
// branch (and the task-delete cascade ends the binding); DeleteRole then removes
// the role row. When the task is bound under MULTIPLE orchestrators, only the
// role row is deleted (its binding cascades) and the task is preserved — the
// other orchestrator's binding to the SAME task is untouched.
func (a *App) heraDeleteRole(r *hera.RoleView) {
	if r.Live && r.TaskID != "" && a.heraTaskSolelyBoundTo(r) {
		if t, err := a.db.Get(r.TaskID); err == nil && t != nil {
			a.deleteTask(t) // stops session, removes worktree/branch, ends binding
		}
	}
	if err := a.heraOps.DeleteRole(r.RoleID); err != nil {
		a.statusbar.SetError("Delete role failed: " + err.Error())
	}
	a.heraRefresh()
}

func (a *App) heraDeleteOrchestrator(orchID int64) {
	if err := a.heraOps.DeleteOrchestrator(orchID); err != nil {
		a.statusbar.SetError("Delete orchestrator failed: " + err.Error())
	}
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
	taskID := sel.TaskID()
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
