package daemon

import (
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// HeadlessInput captures every field a non-TUI caller (HTTP API, MCP, the
// scheduler) needs to pass to agent.CreateAndStart. Callers that don't stack
// leave BaseBranch at zero and behave exactly as before. Adding more fields
// here does not break the call sites in daemon.go that wrap this entry point.
type HeadlessInput struct {
	Name       string
	Prompt     string
	Project    string
	Backend    string
	Model      string // optional per-task model override (empty = backend default)
	AutoName   bool
	BaseBranch string
	// SandboxOverride is an optional per-task tri-state override of the
	// resolved sandbox setting: "" (inherit), "enabled", or "disabled"
	// (add-task-sandbox-override).
	SandboxOverride string
}

// HeadlessCreateTask creates a task, its worktree, and starts an agent session
// without requiring a TUI. Used by the HTTP API, MCP server, and scheduler.
//
// Delegates to agent.CreateAndStart, which is fully transactional: any failure
// during worktree creation, DB insertion, or session start triggers LIFO
// cleanup of the preceding steps — so no orphan worktree, branch, or task row
// is left behind.
//
// AutoName, when true, fires the post-creation Haiku rename. Pass true iff
// Name was synthesized from Prompt (vs typed by a user or derived from a
// meaningful slug like "<src>-fork").
//
// The agent session starts immediately on creation (the legacy depends_on
// auto-gating was retired — sequencing is a coordinator's job now).
//
// BeforeStart/AfterStart hooks are intentionally nil — those are for the TUI's
// startGen tick-reconciliation counter, which has no analogue in headless mode.
func HeadlessCreateTask(database *db.DB, runner agent.SessionProvider, in HeadlessInput) (*model.Task, error) {
	task, _, err := agent.CreateAndStart(database, runner, agent.CreateInput{
		Name:            in.Name,
		Prompt:          in.Prompt,
		Project:         in.Project,
		Backend:         in.Backend,
		Model:           in.Model,
		AutoName:        in.AutoName,
		BaseBranch:      in.BaseBranch,
		SandboxOverride: in.SandboxOverride,
	})
	return task, err
}
