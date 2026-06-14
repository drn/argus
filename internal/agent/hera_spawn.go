package agent

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// HeraWorkerSpawnInput is the resolved payload for a born-bound hera worker
// spawn. The caller owns name/project/prompt resolution and the orientation
// prefix; this primitive owns ONLY the transactional task + role + binding
// creation. It is shared by every entry point that spawns a hera worker — the
// daemon's MCP arm (hera_spawn_worker) and the native Hera view's rail `w` key —
// so there is exactly one implementation of the LIFO-cleanup spawn semantics.
type HeraWorkerSpawnInput struct {
	OrchestratorID int64  // orchestrator the new worker role + binding belong to
	BaseName       string // base role name; uniquified within the orchestrator
	TaskPrompt     string // orientation-prefixed prompt delivered to the session
	RolePrompt     string // verbatim user prompt, stored on the role row
	Project        string // resolved argus project
	Branch         string // optional base branch
	Backend        string // optional backend override
	Model          string // optional per-worker model override (empty = backend default)
}

// HeraWorkerSpawnResult is the success payload from SpawnHeraWorker.
type HeraWorkerSpawnResult struct {
	Task    *model.Task
	Role    *db.HeraRole
	Binding *db.HeraBinding
}

// SpawnHeraWorker performs the transactional born-bound worker spawn (M4). The
// role + binding write is an AfterPersist hook inside CreateAndStart, so it
// joins that call's LIFO compensating stack: a role/binding-insert failure
// unwinds the task+worktree+row (no orphan task), and a later session-start
// failure unwinds the role+binding too (no orphan role/binding).
// meta:hera.role=worker is stamped inside the hook, before the session starts,
// because the auto-adopt watcher (rule D4) and rail rendering key on it.
//
// This is the single source of truth for hera worker spawn semantics: both the
// daemon's MCP hera_spawn_worker arm and the native Hera view call it, rather
// than each re-deriving the transactional steps.
func SpawnHeraWorker(database *db.DB, runner SessionProvider, in HeraWorkerSpawnInput) (*HeraWorkerSpawnResult, error) {
	// Uniquify the role name within the orchestrator up front so the argus task
	// is titled after the role (not the orientation preamble). The partial
	// unique index on hera_roles is the backstop against a concurrent-spawn race.
	uniqueName, err := database.UniqueHeraRoleName(in.OrchestratorID, in.BaseName)
	if err != nil {
		return nil, err
	}

	var role *db.HeraRole
	var binding *db.HeraBinding
	task, _, err := CreateAndStart(database, runner, CreateInput{
		Name:       uniqueName,
		Prompt:     in.TaskPrompt,
		Project:    in.Project,
		Backend:    in.Backend,
		Model:      in.Model,
		BaseBranch: in.Branch,
		AutoName:   false, // name is the meaningful role slug — no Haiku rename
		AfterPersist: func(t *model.Task) (func(), error) {
			// Stamp meta:hera.role=worker BEFORE the session starts. Best-effort:
			// a meta failure must not abort an otherwise-valid spawn.
			if mErr := database.SetMeta(t.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindWorker)); mErr != nil {
				slog.Warn("[hera] spawn: meta role stamp failed (continuing)", "task", t.ID, "err", mErr)
			}
			r, b, cErr := database.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
				OrchestratorID: in.OrchestratorID,
				Name:           uniqueName,
				Kind:           db.HeraKindWorker,
				ArgusProject:   in.Project,
				Prompt:         in.RolePrompt,
			}, t.ID, t.Worktree)
			if cErr != nil {
				// Returning the error makes CreateAndStart unwind the task row +
				// worktree; the session was not started yet, so nothing leaks.
				return nil, cErr
			}
			role, binding = r, b
			// Compensating cleanup for a LATER failure (runner.Start). Deleting
			// the role cascades its binding away, so the subsequent db.Delete
			// (task row) finds no live binding to end — no orphan either way.
			cleanup := func() {
				if dErr := database.DeleteHeraRole(r.ID); dErr != nil {
					slog.Warn("[hera] spawn unwind: delete role failed", "role_id", r.ID, "err", dErr)
				}
			}
			return cleanup, nil
		},
	})
	if err != nil {
		return nil, err
	}
	return &HeraWorkerSpawnResult{Task: task, Role: role, Binding: binding}, nil
}

// heraWorkerNameRe matches runs of ASCII lowercase letters and digits, used to
// build a URL-slug-style role name from a prompt.
var heraWorkerNameRe = regexp.MustCompile(`[a-z0-9]+`)

// DeriveHeraWorkerName produces a slug from the first 40 chars of the prompt,
// mirroring Hera's swDeriveWorkerName. Returns "worker" for empty/symbol input.
// Shared by the MCP hera_spawn_worker arm and the native Hera view.
func DeriveHeraWorkerName(prompt string) string {
	runes := []rune(prompt)
	if len(runes) > 40 {
		runes = runes[:40]
	}
	lower := strings.Map(func(r rune) rune { return unicode.ToLower(r) }, string(runes))
	tokens := heraWorkerNameRe.FindAllString(lower, -1)
	if len(tokens) == 0 {
		return "worker"
	}
	slug := strings.Join(tokens, "-")
	if slug == "" {
		return "worker"
	}
	return slug
}

// HeraWorkerOrientation is the orientation prefix prepended to a spawned
// worker's prompt. Ports Hera's spawn-handler guidance verbatim (hera_send for
// progress, sub-coordinator escalation, iris for PRs), augmented to name the
// orchestrator and state that the worker is born-bound. Shared by the MCP arm
// and the native Hera view.
func HeraWorkerOrientation(orchestrator, coordinator string) string {
	return fmt.Sprintf(
		"You are a worker agent born bound to hera orchestrator %q under coordinator %q. "+
			"You may report progress via hera_send. If this task requires changes to another repo "+
			"or you need to spawn sub-agents, call hera_new_orchestrator(cwd=$PWD, name=\"...\", "+
			"coordinator_role_name=\"coord\", prompt=\"...\") to become a sub-coordinator, then use "+
			"hera_spawn_worker(project=\"TARGET-PROJECT\", ...) to dispatch workers in that project. "+
			"When opening pull requests, use mcp__argus__iris_gh_pr_create (not gh pr create directly) "+
			"so argus records the PR URL and the hera rail shows the PR indicator.",
		orchestrator, coordinator)
}
