package daemon

import (
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// HeadlessInput captures every field a non-TUI caller (HTTP API, MCP, the
// scheduler) needs to pass to agent.CreateAndStart. Existing single-task
// callers leave the orchestration fields (BaseBranch, DependsOn) at zero and
// behave exactly as before. Adding more orchestration fields here does not
// break the three call sites in daemon.go that wrap this entry point.
type HeadlessInput struct {
	Name       string
	Prompt     string
	Project    string
	Backend    string
	AutoName   bool
	BaseBranch string
	DependsOn  []string
	PlanSlug   string // optional orchestrator grouping label; opaque to daemon
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
// When DependsOn is non-empty, the task is persisted in Pending state and no
// agent process is spawned — internal/depswatcher will start the session
// once every listed dep reports complete.
//
// BeforeStart/AfterStart hooks are intentionally nil — those are for the TUI's
// startGen tick-reconciliation counter, which has no analogue in headless mode.
func HeadlessCreateTask(database *db.DB, runner agent.SessionProvider, in HeadlessInput) (*model.Task, error) {
	task, _, err := agent.CreateAndStart(database, runner, agent.CreateInput{
		Name:       in.Name,
		Prompt:     in.Prompt,
		Project:    in.Project,
		Backend:    in.Backend,
		AutoName:   in.AutoName,
		BaseBranch: in.BaseBranch,
		DependsOn:  in.DependsOn,
		PlanSlug:   in.PlanSlug,
	})
	return task, err
}
