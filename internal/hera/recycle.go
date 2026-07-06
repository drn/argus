package hera

import (
	"fmt"
	"sort"
	"strings"

	"github.com/drn/argus/internal/db"
)

// RecycleTrigger distinguishes recycle_coord's two trigger paths (design.md
// D5): they share the same kill-and-restart mechanism but differ in whether
// they wait for the session to go idle first.
type RecycleTrigger int

const (
	// RecycleSelfService is the graceful path: a coordinator has signaled
	// recycle intent (hera_status request_recycle=true, see the
	// hera-coordination extension). RecycleCoord defers the actual
	// kill-and-restart until the session is genuinely idle — it is a
	// no-op (not an error) while the session is still producing output, so
	// callers are expected to invoke it repeatedly (once per background-
	// watcher tick; see RecycleWatcher) until it takes effect.
	RecycleSelfService RecycleTrigger = iota
	// RecycleHumanForced is the immediate path: an operator forces a
	// recycle on a coordinator (e.g. the hera-view rail keybinding)
	// regardless of activity state. This is the must-have path for a
	// coordinator that is wedged and will never become idle on its own.
	RecycleHumanForced
)

// RecycleStore is the narrow DB surface RecycleCoord and BuildRecycleSeedPrompt
// need. Satisfied by the real *db.DB.
type RecycleStore interface {
	HeraLiveBindingByRole(roleID int64) (*db.HeraBinding, error)
	HeraRole(id int64) (*db.HeraRole, error)
	ListHeraRoles(orchID int64, includeArchived bool) ([]*db.HeraRole, error)
	HeraRoleStatusFor(roleID int64) (*db.HeraRoleStatus, error)
	ListMeta(taskID, namespace string) ([]db.TaskMetaEntry, error)
	SetMeta(taskID, namespace, key, value string) error
}

// RecycleRunner is the daemon-side seam for the actual session kill/restart —
// injected so RecycleCoord's gating logic is testable without a real PTY or
// `claude agents` registry. The real implementation (internal/daemon) wires
// IsIdle/Restart to *agent.Runner (or the supervisor-client) and StopStrayJobs
// to agent.StopStrayJobs.
type RecycleRunner interface {
	// IsIdle reports whether the coordinator's session for taskID is
	// currently idle (no output being actively produced).
	IsIdle(taskID string) bool
	// StopStrayJobs terminates any background job tied to sessionID that
	// survives independently of the primary PTY (the task_stop-doesn't-
	// kill-everything failure mode documented in design.md's Risks) — must
	// run before Restart so a surviving job cannot conflict with the fresh
	// session's worktree writes.
	StopStrayJobs(taskID, sessionID string) error
	// Restart kills the coordinator's current session (if any) and starts a
	// fresh one on the same task — same worktree, same branch, same hera
	// binding, resume=false so the new session starts with empty context.
	Restart(taskID string) error
}

// RecycleCoord kills and restarts a coordinator role's session on its
// existing argus task, per design.md D5. It never rebinds or mints a new
// task/worktree — hera bindings key on (role, orchestrator, task), not
// session ID, so nothing about the binding needs to change.
//
// trigger == RecycleSelfService defers the kill-and-restart until
// runner.IsIdle(taskID) — it returns nil without restarting while the
// session is still producing output, so calling it repeatedly (once per
// background-watcher tick) is the intended usage.
//
// trigger == RecycleHumanForced acts immediately regardless of idleness —
// this is the must-have path for a coordinator that is wedged and will never
// become idle on its own.
//
// sessionID identifies the outgoing session for stray-job cleanup; it is the
// caller's responsibility to resolve it (typically the argus task's current
// SessionID) since RecycleStore has no notion of "current session" beyond
// the hera binding.
func RecycleCoord(store RecycleStore, runner RecycleRunner, roleID int64, sessionID string, trigger RecycleTrigger) error {
	binding, err := store.HeraLiveBindingByRole(roleID)
	if err != nil {
		return fmt.Errorf("recycle_coord: resolve binding for role %d: %w", roleID, err)
	}
	taskID := binding.ArgusTaskID

	if trigger == RecycleSelfService && !runner.IsIdle(taskID) {
		return nil // deferred — the next watcher tick re-checks
	}

	if err := runner.StopStrayJobs(taskID, sessionID); err != nil {
		return fmt.Errorf("recycle_coord: stop stray jobs for task %s: %w", taskID, err)
	}

	if err := runner.Restart(taskID); err != nil {
		return fmt.Errorf("recycle_coord: restart task %s: %w", taskID, err)
	}

	// Clear the self-service intent so a background watcher doesn't re-fire
	// on its next tick, and so a stale flag can't immediately re-trigger a
	// human-forced recycle's successor session. Best-effort: the recycle
	// itself already succeeded, so a failure here is logged by the caller's
	// own store implementation, not escalated into this call's result.
	_ = store.SetMeta(taskID, db.HeraMetaNamespace, db.HeraMetaKeyPendingRecycle, "false")

	return nil
}

// BuildRecycleSeedPrompt composes the opening prompt for the fresh session
// started by RecycleCoord's Restart step, per design.md D5: the role's stored
// mission, the current plan-DAG node states for the role's orchestrator, and
// any handoff_note left in task_meta. The result requires zero follow-up tool
// calls from the new session — everything it needs to continue coherently is
// already in its first message.
//
// Only the role lookup itself is fatal: a missing role means there is nothing
// to seed. Listing sibling roles, their statuses, and the handoff note all
// degrade gracefully (skipped, not failed) so a transient error in one part
// of the seed can never block the restart.
func BuildRecycleSeedPrompt(store RecycleStore, roleID int64) (string, error) {
	role, err := store.HeraRole(roleID)
	if err != nil {
		return "", fmt.Errorf("build recycle seed prompt: resolve role %d: %w", roleID, err)
	}

	var b strings.Builder
	b.WriteString(role.Prompt)
	b.WriteString("\n\n")
	b.WriteString("---\n")
	b.WriteString("You are a fresh session recycled from a prior coordinator session on this same task. ")
	b.WriteString("The above is your original mission. Below is the current state of your orchestration and any handoff note your prior session left — read them before doing anything else; no tool call is needed to obtain them.\n")

	b.WriteString("\n## Current plan-DAG / role state\n")
	if planState := buildPlanStateSection(store, role.OrchestratorID, roleID); planState != "" {
		b.WriteString(planState)
	} else {
		b.WriteString("(no other roles in this orchestrator)\n")
	}

	if note := recycleHandoffNote(store, roleID); note != "" {
		b.WriteString("\n## Handoff note from your prior session\n")
		b.WriteString(note)
		b.WriteString("\n")
	}

	return b.String(), nil
}

// buildPlanStateSection lists every other role in the orchestrator alongside
// its current hera role_status, one line each, sorted by name for stable
// output. Errors (orchestrator listing or a single role's status lookup)
// are skipped rather than propagated — a partial seed is far better than no
// seed at all.
func buildPlanStateSection(store RecycleStore, orchID, excludeRoleID int64) string {
	roles, err := store.ListHeraRoles(orchID, false)
	if err != nil {
		return ""
	}

	type line struct {
		name, text string
	}
	var lines []line
	for _, r := range roles {
		if r.ID == excludeRoleID {
			continue
		}
		status := "no status yet"
		if st, err := store.HeraRoleStatusFor(r.ID); err == nil && st != nil {
			status = string(st.Status)
		}
		lines = append(lines, line{
			name: r.Name,
			text: fmt.Sprintf("- %s (%s, %s): %s\n", r.Name, r.Kind, r.NodeKind, status),
		})
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].name < lines[j].name })

	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.text)
	}
	return b.String()
}

// recycleHandoffNote reads task_meta(hera, handoff_note) for roleID's live
// task, if any. Returns "" on any error or absence — handoff notes are
// optional (design.md D5: "if present").
func recycleHandoffNote(store RecycleStore, roleID int64) string {
	binding, err := store.HeraLiveBindingByRole(roleID)
	if err != nil {
		return ""
	}
	meta, err := store.ListMeta(binding.ArgusTaskID, db.HeraMetaNamespace)
	if err != nil {
		return ""
	}
	for _, e := range meta {
		if e.Key == db.HeraMetaKeyHandoffNote {
			return e.Value
		}
	}
	return ""
}
